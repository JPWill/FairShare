-- FairShare/Core/Comm.lua
-- Addon messaging: encoding, chunking over the 255-byte limit, a token-bucket throttle
-- matching the client's burst-10 / +1-per-second budget, and queue-and-retry during
-- encounter lockdown. Every send and receive is announced on the message bus so the
-- transcript can record it.
--
-- Wire format
--   fields are joined with '^'; '~' and '^' inside a field are escaped as '~0' and '~1'
--   single message:   "=" .. body
--   chunked message:  "!<id>:<i>:<n>^" .. slice    (reassembled per sender + id)
local ADDON, ns = ...

local Comm = {}
ns.Core.Comm = Comm

local Events = ns.Core.Events
local PREFIX = ns.PREFIX
local SEP = "^"
local MAX_BYTES = 255
local CHUNK_HEADER_MAX = 14            -- "!" + id(≤5) + ":" + i(≤3) + ":" + n(≤3) + "^"
local CHUNK_PAYLOAD = MAX_BYTES - CHUNK_HEADER_MAX
local BURST, RATE = 9, 1               -- one below the documented burst of 10, for safety
local LOCKDOWN_RETRY = 2               -- seconds between retries while locked down

local R = (Enum and Enum.SendAddonMessageResult) or {}
local RESULT_OK = R.Success or 0
local RESULT_THROTTLE = R.AddonMessageThrottle or 3
local RESULT_LOCKDOWN = R.AddOnMessageLockdown or 11

local handlers = {}        -- msgType -> array of fn(sender, fields, meta)
local queue = {}           -- array of {text, channel, target, prio}, lowest prio first
local tokens, lastRefill = BURST, nil
local ticker
local lockdownUntil = 0
local seq = 0
local inbound = {}         -- "sender#id" -> {parts, got, total, t}
local lastSeen = {}        -- sender -> GetTime() of their last message
local reported = {}        -- msgType -> true once a handler error has been printed
local CHUNK_TTL = 30       -- seconds a partial chunked message is kept
local MAX_CHUNKS = 99

-- Auction traffic goes ahead of everything else in the send queue, so a flood of PINGs
-- (or any other chatter) can never push a COMMIT or REVEAL past its window.
local PRIORITY = { COMMIT = 1, REVEAL = 1, CLOSE = 1, RESULT = 1, OPEN = 1 }

----------------------------------------------------------------------------- names

-- One realm in Forever: identity is the bare name, so strip any "-Realm" the client adds.
local function normalizeName(name)
    if not name or name == "" then return name end
    local dash = name:find("-", 1, true)
    if dash then return name:sub(1, dash - 1) end
    return name
end

function Comm.PlayerName()
    return normalizeName(UnitName("player"))
end

----------------------------------------------------------------------------- encoding

local function escape(s)
    s = tostring(s):gsub("~", "~0")
    s = s:gsub("%^", "~1")
    return s
end

local UNESCAPE = { ["0"] = "~", ["1"] = "^" }
local function unescape(s)
    return (s:gsub("~([01])", UNESCAPE))
end

local function split(body)
    local out, n, pos = {}, 0, 1
    while true do
        local i = body:find(SEP, pos, true)
        n = n + 1
        if not i then
            out[n] = unescape(body:sub(pos))
            return out
        end
        out[n] = unescape(body:sub(pos, i - 1))
        pos = i + 1
    end
end

function Comm.Encode(msgType, fields)
    local parts = { msgType }
    for i = 1, #fields do
        parts[i + 1] = escape(fields[i])
    end
    return table.concat(parts, SEP)
end

function Comm.Decode(body)
    local parts = split(body)
    local msgType = table.remove(parts, 1)
    return msgType, parts
end

local function frameBody(body)
    if #body + 1 <= MAX_BYTES then
        return { "=" .. body }
    end
    seq = (seq % 99999) + 1
    local n = math.ceil(#body / CHUNK_PAYLOAD)
    local out = {}
    for i = 1, n do
        local slice = body:sub((i - 1) * CHUNK_PAYLOAD + 1, i * CHUNK_PAYLOAD)
        out[i] = "!" .. seq .. ":" .. i .. ":" .. n .. SEP .. slice
    end
    return out
end

----------------------------------------------------------------------------- sending

local function refill()
    local now = GetTime()
    if not lastRefill then lastRefill = now end
    tokens = math.min(BURST, tokens + (now - lastRefill) * RATE)
    lastRefill = now
end

local drain
local function ensureTicker()
    if queue[1] then
        if not ticker then ticker = ns.Core.Timer.Every(0.25, drain) end
    elseif ticker then
        ticker:Cancel()
        ticker = nil
    end
end

drain = function()
    refill()
    local now = GetTime()
    while queue[1] do
        if now < lockdownUntil then break end
        if tokens < 1 then break end
        local item = queue[1]
        local result = C_ChatInfo.SendAddonMessage(PREFIX, item.text, item.channel, item.target)
        if result == RESULT_LOCKDOWN then
            lockdownUntil = now + LOCKDOWN_RETRY
            Events.Fire("COMM_LOCKDOWN")
            break
        elseif result == RESULT_THROTTLE then
            tokens = 0
            break
        else
            table.remove(queue, 1)
            tokens = tokens - 1
            if result ~= RESULT_OK and result ~= true and result ~= nil then
                Events.Fire("COMM_ERROR", result, item)
            end
        end
    end
    ensureTicker()
end

local function resolveChannel(opts)
    if opts and opts.channel then return opts.channel, opts.target end
    -- A dev raid stays on this client: self-whisper even inside a real group.
    if ns.Dev and ns.Dev.Sandboxed and ns.Dev.Sandboxed() then return "WHISPER", Comm.PlayerName() end
    if IsInRaid() then return "RAID" end
    if IsInGroup() then return "PARTY" end
    -- Solo: whisper ourselves so the protocol can be exercised without a group.
    return "WHISPER", Comm.PlayerName()
end

-- Comm.Send("PING", { token, version })            -> group channel (or self-whisper when solo)
-- Comm.Send("ROW", { ... }, { channel = "WHISPER", target = "Name-Realm" })
function Comm.Send(msgType, fields, opts)
    fields = fields or {}
    local body = Comm.Encode(msgType, fields)
    local channel, target = resolveChannel(opts)
    local frames = frameBody(body)
    local prio = PRIORITY[msgType] or 2
    -- Insert after the last queued item of equal or higher priority, so order is kept
    -- within a class and the frames of one message stay together.
    local at = #queue + 1
    while at > 1 and queue[at - 1].prio > prio do at = at - 1 end
    for i = 1, #frames do
        table.insert(queue, at + i - 1, { text = frames[i], channel = channel, target = target, prio = prio })
    end
    Events.Fire("COMM_SENT", msgType, fields, body, channel, target)
    drain()
    return true
end

-- GetTime() of the last message heard from name, or nil.
function Comm.LastSeen(name)
    return lastSeen[name]
end

function Comm.QueueLength()
    return #queue
end

----------------------------------------------------------------------------- receiving

function Comm.On(msgType, fn)
    local list = handlers[msgType]
    if not list then
        list = {}
        handlers[msgType] = list
    end
    list[#list + 1] = fn
end

-- The channel protocol messages must arrive on: the group's own channel. A whisper is
-- accepted only from ourselves (solo and dev self-whisper) or for the two message types
-- that are whispered by design. Anything else is a raider trying to show one client a
-- message the rest of the raid never saw.
local WHISPERED = { SYNC = true, ROW = true }
local function channelAllowed(msgType, sender, channel)
    if IsInRaid() then
        if channel == "RAID" then return true end
    elseif IsInGroup() then
        if channel == "PARTY" then return true end
    end
    if channel == "WHISPER" then
        return sender == Comm.PlayerName() or WHISPERED[msgType] == true
    end
    return false
end

local function dispatch(sender, body, channel, injected)
    local msgType, fields = Comm.Decode(body)
    if not injected and not channelAllowed(msgType, sender, channel) then
        Events.Fire("RAID_NOTE", "off_channel:" .. tostring(msgType) .. ":" .. tostring(sender))
        return
    end
    lastSeen[sender] = GetTime()
    local meta = { channel = channel, t = GetServerTime(), injected = injected or nil }
    Events.Fire("COMM_RECEIVED", msgType, fields, body, sender, meta)
    local list = handlers[msgType]
    if not list then return end
    for i = 1, #list do
        -- One failing handler must not silence the others, and never the whole frame.
        local ok, err = pcall(list[i], sender, fields, meta)
        if not ok and not reported[msgType] then
            reported[msgType] = true
            ns.Print(("error handling %s from %s: %s"):format(tostring(msgType), tostring(sender), tostring(err)))
        end
    end
end

local function expireChunks(now)
    for key, buf in pairs(inbound) do
        if now - buf.t > CHUNK_TTL then inbound[key] = nil end
    end
end

local function onAddonMessage(_, prefix, text, channel, sender)
    if prefix ~= PREFIX then return end
    sender = normalizeName(sender)
    local first = text:sub(1, 1)
    if first == "=" then
        dispatch(sender, text:sub(2), channel)
    elseif first == "!" then
        local id, i, n, rest = text:match("^!(%d+):(%d+):(%d+)%^(.*)$")
        if not id then return end
        i, n = tonumber(i), tonumber(n)
        if not i or not n or n < 1 or n > MAX_CHUNKS or i < 1 or i > n then return end
        local now = GetTime()
        expireChunks(now)
        local key = sender .. "#" .. id
        local buf = inbound[key]
        if buf and buf.total ~= n then
            inbound[key] = nil
            buf = nil
        end
        if not buf then
            buf = { parts = {}, got = 0, total = n, t = now }
            inbound[key] = buf
        end
        if not buf.parts[i] then
            buf.parts[i] = rest
            buf.got = buf.got + 1
        end
        if buf.got < buf.total then return end
        for k = 1, buf.total do
            if not buf.parts[k] then
                inbound[key] = nil
                return
            end
        end
        inbound[key] = nil
        dispatch(sender, table.concat(buf.parts, "", 1, buf.total), channel)
    end
end

-- Developer simulation: feed a message into the receive path as if sender had sent it.
-- It takes exactly the route a real message takes after the server stamps the sender.
-- Only inside a dev raid: a virtual raider must never speak into a real one.
function Comm.Inject(sender, body, channel)
    if not (ns.Dev and ns.Dev.Sandboxed and ns.Dev.Sandboxed()) then
        ns.Print("Dev raiders only work in a dev raid")
        return false
    end
    dispatch(normalizeName(sender), body, channel or "RAID", true)
    return true
end

Events.Register("CHAT_MSG_ADDON", onAddonMessage)
Events.Register("ENCOUNTER_END", function()
    lockdownUntil = 0
    drain()
end)

if C_ChatInfo.RegisterAddonMessagePrefix then
    C_ChatInfo.RegisterAddonMessagePrefix(PREFIX)
end
