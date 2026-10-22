-- FairShare/Core/Launcher.lua
-- The three ways into the window, since there are no slash commands:
-- a draggable minimap button, the addon compartment menu, and a keybinding.
local ADDON, ns = ...

local Launcher = {}
ns.Core.Launcher = Launcher

local RADIUS = 80
local ICON = "Interface\\AddOns\\FairShare\\Media\\icon"
local button

local function settings()
    return ns.db.settings.minimap
end

local function updatePosition()
    local angle = math.rad(settings().angle or 220)
    button:ClearAllPoints()
    button:SetPoint("CENTER", Minimap, "CENTER", RADIUS * math.cos(angle), RADIUS * math.sin(angle))
end

local function onDragUpdate()
    local mx, my = Minimap:GetCenter()
    local cx, cy = GetCursorPosition()
    local scale = Minimap:GetEffectiveScale()
    cx, cy = cx / scale, cy / scale
    settings().angle = math.deg(math.atan2(cy - my, cx - mx))
    updatePosition()
end

function Launcher.CreateMinimapButton()
    if button then return button end
    button = CreateFrame("Button", "FairShareMinimapButton", Minimap)
    button:SetSize(31, 31)
    button:SetFrameStrata("MEDIUM")
    button:SetFrameLevel(8)
    button:RegisterForClicks("AnyUp")
    button:RegisterForDrag("LeftButton")
    button:SetHighlightTexture("Interface\\Minimap\\UI-Minimap-ZoomButton-Highlight")

    local overlay = button:CreateTexture(nil, "OVERLAY")
    overlay:SetSize(53, 53)
    overlay:SetTexture("Interface\\Minimap\\MiniMap-TrackingBorder")
    overlay:SetPoint("TOPLEFT")

    local icon = button:CreateTexture(nil, "BACKGROUND")
    icon:SetSize(20, 20)
    icon:SetTexture(ICON)
    icon:SetPoint("TOPLEFT", 7, -5)
    button.icon = icon

    button:SetScript("OnClick", function() ns.UI.Toggle() end)
    button:SetScript("OnDragStart", function(self) self:SetScript("OnUpdate", onDragUpdate) end)
    button:SetScript("OnDragStop", function(self) self:SetScript("OnUpdate", nil) end)
    button:SetScript("OnEnter", function(self)
        GameTooltip:SetOwner(self, "ANCHOR_LEFT")
        GameTooltip:SetText("FairShare")
        GameTooltip:AddLine("Click to open. Drag to move.", 1, 1, 1)
        GameTooltip:Show()
    end)
    button:SetScript("OnLeave", function() GameTooltip:Hide() end)

    updatePosition()
    if settings().hide then button:Hide() end
    return button
end

function Launcher.SetMinimapShown(shown)
    settings().hide = not shown
    if not button then return end
    if shown then button:Show() else button:Hide() end
end

ns.Core.Events.Register("PLAYER_LOGIN", function()
    Launcher.CreateMinimapButton()
end)

-- Globals the game calls: keybinding (Bindings.xml) and the addon compartment (TOC).
function _G.FairShare_ToggleWindow()
    ns.UI.Toggle()
end

function _G.FairShare_OnCompartmentClick()
    ns.UI.Toggle()
end

_G.BINDING_HEADER_FAIRSHARE = "FairShare"
_G.BINDING_NAME_FAIRSHARE_TOGGLE = "Toggle the FairShare window"
