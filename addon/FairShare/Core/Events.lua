-- FairShare/Core/Events.lua
-- One hidden frame dispatches game events to registered handlers, and a separate
-- in-addon message bus lets modules talk without depending on each other.
local ADDON, ns = ...

local Events = {}
ns.Core.Events = Events

local frame = CreateFrame("Frame", "FairShareEventFrame")
local gameHandlers = {} -- event name -> array of fn(event, ...)
local busHandlers = {}  -- message name -> array of fn(...)

frame:SetScript("OnEvent", function(_, event, ...)
    local list = gameHandlers[event]
    if not list then return end
    for i = 1, #list do
        list[i](event, ...)
    end
end)

-- Game events (ADDON_LOADED, CHAT_MSG_ADDON, ...).
function Events.Register(event, fn)
    local list = gameHandlers[event]
    if not list then
        list = {}
        gameHandlers[event] = list
        frame:RegisterEvent(event)
    end
    list[#list + 1] = fn
end

-- Developer simulation: run the handlers for a game event as if the client fired it.
function Events.Simulate(event, ...)
    local list = gameHandlers[event]
    if not list then return end
    for i = 1, #list do
        list[i](event, ...)
    end
end

-- Internal messages (DB_READY, COMM_RECEIVED, PING_RESULT, ...).
function Events.On(name, fn)
    local list = busHandlers[name]
    if not list then
        list = {}
        busHandlers[name] = list
    end
    list[#list + 1] = fn
end

function Events.Fire(name, ...)
    local list = busHandlers[name]
    if not list then return end
    for i = 1, #list do
        list[i](...)
    end
end
