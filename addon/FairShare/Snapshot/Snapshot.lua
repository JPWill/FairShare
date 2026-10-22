-- FairShare/Snapshot/Snapshot.lua
-- The leader's copy of the backend snapshot: loaded from Data.lua (sync client) or a
-- pasted string, checksum verified, newest by issue time wins. Raiders never need this
-- file's data directly; they get their rows over the wire (Snapshot/Broadcast.lua).
local ADDON, ns = ...

local Snapshot = {}
ns.Snapshot = Snapshot

local S, Sha, B64, Events = ns.Lib.Serialize, ns.Lib.Sha256, ns.Lib.Base64, ns.Core.Events
local current

local function adopt(text, source)
    local snap, err = S.ParseSnapshot(text)
    if not snap then return nil, err end
    local checksum = Sha.hex(text)
    snap.text, snap.checksum, snap.short, snap.source = text, checksum, checksum:sub(1, 8), source
    return snap
end

local function persist(snap)
    if not ns.db then return end
    ns.db.snapshot = { text = snap.text, checksum = snap.checksum, issued = snap.issued, raidID = snap.raidID, source = snap.source }
end

-- Import a pasted string (base64) or raw canonical text. Returns snap or nil, err.
function Snapshot.Import(str, source)
    if type(str) ~= "string" then return nil, "nothing to import" end
    local text = str:gsub("^%s+", "")
    text = text:gsub("%s+$", "")
    if text == "" then return nil, "nothing to import" end
    if text:sub(1, 9) ~= "FAIRSHARE" then
        local decoded = B64.decode(text)
        if not decoded or decoded:sub(1, 9) ~= "FAIRSHARE" then return nil, "not a FairShare snapshot string" end
        text = decoded
    end
    local snap, err = adopt(text, source or "paste")
    if not snap then return nil, err end
    current = snap
    persist(snap)
    Events.Fire("SNAPSHOT_CHANGED", snap)
    return snap
end

function Snapshot.Current()
    return current
end

function Snapshot.Row(name)
    return current and current.rows[name] or nil
end

function Snapshot.MyRow()
    return Snapshot.Row(ns.Core.Group.Me())
end

function Snapshot.Clear()
    current = nil
    if ns.db then ns.db.snapshot = nil end
    Events.Fire("SNAPSHOT_CHANGED", nil)
end

-- Export the current snapshot as a paste string (useful for handing to a co-leader).
function Snapshot.ExportString()
    if not current then return nil end
    return B64.encode(current.text)
end

-- On load: consider the saved copy and Data.lua, keep the newest valid one.
Events.On("DB_READY", function()
    local best
    local saved = ns.db.snapshot
    if saved and type(saved.text) == "string" then
        local s = adopt(saved.text, saved.source or "saved")
        if s then best = s end
    end
    local data = _G.FairShareData
    if type(data) == "table" and type(data.snapshot) == "string" then
        local s, err = adopt(data.snapshot, "Data.lua")
        if not s then
            Snapshot.dataError = "Data.lua: " .. tostring(err)
        elseif type(data.checksum) == "string" and data.checksum ~= s.checksum then
            Snapshot.dataError = "Data.lua checksum does not match its contents"
        elseif not best or (s.issued or 0) >= (best.issued or 0) then
            best = s
        end
    end
    if best then
        current = best
        persist(best)
        Events.Fire("SNAPSHOT_CHANGED", best)
    end
end)
