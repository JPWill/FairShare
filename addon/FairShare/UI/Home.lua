-- FairShare/UI/Home.lua
-- The first thing anyone sees. A raider gets their balance, the loot they won tonight
-- (ledger-style rows), and the raid: everyone present with what they can bid. The leader
-- gets Quick Auction (one item, straight to auction) and Export on top instead of the
-- loot list, then the same balance and raid. The raid block also says who leads, and
-- offers Take over when the game has handed lead to someone who is not yet the FairShare
-- leader. Batches of loot are run from the Ledger.
local ADDON, ns = ...

local UI, Events, Group, Auction = ns.UI, ns.Core.Events, ns.Core.Group, ns.Auction
local Q, Book, Tr, Ping = Auction.Queue, Auction.Book, Auction.Trade, ns.Core.Ping

local LOOT_ROWS = 4
local MEMBER_ROWS = 14
local ROW_H = 16
local COLS = { balance = 64, raid = 70, addon = 110 }
local WHITE = "Interface\\Buttons\\WHITE8x8"

UI.RegisterPanel("Home", function(panel)
    local W, t = UI.Widgets, UI.Skin.Current()
    local COL = W.LABEL_COLUMN + 4
    local refresh

    ----------------------------------------------------------------- Quick Auction | Export
    local quick = CreateFrame("Frame", nil, panel)
    quick:SetPoint("TOPLEFT", 0, 0)
    quick:SetPoint("RIGHT", 0, 0)
    quick:SetHeight(112)
    -- Two equal columns, everything in each centred on the column's middle.
    local quickCol = CreateFrame("Frame", nil, quick)
    quickCol:SetPoint("TOPLEFT", quick, "TOPLEFT", 0, 0)
    quickCol:SetPoint("BOTTOMRIGHT", quick, "BOTTOM", 0, 0)
    local exportCol = CreateFrame("Frame", nil, quick)
    exportCol:SetPoint("TOPLEFT", quick, "TOP", 0, 0)
    exportCol:SetPoint("BOTTOMRIGHT", quick, "BOTTOMRIGHT", 0, 0)
    local quickLabel = W.Label(quickCol, "Quick Auction", nil, t.muted, "CENTER")
    quickLabel:SetPoint("TOP", 0, -2)
    local slot = W.ItemSlot(quickCol, 64)
    slot:SetPoint("TOP", 0, -20)
    panel.slot = slot
    local setLink = slot.SetLink
    function slot:SetLink(link)
        setLink(self, link)
        if link then
            local ok, err = Auction.Open(link, "loot")
            if not ok then ns.Print(err) end
            setLink(self, nil)
        end
    end
    local quickInfo = W.Label(quickCol, "", 11, t.muted, "CENTER")
    quickInfo:SetPoint("TOPLEFT", 0, -88)
    quickInfo:SetPoint("RIGHT", 0, 0)
    quickInfo:SetWordWrap(false)
    local exportLabel = W.Label(exportCol, "Export", nil, t.muted, "CENTER")
    exportLabel:SetPoint("TOP", 0, -2)
    local exportBtn = W.Button(exportCol, "Export", 90, 20, function()
        local str, err = ns.Transcript.Export()
        if not str then ns.Print(err) return end
        W.TextPopup("Transcript (copy and upload to the bot)", str)
    end)
    exportBtn:SetPoint("TOP", 0, -20)
    local exportInfo = W.Label(exportCol, "", 10, t.muted, "CENTER")
    exportInfo:SetPoint("TOPLEFT", 10, -46)
    exportInfo:SetPoint("RIGHT", -10, 0)
    exportInfo:SetWordWrap(true)
    exportInfo:SetHeight(44)
    exportInfo:SetJustifyV("TOP")
    W.Separator(quick, -110)

    -- Shift-clicking an item with this tab open and no chat box active auctions it.
    if hooksecurefunc and not ns.UI.insertHooked then
        ns.UI.insertHooked = true
        hooksecurefunc("ChatEdit_InsertLink", function(link)
            if type(link) ~= "string" or not link:find("|Hitem:", 1, true) then return end
            if ChatEdit_GetActiveWindow and ChatEdit_GetActiveWindow() then return end
            local win = UI.Window()
            if win and win:IsShown() and UI.CurrentPanel() == "Home" and ns.Raid.IsLeader() then slot:SetLink(link) end
        end)
    end

    ----------------------------------------------------------------- my loot (raiders)
    local loot = CreateFrame("Frame", nil, panel)
    loot:SetPoint("RIGHT", 0, 0)
    loot:SetHeight(24 + LOOT_ROWS * ROW_H)
    local lootLabel = W.Label(loot, "Your loot", nil, t.muted, "LEFT")
    lootLabel:SetPoint("TOPLEFT", 0, 0)
    lootLabel:SetWidth(W.LABEL_COLUMN)
    local lootInfo = W.Label(loot, "", 11, t.muted, "LEFT")
    lootInfo:SetPoint("TOPLEFT", COL, -1)
    lootInfo:SetPoint("RIGHT", 0, 0)
    lootInfo:SetWordWrap(true)
    lootInfo:SetMaxLines(1)
    local expanded = {}
    local lootList = W.VirtualList(loot, LOOT_ROWS, ROW_H, function(row)
        row.entryID = false
        row.isHeader = false
        row.link = false
        row.hl = row:CreateTexture(nil, "BACKGROUND")
        row.hl:SetTexture(WHITE)
        row.hl:SetAllPoints()
        row.hl:SetVertexColor(t.accent[1], t.accent[2], t.accent[3], 0.10)
        row.hl:Hide()
        row.expander = CreateFrame("Button", nil, row)
        row.expander:SetSize(14, 14)
        row.expander:SetNormalTexture("Interface\\Buttons\\UI-PlusButton-Up")
        row.expander:SetHighlightTexture("Interface\\Buttons\\UI-PlusButton-Hilight", "ADD")
        row.expander:SetPoint("LEFT", 2, 0)
        row.expander:SetScript("OnClick", function()
            if not row.entryID or not row.isHeader then return end
            expanded[row.entryID] = not expanded[row.entryID] or nil
            refresh()
        end)
        row.text = W.Label(row, "", nil, t.text, "LEFT")
        row.text:SetPoint("LEFT", 20, 0)
        row.text:SetPoint("RIGHT", -70, 0)
        row.text:SetWordWrap(false)
        row.text:SetMaxLines(1)
        row.right = W.Label(row, "", nil, t.text, "RIGHT")
        row.right:SetPoint("RIGHT", 0, 0)
        row.right:SetWidth(66)
        row:SetScript("OnClick", function(self)
            if not self.entryID or not self.isHeader then return end
            expanded[self.entryID] = not expanded[self.entryID] or nil
            refresh()
        end)
        row:SetScript("OnEnter", function(self)
            if self.link then
                GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
                GameTooltip:SetHyperlink(self.link)
                GameTooltip:Show()
            elseif self.text.IsTruncated and self.text:IsTruncated() then
                GameTooltip:SetOwner(self, "ANCHOR_TOP")
                GameTooltip:SetText(self.text:GetText() or "", 1, 1, 1, 1, true)
                GameTooltip:Show()
            end
        end)
        row:SetScript("OnLeave", function() GameTooltip:Hide() end)
    end, function(row, l)
        row.entryID = l.e and l.e.id or false
        row.isHeader = l.header or false
        row.link = (l.header and l.e.itemLink) or false
        if l.header then
            row.hl:Hide()
            row.expander:SetNormalTexture(expanded[l.e.id] and "Interface\\Buttons\\UI-MinusButton-Up" or "Interface\\Buttons\\UI-PlusButton-Up")
            row.expander:Show()
        else
            row.hl:Show()
            row.expander:Hide()
        end
        row.text:SetText(l.text)
        row.text:SetTextColor(l.color[1], l.color[2], l.color[3], 1)
        row.right:SetText(l.right or "")
        row.right:SetTextColor(l.color[1], l.color[2], l.color[3], 1)
    end)
    lootList:SetPoint("TOPLEFT", 0, -20)
    lootList:SetPoint("RIGHT", 0, 0)
    W.Separator(loot, -(22 + LOOT_ROWS * ROW_H))

    local function lootLines()
        local me, lines, n = Group.Me(), {}, 0
        local entries = Book.Entries()
        for i = #entries, 1, -1 do
            local e = entries[i]
            if Book.IsSale(e) and Book.Owner(e) == me then
                n = n + 1
                local st = Tr.Status(e)
                lines[#lines + 1] = {
                    header = true, e = e,
                    text = ("#%s %s - %s"):format(Book.Number(e), e.itemLink or Book.Name(e),
                        st == "delivered" and "In your bags" or (e.transfer and "Transferred to you" or "Owed to you")),
                    right = tostring(e.price), color = t.text,
                }
                if expanded[e.id] then
                    for _, b in ipairs(e.bids or {}) do
                        lines[#lines + 1] = { e = e, text = ("      %-24s %6d%s"):format(b.name, b.bid, b.offspec and "  off-spec" or ""), color = t.muted }
                    end
                    if st == "owed" then
                        lines[#lines + 1] = { e = e, text = "      Held by " .. (Tr.Holder(e) or "the leader") .. "; they trade it to you", color = t.accent }
                    end
                end
            end
        end
        return lines, n
    end

    ----------------------------------------------------------------- the raid
    local raid = CreateFrame("Frame", nil, panel)
    raid:SetPoint("BOTTOMRIGHT", 0, 0)
    local raidLabel = W.Label(raid, "Raid", nil, t.muted, "LEFT")
    raidLabel:SetPoint("TOPLEFT", 0, 0)
    raidLabel:SetWidth(W.LABEL_COLUMN)
    local pingBtn = W.Button(raid, "Ping", 50, 18, function() Ping.Send() end)
    pingBtn:SetPoint("TOPRIGHT", 0, 1)
    local takeBtn = W.Button(raid, "Take over", 80, 18, function()
        local ok, err = ns.Raid.TakeOver()
        if not ok then ns.Print(err) end
    end)
    takeBtn:SetPoint("RIGHT", pingBtn, "LEFT", -6, 0)
    takeBtn:Hide()
    local raidText = W.Label(raid, "", nil, t.text, "LEFT")
    raidText:SetPoint("TOPLEFT", COL, 0)
    raidText:SetPoint("RIGHT", takeBtn, "LEFT", -8, 0)
    raidText:SetWordWrap(false)
    raidText:SetMaxLines(1)
    local leadText = W.Label(raid, "", 11, t.muted, "LEFT")
    leadText:SetPoint("TOPLEFT", COL, -16)
    leadText:SetPoint("RIGHT", 0, 0)
    leadText:SetWordWrap(true)
    leadText:SetMaxLines(2)
    leadText:SetHeight(26)
    leadText:SetJustifyV("TOP")

    -- One font string per column, right-aligned numbers, so nothing depends on glyph widths.
    local function columns(parent, size, color)
        local c = {}
        c.addon = W.Label(parent, "", size, color, "LEFT")
        c.addon:SetPoint("RIGHT", 0, 0)
        c.addon:SetWidth(COLS.addon)
        c.raid = W.Label(parent, "", size, color, "RIGHT")
        c.raid:SetPoint("RIGHT", c.addon, "LEFT", -8, 0)
        c.raid:SetWidth(COLS.raid)
        c.balance = W.Label(parent, "", size, color, "RIGHT")
        c.balance:SetPoint("RIGHT", c.raid, "LEFT", -8, 0)
        c.balance:SetWidth(COLS.balance)
        c.name = W.Label(parent, "", size, color, "LEFT")
        c.name:SetPoint("LEFT", 0, 0)
        c.name:SetPoint("RIGHT", c.balance, "LEFT", -8, 0)
        for _, fs in pairs(c) do
            fs:SetWordWrap(false)
            fs:SetMaxLines(1)
        end
        return c
    end
    local function fill(c, name, bal, raidDelta, addon, color)
        c.name:SetText(name)
        c.balance:SetText(bal)
        c.raid:SetText(raidDelta)
        c.addon:SetText(addon)
        if color then
            for _, fs in pairs(c) do fs:SetTextColor(color[1], color[2], color[3], 1) end
        end
    end
    local header = CreateFrame("Frame", nil, raid)
    header:SetPoint("TOPLEFT", 0, -46)
    header:SetPoint("RIGHT", 0, 0)
    header:SetHeight(14)
    fill(columns(header, 11, t.muted), "Raider", "Shares", "This raid", "Addon")
    local list = W.VirtualList(raid, MEMBER_ROWS, ROW_H, function(row)
        row.cols = columns(row, nil, t.text)
        row.text = row.cols.name
    end, function(row, it)
        fill(row.cols, it.name, it.balance, it.raid, it.addon, it.color)
    end)
    list:SetPoint("TOPLEFT", 0, -62)
    list:SetPoint("RIGHT", 0, 0)

    -- Shares is what the raider holds right now: snapshot balance plus this raid's estimate.
    -- Credit never shows here. The player is always the first row.
    local function memberItems()
        local c = ns.Raid.context
        local deltas = Book.Deltas()
        local pings = {}
        for _, r in ipairs(Ping.results or {}) do pings[r.name] = r end
        local me = Group.Me()
        local names = {}
        for _, n in ipairs(Group.Members()) do
            if n ~= me then names[#names + 1] = n end
        end
        table.insert(names, 1, me)
        local items = {}
        for _, n in ipairs(names) do
            local row = c and c.rows[n]
            local p = pings[n]
            local addon = p and (("v%s %dms"):format(p.version or "?", math.floor((p.rtt or 0) * 1000 + 0.5))) or ""
            if row then
                local d = deltas[n] or 0
                items[#items + 1] = {
                    name = n, balance = tostring(row.balance + d),
                    raid = d ~= 0 and ("%+d"):format(d) or "", addon = addon,
                    color = (n == me) and t.accent or t.text,
                }
            else
                items[#items + 1] = { name = n, balance = "-", raid = c and "Not in snapshot" or "", addon = addon, color = t.muted }
            end
        end
        return items
    end

    local function leadership()
        local c = ns.Raid.context
        local me, gameLead, fsLead = Group.Me(), Group.LeaderName(), c and c.leader or nil
        takeBtn:Hide()
        if not c or not fsLead then return "", nil end
        if fsLead == gameLead then
            if fsLead == me and c.tookOver then
                local have, need = ns.Raid.LeadVotes()
                return ns.Raid.LeadConfirmed() and ("You lead (%d of %d confirmed)"):format(have, need) or ("Taking over, %d of %d confirmed"):format(have, need), nil
            end
            return "Leader: " .. (fsLead == me and "you" or fsLead), nil
        end
        if gameLead == me then
            takeBtn:Show()
            return ("FairShare leader is %s but you lead the group: take over to continue"):format(fsLead), t.bad
        elseif fsLead == me then
            return ("%s holds lead in game: ask for it back, or they take over"):format(gameLead or "?"), t.bad
        end
        return ("Leader %s is not the group leader (%s), who can take over"):format(fsLead, gameLead or "?"), t.bad
    end

    -- The auctions window, one click from the bottom of the tab for everyone.
    local auctionsBtn = W.Button(panel, "Show Auctions Window", 170, 22, function() UI.ShowBid() end)
    auctionsBtn:SetPoint("BOTTOM", 0, 0)
    local TOP, BOTTOM = 4, 28

    local function place(leader)
        if leader then
            quick:Show(); loot:Hide()
            quick:ClearAllPoints(); quick:SetPoint("TOPLEFT", 0, -TOP); quick:SetPoint("RIGHT", 0, 0)
            raid:ClearAllPoints(); raid:SetPoint("TOPLEFT", 0, -(TOP + 120)); raid:SetPoint("BOTTOMRIGHT", 0, BOTTOM)
            list:SetVisible(11)
        else
            quick:Hide(); loot:Show()
            loot:ClearAllPoints(); loot:SetPoint("TOPLEFT", 0, -(TOP + 4)); loot:SetPoint("RIGHT", 0, 0)
            raid:ClearAllPoints(); raid:SetPoint("TOPLEFT", 0, -(TOP + 4 + 30 + LOOT_ROWS * ROW_H)); raid:SetPoint("BOTTOMRIGHT", 0, BOTTOM)
            list:SetVisible(MEMBER_ROWS - 2)
        end
    end

    refresh = function()
        local c = ns.Raid.context
        local leader = UI.IsLeader()
        place(leader)

        -- Quick Auction | Export
        if leader then
            local running, pending = #Auction.Running(), Q.Count()
            quickInfo:SetText("Drop an item here to auction it now.")
            local lines = c and ns.Transcript.Lines(c.raidID) or {}
            exportBtn:SetEnabled(#lines > 0)
            exportInfo:SetText(#lines > 0 and ("%d lines recorded. Export after the last boss and upload it."):format(#lines) or "Nothing recorded yet")
        else
            local lines, n = lootLines()
            lootList:SetItems(lines)
            lootInfo:SetText(n == 0 and (c and "Nothing won yet" or "") or ("%d item%s. Click a row for the bids"):format(n, n == 1 and "" or "s"))
        end

        -- Raid
        local lead, color = leadership()
        local parts = {}
        if IsInRaid() then parts[#parts + 1] = ("Raid of %d"):format(GetNumGroupMembers())
        elseif IsInGroup() then parts[#parts + 1] = ("Party of %d"):format(GetNumGroupMembers())
        else parts[#parts + 1] = "Solo" end
        local results = Ping.results or {}
        if #results > 0 then parts[#parts + 1] = ("%d with the addon"):format(#results) end
        raidText:SetText(table.concat(parts, " - "))
        leadText:SetText(lead or "")
        leadText:SetTextColor(unpack(color or t.muted))
        list:SetItems(memberItems())
    end

    for _, e in ipairs({ "PING_SENT", "PING_RESULT", "SNAPSHOT_CHANGED", "RAID_CONTEXT", "RAID_LEADER_CHANGED", "GROUP_CHANGED", "QUEUE_CHANGED", "AUCTION_OPEN", "AUCTION_RESULT", "AUCTION_VOID", "AUCTION_TRANSFER", "LEDGER_CHANGED", "TRANSCRIPT_LINE" }) do Events.On(e, refresh) end
    Events.Register("GROUP_ROSTER_UPDATE", refresh)
    Events.Register("ENCOUNTER_START", refresh)
    Events.Register("ENCOUNTER_END", refresh)
    panel.OnShow = refresh
    refresh()
end)
