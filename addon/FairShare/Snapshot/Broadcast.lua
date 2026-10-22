-- FairShare/Snapshot/Broadcast.lua
-- The raid context every client holds: SNAP (raid id, checksum, floors, bans) and ROWS
-- (balance + credit for everyone present), both sent by the leader. Also the leader's
-- KILL broadcast after each boss, which is the attendance record for settlement.
local ADDON, ns = ...

local Raid = {}
ns.Raid = Raid

local Comm, Events, Group, S = ns.Core.Comm, ns.Core.Events, ns.Core.Group, ns.Lib.Serialize

Raid.context = nil   -- { raidID, checksum, short, issued, offspecPct, floors, banned, rows, leader }
Raid.lastKill = nil  -- { id, name, roster }
local sentRows = {}  -- names the leader has already sent rows for (this raidID)

local function leaderOnly(msgType, sender)
    -- The FairShare leader once there is a context (a takeover moves it), else the game leader.
    if sender ~= Raid.Leader() then
        Events.Fire("RAID_NOTE", "unauthorized:" .. msgType .. ":" .. sender)
        return false
    end
    return true
end

----------------------------------------------------------------------------- where FairShare runs
-- FairShare tracks one thing: the scheduled raid the bot opened for this leader. A snapshot
-- is used only near its scheduled start, only in a raid group (or a 5-player party when the
-- bot's "5-player groups" switch was on when the raid was opened), and kills and loot count
-- only inside an instance of that kind. Developer testing (a dev raid, or Developer mode
-- while solo) is exempt, since nothing it does reaches anyone else.

Raid.WINDOW_BEFORE = 3 * 3600   -- a snapshot works from 3 hours before its start ...
Raid.WINDOW_AFTER = 12 * 3600   -- ... until 12 hours after

local function sandboxed()
    return ns.Dev and ns.Dev.Sandboxed and ns.Dev.Sandboxed() or false
end

local function isDevID(id) return type(id) == "string" and id:sub(1, 3) == "dev" end

-- Is the snapshot inside its time window? Returns ok, reason.
function Raid.InWindow(snap)
    if not snap or isDevID(snap.raidID) then return true end
    local start = snap.starts or snap.issued
    if not start then return true end
    local now = GetServerTime()
    if now < start - Raid.WINDOW_BEFORE then
        return false, ("raid %s starts at %s; its snapshot works from 3 hours before"):format(snap.raidID, date and date("%a %H:%M", start) or tostring(start))
    end
    if now > start + Raid.WINDOW_AFTER then
        return false, ("raid %s was scheduled more than 12 hours ago; open a new raid in Discord"):format(snap.raidID)
    end
    return true
end

-- Does the current group count as this raid's group? Returns ok, reason.
function Raid.GroupAllowed(snap)
    if sandboxed() then return true end
    if IsInRaid() then return true end
    if IsInGroup() then
        if snap and snap.party then return true end
        return false, "FairShare runs in raid groups; convert the group to a raid (officers can allow 5-player groups for testing)"
    end
    return false, "form the raid group first; the snapshot is sent when it exists"
end

-- Why the leader's snapshot is not being sent right now, or nil when it may be.
function Raid.BroadcastBlocker(snap)
    snap = snap or ns.Snapshot.Current()
    if not snap then return "no snapshot loaded" end
    local ok, why = Raid.InWindow(snap)
    if not ok then return why end
    ok, why = Raid.GroupAllowed(snap)
    if not ok then return why end
    return nil
end

-- Do kills and loot here belong to the raid? Inside a raid instance, or a dungeon when the
-- snapshot allows a party. Clients without the snapshot (raiders) accept any instance:
-- they only have a raid at all because the leader's client decided it counts.
function Raid.InRaidInstance()
    if sandboxed() then return true end
    local inInstance, kind = false, nil
    if IsInInstance then inInstance, kind = IsInInstance() end
    if not inInstance then return false end
    if kind == "raid" then return true end
    local snap = ns.Snapshot.Current()
    if snap and Raid.IsLeader() then return kind == "party" and snap.party == true end
    return true
end

----------------------------------------------------------------------------- leader side

local function rowsFor(snap, names)
    local fields = {}
    for _, name in ipairs(names) do
        local r = snap.rows[name]
        if r then fields[#fields + 1] = name .. "," .. (r.owner:gsub(",", " ")) .. "," .. r.balance .. "," .. r.credit end
    end
    return fields
end

-- Sends SNAP and the rows for everyone currently present. Returns ok, err.
function Raid.Broadcast()
    local snap = ns.Snapshot.Current()
    if not snap then return false, "no snapshot loaded" end
    if not Raid.IsLeader() then return false, Raid.LeaderBlocker() end
    local blocker = Raid.BroadcastBlocker(snap)
    if blocker then return false, blocker end
    local floors, keys = {}, {}
    for k in pairs(snap.floors) do keys[#keys + 1] = k end
    table.sort(keys)
    for _, k in ipairs(keys) do floors[#floors + 1] = k .. "=" .. snap.floors[k] end
    Comm.Send("SNAP", { snap.raidID, snap.checksum, snap.issued, snap.offspecPct or 0, S.JoinCSV(floors), S.JoinCSV(snap.bannedList) })
    local names = Group.Members()
    sentRows = {}
    for _, n in ipairs(names) do sentRows[n] = true end
    local fields = rowsFor(snap, names)
    if #fields > 0 then Comm.Send("ROWS", fields) end
    return true
end

-- Late joiners: send rows for anyone we have not covered yet.
local function sendMissingRows()
    local snap = ns.Snapshot.Current()
    if not snap or not Raid.context or not Raid.IsLeader() then return end
    if Raid.BroadcastBlocker(snap) then return end
    local missing = {}
    for _, n in ipairs(Group.Members()) do
        if not sentRows[n] then
            sentRows[n] = true
            missing[#missing + 1] = n
        end
    end
    if #missing == 0 then return end
    -- Re-send SNAP so a fresh client gets the context too; it is small.
    local floors, keys = {}, {}
    for k in pairs(snap.floors) do keys[#keys + 1] = k end
    table.sort(keys)
    for _, k in ipairs(keys) do floors[#floors + 1] = k .. "=" .. snap.floors[k] end
    Comm.Send("SNAP", { snap.raidID, snap.checksum, snap.issued, snap.offspecPct or 0, S.JoinCSV(floors), S.JoinCSV(snap.bannedList) })
    local fields = rowsFor(snap, missing)
    if #fields > 0 then Comm.Send("ROWS", fields) end
end

Events.Register("GROUP_ROSTER_UPDATE", sendMissingRows)
Events.On("GROUP_CHANGED", sendMissingRows) -- developer simulation adds virtual members

-- Leader announces each kill with the roster present.
function Raid.OnEncounterEnd(encounterID, encounterName, success)
    if not success or success == 0 then return end
    if not Raid.IsLeader() or not Raid.context then return end
    if not Raid.InRaidInstance() then return end -- a dungeon or open-world kill is not this raid's
    Comm.Send("KILL", { tostring(encounterID), tostring(encounterName), S.JoinCSV(Group.Members()) })
end

Events.Register("ENCOUNTER_END", function(_, encounterID, encounterName, _, _, success)
    Raid.OnEncounterEnd(encounterID, encounterName, success)
end)

----------------------------------------------------------------------------- every client

Comm.On("SNAP", function(sender, f)
    if not leaderOnly("SNAP", sender) then return end
    if not f[1] or f[1] == "" then
        Events.Fire("RAID_NOTE", "malformed:SNAP:" .. sender)
        return
    end
    local floors = {}
    for _, kv in ipairs(S.SplitCSV(f[5])) do
        local k, v = kv:match("^(.-)=(.*)$")
        if k then floors[k] = tonumber(v) end
    end
    local banned = {}
    for _, n in ipairs(S.SplitCSV(f[6])) do banned[n] = true end
    local keepRows = Raid.context and Raid.context.raidID == f[1] and Raid.context.rows or {}
    Raid.context = {
        raidID = f[1], checksum = f[2], short = (f[2] or ""):sub(1, 8), issued = tonumber(f[3]),
        offspecPct = tonumber(f[4]) or 0, floors = floors, banned = banned, rows = keepRows, leader = sender,
    }
    Events.Fire("RAID_CONTEXT", Raid.context)
end)

Comm.On("ROWS", function(sender, f)
    local c = Raid.context
    if not c or sender ~= c.leader then return end
    for _, row in ipairs(f) do
        local parts = S.SplitCSV(row)
        if #parts >= 4 then
            local name = parts[1]
            local credit = tonumber(parts[#parts])
            local balance = tonumber(parts[#parts - 1])
            local owner = table.concat(parts, ",", 2, #parts - 2)
            if name ~= "" and balance and credit then
                c.rows[name] = { owner = owner, balance = balance, credit = credit }
            end
        end
    end
    Events.Fire("RAID_CONTEXT", c)
end)

Comm.On("KILL", function(sender, f)
    if not leaderOnly("KILL", sender) then return end
    Raid.lastKill = { id = f[1], name = f[2], roster = S.SplitCSV(f[3]) }
    Events.Fire("RAID_KILL", Raid.lastKill)
end)

----------------------------------------------------------------------------- helpers

local function itemInfo(link)
    if C_Item and C_Item.GetItemInfo then return C_Item.GetItemInfo(link) end
    if GetItemInfo then return GetItemInfo(link) end
end

-- Floor table key for an item: its equip location when the table has one, else DEFAULT.
function Raid.FloorKeyFor(itemLink)
    local c = Raid.context
    if not c then return "DEFAULT" end
    local equipLoc = select(9, itemInfo(itemLink))
    if equipLoc and c.floors[equipLoc] then return equipLoc end
    return "DEFAULT"
end

-- Floor for a floor table key, from the raid context (nil without one).
function Raid.FloorForKey(key)
    local c = Raid.context
    if not c then return nil end
    return c.floors[key] or c.floors.DEFAULT or 1
end

-- Floor for an item from the raid context's floor table (by equip location, else DEFAULT).
function Raid.FloorFor(itemLink)
    if not Raid.context then return nil end
    return Raid.FloorForKey(Raid.FloorKeyFor(itemLink))
end

-- My row in the current raid context (what I can bid with), or from the leader's own snapshot.
function Raid.MyRow()
    local me = Group.Me()
    if Raid.context and Raid.context.rows[me] then return Raid.context.rows[me] end
    return ns.Snapshot.Row(me)
end

function Raid.Clear()
    Raid.context, Raid.lastKill, sentRows = nil, nil, {}
    Events.Fire("RAID_CONTEXT", nil)
end

----------------------------------------------------------------------------- automatic broadcast

-- A snapshot goes out by itself: once when it is loaded or imported, and again whenever the
-- leader finds the group without it (after a reload, or on taking lead). Each checksum is
-- sent once this way; Rebroadcast on the Leader tab repeats it by hand if a client missed it.
local autoSent = {}

local function autoBroadcast()
    local snap = ns.Snapshot.Current()
    if not snap or not Raid.IsLeader() then return end
    if autoSent[snap.checksum] then return end
    local c = ns.Raid.context
    if c and c.checksum == snap.checksum then return end
    -- Wait, without marking it sent, until the group and the time are right: the raid
    -- group forming, or the start window opening, triggers it.
    if Raid.BroadcastBlocker(snap) then return end
    autoSent[snap.checksum] = true
    Raid.Broadcast()
end

-- Leaving the raid ends it on this client: nothing more is recorded until a leader sends
-- a snapshot again. The transcript and the auction records stay saved.
local function endIfGroupGone()
    local c = Raid.context
    if not c or sandboxed() then return end
    local gone = not IsInGroup()
    if not gone and Raid.IsLeader() then
        local snap = ns.Snapshot.Current()
        gone = snap ~= nil and snap.raidID == c.raidID and not Raid.GroupAllowed(snap)
    end
    if gone then
        ns.Print(("Raid %s ended on this client: you are no longer in its group. Nothing more is recorded for it."):format(c.raidID))
        Raid.Clear()
    end
end

Events.Register("GROUP_ROSTER_UPDATE", endIfGroupGone)

Events.On("SNAPSHOT_CHANGED", autoBroadcast)
Events.Register("PLAYER_ENTERING_WORLD", autoBroadcast)
Events.Register("GROUP_ROSTER_UPDATE", autoBroadcast)
Events.On("GROUP_CHANGED", autoBroadcast)
