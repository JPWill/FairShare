-- FairShare/UI/Loot.lua
-- When the leader opens a loot window, a small strip beside it shows the auctionable
-- items and what the queue is doing with them: queued (press Skip to drop one), already
-- auctioned, or a Queue button when auto-queue is off. Nothing to type, usually nothing to click.
local ADDON, ns = ...

local UI, Events, Group, Auction = ns.UI, ns.Core.Events, ns.Core.Group, ns.Auction
local Q = Auction.Queue

local frame
local MAX_ROWS = 8

local function build()
    local W, t = UI.Widgets, UI.Skin.Current()
    frame = CreateFrame("Frame", "FairShareLootFrame", UIParent, "BackdropTemplate")
    frame:SetSize(300, 40)
    frame:SetFrameStrata("HIGH")
    UI.Skin.Backdrop(frame)
    frame.title = W.Label(frame, "FairShare", 12, t.accent, "LEFT")
    frame.title:SetPoint("TOPLEFT", 8, -6)
    frame.state = W.Label(frame, "", 10, t.muted, "RIGHT")
    frame.state:SetPoint("TOPRIGHT", -8, -7)
    frame.rows = {}
    for i = 1, MAX_ROWS do
        local row = {}
        row.item = W.ItemLabel(frame, 12)
        row.item:SetPoint("TOPLEFT", 8, -24 - (i - 1) * 24)
        row.item:SetWidth(180)
        row.status = W.Label(frame, "", 10, t.muted, "RIGHT")
        row.status:SetPoint("LEFT", row.item, "RIGHT", 4, 0)
        row.status:SetWidth(50)
        row.btn = W.Button(frame, "Pending", 60, 20, function()
            if row.queuedAt then
                Q.Remove(row.queuedAt)
            else
                local ok, err = Q.Add(row.link, "loot window")
                if not ok then ns.Print(err) end
            end
        end)
        row.btn:SetPoint("LEFT", row.status, "RIGHT", 4, 0)
        frame.rows[i] = row
    end
    frame:Hide()
end

local function queuedIndex(link)
    local id = Q.ItemID(link)
    for i, it in ipairs(Q.items) do
        if it.itemID == id then return i end
    end
    return nil
end

local function auctioned(link)
    local id = Q.ItemID(link)
    for _, a in ipairs(Auction.Running()) do
        if a.kind == "loot" and Q.ItemID(a.itemLink) == id then return "running" end
    end
    for i = #Auction.history, 1, -1 do
        local h = Auction.history[i]
        if h.kind == "loot" and Q.ItemID(h.itemLink) == id then return "sold" end
    end
    return nil
end

local function refresh()
    if not frame then build() end
    if not ns.Raid.IsLeader() or not ns.Raid.context or not GetNumLootItems then
        frame:Hide()
        return
    end
    local t = UI.Skin.Current()
    local n = 0
    for slot = 1, GetNumLootItems() do
        local link = GetLootSlotLink(slot)
        local _, _, _, _, quality = GetLootSlotInfo(slot)
        if link and (quality or Q.QualityOf(link) or 0) >= Q.MinQuality() and n < MAX_ROWS then
            n = n + 1
            local row = frame.rows[n]
            row.link = link
            row.item:SetLink(link)
            row.item:Show()
            row.queuedAt = queuedIndex(link)
            local state = auctioned(link)
            if row.queuedAt then
                row.status:SetText("Pending")
                row.status:SetTextColor(unpack(t.accent))
                row.btn:SetLabel("Remove")
                row.btn:Show()
            elseif state then
                row.status:SetText(state)
                row.status:SetTextColor(unpack(state == "sold" and t.good or t.text))
                row.btn:Hide()
            else
                row.status:SetText("")
                row.btn:SetLabel("Pending")
                row.btn:Show()
            end
            row.status:Show()
        end
    end
    for i = n + 1, MAX_ROWS do
        frame.rows[i].item:Hide()
        frame.rows[i].status:Hide()
        frame.rows[i].btn:Hide()
    end
    if n == 0 then
        frame:Hide()
        return
    end
    local n = #Auction.Running()
    frame.state:SetText(n > 0 and ("%d open. Batches run from the Ledger"):format(n) or "Pending items start from the Ledger")
    frame:SetHeight(30 + n * 24)
    frame:ClearAllPoints()
    if LootFrame then
        frame:SetPoint("TOPLEFT", LootFrame, "TOPRIGHT", 4, 0)
    else
        frame:SetPoint("CENTER", UIParent, "CENTER", 200, 0)
    end
    frame:Show()
end

Events.Register("LOOT_OPENED", refresh)
Events.Register("LOOT_SLOT_CLEARED", refresh)
Events.Register("LOOT_CLOSED", function() if frame then frame:Hide() end end)
for _, e in ipairs({ "AUCTION_OPEN", "AUCTION_RESULT", "QUEUE_CHANGED" }) do
    Events.On(e, function() if frame and frame:IsShown() then refresh() end end)
end
