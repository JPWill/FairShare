-- FairShare/UI/Settings.lua
-- Minimap button, sounds, theme, loot quality threshold, loot queue, developer mode.
local ADDON, ns = ...

local UI, Events = ns.UI, ns.Core.Events

local QUALITY_OPTIONS = {
    { value = 2, label = "Uncommon and up" },
    { value = 3, label = "Rare and up" },
    { value = 4, label = "Epic and up" },
}

UI.RegisterPanel("Settings", function(panel)
    local W, Skin = UI.Widgets, UI.Skin
    local t = Skin.Current()
    local COL = W.LABEL_COLUMN + 4

    local minimap = W.Checkbox(panel, "Show minimap button", function(on) ns.Core.Launcher.SetMinimapShown(on) end)
    minimap:SetPoint("TOPLEFT", 0, -2)

    local sound = W.Checkbox(panel, "Play a sound when an auction opens", function(on) ns.db.settings.sound = on end)
    sound:SetPoint("TOPLEFT", 0, -28)

    local themeLabel = W.Label(panel, "Theme", nil, t.muted, "LEFT")
    themeLabel:SetPoint("TOPLEFT", 4, -62)
    local themeOptions = {}
    for _, o in ipairs(Skin.Options()) do
        themeOptions[#themeOptions + 1] = { value = o, label = Skin.Label(o) }
    end
    local themeDrop = W.Dropdown(panel, 160, themeOptions, function(v) ns.db.settings.theme = v end)
    themeDrop:SetPoint("TOPLEFT", COL, -58)
    local themeHint = W.Label(panel, "Applies after a reload", 11, t.muted, "LEFT")
    themeHint:SetPoint("LEFT", themeDrop, "RIGHT", 8, 0)
    themeHint:SetPoint("RIGHT", 0, 0)
    themeHint:SetWordWrap(false)

    local lootLabel = W.Label(panel, "Loot buttons", nil, t.muted, "LEFT")
    lootLabel:SetPoint("TOPLEFT", 4, -92)
    local lootDrop = W.Dropdown(panel, 160, QUALITY_OPTIONS, function(v)
        ns.db.settings.minQuality = v
        Events.Fire("QUEUE_CHANGED")
    end)
    lootDrop:SetPoint("TOPLEFT", COL, -88)
    local lootHint = W.Label(panel, "Which loot is queued for auction", 11, t.muted, "LEFT")
    lootHint:SetPoint("LEFT", lootDrop, "RIGHT", 8, 0)
    lootHint:SetPoint("RIGHT", 0, 0)
    lootHint:SetWordWrap(false)

    local announceLabel = W.Label(panel, "Announce trades", nil, t.muted, "LEFT")
    announceLabel:SetPoint("TOPLEFT", 4, -122)
    local announceDrop = W.Dropdown(panel, 160, {
        { value = "raid", label = "Raid chat" },
        { value = "whisper", label = "Whisper the recipient" },
        { value = "none", label = "Nowhere" },
    }, function(v) ns.Auction.Trade.SetAnnounceChannel(v) end)
    announceDrop:SetPoint("TOPLEFT", COL, -118)
    local announceHint = W.Label(panel, "when an item is handed over through the addon", 11, t.muted, "LEFT")
    announceHint:SetPoint("LEFT", announceDrop, "RIGHT", 8, 0)
    announceHint:SetPoint("RIGHT", 0, 0)
    announceHint:SetWordWrap(false)

    local autoQueue = W.Checkbox(panel, "Collect loot into the Ledger automatically (loot window, and loot lines in an instance)", function(on)
        ns.Auction.Queue.SetAutoQueue(on)
    end)
    autoQueue:SetPoint("TOPLEFT", 0, -146)

    -- Addon key: issued by the bot, signs this player's bids so nobody can forge them.
    local keyLabel = W.Label(panel, "Addon key", nil, t.muted, "LEFT")
    keyLabel:SetPoint("TOPLEFT", 4, -176)
    local keyBox = W.EditBox(panel, 300, 22)
    keyBox:SetPoint("TOPLEFT", COL, -172)
    local saveKey
    local keySave = W.Button(panel, "Save", 60, 22, function() saveKey() end)
    keySave:SetPoint("LEFT", keyBox, "RIGHT", 6, 0)
    local keyHint = W.Label(panel, "From the bot: /fairshare, Addon key. Signs your bids so nobody can forge them.", 11, t.muted, "LEFT")
    keyHint:SetPoint("TOPLEFT", COL, -198)
    keyHint:SetPoint("RIGHT", 0, 0)
    keyHint:SetWordWrap(false)
    saveKey = function()
        local text = (keyBox:GetText() or ""):gsub("^%s+", ""):gsub("%s+$", ""):lower()
        if text == "" then
            ns.db.settings.addonKey = ""
            Events.Fire("ADDON_KEY_CHANGED")
            return
        end
        if not ns.Auction.State.ValidKey(text) then
            ns.Print("An addon key is 32 hex characters: get one from the bot's personal panel")
            return
        end
        ns.db.settings.addonKey = text
        keyBox:SetText(text)
        ns.Print("Addon key saved. Your bids are signed from now on.")
        Events.Fire("ADDON_KEY_CHANGED")
    end
    keyBox:SetScript("OnEnterPressed", function(self) saveKey() self:ClearFocus() end)
    panel.SaveAddonKey = saveKey
    panel.AddonKeyBox = keyBox

    W.Separator(panel, -222)

    -- Past raids: every transcript this client still holds (newest 30). Pick one, Export.
    local histLabel = W.Label(panel, "Past raids", nil, t.muted, "LEFT")
    histLabel:SetPoint("TOPLEFT", 4, -236)
    local histDrop = W.Dropdown(panel, 300, {}, nil)
    histDrop:SetPoint("TOPLEFT", COL, -232)
    local histExport = W.Button(panel, "Export", 90, 22, function()
        local id = histDrop.value
        if not id then return end
        local str, err = ns.Transcript.Export(id)
        if not str then ns.Print(err) return end
        W.TextPopup(("Transcript of raid %s (copy and upload to the bot)"):format(id), str)
    end)
    histExport:SetPoint("LEFT", histDrop, "RIGHT", 6, 0)
    local histHint = W.Label(panel, "Export any raid you attended, any time, if a result is disputed", 11, t.muted, "LEFT")
    histHint:SetPoint("TOPLEFT", COL, -258)
    histHint:SetPoint("RIGHT", 0, 0)
    local function refreshHistory()
        local opts = {}
        for _, h in ipairs(ns.Transcript.History()) do
            opts[#opts + 1] = { value = h.raidID, label = ns.Transcript.HistoryLabel(h) }
        end
        histDrop:SetOptions(opts)
        if #opts == 0 then
            histDrop:SetValue(nil)
            histDrop.label:SetText("No raids recorded yet")
            histExport:SetEnabled(false)
        else
            local known = false
            for _, o in ipairs(opts) do if o.value == histDrop.value then known = true end end
            if not known then histDrop:SetValue(opts[1].value) end
            histExport:SetEnabled(true)
        end
    end
    Events.On("TRANSCRIPT_LINE", refreshHistory)
    Events.On("RAID_CONTEXT", refreshHistory)

    W.Separator(panel, -278)

    local dev = W.Checkbox(panel, "Developer mode (adds a Dev tab with virtual raiders)", function(on)
        ns.db.settings.dev = on
        Events.Fire("DEV_MODE", on)
    end)
    dev:SetPoint("TOPLEFT", 0, -290)

    -- Website: addons cannot open links, so the address sits in a box to copy (click, Ctrl-C).
    local siteLabel = W.Label(panel, "Website", nil, t.muted, "LEFT")
    siteLabel:SetPoint("TOPLEFT", 4, -320)
    local siteBox = W.EditBox(panel, 300, 22)
    siteBox:SetPoint("TOPLEFT", COL, -316)
    siteBox:SetText(ns.SITE_URL)
    siteBox:SetScript("OnEditFocusGained", function(self) self:HighlightText() end)
    siteBox:SetScript("OnTextChanged", function(self, user)
        if user then self:SetText(ns.SITE_URL) self:HighlightText() end
    end)
    siteBox:SetScript("OnEscapePressed", function(self) self:ClearFocus() end)
    siteBox:SetScript("OnEnterPressed", function(self) self:ClearFocus() end)
    local siteHint = W.Label(panel, "Ledger, FAQ and rules. Click the box, then Ctrl-C to copy.", 11, t.muted, "LEFT")
    siteHint:SetPoint("TOPLEFT", COL, -340)
    siteHint:SetPoint("RIGHT", 0, 0)

    local version = W.Label(panel, "FairShare " .. tostring(ns.VERSION), 11, t.muted, "LEFT")
    version:SetPoint("BOTTOMLEFT", 0, 0)

    panel.OnShow = function()
        minimap:SetChecked(not ns.db.settings.minimap.hide)
        keyBox:SetText(ns.db.settings.addonKey or "")
        sound:SetChecked(ns.db.settings.sound ~= false)
        themeDrop:SetValue(ns.db.settings.theme or "auto")
        lootDrop:SetValue(ns.db.settings.minQuality or 3)
        autoQueue:SetChecked(ns.Auction.Queue.AutoQueue())
        announceDrop:SetValue(ns.Auction.Trade.AnnounceChannel())
        dev:SetChecked(ns.db.settings.dev and true or false)
        refreshHistory()
    end
    panel.OnShow()
end)
