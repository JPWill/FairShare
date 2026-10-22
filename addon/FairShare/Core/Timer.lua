-- FairShare/Core/Timer.lua
-- Thin wrappers over C_Timer so the rest of the addon has one place to look.
local ADDON, ns = ...

local Timer = {}
ns.Core.Timer = Timer

-- Run fn once after delay seconds.
function Timer.After(delay, fn)
    C_Timer.After(delay, fn)
end

-- Run fn every interval seconds until the returned handle's Cancel() is called.
function Timer.Every(interval, fn)
    return C_Timer.NewTicker(interval, fn)
end
