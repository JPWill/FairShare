-- FairShare/UI/Ledger.lua
-- The Ledger tab: everything loot-related in one list. Pending items (looted, not yet
-- auctioned) sit on top, then every auction of this raid newest first, running ones with
-- their phase and time left. Rows collapse to item - winner - price; expand one for the
-- bids, checks, who holds the item and who it is owed to, and that item's controls:
-- Start or Remove for a pending item, Trade for whoever holds a won item, and for the
-- leader Run again and Transfer. Tick several rows and Auction checked opens every pending
-- one at once (they run side by side); anything already sold asks whether to void the old
-- sale or sell a second copy.
local ADDON, ns = ...

local UI, Events, Auction, Group = ns.UI, ns.Core.Events, ns.Auction, ns.Core.Group
local Book, Tr, Q = Auction.Book, Auction.Trade, Auction.Queue

local ROWS, ROW_H = 13, 18
local VOIDED = { 0.55, 0.12, 0.12, 1 }   -- deep red for struck sales
local WHITE = "Interface\\Buttons\\WHITE8x8"

UI.RegisterPanel("Ledger", function(panel)
    local W, t = UI.Widgets, UI.Skin.Current()
    local render

    local summary = W.Label(panel, "", nil, t.muted, "LEFT")
    summary:SetPoint("TOPLEFT", 0, -4)
    summary:SetPoint("RIGHT", 0, 0)
    summary:SetWordWrap(false)

    local expanded, checked, xferTo = {}, {}, {}
    local function keyOfPending(i) return "pending:" .. i end

    ----------------------------------------------------------------- actions
    local function openAgain(entries, void)
        local links = {}
        for _, e in ipairs(entries) do
            local okToOpen = true
            if void then
                local ok, err = Auction.Void(e.id, "re-run")
                if not ok then
                    ns.Print(("#%s: cannot void (%s); sold as a second copy instead"):format(Book.Number(e), err))
                end
            end
            if okToOpen then links[#links + 1] = e.itemLink end
        end
        local n, err = Auction.OpenMany(links)
        if n > 0 then ns.Print(("Opened %d auction%s"):format(n, n == 1 and "" or "s")) end
        if err then ns.Print(err) end
        checked = {}
        render()
    end

    -- Re-auction these finished entries. Sold ones first ask: void the old sale, or sell another copy.
    local function runAgain(entries)
        local sold, plain = {}, {}
        for _, e in ipairs(entries) do
            if Book.IsSale(e) then sold[#sold + 1] = e else plain[#plain + 1] = e end
        end
        if #sold == 0 then
            openAgain(plain, false)
            return
        end
        local names = {}
        for i, e in ipairs(sold) do
            if i <= 3 then names[#names + 1] = ("#%s %s (%s, %d)"):format(Book.Number(e), Book.Name(e), e.winner, e.price) end
        end
        if #sold > 3 then names[#names + 1] = ("and %d more"):format(#sold - 3) end
        W.Confirm("Already sold",
            ("%s already %s a winner.\n\nRe-run voids %s sale (nothing is settled for it) and auctions the item again. Second copy keeps the sale and sells another copy, for when two dropped."):format(
                table.concat(names, ", "), #sold == 1 and "has" or "have", #sold == 1 and "that" or "each"),
            {
                { "Re-run (void sale)", function()
                    openAgain(plain, false)
                    openAgain(sold, true)
                end },
                { "Second copy", function()
                    openAgain(plain, false)
                    openAgain(sold, false)
                end },
                { "Cancel" },
            })
    end

    local function startPending(indices)
        local n, err = Q.Start(indices)
        if n > 0 then ns.Print(("Opened %d auction%s"):format(n, n == 1 and "" or "s")) end
        if err then ns.Print(err) end
        checked = {}
        render()
    end

    local function recipients(e)
        local out, owner = {}, Book.Owner(e)
        for _, n in ipairs(Group.Members()) do
            if n ~= owner then out[#out + 1] = { value = n, label = n } end
        end
        return out
    end

    ----------------------------------------------------------------- rows
    local list = W.VirtualList(panel, ROWS, ROW_H, function(row)
        row.hl = row:CreateTexture(nil, "BACKGROUND")
        row.hl:SetTexture(WHITE)
        row.hl:SetPoint("TOPLEFT", 14, 0)
        row.hl:SetPoint("BOTTOMRIGHT", 0, 0)
        row.hl:SetVertexColor(t.accent[1], t.accent[2], t.accent[3], 0.10)
        row.hl:Hide()
        row.key = false
        row.entryID = false
        row.pendingIndex = false
        row.isHeader = false
        row.link = false
        row.check = W.Check(row, 12, function(on)
            if row.key then checked[row.key] = on or nil end
            render()
        end)
        row.check:SetPoint("LEFT", 2, 0)
        -- The game's own plus/minus square expands and collapses the row.
        row.expander = CreateFrame("Button", nil, row)
        row.expander:SetSize(14, 14)
        row.expander:SetNormalTexture("Interface\\Buttons\\UI-PlusButton-Up")
        row.expander:SetHighlightTexture("Interface\\Buttons\\UI-PlusButton-Hilight", "ADD")
        row.expander:SetPoint("LEFT", 18, 0)
        row.expander:SetScript("OnClick", function()
            if not row.key or not row.isHeader then return end
            expanded[row.key] = not expanded[row.key] or nil
            render()
        end)
        row.text = W.Label(row, "", nil, t.text, "LEFT")
        row.text:SetPoint("LEFT", 36, 0)
        row.text:SetPoint("RIGHT", -70, 0)
        row.text:SetWordWrap(false)
        row.text:SetMaxLines(1)
        row.right = W.Label(row, "", nil, t.text, "RIGHT")
        row.right:SetPoint("RIGHT", -8, 0)
        row.right:SetWidth(66)
        -- Action widgets, shown on an entry's action line only.
        row.start = W.Button(row, "Start", 60, 15, function()
            if row.pendingIndex then startPending({ row.pendingIndex }) end
        end)
        row.start:SetPoint("LEFT", 20, 0)
        row.remove = W.Button(row, "Remove", 70, 15, function()
            if row.pendingIndex then Q.Remove(row.pendingIndex) end
        end)
        row.remove:SetPoint("LEFT", row.start, "RIGHT", 6, 0)
        row.trade = W.Button(row, "Trade", 60, 15, function()
            local e = row.entryID and Book.Find(row.entryID)
            if not e then return end
            local ok, err = Tr.Initiate(e)
            if not ok then ns.Print(err) end
        end)
        row.trade:SetPoint("LEFT", 20, 0)
        row.run = W.Button(row, "Run again", 80, 15, function()
            local e = row.entryID and Book.Find(row.entryID)
            if e then runAgain({ e }) end
        end)
        row.run:SetPoint("LEFT", row.trade, "RIGHT", 6, 0)
        -- The transfer pair sits on the right edge and reads "Transfer to <name>".
        row.to = W.Dropdown(row, 140, {}, function(v)
            if row.entryID then xferTo[row.entryID] = v end
        end)
        row.to:SetHeight(15)
        row.to:SetPoint("RIGHT", -8, 0)
        row.xfer = W.Button(row, "Transfer to", 80, 15, function()
            local e = row.entryID and Book.Find(row.entryID)
            if not e then return end
            local to = xferTo[e.id] or (recipients(e)[1] or {}).value
            if not to then ns.Print("Nobody to transfer to") return end
            local ok, err = Auction.Transfer(e.id, to)
            if not ok then ns.Print(err) end
        end)
        row.xfer:SetPoint("RIGHT", row.to, "LEFT", -4, 0)
        row.actions = { row.start, row.remove, row.trade, row.run, row.to, row.xfer }
        row:SetScript("OnClick", function(self)
            if not self.key or not self.isHeader then return end
            expanded[self.key] = not expanded[self.key] or nil
            render()
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
        row.key = l.key or false
        row.entryID = l.e and l.e.id or false
        row.pendingIndex = l.pendingIndex or false
        row.isHeader = l.kind == "header"
        row.link = (l.kind == "header" and l.link) or false
        for _, w in ipairs(row.actions) do w:Hide() end
        row.check:Hide()
        row.expander:Hide()
        row.hl:Hide()
        row.text:SetText("")
        row.right:SetText("")
        row.text:ClearAllPoints()
        row.text:SetPoint("LEFT", 24, 0)
        row.text:SetPoint("RIGHT", -78, 0)
        if l.kind == "header" then
            local withCheck = UI.IsLeader() and l.checkable
            if withCheck then
                row.check:SetChecked(checked[l.key] or false)
                row.check:Show()
            end
            -- Checkbox first, then the expander; the expander slides left when there is no box.
            row.expander:ClearAllPoints()
            row.expander:SetPoint("LEFT", withCheck and 18 or 2, 0)
            row.expander:SetNormalTexture(expanded[l.key] and "Interface\\Buttons\\UI-MinusButton-Up" or "Interface\\Buttons\\UI-PlusButton-Up")
            row.expander:Show()
            row.text:ClearAllPoints()
            row.text:SetPoint("LEFT", withCheck and 36 or 20, 0)
            row.text:SetPoint("RIGHT", -70, 0)
            if expanded[l.key] or checked[l.key] then row.hl:Show() end
            row.text:SetText(l.text)
            row.text:SetTextColor(l.color[1], l.color[2], l.color[3], 1)
            row.right:SetText(l.right or "")
            row.right:SetTextColor(l.color[1], l.color[2], l.color[3], 1)
        elseif l.kind == "detail" then
            row.hl:Show()
            row.text:SetText(l.text)
            row.text:SetTextColor(l.color[1], l.color[2], l.color[3], 1)
            row.right:SetText(l.right or "")
            row.right:SetTextColor(l.color[1], l.color[2], l.color[3], 1)
        elseif l.kind == "pending-actions" then
            row.hl:Show()
            if UI.IsLeader() then
                row.start:Show()
                row.start:SetEnabled(ns.Raid.IsLeader() and not Auction.IsPaused())
                row.remove:Show()
            end
        else -- actions
            local e = l.e
            row.hl:Show()
            local st = Tr.Status(e)
            local owner = Book.Owner(e)
            row.trade:Show()
            row.trade:SetEnabled(st == "owed" and owner ~= Group.Me())
            if UI.IsLeader() then
                row.run:Show()
                row.run:SetEnabled(e.kind == "loot" and not (e.phase and e.phase ~= "done"))
                local canXfer = Book.IsSale(e) and not e.transfer
                row.to:Show()
                row.to:SetOptions(recipients(e))
                row.to:SetValue(xferTo[e.id] or (recipients(e)[1] or {}).value)
                row.to:SetEnabled(canXfer)
                row.xfer:Show()
                row.xfer:SetEnabled(canXfer)
            end
        end
    end)
    list:SetPoint("TOPLEFT", 0, -28)
    list:SetPoint("RIGHT", 0, 0)

    local function detailLines(e, out)
        local pad = "    "
        -- Names on the left, numbers in the right-hand column so they line up whatever the font.
        local function add(text, color, right) out[#out + 1] = { kind = "detail", e = e, text = pad .. text, right = right, color = color or t.muted } end
        if e.phase and e.phase ~= "done" then
            local a = Auction.Find(e.id)
            local now = GetServerTime()
            if a and a.phase == "bidding" then
                add(("Bidding, %ds left, %d bid%s placed (names hidden until reveal)%s"):format(math.max(0, (a.deadline or now) - now), #a:Committers(), #a:Committers() == 1 and "" or "s", Auction.IsPaused() and ", paused for the boss" or ""))
            else
                add("Revealing")
            end
        end
        if e.voided then add("VOIDED (" .. tostring(e.voided) .. "): nothing is settled for this sale", VOIDED) end
        if e.abandoned then add("ABANDONED (" .. tostring(e.abandoned) .. "): no result was ever announced; nothing is settled", t.bad) end
        if e.kind == "test" then add("Test auction: not settled") end
        for _, b in ipairs(e.bids or {}) do
            add(b.name .. (b.offspec and " (off-spec)" or ""), t.text, tostring(b.bid))
        end
        add("Min price", nil, tostring(e.floor or 0))
        for name, inv in pairs(e.invalid or {}) do
            add(("%s rejected: %s"):format(name, inv.reason or "?"), t.bad)
        end
        if e.flags and #e.flags > 0 then add("Checks: " .. table.concat(e.flags, ", "), t.bad) end
        if e.transfer then add(("Transferred from %s to %s at the same price"):format(e.transfer.from, e.transfer.to)) end
        local st = Tr.Status(e)
        if st == "owed" then
            local holder = Tr.Holder(e)
            add(("Item held by %s, owed to %s"):format(holder or "the leader (not looted yet)", Book.Owner(e)), t.accent)
            if Tr.Disputed(e) then add("Result disputed on this client: not traded by the helper until an officer checks", t.bad) end
            if Tr.Claim(e) then add(("Delivery claimed by %s, not confirmed by %s"):format(Tr.Claim(e), Book.Owner(e)), t.muted) end
        elseif st == "delivered" then
            add("Item delivered to " .. Book.Owner(e), t.good)
        end
        out[#out + 1] = { kind = "actions", e = e, key = e.id }
    end

    local function buildLines()
        local lines = {}
        for i, it in ipairs(Q.items) do
            local key = keyOfPending(i)
            lines[#lines + 1] = {
                kind = "header", key = key, pendingIndex = i, link = it.link, checkable = true,
                text = ("Pending: %s - %s"):format(it.link or "?", it.source or ""),
                right = "", color = t.accent,
            }
            if expanded[key] then lines[#lines + 1] = { kind = "pending-actions", key = key, pendingIndex = i } end
        end
        local entries = Book.Entries()
        for i = #entries, 1, -1 do
            local e = entries[i]
            local who, price
            if e.phase and e.phase ~= "done" then
                who, price = e.phase == "bidding" and "Bidding" or "Revealing", ""
            elseif e.abandoned then
                who, price = "Abandoned", ""
            elseif not e.winner or e.winner == "" then
                who, price = "No sale", ""
            else
                who = e.transfer and (e.winner .. " > " .. e.transfer.to) or e.winner
                price = tostring(e.price)
            end
            local note = e.voided and " VOIDED" or (Tr.Status(e) == "owed" and " (owed)" or "")
            local color = t.text
            if e.voided then color = VOIDED
            elseif e.kind == "test" or e.abandoned then color = t.muted
            elseif e.flags and #e.flags > 0 then color = t.bad end
            lines[#lines + 1] = {
                kind = "header", key = e.id, e = e, link = e.itemLink, checkable = e.kind == "loot" and not (e.phase and e.phase ~= "done"),
                text = ("#%s %s - %s%s"):format(Book.Number(e), e.itemLink or Book.Name(e), who, note),
                right = price, color = color,
            }
            if expanded[e.id] then detailLines(e, lines) end
        end
        return lines
    end

    ----------------------------------------------------------------- bottom bar
    W.Separator(panel, -(30 + ROWS * ROW_H + 4))
    local barY = -(30 + ROWS * ROW_H + 10)
    -- The three buttons sit in a fixed-width strip centred on the panel; the hint goes under them.
    local bar = CreateFrame("Frame", nil, panel)
    bar:SetSize(90 + 6 + 130 + 6 + 80, 22)
    bar:SetPoint("TOP", 0, barY)
    local checkedText = W.Label(panel, "", 11, t.muted, "CENTER")
    checkedText:SetPoint("TOPLEFT", 0, barY - 26)
    checkedText:SetPoint("RIGHT", 0, 0)
    checkedText:SetWordWrap(false)
    checkedText:SetMaxLines(1)
    checkedText:SetHeight(14)
    local clearBtn = W.Button(bar, "Clear ticks", 80, 22, function()
        checked = {}
        render()
    end)
    clearBtn:SetPoint("RIGHT", 0, 0)
    local runChecked = W.Button(bar, "Auction checked", 130, 22, function()
        local pendingIdx, finished = {}, {}
        for i in ipairs(Q.items) do
            if checked[keyOfPending(i)] then pendingIdx[#pendingIdx + 1] = i end
        end
        for _, e in ipairs(Book.Entries()) do
            if checked[e.id] then finished[#finished + 1] = e end
        end
        if #pendingIdx > 0 then startPending(pendingIdx) end
        if #finished > 0 then runAgain(finished) end
    end)
    runChecked:SetPoint("RIGHT", clearBtn, "LEFT", -6, 0)
    local allBtn = W.Button(bar, "Tick pending", 90, 22, function()
        for i in ipairs(Q.items) do checked[keyOfPending(i)] = true end
        render()
    end)
    allBtn:SetPoint("RIGHT", runChecked, "LEFT", -6, 0)

    render = function()
        local lines = buildLines()
        if #lines == 0 then lines = { { kind = "detail", text = "No loot yet this raid", color = t.muted } } end
        list:SetItems(lines)
        local running, pending = #Auction.Running(), Q.Count()
        local owed = Tr.Owed()
        local parts = {}
        if running > 0 then parts[#parts + 1] = ("%d open"):format(running) end
        if pending > 0 then parts[#parts + 1] = ("%d pending"):format(pending) end
        if #owed > 0 then parts[#parts + 1] = ("%d for you to hand over"):format(#owed) end
        summary:SetText(#parts > 0 and table.concat(parts, " - ") or "Every auction this raid. Click a row to expand it.")
        local n = 0
        for _ in pairs(checked) do n = n + 1 end
        local leader = UI.IsLeader()
        for _, w in ipairs({ checkedText, clearBtn, runChecked, allBtn }) do if leader then w:Show() else w:Hide() end end
        checkedText:SetText(n > 0 and ("%d checked"):format(n) or "Tick rows, then Auction checked runs them together.")
        runChecked:SetEnabled(n > 0)
        clearBtn:SetEnabled(n > 0)
        allBtn:SetEnabled(pending > 0)
    end

    local function refresh() render() end
    for _, e in ipairs({ "AUCTION_OPEN", "AUCTION_UPDATE", "AUCTION_CLOSE", "AUCTION_RESULT", "AUCTION_TRANSFER", "AUCTION_VOID", "RAID_CONTEXT", "GROUP_CHANGED", "LEDGER_CHANGED", "QUEUE_CHANGED" }) do Events.On(e, refresh) end
    Events.Register("GROUP_ROSTER_UPDATE", refresh)
    ns.Core.Timer.Every(1, function()
        if panel:IsShown() and Auction.IsRunning() then refresh() end
    end)
    panel.OnShow = refresh
    refresh()
end)
