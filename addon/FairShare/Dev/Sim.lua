-- FairShare/Dev/Sim.lua
-- Developer mode: lets one person exercise the whole protocol alone. Real messages still
-- go through the server (self-whisper when solo); virtual raiders live inside this client
-- and answer by injecting messages into the normal receive path, so the state machine,
-- transcript and UI cannot tell them from real players.
local ADDON, ns = ...

local Dev = {}
ns.Dev = Dev

local Comm, Events, Timer, Group, S = ns.Core.Comm, ns.Core.Events, ns.Core.Timer, ns.Core.Group, ns.Lib.Serialize
local State = ns.Auction.State

local NAMES = { "Simone", "Simba", "Simon", "Simran", "Simeon", "Sima", "Simkin", "Simza" }

Dev.raiders = {}        -- name -> { mode = "normal" | "noreveal" | "badreveal" | "silent", bids = true }
Dev.bidding = true      -- virtual raiders bid on auctions
Dev.chaos = false       -- one never reveals, one reveals a wrong bid
Dev.log = {}            -- last N comm lines for the Dev panel
local LOG_MAX = 60
local pendingBids = {}  -- auctionID -> name -> { bid, offspec, salt }

function Dev.Enabled()
    return ns.db and ns.db.settings and ns.db.settings.dev or false
end

local function later(min, max, fn)
    Timer.After(min + math.random() * (max - min), fn)
end

local function inject(name, msgType, fields)
    Comm.Inject(name, Comm.Encode(msgType, fields))
end

----------------------------------------------------------------------------- roster

function Dev.AddRaiders(n)
    n = n or 5
    local added = 0
    for _, base in ipairs(NAMES) do
        if added >= n then break end
        local name = base
        if not Dev.raiders[name] then
            Dev.raiders[name] = { mode = "normal" }
            Group.extra[name] = true
            added = added + 1
        end
    end
    Dev.ApplyChaos()
    Events.Fire("GROUP_CHANGED")
    Events.Fire("DEV_CHANGED")
    return added
end

function Dev.ClearRaiders()
    Dev.raiders = {}
    Group.extra = {}
    Events.Fire("GROUP_CHANGED")
    Events.Fire("DEV_CHANGED")
end

function Dev.RaiderNames()
    local out = {}
    for name in pairs(Dev.raiders) do out[#out + 1] = name end
    table.sort(out)
    return out
end

function Dev.ApplyChaos()
    local names = Dev.RaiderNames()
    for _, name in ipairs(names) do Dev.raiders[name].mode = "normal" end
    if Dev.chaos then
        if names[1] then Dev.raiders[names[1]].mode = "noreveal" end
        if names[2] then Dev.raiders[names[2]].mode = "badreveal" end
    end
end

function Dev.SetChaos(on)
    Dev.chaos = on and true or false
    Dev.ApplyChaos()
    Events.Fire("DEV_CHANGED")
end

----------------------------------------------------------------------------- sample snapshot

-- A snapshot containing the player and every virtual raider, imported through the normal path.
function Dev.LoadSampleSnapshot()
    if next(Dev.raiders) == nil then Dev.AddRaiders(5) end
    local rows = { [Group.Me()] = { owner = "You", balance = 40, credit = 100 } }
    local roster = { [Group.Me()] = { role = "tank", status = "confirmed" } }
    local roles = { "healer", "melee", "ranged", "melee", "ranged", "healer", "tank", "ranged" }
    local i = 0
    for _, name in ipairs(Dev.RaiderNames()) do
        i = i + 1
        rows[name] = { owner = name, balance = (i % 3 == 0) and -30 or i * 25, credit = 100 + (i % 2) * 100 }
        roster[name] = { role = roles[i] or "melee", status = (i == 5) and "standby" or "confirmed" }
    end
    -- One confirmed raider who is not in the group yet, so the Roster tab has someone to invite.
    rows["Latecomer"] = { owner = "Latecomer", balance = 10, credit = 100 }
    roster["Latecomer"] = { role = "ranged", status = "confirmed" }
    local text = S.BuildSnapshot({
        raidID = "dev" .. tostring(GetServerTime() % 100000),
        issued = GetServerTime(),
        offspecPct = 0,
        floors = { DEFAULT = 10, INVTYPE_2HWEAPON = 20 },
        banned = {},
        roster = roster,
        rows = rows,
    })
    return ns.Snapshot.Import(text, "sample")
end

----------------------------------------------------------------------------- reactions

local function decideBid(a, name)
    local row = a.ctx.rows[name]
    if not row then return nil end
    local offspec = math.random() < 0.2
    local floor = State.ApplicableFloor(a.floor, offspec, a.ctx.offspecPct)
    local max = row.balance + row.credit
    if max < floor then return nil end
    local bid = math.random(floor, max)
    return { bid = bid, offspec = offspec, salt = (function()
        local t = {}
        for i = 1, 16 do t[i] = string.format("%02x", math.random(0, 255)) end
        return table.concat(t)
    end)() }
end

Events.On("COMM_RECEIVED", function(msgType, fields, body, sender)
    -- Comm log for the Dev panel (always, cheap).
    Dev.log[#Dev.log + 1] = ("%s  <- %s  %s"):format(date and date("%H:%M:%S") or "", sender, body:sub(1, 80))
    if #Dev.log > LOG_MAX then table.remove(Dev.log, 1) end
    Events.Fire("DEV_LOG")

    if not Dev.Sandboxed() or next(Dev.raiders) == nil then return end
    if msgType == "PING" then
        for _, name in ipairs(Dev.RaiderNames()) do
            later(0.2, 1.5, function() inject(name, "PONG", { fields[1], "sim" }) end)
        end
    end
    -- Virtual raiders back whoever the game made leader, like any client would.
    if msgType == "LEAD" then
        for _, name in ipairs(Dev.RaiderNames()) do
            later(0.2, 1.5, function() inject(name, "ACK", { fields[1] }) end)
        end
    end
end)

-- Auction reactions hang off the state machine's events so the auction exists when we decide.
-- Virtual raiders bid a random amount within their means in every auction, test ones
-- included, so a solo test auction already looks like a contested sale.
Events.On("AUCTION_OPEN", function(a)
    if not Dev.Sandboxed() or not Dev.bidding or next(Dev.raiders) == nil then return end
    pendingBids[a.id] = {}
    for _, name in ipairs(Dev.RaiderNames()) do
        local r = Dev.raiders[name]
        if r.mode ~= "silent" and (a.kind == "test" or r.mode ~= "normal" or math.random() < 0.75) then
            local b = decideBid(a, name)
            if b then
                pendingBids[a.id][name] = b
                later(0.5, 4, function() inject(name, "COMMIT", { a.id, State.Hash(a.id, name, b.bid, b.offspec, b.salt) }) end)
            end
        end
    end
end)

Events.On("AUCTION_CLOSE", function(a)
    local bids = pendingBids[a.id]
    if not bids then return end
    for name, b in pairs(bids) do
        local mode = Dev.raiders[name] and Dev.raiders[name].mode or "normal"
        if mode == "badreveal" then
            later(0.5, 2, function() inject(name, "REVEAL", { a.id, b.bid + 1, b.offspec and 1 or 0, b.salt }) end)
        elseif mode ~= "noreveal" then
            later(0.5, 3, function() inject(name, "REVEAL", { a.id, b.bid, b.offspec and 1 or 0, b.salt }) end)
        end
    end
    pendingBids[a.id] = nil
end)

Events.On("COMM_SENT", function(msgType, fields, body, channel, target)
    Dev.log[#Dev.log + 1] = ("%s  -> %s  %s"):format(date and date("%H:%M:%S") or "", channel .. (target and ("/" .. target) or ""), body:sub(1, 80))
    if #Dev.log > LOG_MAX then table.remove(Dev.log, 1) end
    Events.Fire("DEV_LOG")
end)

----------------------------------------------------------------------------- simulated game events

-- A line in the Dev tab's message log, so every simulated event shows what it did.
function Dev.Note(text)
    Dev.log[#Dev.log + 1] = ("%s  --  %s"):format(date and date("%H:%M:%S") or "", text)
    if #Dev.log > LOG_MAX then table.remove(Dev.log, 1) end
    Events.Fire("DEV_LOG")
end

function Dev.SimulateKill()
    Dev.Note("boss kill: ENCOUNTER_END success; the leader sends KILL with the roster, paused auctions resume, pending loot can start")
    Events.Simulate("ENCOUNTER_END", 9001, "Training Dummy", 1, GetNumGroupMembers(), 1)
end

function Dev.SimulateEncounter(start)
    Dev.Note(start and "boss start: ENCOUNTER_START; open auctions pause and their bars turn red" or "boss wipe: ENCOUNTER_END without a kill; auctions resume, no KILL line")
    if start then
        Events.Simulate("ENCOUNTER_START", 9001, "Training Dummy", 1, GetNumGroupMembers())
    else
        Events.Simulate("ENCOUNTER_END", 9001, "Training Dummy", 1, GetNumGroupMembers(), 0)
    end
end

-- A loot line as the client would print it, for the transcript's loot parser.
function Dev.SimulateLoot(link)
    Dev.Note("loot line: a CHAT_MSG_LOOT for Thunderfury; it lands in the transcript and, for the leader, as a pending row on the Ledger")
    local fmt = _G.LOOT_ITEM_SELF or "You receive loot: %s."
    Events.Simulate("CHAT_MSG_LOOT", fmt:format(link or "|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r"))
end

function Dev.ClearLog()
    Dev.log = {}
    Events.Fire("DEV_LOG")
end

----------------------------------------------------------------------------- sandbox

-- Dev raids carry ids starting with "dev". Everything the addon persists is keyed by raid
-- id, so a dev raid never shares a record with a real one, and a reset only ever touches
-- dev ids. While a dev raid is loaded every message goes by self-whisper, even inside a
-- real group, so the raid never sees dev traffic.
local function isDev(id)
    return type(id) == "string" and id:sub(1, 3) == "dev"
end

-- True when the simulator may act: a dev raid (its id starts with "dev"), or Developer
-- mode while solo. Solo, every message is a self-whisper, so virtual raiders can rehearse
-- the bot's sandbox snapshot (a numeric raid id) without anyone else ever seeing a line.
-- Inside a real group only a dev raid counts, so nothing injected can reach other players.
function Dev.Sandboxed()
    if not Dev.Enabled() then return false end
    local c = ns.Raid.context
    local id = c and c.raidID
    if not id then
        local snap = ns.Snapshot.Current()
        id = snap and snap.raidID
    end
    if id and isDev(id) then return true end
    return not IsInGroup()
end

-- Forget every dev raid: transcripts, auctions, deliveries, open auctions, the context,
-- virtual raiders and pending loot. Real raid ids are untouched.
function Dev.Reset()
    local Auction = ns.Auction
    if ns.db then
        for _, key in ipairs({ "transcript", "auctions", "deliveries" }) do
            local t = ns.db[key]
            if type(t) == "table" then
                for id in pairs(t) do
                    if isDev(id) then t[id] = nil end
                end
            end
        end
    end
    for id in pairs(Auction.open) do
        if isDev(id) then Auction.open[id] = nil end
    end
    for i = #Auction.history, 1, -1 do
        if isDev(Auction.history[i].id) then table.remove(Auction.history, i) end
    end
    if Auction.active and isDev(Auction.active.id) then Auction.active = nil end
    if ns.Raid.context and isDev(ns.Raid.context.raidID) then ns.Raid.Clear() end
    Auction.Queue.Clear()
    Dev.ClearRaiders()
    Dev.log = {}
    Events.Fire("LEDGER_CHANGED")
    Events.Fire("DEV_LOG")
    Events.Fire("DEV_CHANGED")
end
