-- FairShare/UI/Widgets.lua
-- The one factory every panel builds from: Window, Label, Button, EditBox, Checkbox, List,
-- Tabs, Field, Bar, Slider, Dropdown, Check, VirtualList, Confirm, ItemSlot, ItemLabel,
-- Separator, TextPopup. Nothing here knows about auctions. The theme decides whether a
-- widget is drawn flat (backdrops) or from the game's own templates (Skin.IsBlizzard()).
-- Text is never left to trail off: anything cut short shows the full text on hover.
local ADDON, ns = ...

local W = {}
ns.UI.Widgets = W
local Skin = ns.UI.Skin

local WHITE = "Interface\\Buttons\\WHITE8x8"
local ICON = "Interface\\AddOns\\FairShare\\Media\\icon128"

W.LABEL_COLUMN = 120   -- width of the label column in W.Field rows; panels align to it
W.PORTRAIT = { x = -7, y = 11, size = 64 }   -- where the icon sits on the Blizzard frame's portrait ring

-- A child the template may or may not provide (the offline stubs answer every unknown key
-- with a function, so only a table counts).
local function child(f, key)
    local v = f[key]
    return type(v) == "table" and v or nil
end

-- Every label is registered so W.AuditText can find text that is cut short or runs past
-- its window. Rule: no text is clipped or drawn outside its window; long text
-- is bounded on the right and either wraps into a sized area or shows in full on hover.
W.labels = setmetatable({}, { __mode = "k" })
local labelSeq = 0

function W.Label(parent, text, size, color, justify)
    local fs = parent:CreateFontString(nil, "OVERLAY", "GameFontHighlight")
    Skin.Font(fs, size, color)
    fs:SetText(text or "")
    if justify then fs:SetJustifyH(justify) end
    labelSeq = labelSeq + 1
    W.labels[fs] = { id = labelSeq, parent = parent, where = (debugstack and debugstack(2, 1, 0) or ""):match("[^\n]*") or "" }
    return fs
end

-- The window a region ultimately sits in (the first ancestor with no parent but UIParent).
local function topWindow(region)
    local f = region:GetParent()
    while f and f.GetParent and f:GetParent() and f:GetParent() ~= UIParent do f = f:GetParent() end
    return f
end

-- In-game check: every visible label that is cut short, or that ends past its window's
-- right edge. Returns a list of "<where>: <reason>: <text>" strings; empty is clean.
function W.AuditText()
    local out = {}
    for fs, info in pairs(W.labels) do
        if fs:IsVisible() and (fs:GetText() or "") ~= "" then
            local win = topWindow(fs)
            local right, winRight = fs:GetRight(), win and win:GetRight()
            if fs.IsTruncated and fs:IsTruncated() then
                out[#out + 1] = ("%s: cut short: %s"):format(info.where, fs:GetText())
            elseif right and winRight and right > winRight + 1 then
                out[#out + 1] = ("%s: past the window edge by %dpx: %s"):format(info.where, right - winRight, fs:GetText())
            end
        end
    end
    table.sort(out)
    return out
end

-- Structural check that works without a renderer (the offline suite): a label holding
-- more than a few words must be bounded on the right, by a width or a right-side anchor,
-- so it can be cut and shown on hover rather than overflow. Returns a list of offenders.
function W.AuditStructure()
    local out = {}
    for fs, info in pairs(W.labels) do
        local text = fs:GetText() or ""
        if #text > 24 then
            local bounded = (fs.GetWidth and (fs:GetWidth() or 0) > 0)
            if not bounded and fs.GetNumPoints then
                for i = 1, fs:GetNumPoints() or 0 do
                    local point = fs:GetPoint(i)
                    if type(point) == "string" and point:find("RIGHT", 1, true) then bounded = true end
                end
            end
            if not bounded then out[#out + 1] = ("%s: unbounded: %s"):format(info.where, text:sub(1, 60)) end
        end
    end
    table.sort(out)
    return out
end

-- Full text in a tooltip when the font string is cut short. fs may be a function(frame)
-- returning the font string. The frame must take mouse input (buttons do; frames need
-- EnableMouse). Returns the frame.
function W.HoverFull(frame, fs)
    frame:SetScript("OnEnter", function(self)
        local s = fs
        if type(s) == "function" then s = s(self) end
        if type(s) ~= "table" or not s.IsTruncated or not s:IsTruncated() then return end
        GameTooltip:SetOwner(self, "ANCHOR_TOP")
        GameTooltip:SetText(s:GetText() or "", 1, 1, 1, 1, true)
        GameTooltip:Show()
    end)
    frame:SetScript("OnLeave", function() GameTooltip:Hide() end)
    return frame
end

-- An invisible mouse region over a font string so W.HoverFull can watch it.
local function hoverRegion(parent, fs)
    local hit = CreateFrame("Frame", nil, parent)
    hit:SetAllPoints(fs)
    hit:EnableMouse(true)
    W.HoverFull(hit, fs)
    return hit
end

function W.Button(parent, text, width, height, onClick)
    local t = Skin.Current()
    local b
    if Skin.IsBlizzard() then
        b = CreateFrame("Button", nil, parent, "UIPanelButtonTemplate")
        b:SetSize(width or 100, height or 24)
        b:SetText(text or "")
        b.label = child(b, "Text") or b:GetFontString() or W.Label(b, text, nil, t.text)
        b:SetScript("OnClick", function(self) if self.enabled ~= false and onClick then onClick(self) end end)
        function b:SetLabel(s) self:SetText(s) end
        local setEnabled = b.SetEnabled
        function b:SetEnabled(on)
            self.enabled = on and true or false
            setEnabled(self, self.enabled)
        end
        return b
    end
    b = CreateFrame("Button", nil, parent, "BackdropTemplate")
    b:SetSize(width or 100, height or 24)
    Skin.Backdrop(b, t.button, t.buttonBorder)
    b.label = W.Label(b, text, nil, t.text)
    b.label:SetPoint("CENTER")
    b:SetScript("OnEnter", function(self) if self.enabled ~= false then self:SetBackdropColor(unpack(t.buttonHover)) end end)
    b:SetScript("OnLeave", function(self) self:SetBackdropColor(unpack(t.button)) end)
    b:SetScript("OnClick", function(self) if self.enabled ~= false and onClick then onClick(self) end end)
    function b:SetLabel(s) self.label:SetText(s) end
    function b:SetEnabled(on)
        self.enabled = on
        self:SetAlpha(on and 1 or 0.4)
    end
    return b
end

-- Marks a button as the selected one in a group (tabs). Flat: accent border and text;
-- Blizzard: held down.
function W.SetSelected(b, on)
    local t = Skin.Current()
    if Skin.IsBlizzard() then
        -- Gold text and the held highlight; never the pushed state, which drops the text.
        if on then
            if b.LockHighlight then b:LockHighlight() end
        elseif b.UnlockHighlight then
            b:UnlockHighlight()
        end
        if b.label then b.label:SetTextColor(unpack(on and t.accent or t.text)) end
        return
    end
    b:SetBackdropBorderColor(unpack(on and t.accent or t.buttonBorder))
    b.label:SetTextColor(unpack(on and t.accent or t.text))
end

function W.EditBox(parent, width, height, multiline)
    local t = Skin.Current()
    local e
    if Skin.IsBlizzard() and not multiline then
        e = CreateFrame("EditBox", nil, parent, "InputBoxTemplate")
        e:SetSize(width or 200, height or 24)
        e:SetAutoFocus(false)
        e:SetScript("OnEscapePressed", function(self) self:ClearFocus() end)
        return e
    end
    e = CreateFrame("EditBox", nil, parent, "BackdropTemplate")
    e:SetSize(width or 200, height or 24)
    e:SetAutoFocus(false)
    e:SetFontObject("ChatFontNormal")
    e:SetTextInsets(6, 6, 3, 3)
    Skin.Backdrop(e, t.panel, t.border)
    if multiline then e:SetMultiLine(true) end
    e:SetScript("OnEscapePressed", function(self) self:ClearFocus() end)
    return e
end

function W.Checkbox(parent, text, onChange)
    local c = CreateFrame("CheckButton", nil, parent, "UICheckButtonTemplate")
    c:SetSize(24, 24)
    c.label = W.Label(c, text)
    c.label:SetPoint("LEFT", c, "RIGHT", 4, 0)
    c.label:SetPoint("RIGHT", parent, "RIGHT", 0, 0)
    c.label:SetJustifyH("LEFT")
    c.label:SetWordWrap(false)
    c.label:SetMaxLines(1)
    W.HoverFull(c, c.label)
    c:SetScript("OnClick", function(self)
        if onChange then onChange(self:GetChecked() and true or false) end
    end)
    return c
end

-- A fixed number of single-line text rows. list:SetRows({ "a", "b" }) fills from the top and
-- blanks the rest. Rows never wrap; a row that is cut short shows the whole line on hover.
function W.List(parent, maxRows, rowHeight, rightWidth)
    local t = Skin.Current()
    maxRows, rowHeight = maxRows or 10, rowHeight or 16
    local f = CreateFrame("Frame", nil, parent)
    f:SetHeight(maxRows * rowHeight)
    f.rows = {}
    for i = 1, maxRows do
        local fs = W.Label(f, "", nil, t.text, "LEFT")
        fs:SetPoint("TOPLEFT", 0, -(i - 1) * rowHeight)
        fs:SetPoint("RIGHT")
        fs:SetHeight(rowHeight)
        fs:SetWordWrap(false)
        fs:SetMaxLines(1)
        fs.hit = hoverRegion(f, fs)
        -- Optional right-hand column (row.right) so values line up whatever the font.
        fs.right = W.Label(f, "", nil, t.text, "RIGHT")
        fs.right:SetPoint("TOPRIGHT", 0, -(i - 1) * rowHeight)
        fs.right:SetHeight(rowHeight)
        fs.right:SetWidth(rightWidth or 150)
        fs.right:SetWordWrap(false)
        fs.right:SetMaxLines(1)
        f.rows[i] = fs
    end
    -- Empty rows take no mouse input, so a short list never sits over what is below it.
    function f:SetRows(rows)
        for i = 1, maxRows do
            local r = rows[i]
            local fs = self.rows[i]
            fs.right:SetText("")
            fs:ClearAllPoints()
            fs:SetPoint("TOPLEFT", 0, -(i - 1) * rowHeight)
            fs:SetPoint("RIGHT")
            if type(r) == "table" then
                fs:SetText(r.text or "")
                local c = r.color or t.text
                fs:SetTextColor(c[1], c[2], c[3], c[4] or 1)
                if r.right then
                    fs.right:SetText(r.right)
                    fs.right:SetTextColor(c[1], c[2], c[3], c[4] or 1)
                    fs:SetPoint("RIGHT", -((rightWidth or 150) + 6), 0)
                end
            else
                fs:SetText(r or "")
                fs:SetTextColor(t.text[1], t.text[2], t.text[3], 1)
            end
            if (fs:GetText() or "") == "" then fs.hit:Hide() else fs.hit:Show() end
        end
    end
    return f
end

-- Tab bar. bar:SetNames({...}) rebuilds the buttons; bar:Select(i) highlights one and calls
-- onSelect(name, i). Buttons share the bar's width equally so they never run past the window.
function W.Tabs(parent, onSelect)
    local GAP = 4
    local bar = CreateFrame("Frame", nil, parent)
    bar:SetHeight(26)
    bar.buttons, bar.names = {}, {}
    local function layout(self)
        local n = #self.names
        if n == 0 then return end
        local total = self:GetWidth() or 0
        local w = total > 0 and math.floor((total - GAP * (n - 1)) / n) or 90
        local x = 0
        for i = 1, n do
            local b = self.buttons[i]
            b:SetWidth(w)
            b:ClearAllPoints()
            b:SetPoint("LEFT", x, 0)
            x = x + w + GAP
        end
    end
    bar:SetScript("OnSizeChanged", layout)
    function bar:SetNames(names)
        self.names = names
        for i, name in ipairs(names) do
            local b = self.buttons[i]
            if not b then
                b = W.Button(self, name, 90, 24, function() self:Select(i) end)
                self.buttons[i] = b
            end
            b:SetLabel(name)
            b:Show()
        end
        for i = #names + 1, #self.buttons do self.buttons[i]:Hide() end
        layout(self)
    end
    function bar:Select(i)
        self.selected = i
        for j, b in ipairs(self.buttons) do W.SetSelected(b, j == i) end
        if onSelect and self.names[i] then onSelect(self.names[i], i) end
    end
    return bar
end

-- Aligned "Label:   value" row. Labels sit in a W.LABEL_COLUMN column so every panel lines up.
-- rightInset (optional) keeps the value clear of a button on the right. Values never wrap;
-- a value that is cut short shows in full on hover.
function W.Field(parent, label, y, valueColor, rightInset)
    local t = Skin.Current()
    local l = W.Label(parent, label, nil, t.muted, "LEFT")
    l:SetPoint("TOPLEFT", 0, y)
    l:SetWidth(W.LABEL_COLUMN)
    local v = W.Label(parent, "", nil, valueColor or t.text, "LEFT")
    v:SetPoint("TOPLEFT", W.LABEL_COLUMN + 4, y)
    v:SetPoint("RIGHT", -(rightInset or 0), 0)
    v:SetWordWrap(false)
    v:SetMaxLines(1)
    v.label = l
    hoverRegion(parent, v)
    return v
end

-- Horizontal progress bar with a caption: the game's status bar texture in the Blizzard theme,
-- a flat fill otherwise. bar:SetFraction(0..1), bar:SetColor({r,g,b,a}), bar:SetText(s).
function W.Bar(parent, width, height)
    local t = Skin.Current()
    local f = CreateFrame("StatusBar", nil, parent, "BackdropTemplate")
    f:SetSize(width or 200, height or 6)
    Skin.Backdrop(f, t.panel, t.border, "thin")
    f:SetStatusBarTexture(Skin.IsBlizzard() and "Interface\\TargetingFrame\\UI-StatusBar" or WHITE)
    f:SetStatusBarColor(unpack(t.accent))
    f:SetMinMaxValues(0, 1)
    f:SetValue(0)
    f.caption = W.Label(f, "", 11, t.text)
    f.caption:SetPoint("CENTER")
    function f:SetFraction(fr) self:SetValue(math.max(0, math.min(1, fr or 0))) end
    function f:SetColor(c) self:SetStatusBarColor(c[1], c[2], c[3], c[4] or 1) end
    function f:SetText(s) self.caption:SetText(s or "") end
    return f
end

-- Horizontal integer slider with an optional sticky point. s:SetRange(min, max, stick) sets the
-- bounds and the value the thumb snaps to when dragged near it (nil for none). The track is
-- skewed: min..stick takes W.SLIDER_STICK_FRACTION of its length and stick..max the rest, and
-- the part past the sticky point is tinted as debt with a tick at the cut-off. s:SetValue(v)
-- moves it without firing onChange; dragging calls onChange(value). s:GetValue() returns the
-- value, not the position. In the Blizzard theme this is the game's own stepper slider.
W.SLIDER_STICK_FRACTION = 0.8
local SLIDER_STEPS = 1000

function W.Slider(parent, width, height, onChange)
    local t = Skin.Current()
    width = width or 200
    local s, track, thumbWidth
    local blizzard = Skin.IsBlizzard() and type(_G.MinimalSliderWithSteppersMixin) == "table"
    if blizzard then
        s = CreateFrame("Frame", nil, parent, "MinimalSliderWithSteppersTemplate")
        s:SetSize(width, 20)
        track = child(s, "Slider")
        if track and s.Init then s:Init(0, 0, SLIDER_STEPS, SLIDER_STEPS, {}) end
        thumbWidth = 20
    end
    if not track then
        blizzard = false
        height = height or 14
        s = CreateFrame("Slider", nil, parent, "BackdropTemplate")
        s:SetSize(width, height)
        s:SetOrientation("HORIZONTAL")
        Skin.Backdrop(s, t.panel, t.border, "thin")
        s:SetThumbTexture(WHITE)
        local thumb = s.GetThumbTexture and s:GetThumbTexture()
        if type(thumb) == "table" then
            thumb:SetSize(10, height)
            thumb:SetVertexColor(unpack(t.accent))
        end
        s:SetValueStep(1)
        s:SetObeyStepOnDrag(true)
        s:SetMinMaxValues(0, SLIDER_STEPS)
        track = s
        thumbWidth = 10
    end
    s.track = track
    s.debt = track:CreateTexture(nil, "BACKGROUND")
    s.debt:SetTexture(WHITE)
    s.debt:SetVertexColor(t.bad[1], t.bad[2], t.bad[3], 0.25)
    s.debt:Hide()
    s.mark = track:CreateTexture(nil, "OVERLAY")
    s.mark:SetTexture(WHITE)
    s.mark:SetVertexColor(unpack(t.accent))
    s.mark:SetSize(2, blizzard and 14 or (height or 14))
    s.mark:Hide()

    -- Range state lives in a closure, not on the frame, so a missing field is never mistaken
    -- for a method (the offline stubs answer every unknown key with a no-op function).
    local st = { min = 0, max = 100, stick = nil, quiet = false }
    function s:GetStick() return st.stick end

    -- Value <-> thumb position. Two linear pieces meeting at the sticky point.
    local function knee() return math.floor(SLIDER_STEPS * W.SLIDER_STICK_FRACTION + 0.5) end
    local function toPos(v)
        v = math.max(st.min, math.min(st.max, v))
        if st.stick then
            local k = knee()
            if v <= st.stick then return k * (v - st.min) / (st.stick - st.min) end
            return k + (SLIDER_STEPS - k) * (v - st.stick) / (st.max - st.stick)
        end
        if st.max <= st.min then return 0 end
        return SLIDER_STEPS * (v - st.min) / (st.max - st.min)
    end
    local function toVal(p)
        p = math.max(0, math.min(SLIDER_STEPS, p or 0))
        local v
        if st.stick then
            local k = knee()
            if p <= k then v = st.min + (st.stick - st.min) * p / k
            else v = st.stick + (st.max - st.stick) * (p - k) / (SLIDER_STEPS - k) end
        else
            v = st.min + (st.max - st.min) * p / SLIDER_STEPS
        end
        return math.max(st.min, math.min(st.max, math.floor(v + 0.5)))
    end

    function s:SetRange(min, max, stick)
        st.min, st.max = min, math.max(min, max)
        st.stick = (stick and stick > min and stick < st.max) and stick or nil
        if st.stick then
            local w = track:GetWidth() or 0
            local x = thumbWidth / 2 + math.max(0, w - thumbWidth) * knee() / SLIDER_STEPS
            self.mark:ClearAllPoints()
            self.mark:SetPoint("CENTER", track, "LEFT", x, 0)
            self.mark:Show()
            self.debt:ClearAllPoints()
            self.debt:SetPoint("TOPLEFT", track, "TOPLEFT", x, blizzard and -4 or -1)
            self.debt:SetPoint("BOTTOMRIGHT", track, "BOTTOMRIGHT", -thumbWidth / 2, blizzard and 4 or 1)
            self.debt:Show()
        else
            self.mark:Hide()
            self.debt:Hide()
        end
    end

    local setValue, getValue = track.SetValue, track.GetValue
    function s:SetValue(v)
        st.quiet = true
        setValue(track, toPos(tonumber(v) or st.min))
        st.quiet = false
    end
    function s:GetValue()
        return toVal(getValue(track))
    end
    function s:SetEnabled(on)
        if track.SetEnabled then track:SetEnabled(on) end
    end

    local function changed(pos, userInput)
        if st.quiet then return end
        local value = toVal(pos)
        if userInput ~= false and st.stick and math.abs((pos or 0) - knee()) <= SLIDER_STEPS * 0.04 and value ~= st.stick then
            value = st.stick
            s:SetValue(value)
        end
        if onChange then onChange(value, userInput ~= false) end
    end
    if blizzard then
        -- The game's slider reports through its mixin; the steppers move one point, not one
        -- thousandth of the track.
        if s.RegisterCallback and MinimalSliderWithSteppersMixin.Event then
            s:RegisterCallback(MinimalSliderWithSteppersMixin.Event.OnValueChanged, function(_, value, userInput) changed(value, userInput) end, s)
        else
            track:HookScript("OnValueChanged", function(_, value, userInput) changed(value, userInput) end)
        end
        local function step(d)
            return function()
                s:SetValue(s:GetValue() + d)
                if onChange then onChange(s:GetValue(), true) end
            end
        end
        local back, forward = child(s, "Back"), child(s, "Forward")
        if back then back:SetScript("OnClick", step(-1)) end
        if forward then forward:SetScript("OnClick", step(1)) end
    else
        s:SetScript("OnValueChanged", function(_, pos, userInput) changed(pos, userInput) end)
    end
    return s
end

-- Dropdown: a button showing the current choice; click opens a list of options under it.
-- options = { { value = "x", label = "X" }, ... }. d:SetValue(v), d.value, d:SetOptions(opts).
-- Long lists scroll with the mouse wheel (a 40-player raid fits). Built from plain frames
-- so it never touches Blizzard's menu code (no taint, any skin).
function W.Dropdown(parent, width, options, onSelect)
    local t = Skin.Current()
    local ROW, MAX_VISIBLE = 20, 10
    width = width or 150
    local d = W.Button(parent, "", width, 22, function(self) self:Toggle() end)
    d.label:ClearAllPoints()
    d.label:SetPoint("LEFT", 8, 0)
    d.label:SetPoint("RIGHT", -18, 0)
    d.label:SetJustifyH("LEFT")
    d.arrow = W.Label(d, "v", 10, t.muted)
    d.arrow:SetPoint("RIGHT", -7, 0)

    local menu = CreateFrame("Frame", nil, d, "BackdropTemplate")
    menu:SetPoint("TOPLEFT", d, "BOTTOMLEFT", 0, -2)
    menu:SetWidth(width)
    Skin.Backdrop(menu, t.panel, t.buttonBorder)
    menu:SetFrameLevel((d:GetFrameLevel() or 1) + 20)
    menu:EnableMouseWheel(true)
    menu:Hide()
    d.menu = menu
    local st = { options = {}, offset = 0, value = nil }
    local rows = {}
    for i = 1, MAX_VISIBLE do
        local r = W.Button(menu, "", width - 4, ROW, function()
            local opt = st.options[st.offset + i]
            if not opt then return end
            d:SetValue(opt.value)
            menu:Hide()
            if onSelect then onSelect(opt.value) end
        end)
        r:SetPoint("TOPLEFT", 2, -2 - (i - 1) * ROW)
        r.label:ClearAllPoints()
        r.label:SetPoint("LEFT", 6, 0)
        r.label:SetPoint("RIGHT", -4, 0)
        r.label:SetWordWrap(false)
        r.label:SetMaxLines(1)
        rows[i] = r
    end
    local function render()
        local n = #st.options
        local visible = math.min(n, MAX_VISIBLE)
        st.offset = math.max(0, math.min(st.offset, n - visible))
        for i = 1, MAX_VISIBLE do
            local opt = i <= visible and st.options[st.offset + i]
            if opt then
                rows[i]:SetLabel(opt.label or tostring(opt.value))
                rows[i].label:SetTextColor(unpack(opt.value == st.value and t.accent or t.text))
                rows[i]:Show()
            else
                rows[i]:Hide()
            end
        end
        menu:SetHeight(visible * ROW + 4)
    end
    menu:SetScript("OnMouseWheel", function(_, delta)
        st.offset = st.offset - delta * 3
        render()
    end)

    function d:SetOptions(opts)
        st.options = opts or {}
        st.offset = 0
        self:SetValue(st.value)
    end
    function d:SetValue(v)
        st.value = v
        self.value = v
        local label = v == nil and "" or tostring(v)
        for _, o in ipairs(st.options) do
            if o.value == v then label = o.label or label end
        end
        self:SetLabel(label)
        render()
    end
    function d:GetValue() return st.value end
    function d:Toggle()
        if menu:IsShown() then menu:Hide() else st.offset = 0; render(); menu:Show() end
    end
    d:SetScript("OnHide", function() menu:Hide() end)
    d:SetOptions(options)
    return d
end

-- Small square checkbox that fits a 16px row. c:SetChecked(on), c:GetChecked(); onChange(on).
function W.Check(parent, size, onChange)
    local t = Skin.Current()
    size = size or 12
    local c = CreateFrame("Button", nil, parent, "BackdropTemplate")
    c:SetSize(size, size)
    Skin.Backdrop(c, t.panel, t.buttonBorder, "thin")
    c.mark = c:CreateTexture(nil, "ARTWORK")
    c.mark:SetTexture(WHITE)
    c.mark:SetPoint("TOPLEFT", 3, -3)
    c.mark:SetPoint("BOTTOMRIGHT", -3, 3)
    c.mark:SetVertexColor(unpack(t.accent))
    c.mark:Hide()
    local st = { checked = false }
    function c:SetChecked(on)
        st.checked = on and true or false
        if st.checked then self.mark:Show() else self.mark:Hide() end
    end
    function c:GetChecked() return st.checked end
    c:SetScript("OnClick", function(self)
        self:SetChecked(not st.checked)
        if onChange then onChange(st.checked) end
    end)
    return c
end

-- Virtual list: maxRows row frames reused over any number of items, scrolled with the
-- mouse wheel. buildRow(row) decorates a row once; bindRow(row, item, index) fills it.
-- list:SetItems(items), list:SetVisible(n), list:Render(), list:ScrollTo(index). A row
-- whose buildRow sets no OnEnter shows its row.text in full on hover when cut short.
function W.VirtualList(parent, maxRows, rowHeight, buildRow, bindRow)
    local f = CreateFrame("Frame", nil, parent)
    f:SetHeight(maxRows * rowHeight)
    f:EnableMouseWheel(true)
    local st = { items = {}, offset = 0, visible = maxRows }
    f.rows = {}
    for i = 1, maxRows do
        local r = CreateFrame("Button", nil, f)
        r:SetPoint("TOPLEFT", 0, -(i - 1) * rowHeight)
        r:SetPoint("RIGHT", 0, 0)
        r:SetHeight(rowHeight)
        buildRow(r)
        if not r:GetScript("OnEnter") then W.HoverFull(r, function(row) return row.text end) end
        r:Hide()
        f.rows[i] = r
    end
    function f:Render()
        local n = #st.items
        st.offset = math.max(0, math.min(st.offset, n - st.visible))
        for i = 1, maxRows do
            local it = i <= st.visible and st.items[st.offset + i] or nil
            if it then
                bindRow(self.rows[i], it, st.offset + i)
                self.rows[i]:Show()
            else
                self.rows[i]:Hide()
            end
        end
    end
    function f:SetItems(items)
        st.items = items or {}
        self:Render()
    end
    function f:Items() return st.items end
    function f:SetVisible(n)
        st.visible = math.max(1, math.min(maxRows, n))
        self:SetHeight(st.visible * rowHeight)
        self:Render()
    end
    function f:ScrollTo(index)
        st.offset = math.max(0, index - 1)
        self:Render()
    end
    f:SetScript("OnMouseWheel", function(self, delta)
        st.offset = st.offset - delta * 3
        self:Render()
    end)
    return f
end

-- Modal question with up to three answers. buttons = { { "Label", fn }, ... }; the last one
-- is the safe choice. One window, reused; it grows to fit the whole question.
local confirm
function W.Confirm(title, text, buttons)
    if not confirm then
        local t = Skin.Current()
        confirm = W.Window("FairShareConfirm", title, 440, 120)
        confirm:SetFrameStrata("FULLSCREEN_DIALOG")
        confirm.text = W.Label(confirm.content, "", nil, t.text, "LEFT")
        confirm.text:SetPoint("TOPLEFT", 14, -12)
        confirm.text:SetPoint("TOPRIGHT", -14, -12)
        confirm.text:SetJustifyV("TOP")
        confirm.text:SetWordWrap(true)
        confirm.buttons = {}
        confirm.actions = {}
        for i = 1, 3 do
            confirm.buttons[i] = W.Button(confirm.content, "", 130, 24, function()
                confirm:Hide()
                local fn = confirm.actions[i]
                if fn then fn() end
            end)
        end
        table.insert(UISpecialFrames, "FairShareConfirm")
    end
    confirm.title:SetText(title)
    confirm.text:SetHeight(0)
    confirm.text:SetText(text or "")
    local h = confirm.text.GetStringHeight and confirm.text:GetStringHeight()
    if type(h) ~= "number" or h < 16 then h = 48 end
    confirm.text:SetHeight(h)
    confirm:SetContentHeight(12 + h + 16 + 24 + 12)
    confirm.actions = {}
    local x = -14
    for i = 3, 1, -1 do
        local b, spec = confirm.buttons[i], buttons[i]
        if spec then
            b:SetLabel(spec[1])
            confirm.actions[i] = spec[2]
            b:ClearAllPoints()
            b:SetPoint("BOTTOMRIGHT", x, 12)
            x = x - 136
            b:Show()
        else
            b:Hide()
        end
    end
    confirm:Show()
end

-- Square drop target for an item. Drag an item from a bag onto it, or pick one up and click it.
-- Right-click clears it. slot:SetLink(link) / slot.link. Hover shows the item tooltip.
function W.ItemSlot(parent, size, onChange)
    local t = Skin.Current()
    size = size or 74
    local f = CreateFrame("Button", nil, parent, "BackdropTemplate")
    f:SetSize(size, size)
    Skin.Backdrop(f, t.panel, t.border)
    f:RegisterForClicks("LeftButtonUp", "RightButtonUp")
    f.icon = f:CreateTexture(nil, "ARTWORK")
    f.icon:SetPoint("TOPLEFT", 4, -4)
    f.icon:SetPoint("BOTTOMRIGHT", -4, 4)
    f.icon:Hide()
    f.hint = W.Label(f, "Drop\nitem\nhere", 11, t.muted)
    f.hint:SetPoint("CENTER")

    local function iconFor(link)
        if C_Item and C_Item.GetItemInfoInstant then
            local _, _, _, _, icon = C_Item.GetItemInfoInstant(link)
            if icon then return icon end
        end
        if GetItemIcon then return GetItemIcon(link) end
    end

    function f:SetLink(link)
        if link == "" then link = nil end
        self.link = link
        local icon = link and iconFor(link)
        if icon then
            self.icon:SetTexture(icon)
            self.icon:Show()
            self.hint:Hide()
        else
            self.icon:Hide()
            self.hint:Show()
            self.hint:SetText(link and "Item" or "Drop\nitem\nhere")
        end
        self:SetBackdropBorderColor(unpack(link and t.accent or t.border))
        if onChange then onChange(link) end
    end

    -- Whatever the cursor is holding, if it is an item.
    local function takeCursor(self)
        if not GetCursorInfo then return false end
        local kind, _, link = GetCursorInfo()
        if kind == "item" and link then
            self:SetLink(link)
            if ClearCursor then ClearCursor() end
            return true
        end
        return false
    end

    f:SetScript("OnReceiveDrag", function(self) takeCursor(self) end)
    f:SetScript("OnClick", function(self, button)
        if takeCursor(self) then return end
        if button == "RightButton" then self:SetLink(nil) end
    end)
    f:SetScript("OnEnter", function(self)
        if not self.link then return end
        GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
        GameTooltip:SetHyperlink(self.link)
        GameTooltip:Show()
    end)
    f:SetScript("OnLeave", function() GameTooltip:Hide() end)
    return f
end

-- Item link label with a tooltip on hover. f:SetLink(link). Clicking inserts into chat like any link.
function W.ItemLabel(parent, size)
    local f = CreateFrame("Button", nil, parent)
    f:SetHeight(18)
    f.text = W.Label(f, "", size, nil, "LEFT")
    f.text:SetPoint("LEFT")
    f.text:SetPoint("RIGHT")
    f.text:SetWordWrap(false)
    f.text:SetMaxLines(1)
    f:SetScript("OnEnter", function(self)
        if not self.link then return end
        GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
        GameTooltip:SetHyperlink(self.link)
        GameTooltip:Show()
    end)
    f:SetScript("OnLeave", function() GameTooltip:Hide() end)
    f:SetScript("OnClick", function(self)
        if self.link and ChatEdit_InsertLink then ChatEdit_InsertLink(self.link) end
    end)
    function f:SetLink(link)
        self.link = link
        self.text:SetText(link or "")
    end
    return f
end

function W.Separator(parent, y)
    local t = Skin.Current()
    local line = parent:CreateTexture(nil, "ARTWORK")
    line:SetTexture(WHITE)
    line:SetVertexColor(t.border[1], t.border[2], t.border[3], Skin.IsBlizzard() and 0.35 or 1)
    line:SetHeight(1)
    line:SetPoint("TOPLEFT", 0, y)
    line:SetPoint("TOPRIGHT", 0, y)
    return line
end

-- Singleton popup showing a long string selected for Ctrl-C (export) or accepting a paste (import).
local textPopup
function W.TextPopup(title, text, onAccept, acceptLabel)
    if not textPopup then
        local t = Skin.Current()
        textPopup = W.Window("FairShareTextPopup", title, 460, 232)
        textPopup:SetFrameStrata("FULLSCREEN_DIALOG")
        local scroll = CreateFrame("ScrollFrame", "FairShareTextPopupScroll", textPopup.content, "UIPanelScrollFrameTemplate")
        scroll:SetPoint("TOPLEFT", 10, -8)
        scroll:SetPoint("BOTTOMRIGHT", -30, 56)
        local box = W.EditBox(scroll, 400, 400, true)
        box:SetWidth(400)
        scroll:SetScrollChild(box)
        textPopup.box = box
        textPopup.hint = W.Label(textPopup.content, "", 11, t.muted, "LEFT")
        textPopup.hint:SetPoint("BOTTOMLEFT", 10, 10)
        textPopup.hint:SetPoint("RIGHT", -110, 0)
        textPopup.hint:SetHeight(40)
        textPopup.hint:SetJustifyV("BOTTOM")
        textPopup.hint:SetWordWrap(true)
        textPopup.accept = W.Button(textPopup.content, "OK", 90, 24, function()
            if textPopup.onAccept then textPopup.onAccept(textPopup.box:GetText()) end
            textPopup:Hide()
        end)
        textPopup.accept:SetPoint("BOTTOMRIGHT", -10, 8)
        table.insert(UISpecialFrames, "FairShareTextPopup")
    end
    textPopup.title:SetText(title)
    textPopup.onAccept = onAccept
    textPopup.box:SetText(text or "")
    if onAccept then
        textPopup.hint:SetText("Paste here, then press " .. (acceptLabel or "Import") .. ".")
        textPopup.accept:SetLabel(acceptLabel or "Import")
        textPopup.accept:Show()
    else
        local n = #(text or "")
        if n > 4000 then
            textPopup.hint:SetText(("Text is selected (%d characters). Ctrl-C, save it as a .txt and attach it with /fairshare-upload file:, since Discord cuts pasted text at 4000."):format(n))
        else
            textPopup.hint:SetText(("Text is selected (%d characters). Press Ctrl-C to copy."):format(n))
        end
        textPopup.accept:Hide()
    end
    textPopup:Show()
    textPopup.box:SetFocus()
    textPopup.box:HighlightText()
    return textPopup
end

-- A movable window with a title, a close button and f.content for the panel to fill.
-- width and contentHeight describe the content area; the title row and, in the Blizzard
-- theme, the frame art come on top. opts.header asks for a strip for the tab bar: in the
-- Blizzard theme it sits in the band beside the portrait, like the quest log's tabs; flat,
-- it is a row under the title. f:SetContentHeight(h) resizes keeping the chrome.
function W.Window(name, title, width, contentHeight, opts)
    local t = Skin.Current()
    local header = opts and opts.header
    local f
    if Skin.IsBlizzard() then
        f = CreateFrame("Frame", name, UIParent, "ButtonFrameTemplate")
        if ButtonFrameTemplate_HideButtonBar then ButtonFrameTemplate_HideButtonBar(f) end
        local top = header and 60 or 28
        f.chromeHeight = top + 4 + 10
        f:SetSize(width + 10, contentHeight + f.chromeHeight)
        local portraitContainer = child(f, "PortraitContainer")
        local portrait = portraitContainer and child(portraitContainer, "portrait")
        if header and portrait then
            -- The ring is part of the frame art at the top-left corner; the template places the
            -- portrait over it from the container's top-left. W.PORTRAIT tunes that by eye.
            portrait:SetTexture(ICON)
            portrait:SetTexCoord(0, 1, 0, 1)
            portrait:ClearAllPoints()
            portrait:SetPoint("TOPLEFT", portraitContainer, "TOPLEFT", W.PORTRAIT.x, W.PORTRAIT.y)
            portrait:SetSize(W.PORTRAIT.size, W.PORTRAIT.size)
        elseif ButtonFrameTemplate_HidePortrait then
            ButtonFrameTemplate_HidePortrait(f)
        end
        if f.SetTitle then f:SetTitle(title) end
        local titleContainer = child(f, "TitleContainer")
        f.title = (titleContainer and child(titleContainer, "TitleText")) or child(f, "TitleText")
        if not f.title then
            f.title = W.Label(f, title, 13, t.accent)
            f.title:SetPoint("TOP", 0, -5)
        end
        f.close = child(f, "CloseButton")
        if header then
            f.header = CreateFrame("Frame", nil, f)
            f.header:SetPoint("TOPLEFT", 66, -30)
            f.header:SetPoint("TOPRIGHT", -10, -30)
            f.header:SetHeight(26)
        end
        local inset = child(f, "Inset")
        if inset then
            inset:ClearAllPoints()
            inset:SetPoint("TOPLEFT", 4, -top)
            inset:SetPoint("BOTTOMRIGHT", -6, 6)
        end
        f.content = CreateFrame("Frame", nil, f)
        f.content:SetPoint("TOPLEFT", 8, -(top + 4))
        f.content:SetPoint("BOTTOMRIGHT", -10, 10)
    else
        f = CreateFrame("Frame", name, UIParent, "BackdropTemplate")
        f.chromeHeight = 28 + (header and 36 or 0)
        f:SetSize(width, contentHeight + f.chromeHeight)
        Skin.Backdrop(f)
        local bar = CreateFrame("Frame", nil, f, "BackdropTemplate")
        bar:SetPoint("TOPLEFT")
        bar:SetPoint("TOPRIGHT")
        bar:SetHeight(28)
        Skin.Backdrop(bar, t.panel, t.border)
        f.titleBar = bar
        f.title = W.Label(bar, title, 13, t.accent)
        f.title:SetPoint("LEFT", 10, 0)
        f.title:SetPoint("RIGHT", -56, 0)
        f.title:SetJustifyH("LEFT")
        f.title:SetWordWrap(false)
        f.title:SetMaxLines(1)
        f.close = W.Button(bar, "X", 22, 22, function() f:Hide() end)
        f.close:SetPoint("RIGHT", -3, 0)
        -- A capital glyph sits on the centre line; lowercase hangs below it. Lift it a pixel
        -- for the font's descent so it reads centred in the square.
        f.close.label:ClearAllPoints()
        f.close.label:SetPoint("CENTER", 0, 1)
        if header then
            f.header = CreateFrame("Frame", nil, f)
            f.header:SetPoint("TOPLEFT", 8, -34)
            f.header:SetPoint("TOPRIGHT", -8, -34)
            f.header:SetHeight(26)
        end
        f.content = CreateFrame("Frame", nil, f)
        f.content:SetPoint("TOPLEFT", 0, -f.chromeHeight)
        f.content:SetPoint("BOTTOMRIGHT")
    end
    f.minimized = false
    f.contentHeight = contentHeight
    function f:SetContentHeight(h)
        self.contentHeight = h
        if not self.minimized then self:SetHeight(h + self.chromeHeight) end
    end
    -- Minimized: only the title row stays, with whatever the owner puts in the title.
    function f:SetMinimized(on)
        self.minimized = on and true or false
        local inset = child(self, "Inset")
        if self.minimized then
            self.content:Hide()
            if self.header then self.header:Hide() end
            if inset then inset:Hide() end
            -- Enough of the frame art below the title to read as a closed panel, not a sliver.
            self:SetHeight(Skin.IsBlizzard() and 52 or 28)
        else
            self.content:Show()
            if self.header then self.header:Show() end
            if inset then inset:Show() end
            self:SetHeight(self.contentHeight + self.chromeHeight)
        end
        if self.OnMinimize then self.OnMinimize(self.minimized) end
    end
    if opts and opts.minimize then
        if Skin.IsBlizzard() then
            -- The game's own minimize glyph, sized and placed like the close button beside it.
            local m = CreateFrame("Button", nil, f)
            m:SetSize(16, 16)
            m:SetNormalTexture("Interface\\Buttons\\UI-MinusButton-Up")
            m:SetPushedTexture("Interface\\Buttons\\UI-MinusButton-Down")
            m:SetHighlightTexture("Interface\\Buttons\\UI-PlusButton-Hilight", "ADD")
            m:SetScript("OnClick", function() f:SetMinimized(not f.minimized) end)
            f.minimize = m
        else
            f.minimize = W.Button(f, "_", 20, 20, function() f:SetMinimized(not f.minimized) end)
        end
        if f.close then
            f.minimize:SetPoint("RIGHT", f.close, "LEFT", Skin.IsBlizzard() and -2 or -2, 0)
            if f.close.GetFrameLevel then f.minimize:SetFrameLevel((f.close:GetFrameLevel() or 1) + 1) end
        else
            f.minimize:SetPoint("TOPRIGHT", -28, -4)
            f.minimize:SetFrameLevel((f:GetFrameLevel() or 1) + 5)
        end
        f.minimize:Show()
    end
    f:SetPoint("CENTER")
    f:SetFrameStrata("DIALOG")
    f:SetMovable(true)
    f:EnableMouse(true)
    f:SetClampedToScreen(true)
    f:RegisterForDrag("LeftButton")
    f:SetScript("OnDragStart", function(self) self:StartMoving() end)
    f:SetScript("OnDragStop", function(self) self:StopMovingOrSizing() end)
    f:Hide()
    return f
end
