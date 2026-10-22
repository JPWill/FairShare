-- FairShare/UI/Bid.lua
-- The auctions window, the same for every raider. One card per auction, stacked in
-- number order, collapsible like the Ledger:
--   head:   +/-  item   [time bar]   your bid / result
--   open:   slider | bid box
--           Pass  Off-spec  Bid
--           (a line only while the bid runs past your shares: how much goes on credit)
--   after:  status and the bids
-- The window stays until the player closes it; finished cards stay until Clear finished.
local ADDON, ns = ...

local UI, Events, Timer, Auction, Group = ns.UI, ns.Core.Events, ns.Core.Timer, ns.Auction, ns.Core.Group
local State, Book = Auction.State, Auction.Book

local frame, ticker
local WIDTH = 380
local HEAD_H, INPUT_H, DEBT_H, LIST_ROWS, LIST_ROW_H = 26, 56, 16, 5, 14
local BAR_H = 5
local MAX_STACK = 420          -- content height the cards may use before they scroll
local cards = {}               -- auctionID -> card
local pool = {}                -- released cards
local offset = 0

local function number(id)
    return tonumber((id or ""):match("%-(%d+)$")) or 0
end

----------------------------------------------------------------------------- cards

local function limitsTip(self)
    local card = self.card
    local a = card and card.auctionID and Auction.Find(card.auctionID)
    local row = a and a.ctx.rows[Group.Me()]
    if not row then return end
    local me = Group.Me()
    local offspec = card.offspec:GetChecked() and true or false
    local floor = State.ApplicableFloor(a.floor, offspec, a.ctx.offspecPct)
    local max = Auction.MaxBid(a) or 0
    local owed = Book.Spent()[me] or 0
    GameTooltip:SetOwner(self, "ANCHOR_TOP")
    GameTooltip:SetText("Your bid", 1, 1, 1)
    GameTooltip:AddLine(("Min price %d. Your shares %d, credit %d."):format(floor, row.balance, row.credit), 0.9, 0.9, 0.9, true)
    if owed > 0 then GameTooltip:AddLine(("You already owe %d from earlier sales tonight."):format(owed), 0.9, 0.9, 0.9, true) end
    if max < row.balance + row.credit - owed then GameTooltip:AddLine("Bids you placed in other open auctions are counted too.", 0.9, 0.9, 0.9, true) end
    GameTooltip:AddLine(("You can bid up to %d shares. Past %d the rest goes on credit."):format(max, math.max(0, row.balance - owed)), 0.9, 0.9, 0.9, true)
    GameTooltip:Show()
end

local function newCard()
    local W, t = UI.Widgets, UI.Skin.Current()
    local card = CreateFrame("Frame", nil, frame.content)
    card.auctionID = false
    card.override = "auto"       -- auto | open | closed
    card.deadlineFor = false
    card.deadlineClient = 0
    card.passed = false
    card.rangeKey = ""
    card.syncing = false

    local head = CreateFrame("Button", nil, card)
    head:SetPoint("TOPLEFT", 0, 0)
    head:SetPoint("TOPRIGHT", 0, 0)
    head:SetHeight(HEAD_H)
    head.card = card
    head.hl = head:CreateTexture(nil, "BACKGROUND")
    head.hl:SetTexture("Interface\\Buttons\\WHITE8x8")
    head.hl:SetAllPoints()
    head.hl:SetVertexColor(t.accent[1], t.accent[2], t.accent[3], 0.10)
    card.head = head
    card.toggle = W.Label(head, "+", 12, t.muted)
    card.toggle:SetPoint("LEFT", 2, 0)
    card.item = W.ItemLabel(head, 12)
    card.item:SetPoint("TOPLEFT", 14, -1)
    card.item:SetPoint("RIGHT", -124, 0)
    -- Time left is a bar across the row that drains as the clock runs, no number.
    card.bar = W.Bar(head, 100, BAR_H)
    card.bar:ClearAllPoints()
    card.bar:SetPoint("BOTTOMLEFT", 14, 1)
    card.bar:SetPoint("BOTTOMRIGHT", 0, 1)
    card.state = W.Label(head, "", 11, t.muted, "RIGHT")
    card.state:SetPoint("TOPRIGHT", -8, -3)
    card.state:SetWidth(118)
    card.state:SetWordWrap(false)
    head:SetScript("OnClick", function(self)
        local c = self.card
        c.override = c.shownExpanded and "closed" or "open"
        frame.Refresh()
    end)
    head:SetScript("OnEnter", function(self)
        local link = self.card.item.link
        if not link then return end
        GameTooltip:SetOwner(self, "ANCHOR_RIGHT")
        GameTooltip:SetHyperlink(link)
        GameTooltip:Show()
    end)
    head:SetScript("OnLeave", function() GameTooltip:Hide() end)

    -- Input body
    local input = CreateFrame("Frame", nil, card)
    input:SetPoint("TOPLEFT", 14, -HEAD_H)
    input:SetPoint("TOPRIGHT", 0, -HEAD_H)
    input:SetHeight(INPUT_H)
    card.input = input
    card.box = W.EditBox(input, 64, 20)
    card.box:SetPoint("TOPRIGHT", -2, -4)
    card.box:SetNumeric(true)
    card.box:SetMaxLetters(7)
    card.box.card = card
    card.slider = W.Slider(input, WIDTH - 20 - 14 - 84, 14, function(value)
        if card.syncing then return end
        card.syncing = true
        card.box:SetText(tostring(value))
        card.syncing = false
        frame.Refresh()
    end)
    card.slider:SetPoint("TOPLEFT", 0, -6)
    card.stickText = W.Label(input, "", 9, t.accent)
    card.stickText:SetPoint("TOP", card.slider.mark, "BOTTOM", 0, -1)
    card.pass = W.Button(input, "Pass", 56, 20, function()
        card.passed = true
        card.override = "closed"
        frame.Refresh()
    end)
    card.pass:SetPoint("BOTTOMLEFT", 0, 2)
    card.offspec = W.Checkbox(input, "Off-spec", function() frame.Refresh() end)
    card.offspec:SetPoint("LEFT", card.pass, "RIGHT", 16, 0)
    card.bid = W.Button(input, "Bid", 64, 20, function() card:PlaceBid() end)
    card.bid:SetPoint("BOTTOMRIGHT", -2, 2)
    card.box:SetScript("OnEnterPressed", function() card:PlaceBid() end)
    card.box:SetScript("OnTextChanged", function(self)
        if card.syncing then return end
        card.syncing = true
        local v = tonumber(self:GetText())
        if v then card.slider:SetValue(v) end
        card.syncing = false
        frame.Refresh()
    end)
    card.box:SetScript("OnEnter", limitsTip)
    card.box:SetScript("OnLeave", function() GameTooltip:Hide() end)
    if card.slider.track and card.slider.track.HookScript then
        card.slider.track.card = card
        card.slider.track:HookScript("OnEnter", limitsTip)
        card.slider.track:HookScript("OnLeave", function() GameTooltip:Hide() end)
    end
    card.debt = W.Label(card, "", 10, t.bad, "LEFT")
    card.debt:SetPoint("TOPLEFT", 14, -(HEAD_H + INPUT_H))
    card.debt:SetPoint("RIGHT", 0, 0)
    card.debt:SetWordWrap(false)

    -- Result body
    local result = CreateFrame("Frame", nil, card)
    result:SetPoint("TOPLEFT", 14, -HEAD_H)
    result:SetPoint("TOPRIGHT", 0, -HEAD_H)
    result:SetHeight(20)
    card.result = result
    card.status = W.Label(result, "", 11, t.text, "LEFT")
    card.status:SetPoint("TOPLEFT", 0, -2)
    card.status:SetPoint("RIGHT", 0, 0)
    card.status:SetWordWrap(false)
    card.list = W.List(result, LIST_ROWS, LIST_ROW_H)
    card.list:SetPoint("TOPLEFT", 0, -18)
    card.list:SetPoint("RIGHT", 0, 0)

    function card:PlaceBid()
        local a = self.auctionID and Auction.Find(self.auctionID)
        if not a then return end
        local ok, err = Auction.Bid(self.box:GetText(), self.offspec:GetChecked(), a)
        if not ok then
            self.debt:SetText(err)
            self.debt:Show()
            return
        end
        self.override = "auto"
        frame.Refresh()
    end

    -- Set the slider's range for this auction and the current off-spec choice. Only when it
    -- changes, so the half-second refresh never moves the thumb under the player's cursor.
    function card:SetRange(a, row, floor, max)
        local owed = Book.Spent()[Group.Me()] or 0
        local key = a.id .. "|" .. floor .. "|" .. max .. "|" .. row.balance .. "|" .. owed
        if self.rangeKey == key then return end
        self.rangeKey = key
        self.slider:SetRange(floor, max, row.balance - owed)
        if self.slider:GetStick() then
            self.stickText:SetText(("shares %d"):format(row.balance - owed))
            self.stickText:Show()
        else
            self.stickText:Hide()
        end
        local v = tonumber(self.box:GetText()) or floor
        self.syncing = true
        self.slider:SetValue(math.max(floor, math.min(max, v)))
        self.box:SetText(tostring(self.slider:GetValue() or v))
        self.syncing = false
    end

    -- Fill the card for auction a and return its height.
    function card:Bind(a)
        local W2, t2 = UI.Widgets, UI.Skin.Current()
        local me = Group.Me()
        local now = GetServerTime()
        local row = a.ctx.rows[me]
        local mine = Auction.MyBid(a)
        local blocker = Auction.BidBlocker(a)
        local offspec = self.offspec:GetChecked() and true or false
        local floor = State.ApplicableFloor(a.floor, offspec, a.ctx.offspecPct)
        local max = row and (Auction.MaxBid(a) or 0) or 0
        local canBid = a.phase == "bidding" and a.kind ~= "test" and not mine and not blocker and row ~= nil and max >= floor and not self.passed
        self.item:SetLink(a.itemLink)
        if a.kind == "test" then self.item.text:SetText("Test auction " .. (a.itemLink or "")) end

        -- Head: time bar and state. The bar drains on the client clock, smoothly, from the
        -- moment this card first saw the auction, against the deadline the leader sent.
        if a.phase == "bidding" then
            if self.deadlineFor ~= a.id then
                self.deadlineFor = a.id
                self.deadlineClient = (GetTime and GetTime() or now) + math.max(0, (a.deadline or now) - now)
            end
            local clock = GetTime and GetTime() or now
            local left = math.max(0, self.deadlineClient - clock)
            self.bar:SetFraction(left / Auction.BID_SECONDS)
            self.bar:SetColor(Auction.IsPaused() and t2.bad or t2.accent)
            self.bar:SetText("")
            self.bar:Show()
            if a.kind == "test" then self.state:SetText("Test, replied")
            elseif mine then self.state:SetText(("your bid %d%s"):format(mine.bid, mine.offspec and " os" or ""))
            elseif self.passed then self.state:SetText("Passed")
            elseif blocker then self.state:SetText("Watching")
            elseif max < floor then self.state:SetText("Over your cap")
            else self.state:SetText("Bid now") end
            self.state:SetTextColor(unpack(mine and t2.accent or t2.muted))
        elseif a.phase == "reveal" then
            self.bar:SetFraction(1)
            self.bar:SetColor(t2.muted)
            self.bar:SetText("Reveal")
            self.state:SetText(mine and ("your bid %d"):format(mine.bid) or "revealing")
            self.state:SetTextColor(unpack(t2.muted))
        else
            self.bar:SetFraction(0)
            self.bar:SetText("")
            local r = a.result or { winner = "", price = 0 }
            if a.abandoned then self.state:SetText("Abandoned"); self.state:SetTextColor(unpack(t2.muted))
            elseif a.voided then self.state:SetText("Voided"); self.state:SetTextColor(unpack(t2.muted))
            elseif r.winner == "" then self.state:SetText("No sale"); self.state:SetTextColor(unpack(t2.muted))
            elseif r.winner == me then self.state:SetText(("You Won!  %d"):format(r.price)); self.state:SetTextColor(unpack(t2.good))
            else self.state:SetText(("%s, %d"):format(r.winner, r.price)); self.state:SetTextColor(unpack(t2.text)) end
        end

        -- Expanded?
        local expanded
        if self.override == "open" then expanded = true
        elseif self.override == "closed" then expanded = false
        else expanded = canBid end
        self.shownExpanded = expanded
        self.toggle:SetText(expanded and "-" or "+")
        if not expanded then
            self.input:Hide(); self.result:Hide(); self.debt:Hide()
            return HEAD_H
        end

        if canBid then
            self:SetRange(a, row, floor, max)
            self.result:Hide()
            self.input:Show()
            local v = tonumber(self.box:GetText())
            local shares = math.max(0, row.balance - (Book.Spent()[me] or 0))
            if v and v > shares then
                self.debt:SetText(("%d from your shares, %d on credit (you can go to %d)"):format(shares, v - shares, max))
                self.debt:Show()
                return HEAD_H + INPUT_H + DEBT_H
            end
            self.debt:Hide()
            return HEAD_H + INPUT_H
        end

        -- Status and bids
        self.input:Hide(); self.debt:Hide()
        self.result:Show()
        local rows = {}
        self.status:SetTextColor(unpack(t2.text))
        if a.phase == "bidding" then
            if a.kind == "test" then self.status:SetText(mine and "Test auction: your reply was sent automatically." or (blocker or "Test auction"))
            elseif mine then self.status:SetText(("Bid placed: %d%s. Waiting for bidding to close."):format(mine.bid, mine.offspec and " (off-spec)" or ""))
            elseif self.passed then self.status:SetText("You passed. Click the row to change your mind while bidding is open."); self.status:SetTextColor(unpack(t2.muted))
            elseif blocker then self.status:SetText(blocker); self.status:SetTextColor(unpack(t2.muted))
            else self.status:SetText(("You can bid at most %d after what you already owe or bid elsewhere; the min price is %d."):format(max, floor)); self.status:SetTextColor(unpack(t2.muted)) end
        elseif a.phase == "reveal" then
            local revealed, total = 0, #a:Committers()
            for _ in pairs(a.reveals) do revealed = revealed + 1 end
            for _ in pairs(a.invalid) do revealed = revealed + 1 end
            self.status:SetText(("Bidding closed. %d of %d revealed."):format(revealed, total))
            for i, b in ipairs(a:Bids()) do rows[i] = ("%-24s %6d%s"):format(b.name, b.bid, b.offspec and "  off-spec" or "") end
        else
            local r = a.result or { winner = "", price = 0 }
            if a.abandoned then self.status:SetText("Abandoned: no result was announced."); self.status:SetTextColor(unpack(t2.muted))
            elseif r.winner == "" then self.status:SetText("No bids. Item not sold.")
            elseif r.winner == me then self.status:SetText(("You won for %d shares."):format(r.price)); self.status:SetTextColor(unpack(t2.good))
            else self.status:SetText(("%s won for %d shares."):format(r.winner, r.price)) end
            if #a.flags > 0 then
                self.status:SetText(self.status:GetText() .. "  FLAGGED: " .. table.concat(a.flags, ", "))
                self.status:SetTextColor(unpack(t2.bad))
            end
            for i, b in ipairs(a:Bids()) do rows[i] = ("%-24s %6d%s"):format(b.name, b.bid, b.offspec and "  off-spec" or "") end
        end
        self.list:SetRows(rows)
        local n = math.min(LIST_ROWS, #rows)
        self.result:SetHeight(20 + n * LIST_ROW_H)
        return HEAD_H + 20 + n * LIST_ROW_H
    end

    return card
end

local function acquire(id)
    local card = table.remove(pool) or newCard()
    card.auctionID = id
    card.override = "auto"
    card.passed = false
    card.rangeKey = ""
    card.box:SetText("")
    card.offspec:SetChecked(false)
    cards[id] = card
    return card
end

local function release(id)
    local card = cards[id]
    if not card then return end
    cards[id] = nil
    card.auctionID = false
    card:Hide()
    pool[#pool + 1] = card
end

----------------------------------------------------------------------------- window

local function build()
    local W, t = UI.Widgets, UI.Skin.Current()
    frame = W.Window("FairShareBidFrame", "Auction", WIDTH, 80, { minimize = true })
    frame.OnMinimize = function() frame.Refresh() end
    frame:ClearAllPoints()
    frame:SetPoint("TOP", UIParent, "TOP", 0, -140)
    frame:SetFrameStrata("FULLSCREEN_DIALOG")
    frame.content:EnableMouseWheel(true)
    frame.content:SetScript("OnMouseWheel", function(_, delta)
        offset = math.max(0, offset - delta)
        frame.Refresh()
    end)
    frame.empty = W.Label(frame.content, "no auctions open", 11, t.muted)
    frame.empty:SetPoint("TOP", 0, -12)
    frame.clearBtn = W.Button(frame.content, "Clear finished", 110, 20, function()
        for id in pairs(cards) do
            local a = Auction.Find(id)
            if not a or a.phase == "done" then release(id) end
        end
        frame.Refresh()
    end)
    frame.closeBtn = W.Button(frame.content, "Close", 64, 20, function() frame:Hide() end)

    -- Cards in number order: everything open, plus finished ones not yet cleared.
    function frame.Cards()
        local ids = {}
        for _, a in ipairs(Auction.Running()) do
            if not cards[a.id] then acquire(a.id) end
        end
        for id in pairs(cards) do ids[#ids + 1] = id end
        table.sort(ids, function(x, y) return number(x) < number(y) end)
        local out = {}
        for _, id in ipairs(ids) do out[#out + 1] = cards[id] end
        return out
    end

    function frame.FirstBiddable()
        for _, card in ipairs(frame.Cards()) do
            local a = Auction.Find(card.auctionID)
            if a and card.shownExpanded and not Auction.MyBid(a) and not Auction.BidBlocker(a) then return card end
        end
    end

    function frame.Refresh()
        local me = Group.Me()
        local row = ns.Raid.MyRow()
        local bal = row and (row.balance + (Book.Deltas()[me] or 0)) or nil
        frame.title:SetText(("Auctions - %s%s"):format(me, bal and (" - %d Shares"):format(bal) or ""))
        local list = frame.Cards()
        offset = math.max(0, math.min(offset, #list - 1))
        local y, shown = 6, 0
        for i, card in ipairs(list) do
            local a = Auction.Find(card.auctionID)
            if not a then
                release(card.auctionID)
            elseif i <= offset then
                card:Hide()
            else
                local h = card:Bind(a)
                if shown > 0 and y + h > MAX_STACK then
                    card:Hide()
                else
                    card:ClearAllPoints()
                    card:SetPoint("TOPLEFT", 10, -y)
                    card:SetPoint("RIGHT", -10, 0)
                    card:SetHeight(h)
                    card:Show()
                    y = y + h + 4
                    shown = shown + 1
                end
            end
        end
        if #list == 0 then frame.empty:Show(); y = 30 else frame.empty:Hide() end
        frame.clearBtn:ClearAllPoints()
        frame.clearBtn:SetPoint("TOPLEFT", 10, -(y + 4))
        frame.closeBtn:ClearAllPoints()
        frame.closeBtn:SetPoint("TOPRIGHT", -10, -(y + 4))
        frame:SetContentHeight(y + 34)
    end
end

local function startTicker()
    if ticker then return end
    ticker = Timer.Every(0.1, function()
        if frame and frame:IsShown() and Auction.IsRunning() then frame.Refresh() end
    end)
end

function UI.ShowBid()
    if not frame then build() end
    frame:Show()
    if frame.minimized then frame:SetMinimized(false) end
    frame.Refresh()
    local card = frame.FirstBiddable()
    if card then
        card.box:SetFocus()
        card.box:HighlightText()
    end
    startTicker()
end

Events.On("AUCTION_OPEN", function()
    UI.ShowBid()
    if ns.db and ns.db.settings.sound and PlaySound and SOUNDKIT and SOUNDKIT.RAID_WARNING then PlaySound(SOUNDKIT.RAID_WARNING) end
end)
for _, e in ipairs({ "AUCTION_UPDATE", "AUCTION_CLOSE", "AUCTION_RESULT", "AUCTION_VOID" }) do
    Events.On(e, function() if frame and frame:IsShown() then frame.Refresh() end end)
end
Events.On("RAID_CONTEXT", function(c)
    if c == nil and frame then
        for id in pairs(cards) do release(id) end
        frame.Refresh()
    end
end)
