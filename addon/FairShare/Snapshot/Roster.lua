-- FairShare/Snapshot/Roster.lua
-- The confirmed roster from the snapshot (docs/protocol.md §4 roster lines) and
-- one-click invites. Invites are queued: up to four go out as party invites, the group
-- is converted to a raid once it exists, then the rest follow with a short gap so the
-- client never drops them.
local ADDON, ns = ...

local Roster = {}
ns.Roster = Roster

local Events, Group, Timer = ns.Core.Events, ns.Core.Group, ns.Core.Timer

local queue = {}
local ticking = false
local GAP = 0.4

local function invite(name)
    if C_PartyInfo and C_PartyInfo.InviteUnit then
        C_PartyInfo.InviteUnit(name)
    elseif InviteUnit then
        InviteUnit(name)
    end
end

local function convertToRaid()
    if C_PartyInfo and C_PartyInfo.ConvertToRaid then
        C_PartyInfo.ConvertToRaid()
    elseif ConvertToRaid then
        ConvertToRaid()
    end
end

local function tick()
    if #queue == 0 then
        ticking = false
        Events.Fire("ROSTER_INVITES_DONE")
        return
    end
    -- A party holds five. Beyond that we need a raid, which needs a party first.
    if IsInGroup() and not IsInRaid() and GetNumGroupMembers() + #queue > 5 then
        convertToRaid()
    elseif not IsInGroup() and GetNumGroupMembers() == 0 and Roster.sentWithoutGroup >= 4 then
        -- Four invites are out and nobody has accepted yet: wait for the party to form.
        Timer.After(1, tick)
        return
    end
    local name = table.remove(queue, 1)
    if not IsInGroup() then Roster.sentWithoutGroup = Roster.sentWithoutGroup + 1 end
    invite(name)
    Events.Fire("ROSTER_INVITED", name)
    Timer.After(GAP, tick)
end

-- Queue invites for every name not already in the group. Returns how many were queued.
function Roster.Invite(names)
    local n = 0
    for _, name in ipairs(names) do
        if name ~= Group.Me() and not Group.IsMember(name) then
            queue[#queue + 1] = name
            n = n + 1
        end
    end
    if n > 0 and not ticking then
        ticking = true
        Roster.sentWithoutGroup = 0
        tick()
    end
    return n
end

Roster.sentWithoutGroup = 0

function Roster.Pending()
    return #queue
end

-- Roster entries from the leader's snapshot with their current group status.
-- Each: { name, role, status = confirmed|standby, present = bool }
function Roster.Entries()
    local snap = ns.Snapshot.Current()
    if not snap or #snap.rosterList == 0 then return {} end
    local present = {}
    for _, n in ipairs(Group.Members()) do present[n] = true end
    local out = {}
    for _, e in ipairs(snap.rosterList) do
        out[#out + 1] = { name = e.name, role = e.role, status = e.status, present = present[e.name] or false }
    end
    return out
end

function Roster.Missing(status)
    local names = {}
    for _, e in ipairs(Roster.Entries()) do
        if not e.present and (not status or e.status == status) then names[#names + 1] = e.name end
    end
    return names
end

Events.Register("GROUP_ROSTER_UPDATE", function()
    if ticking and IsInGroup() then
        -- A party formed: let the queue continue immediately.
        Roster.sentWithoutGroup = 0
    end
end)
