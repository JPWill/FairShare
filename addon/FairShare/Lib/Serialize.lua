-- FairShare/Lib/Serialize.lua
-- Canonical text formats shared with the Go backend (docs/protocol.md §4, §5).
-- Snapshot and transcript are plain sorted text so the checksum is identical in Lua and Go.
local ADDON, ns = ...
ns.Lib = ns.Lib or {}

local S = {}
ns.Lib.Serialize = S

local function splitLines(text)
    local out = {}
    local pos = 1
    while true do
        local i = text:find("\n", pos, true)
        if not i then
            out[#out + 1] = text:sub(pos)
            return out
        end
        out[#out + 1] = text:sub(pos, i - 1)
        pos = i + 1
    end
end

local function splitTabs(line)
    local out, pos = {}, 1
    while true do
        local i = line:find("\t", pos, true)
        if not i then
            out[#out + 1] = line:sub(pos)
            return out
        end
        out[#out + 1] = line:sub(pos, i - 1)
        pos = i + 1
    end
end

-- Parses canonical snapshot text. Returns a table or nil, err.
--   { raidID, issued, offspecPct, starts, party, floors = {KEY=value}, banned = {name=true}, bannedList = {sorted},
--     rows = { [Name-Realm] = {owner, balance, credit} }, rowCount }
function S.ParseSnapshot(text)
    if type(text) ~= "string" then return nil, "not a string" end
    text = text:gsub("\r", "")
    local lines = splitLines(text)
    if lines[1] ~= "FAIRSHARE SNAPSHOT 1" then return nil, "not a FairShare snapshot" end
    local snap = { floors = {}, banned = {}, bannedList = {}, roster = {}, rosterList = {}, rows = {}, rowCount = 0 }
    for i = 2, #lines do
        local line = lines[i]
        if line ~= "" then
            local key, value = line:match("^([^=]+)=(.*)$")
            if key == "raid" then
                snap.raidID = value
            elseif key == "issued" then
                snap.issued = tonumber(value)
            elseif key == "offspec" then
                snap.offspecPct = tonumber(value)
            elseif key == "starts" then
                snap.starts = tonumber(value)
                if not snap.starts then return nil, "bad starts line " .. i end
            elseif line == "party=1" then
                snap.party = true
            elseif key and key:sub(1, 6) == "floor:" then
                snap.floors[key:sub(7)] = tonumber(value)
            elseif line:sub(1, 4) == "ban:" then
                local name = line:sub(5)
                snap.banned[name] = true
                snap.bannedList[#snap.bannedList + 1] = name
            elseif line:sub(1, 7) == "roster:" then
                local f = splitTabs(line:sub(8))
                if #f ~= 3 then return nil, "bad roster line " .. i end
                snap.roster[f[1]] = { role = f[2], status = f[3] }
                snap.rosterList[#snap.rosterList + 1] = { name = f[1], role = f[2], status = f[3] }
            elseif line:sub(1, 4) == "row:" then
                local f = splitTabs(line:sub(5))
                if #f ~= 4 then return nil, "bad row on line " .. i end
                snap.rows[f[1]] = { owner = f[2], balance = tonumber(f[3]), credit = tonumber(f[4]) }
                snap.rowCount = snap.rowCount + 1
            else
                return nil, "unrecognised line " .. i
            end
        end
    end
    if not snap.raidID then return nil, "missing raid id" end
    if not snap.floors.DEFAULT then return nil, "missing DEFAULT floor" end
    snap.offspecPct = snap.offspecPct or 0
    return snap
end

-- Builds canonical snapshot text from a table shaped like ParseSnapshot's output
-- (rows as { [name] = {owner, balance, credit} }). Used by the dev sampler and tests.
function S.BuildSnapshot(snap)
    local out = { "FAIRSHARE SNAPSHOT 1", "raid=" .. snap.raidID, "issued=" .. tostring(snap.issued), "offspec=" .. tostring(snap.offspecPct or 0) }
    if snap.starts and snap.starts > 0 then out[#out + 1] = "starts=" .. tostring(snap.starts) end
    if snap.party then out[#out + 1] = "party=1" end
    local keys = {}
    for k in pairs(snap.floors) do keys[#keys + 1] = k end
    table.sort(keys)
    for _, k in ipairs(keys) do out[#out + 1] = "floor:" .. k .. "=" .. tostring(snap.floors[k]) end
    local bans = {}
    for name in pairs(snap.banned or {}) do bans[#bans + 1] = name end
    table.sort(bans)
    for _, name in ipairs(bans) do out[#out + 1] = "ban:" .. name end
    local rosterNames = {}
    for name in pairs(snap.roster or {}) do rosterNames[#rosterNames + 1] = name end
    table.sort(rosterNames)
    for _, name in ipairs(rosterNames) do
        local r = snap.roster[name]
        out[#out + 1] = "roster:" .. name .. "\t" .. r.role .. "\t" .. r.status
    end
    local names = {}
    for name in pairs(snap.rows) do names[#names + 1] = name end
    table.sort(names)
    for _, name in ipairs(names) do
        local r = snap.rows[name]
        out[#out + 1] = "row:" .. name .. "\t" .. r.owner .. "\t" .. tostring(r.balance) .. "\t" .. tostring(r.credit)
    end
    return table.concat(out, "\n")
end

-- Transcript text per protocol §5. header = { raidID, checksum, player, version }, lines = array of strings.
function S.BuildTranscript(header, lines)
    local out = {
        "FAIRSHARE TRANSCRIPT 1",
        "raid=" .. (header.raidID or ""),
        "snapshot=" .. (header.checksum or ""),
        "player=" .. (header.player or ""),
        "version=" .. (header.version or ""),
    }
    for i = 1, #lines do out[#out + 1] = lines[i] end
    return table.concat(out, "\n")
end

-- Helpers for CSV fields in SNAP messages.
function S.JoinCSV(list)
    return table.concat(list, ",")
end

function S.SplitCSV(s)
    local out = {}
    if s == nil or s == "" then return out end
    for item in (s .. ","):gmatch("([^,]*),") do out[#out + 1] = item end
    return out
end
