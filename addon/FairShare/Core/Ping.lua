-- FairShare/Core/Ping.lua
-- PING/PONG round trip: proves addon messages flow in the current group, shows who has
-- the addon and which version, and measures round-trip time. Used by the Home panel.
local ADDON, ns = ...

local Comm, Events = ns.Core.Comm, ns.Core.Events
local Ping = {}
ns.Core.Ping = Ping

local pending = {}    -- token -> GetTime() when sent
Ping.results = {}     -- array of { name, version, rtt }

function Ping.Send()
    local token = tostring(math.random(100000, 999999))
    pending[token] = GetTime()
    Ping.results = {}
    Events.Fire("PING_SENT", token)
    return Comm.Send("PING", { token, ns.VERSION })
end

-- One PONG per sender per 10 s, and none while an auction runs: a PING flood must never
-- queue replies ahead of anyone's COMMIT or REVEAL (the queue already favours those).
local PONG_EVERY = 10
local lastPong = {}   -- sender -> GetTime() of our last reply

Comm.On("PING", function(sender, fields)
    if ns.Auction and ns.Auction.IsRunning and ns.Auction.IsRunning() then return end
    local now = GetTime()
    if lastPong[sender] and now - lastPong[sender] < PONG_EVERY then return end
    lastPong[sender] = now
    Comm.Send("PONG", { fields[1], ns.VERSION })
end)

Comm.On("PONG", function(sender, fields)
    local sentAt = pending[fields[1]]
    if not sentAt then return end
    Ping.results[#Ping.results + 1] = { name = sender, version = fields[2], rtt = GetTime() - sentAt }
    Events.Fire("PING_RESULT", Ping.results)
end)
