-- FairShare/UI/Main.lua
-- The one window. Panels register themselves (UI.RegisterPanel) and are built lazily on
-- first view. Leader-only panels show for the group leader (solo counts as leader);
-- dev-only panels show when Developer mode is ticked in Settings.
local ADDON, ns = ...

local UI = ns.UI
local Events = ns.Core.Events

local registry = {}   -- ordered { name, build, leaderOnly, devOnly, frame }
local window, tabs, currentName

function UI.RegisterPanel(name, build, opts)
    registry[#registry + 1] = {
        name = name, build = build,
        leaderOnly = opts and opts.leaderOnly or false,
        devOnly = opts and opts.devOnly or false,
    }
end

-- The built frame of a panel (nil until it has been shown once). For the test harness.
function UI.PanelFrame(name)
    for _, p in ipairs(registry) do
        if p.name == name then return p.frame end
    end
end

function UI.IsLeader()
    return ns.Core.Group.IsLeader()
end

local function visiblePanels()
    local out, leader = {}, UI.IsLeader()
    local dev = ns.db and ns.db.settings and ns.db.settings.dev
    for _, p in ipairs(registry) do
        if (not p.leaderOnly or leader) and (not p.devOnly or dev) then out[#out + 1] = p end
    end
    return out
end

-- Comma-joined names of the tabs currently offered (test suite and Settings use it).
function UI.TabNames()
    local names = {}
    for i, p in ipairs(visiblePanels()) do names[i] = p.name end
    return table.concat(names, ",")
end

local function findPanel(name)
    for _, p in ipairs(registry) do
        if p.name == name then return p end
    end
end

local function showPanel(name)
    local p = findPanel(name)
    if not p then return end
    for _, q in ipairs(registry) do
        if q.frame and q ~= p then q.frame:Hide() end
    end
    if not p.frame then
        p.frame = CreateFrame("Frame", nil, window.content)
        p.frame:SetPoint("TOPLEFT", 8, -8)
        p.frame:SetPoint("BOTTOMRIGHT", -8, 8)
        p.build(p.frame)
    end
    p.frame:Show()
    if p.frame.OnShow then p.frame.OnShow() end
    currentName = name
end

function UI.RefreshTabs()
    if not window then return end
    local names = {}
    for i, p in ipairs(visiblePanels()) do names[i] = p.name end
    tabs:SetNames(names)
    local idx = 1
    for i, n in ipairs(names) do
        if n == currentName then idx = i end
    end
    tabs:Select(idx)
end

function UI.Build()
    if window then return window end
    local W = UI.Widgets
    window = W.Window("FairShareMainWindow", "FairShare", 560, 376, { header = true })
    tabs = W.Tabs(window.header, function(name) showPanel(name) end)
    tabs:SetPoint("TOPLEFT", 0, 0)
    tabs:SetPoint("TOPRIGHT", 0, 0)
    table.insert(UISpecialFrames, "FairShareMainWindow") -- Esc closes it
    UI.RefreshTabs()
    return window
end

function UI.Show(name)
    UI.Build()
    if name then currentName = name end
    UI.RefreshTabs()
    window:Show()
end

function UI.Hide()
    if window then window:Hide() end
end

function UI.Toggle()
    if window and window:IsShown() then window:Hide() else UI.Show() end
end

function UI.Window()
    return window
end

function UI.CurrentPanel()
    return currentName
end

Events.Register("PLAYER_LOGIN", function() UI.Build() end)
Events.Register("GROUP_ROSTER_UPDATE", function()
    if window and window:IsShown() then UI.RefreshTabs() end
end)
Events.On("GROUP_CHANGED", function()
    if window and window:IsShown() then UI.RefreshTabs() end
end)
Events.On("DEV_MODE", function()
    if window then UI.RefreshTabs() end
end)
