-- FairShare/Core/Group.lua
-- Who is in the group, who leads it, and name normalization. WoW: Forever is one realm
-- and names may have two words ("Thrall Doomhammer"); identity is the bare in-game name,
-- so any "-Realm" suffix the client appends is stripped (docs/protocol.md §1).
local ADDON, ns = ...

local Group = {}
ns.Core.Group = Group

local function normalize(name)
    if not name or name == "" then return name end
    local dash = name:find("-", 1, true)
    if dash then name = name:sub(1, dash - 1) end
    return name
end
Group.Normalize = normalize

function Group.Me()
    return normalize(UnitName("player"))
end

local function unitName(unit)
    local n = UnitName(unit)
    if not n or n == "" or n == "Unknown" then return nil end
    return normalize(n)
end

-- Names added by the developer simulation; they count as group members for rows and kills.
Group.extra = {}

-- Sorted array of every member's name, including the player.
function Group.Members()
    local set = { [Group.Me()] = true }
    if IsInRaid() then
        for i = 1, GetNumGroupMembers() do
            local name = GetRaidRosterInfo(i)
            if name then set[normalize(name)] = true end
        end
    elseif IsInGroup() then
        for i = 1, GetNumGroupMembers() - 1 do
            local n = unitName("party" .. i)
            if n then set[n] = true end
        end
    end
    -- Virtual raiders count only inside a dev raid; in a real one they must never appear
    -- in a KILL roster, a quorum or a close list.
    if ns.Dev and ns.Dev.Sandboxed and ns.Dev.Sandboxed() then
        for name in pairs(Group.extra) do set[name] = true end
    end
    local out = {}
    for name in pairs(set) do out[#out + 1] = name end
    table.sort(out)
    return out
end

function Group.IsMember(name)
    for _, n in ipairs(Group.Members()) do
        if n == name then return true end
    end
    return false
end

function Group.LeaderName()
    if IsInRaid() then
        for i = 1, GetNumGroupMembers() do
            local name, rank = GetRaidRosterInfo(i)
            if name and rank == 2 then return normalize(name) end
        end
        return Group.Me()
    elseif IsInGroup() then
        if UnitIsGroupLeader("player") then return Group.Me() end
        for i = 1, GetNumGroupMembers() - 1 do
            if UnitIsGroupLeader("party" .. i) then return unitName("party" .. i) or Group.Me() end
        end
        return Group.Me()
    end
    return Group.Me() -- solo: you lead yourself
end

function Group.IsLeader()
    return Group.LeaderName() == Group.Me()
end
