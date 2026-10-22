-- FairShare/UI/Dev.lua
-- Developer tab (Settings -> Developer mode). Everything needed to run a raid night alone.
local ADDON, ns = ...

local UI, Events, Dev = ns.UI, ns.Core.Events, ns.Dev

UI.RegisterPanel("Dev", function(panel)
    local W, t = UI.Widgets, UI.Skin.Current()

    local sample = W.Button(panel, "Load sample snapshot", 150, 22, function()
        local snap, err = Dev.LoadSampleSnapshot()
        if not snap then ns.Print(err) end
    end)
    sample:SetPoint("TOPLEFT", 0, -2)
    local add = W.Button(panel, "Add 5 raiders", 110, 22, function() Dev.AddRaiders(5) end)
    add:SetPoint("LEFT", sample, "RIGHT", 6, 0)
    local clear = W.Button(panel, "Clear raiders", 110, 22, function() Dev.ClearRaiders() end)
    clear:SetPoint("LEFT", add, "RIGHT", 6, 0)
    -- Walks every label on screen and prints any that is cut short or past its window edge.
    local audit = W.Button(panel, "Audit text", 90, 22, function()
        local problems = W.AuditText()
        Dev.Note(("text audit: %d problem%s on screen"):format(#problems, #problems == 1 and "" or "s"))
        ns.Print(("text audit: %d problem%s"):format(#problems, #problems == 1 and "" or "s"))
        for _, p in ipairs(problems) do
            Dev.Note(p)
            ns.Print("text audit: " .. p)
        end
    end)
    audit:SetPoint("TOPRIGHT", 0, -84)
    local reset = W.Button(panel, "Reset dev data", 110, 22, function()
        W.Confirm("Reset dev data", "Forget every dev raid: transcripts, auctions, deliveries, pending loot and virtual raiders. Real raids are never touched. Load the sample snapshot again afterwards.", {
            { "Reset", function() Dev.Reset() end },
            { "Cancel" },
        })
    end)
    reset:SetPoint("LEFT", clear, "RIGHT", 6, 0)

    local bidding = W.Checkbox(panel, "Raiders bid on auctions", function(on) Dev.bidding = on end)
    bidding:SetPoint("TOPLEFT", 0, -30)
    local chaos = W.Checkbox(panel, "Chaos: one never reveals, one reveals a wrong bid", function(on) Dev.SetChaos(on) end)
    chaos:SetPoint("TOPLEFT", 0, -54)

    local kill = W.Button(panel, "Boss kill", 90, 22, function() Dev.SimulateKill() end)
    kill:SetPoint("TOPLEFT", 0, -84)
    local bossStart = W.Button(panel, "Boss start", 90, 22, function() Dev.SimulateEncounter(true) end)
    bossStart:SetPoint("LEFT", kill, "RIGHT", 6, 0)
    local bossEnd = W.Button(panel, "Boss wipe", 90, 22, function() Dev.SimulateEncounter(false) end)
    bossEnd:SetPoint("LEFT", bossStart, "RIGHT", 6, 0)
    local loot = W.Button(panel, "Loot line", 90, 22, function() Dev.SimulateLoot() end)
    loot:SetPoint("LEFT", bossEnd, "RIGHT", 6, 0)

    local raiders = W.Field(panel, "Raiders", -114)

    W.Separator(panel, -136)
    local logLabel = W.Label(panel, "Messages", nil, t.muted, "LEFT")
    logLabel:SetPoint("TOPLEFT", 0, -144)
    local clearLog = W.Button(panel, "Clear", 60, 20, function() Dev.ClearLog() end)
    clearLog:SetPoint("TOPRIGHT", 0, -142)
    local log = W.List(panel, 11, 14)
    log:SetPoint("TOPLEFT", 0, -164)
    log:SetPoint("RIGHT", 0, 0)

    local function refresh()
        bidding:SetChecked(Dev.bidding)
        chaos:SetChecked(Dev.chaos)
        local names = {}
        for _, n in ipairs(Dev.RaiderNames()) do
            local mode = Dev.raiders[n].mode
            names[#names + 1] = n .. (mode ~= "normal" and (" (" .. mode .. ")") or "")
        end
        raiders:SetText(#names > 0 and table.concat(names, ", ") or "none")
        local rows = {}
        local from = math.max(1, #Dev.log - 10)
        for i = from, #Dev.log do rows[#rows + 1] = { text = Dev.log[i], color = t.muted } end
        log:SetRows(rows)
    end

    Events.On("DEV_CHANGED", refresh)
    Events.On("DEV_LOG", refresh)
    panel.OnShow = refresh
    refresh()
end, { devOnly = true })
