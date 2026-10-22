-- FairShare/Transcript/Transcript.lua
-- Records every protocol message received (our own echoes included), every loot event,
-- and local notes into SavedVariables, per raid. Export builds the canonical text
-- (docs/protocol.md §5) and base64-encodes it for pasting.
local ADDON, ns = ...

local T = {}
ns.Transcript = T

local Events, Group, S, B64 = ns.Core.Events, ns.Core.Group, ns.Lib.Serialize, ns.Lib.Base64

local RECORDED = { SNAP = true, ROWS = true, OPEN = true, COMMIT = true, CLOSE = true, REVEAL = true, RESULT = true, KILL = true, TRANSFER = true, VOID = true, LEAD = true, ACK = true, ABANDON = true, DELIVERED = true }

local function bucket(raidID)
    if not ns.db then return nil end
    ns.db.transcript = ns.db.transcript or {}
    local b = ns.db.transcript[raidID]
    if not b then
        b = { lines = {}, startedAt = GetServerTime(), player = Group.Me() }
        ns.db.transcript[raidID] = b
    end
    return b
end

local function clean(s)
    return (tostring(s):gsub("[\t\r\n]", " "))
end

local function record(raidID, dir, sender, body)
    local b = bucket(raidID)
    if not b then return end
    local line = GetServerTime() .. "\t" .. dir .. "\t" .. clean(sender) .. "\t" .. clean(body)
    b.lines[#b.lines + 1] = line
    Events.Fire("TRANSCRIPT_LINE", raidID, line)
end

local function currentRaidID()
    return ns.Raid.context and ns.Raid.context.raidID or nil
end

Events.On("COMM_RECEIVED", function(msgType, fields, body, sender)
    if not RECORDED[msgType] then return end
    local raidID = currentRaidID()
    if msgType == "SNAP" then
        -- SNAP establishes the raid id; it arrives here before the handler sets the context.
        -- Only the leader's SNAP may start a transcript: anyone else's is a note in the
        -- current raid, never a bucket of its own (or the backend would take the forger
        -- for the leader, and SavedVariables would fill with junk raids).
        if sender ~= ns.Raid.Leader() then
            if raidID then record(raidID, "note", Group.Me(), "unauthorized_snap:" .. sender) end
            return
        end
        raidID = fields[1]
        if not raidID or raidID == "" then return end
    end
    if not raidID then return end
    record(raidID, "in", sender, body)
end)

-- The sync client reads these two fields straight from SavedVariables for the §5 header.
Events.On("RAID_CONTEXT", function(c)
    if not c then return end
    local b = bucket(c.raidID)
    if b then
        b.snapshot = c.checksum
        b.version = ns.VERSION
    end
end)

Events.On("RAID_NOTE", function(text)
    local raidID = currentRaidID()
    if raidID then record(raidID, "note", Group.Me(), text) end
end)

function T.Note(text)
    local raidID = currentRaidID()
    if raidID then record(raidID, "note", Group.Me(), text) end
end

----------------------------------------------------------------------------- loot events

-- Build Lua patterns from the client's loot format strings so this works in any locale.
local lootPatterns = {}
local function addPattern(fmt, selfLoot)
    if type(fmt) ~= "string" then return end
    local p = fmt:gsub("([%(%)%.%%%+%-%*%?%[%]%^%$])", "%%%1")
    p = p:gsub("%%%%s", "(.+)")
    p = p:gsub("%%%%d", "%%d+")
    lootPatterns[#lootPatterns + 1] = { pattern = "^" .. p .. "$", selfLoot = selfLoot }
end
addPattern(_G.LOOT_ITEM_SELF_MULTIPLE, true)
addPattern(_G.LOOT_ITEM_SELF, true)
addPattern(_G.LOOT_ITEM_MULTIPLE, false)
addPattern(_G.LOOT_ITEM, false)

function T.ParseLoot(text)
    for _, p in ipairs(lootPatterns) do
        local a, b = text:match(p.pattern)
        if a then
            if p.selfLoot then return Group.Me(), a end
            return Group.Normalize(a), b
        end
    end
end

Events.Register("CHAT_MSG_LOOT", function(_, text)
    local raidID = currentRaidID()
    if not raidID or not ns.Raid.InRaidInstance() then return end
    local looter, link = T.ParseLoot(text)
    if looter and link and link:find("|Hitem:", 1, true) then
        record(raidID, "loot", looter, "LOOT^" .. link)
    end
end)

----------------------------------------------------------------------------- export

function T.Lines(raidID)
    local b = ns.db and ns.db.transcript and ns.db.transcript[raidID]
    return b and b.lines or {}
end

function T.RaidIDs()
    local out = {}
    if ns.db and ns.db.transcript then
        for id in pairs(ns.db.transcript) do out[#out + 1] = id end
    end
    table.sort(out)
    return out
end

T.KEEP_RAIDS = 30

-- Every raid on this client, newest first: { raidID, startedAt, lines, short, current }.
-- Anyone can export any of these later, for a dispute or because the leader's upload
-- was lost; the sync client uploads all of them on its own.
function T.History()
    local out = {}
    if not (ns.db and ns.db.transcript) then return out end
    local cur = currentRaidID()
    for id, b in pairs(ns.db.transcript) do
        out[#out + 1] = {
            raidID = id, startedAt = b.startedAt or 0, lines = #(b.lines or {}),
            short = b.snapshot and b.snapshot:sub(1, 8) or "", current = id == cur,
        }
    end
    table.sort(out, function(a, b)
        if a.startedAt ~= b.startedAt then return a.startedAt > b.startedAt end
        return a.raidID > b.raidID
    end)
    return out
end

-- Keep SavedVariables small: drop the oldest raids beyond keep (never the current one).
function T.Prune(keep)
    keep = keep or T.KEEP_RAIDS
    local hist = T.History()
    local dropped = 0
    for i = keep + 1, #hist do
        if not hist[i].current then
            local id = hist[i].raidID
            ns.db.transcript[id] = nil
            -- Everything else saved per raid goes with the transcript, so SavedVariables
            -- never carries more than the newest raids in any table.
            for _, key in ipairs({ "auctions", "deliveries", "claims" }) do
                if ns.db[key] then ns.db[key][id] = nil end
            end
            dropped = dropped + 1
        end
    end
    return dropped
end

Events.On("DB_READY", function() T.Prune() end)

-- Label for a history entry, e.g. "raid 12 - 20 Oct 21:05 - 143 lines".
function T.HistoryLabel(h)
    local when = date and date("%d %b %H:%M", h.startedAt) or tostring(h.startedAt)
    return ("Raid %s - %s - %d lines%s"):format(h.raidID, when, h.lines, h.current and " (current)" or "")
end

-- Canonical text for a raid, or nil if nothing recorded.
function T.Text(raidID)
    raidID = raidID or currentRaidID()
    if not raidID then return nil end
    local lines = T.Lines(raidID)
    if #lines == 0 then return nil end
    local checksum = (ns.Raid.context and ns.Raid.context.raidID == raidID and ns.Raid.context.checksum)
        or (ns.db.snapshot and ns.db.snapshot.raidID == raidID and ns.db.snapshot.checksum) or ""
    return S.BuildTranscript({ raidID = raidID, checksum = checksum, player = Group.Me(), version = ns.VERSION }, lines)
end

-- Paste string (base64) for a raid.
function T.Export(raidID)
    local text = T.Text(raidID)
    if not text then return nil, "nothing recorded for this raid" end
    return B64.encode(text)
end

function T.Clear(raidID)
    if ns.db and ns.db.transcript then ns.db.transcript[raidID] = nil end
end
