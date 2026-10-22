-- FairShare/UI/Skin.lua
-- Theme selection. Every widget asks Skin for its colors and backdrops, so a theme touches
-- one place. Themes:
--   blizzard  the game's own frame art (ButtonFrameTemplate window, UIPanelButton buttons,
--             InputBox edit boxes, tooltip-style panels, gold labels). The default.
--   dark      flat dark panels with a bronze accent.
--   midnight  flat dark panels with a violet accent.
--   elvui     flat, borrowing ElvUI's media and colors when ElvUI is loaded.
-- "auto" picks elvui when ElvUI is loaded, otherwise blizzard. Decided once per session.
local ADDON, ns = ...

local Skin = {}
ns.UI.Skin = Skin

local WHITE = "Interface\\Buttons\\WHITE8x8"

local function copy(t)
    local c = {}
    for k, v in pairs(t) do
        if type(v) == "table" then c[k] = copy(v) else c[k] = v end
    end
    return c
end

local FLAT_BACKDROP = { bgFile = WHITE, edgeFile = WHITE, edgeSize = 1 }
local TOOLTIP_BACKDROP = {
    bgFile = "Interface\\Tooltips\\UI-Tooltip-Background",
    edgeFile = "Interface\\Tooltips\\UI-Tooltip-Border",
    edgeSize = 12, tile = true, tileSize = 12,
    insets = { left = 3, right = 3, top = 3, bottom = 3 },
}

local DARK = {
    key = "dark",
    name = "Dark",
    kind = "flat",                   -- flat: backdrop widgets; blizzard: game templates
    font = nil,                      -- nil = keep the game's font object
    fontSize = 12,
    bg = { 0.07, 0.07, 0.08, 0.95 },
    border = { 0.22, 0.22, 0.25, 1 },
    panel = { 0.11, 0.11, 0.13, 1 },
    button = { 0.16, 0.16, 0.19, 1 },
    buttonHover = { 0.22, 0.22, 0.26, 1 },
    buttonBorder = { 0.30, 0.30, 0.34, 1 },
    accent = { 0.82, 0.62, 0.36, 1 },   -- bronze, from the FairShare emblem
    text = { 0.90, 0.90, 0.90, 1 },
    muted = { 0.60, 0.60, 0.62, 1 },
    good = { 0.40, 0.85, 0.45, 1 },
    bad = { 0.90, 0.35, 0.35, 1 },
    backdrop = FLAT_BACKDROP,        -- windows, panels, menus
    thinBackdrop = FLAT_BACKDROP,    -- bars, sliders, small checks
}

local MIDNIGHT = copy(DARK)
MIDNIGHT.key, MIDNIGHT.name = "midnight", "Midnight"
MIDNIGHT.bg = { 0.06, 0.05, 0.09, 0.95 }
MIDNIGHT.panel = { 0.10, 0.09, 0.14, 1 }
MIDNIGHT.button = { 0.15, 0.13, 0.21, 1 }
MIDNIGHT.buttonHover = { 0.22, 0.19, 0.30, 1 }
MIDNIGHT.border = { 0.26, 0.22, 0.34, 1 }
MIDNIGHT.buttonBorder = { 0.36, 0.30, 0.46, 1 }
MIDNIGHT.accent = { 0.64, 0.48, 0.92, 1 }

local BLIZZARD = copy(DARK)
BLIZZARD.key, BLIZZARD.name, BLIZZARD.kind = "blizzard", "Blizzard", "blizzard"
BLIZZARD.bg = { 1, 1, 1, 1 }
BLIZZARD.panel = { 0.09, 0.09, 0.11, 0.92 }
BLIZZARD.button = { 0.12, 0.12, 0.14, 1 }
BLIZZARD.buttonHover = { 0.20, 0.20, 0.24, 1 }
BLIZZARD.border = { 0.85, 0.85, 0.85, 1 }
BLIZZARD.buttonBorder = { 0.85, 0.85, 0.85, 1 }
BLIZZARD.accent = { 1.00, 0.82, 0.00, 1 }   -- the game's gold
BLIZZARD.text = { 1, 1, 1, 1 }
BLIZZARD.muted = { 0.86, 0.72, 0.26, 1 }    -- gold labels, like the quest log
BLIZZARD.backdrop = TOOLTIP_BACKDROP
BLIZZARD.thinBackdrop = FLAT_BACKDROP

local THEMES = { blizzard = BLIZZARD, dark = DARK, midnight = MIDNIGHT }

local function rgba(t, a)
    return { t[1] or t.r or 0, t[2] or t.g or 0, t[3] or t.b or 0, t[4] or t.a or a or 1 }
end

local function elvuiTheme()
    local E = _G.ElvUI and _G.ElvUI[1]
    if not E or type(E.media) ~= "table" then return nil end
    local m = E.media
    local t = copy(DARK)
    t.key, t.name = "elvui", "ElvUI"
    if type(m.normFont) == "string" then t.font = m.normFont end
    if E.db and E.db.general and tonumber(E.db.general.fontSize) then t.fontSize = E.db.general.fontSize end
    if type(m.backdropfadecolor) == "table" then t.bg = rgba(m.backdropfadecolor, 0.9) end
    if type(m.backdropcolor) == "table" then
        t.panel = rgba(m.backdropcolor)
        t.button = rgba(m.backdropcolor)
    end
    if type(m.bordercolor) == "table" then
        t.border = rgba(m.bordercolor)
        t.buttonBorder = rgba(m.bordercolor)
    end
    if type(m.rgbvaluecolor) == "table" then t.accent = rgba(m.rgbvaluecolor) end
    t.buttonHover = { t.button[1] + 0.08, t.button[2] + 0.08, t.button[3] + 0.08, 1 }
    return t
end

-- The game's frame templates exist only in the real client; the offline harness has none,
-- so the blizzard theme falls back to flat drawing there.
local function templatesAvailable()
    return type(_G.ButtonFrameTemplate_HidePortrait) == "function"
end

local current

-- The active theme table. Decided once per session from settings.theme.
function Skin.Current()
    if current then return current end
    local want = (ns.db and ns.db.settings and ns.db.settings.theme) or "auto"
    if want == "elvui" or want == "auto" then current = elvuiTheme() end
    if not current and want ~= "elvui" and THEMES[want] then current = THEMES[want] end
    current = current or BLIZZARD
    if current.kind == "blizzard" and not templatesAvailable() then
        current = copy(current)
        current.kind = "flat"
        current.bg = DARK.bg
        current.backdrop = FLAT_BACKDROP
    end
    return current
end

function Skin.IsBlizzard()
    return Skin.Current().kind == "blizzard"
end

function Skin.Options()
    return { "auto", "blizzard", "dark", "midnight", "elvui" }
end

function Skin.Label(key)
    if key == "auto" then
        local E = _G.ElvUI and _G.ElvUI[1]
        return "Auto (" .. (E and "ElvUI" or "Blizzard") .. ")"
    end
    if key == "elvui" then return "ElvUI" .. (_G.ElvUI and "" or " (not loaded)") end
    return THEMES[key] and THEMES[key].name or key
end

-- style: nil for panels and windows, "thin" for bars, sliders and small checks.
function Skin.Backdrop(frame, bg, border, style)
    local t = Skin.Current()
    if not frame.SetBackdrop then return end
    frame:SetBackdrop(style == "thin" and t.thinBackdrop or t.backdrop)
    frame:SetBackdropColor(unpack(bg or t.bg))
    frame:SetBackdropBorderColor(unpack(border or t.border))
end

function Skin.Font(fs, size, color)
    local t = Skin.Current()
    size = size or t.fontSize
    if t.font then
        fs:SetFont(t.font, size, "")
    else
        local path, _, flags = fs:GetFont()
        if path then fs:SetFont(path, size, flags or "") end
    end
    local c = color or t.text
    fs:SetTextColor(c[1], c[2], c[3], c[4] or 1)
end
