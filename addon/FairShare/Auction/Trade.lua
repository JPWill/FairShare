-- FairShare/Auction/Trade.lua
-- Getting the item into the winner's bags. Knows who holds each won item (from the loot
-- lines in the transcript), who it is owed to (winner, or transferee), opens the trade
-- window on the winner with one click, and drops the item into the trade when the trade
-- window opens on them. A completed trade is recorded as delivered.
local ADDON, ns = ...

local Auction = ns.Auction
local Tr = {}
Auction.Trade = Tr

local Comm, Events, Timer, Group, T = ns.Core.Comm, ns.Core.Events, ns.Core.Timer, ns.Core.Group, ns.Transcript
local Q, Book = Auction.Queue, Auction.Book

local placed = {}   -- auctionIDs placed in the open trade window

local function raidID()
    return ns.Raid.context and ns.Raid.context.raidID or nil
end

local function deliveries()
    if not ns.db or not raidID() then return {} end
    ns.db.deliveries = ns.db.deliveries or {}
    ns.db.deliveries[raidID()] = ns.db.deliveries[raidID()] or {}
    return ns.db.deliveries[raidID()]
end

-- Entries and ownership live in Book; kept here as aliases for callers of the trade helper.
Tr.Entries = Book.Entries

-- Who last looted this item id after the auction opened, according to the transcript.
function Tr.Holder(entry)
    local id = raidID()
    if not id then return nil end
    local want = Q.ItemID(entry.itemLink)
    if not want then return nil end
    local holder
    for _, line in ipairs(T.Lines(id)) do
        local t, dir, sender, body = line:match("^(%d+)\t(%S+)\t([^\t]*)\t(.*)$")
        if dir == "loot" and body and Q.ItemID(body:match("^LOOT%^(.*)$")) == want then holder = sender end
    end
    return holder
end

Tr.Owner = Book.Owner

-- Unconfirmed hand-over claims: DELIVERED from someone other than the recipient.
local function claims()
    if not ns.db or not raidID() then return {} end
    ns.db.claims = ns.db.claims or {}
    ns.db.claims[raidID()] = ns.db.claims[raidID()] or {}
    return ns.db.claims[raidID()]
end

-- A sale whose RESULT this client disagreed with is not traded by the helper until an
-- officer has looked: the announced winner might not be the real one.
function Tr.Disputed(entry)
    for _, f in ipairs(entry.flags or {}) do
        if f == "result_mismatch" then return true end
    end
    return false
end

-- nil (not applicable), "owed" (someone still has to hand it over) or "delivered".
function Tr.Status(entry)
    if not Book.IsSale(entry) then return nil end
    local to = Tr.Owner(entry)
    if deliveries()[entry.id] then return "delivered" end
    if Tr.Holder(entry) == to then return "delivered" end
    return "owed"
end

-- Who claimed to have handed this item over without the recipient confirming, or nil.
function Tr.Claim(entry)
    local c = claims()[entry.id]
    return c and c.by or nil
end

-- Items the player still has to hand over: the holder, or the leader when nobody looted it yet.
function Tr.Owed()
    local me, out = Group.Me(), {}
    for _, e in ipairs(Tr.Entries()) do
        if Tr.Status(e) == "owed" and not Tr.Disputed(e) then
            local holder = Tr.Holder(e)
            if holder == me or (holder == nil and ns.Raid.IsLeader()) then
                out[#out + 1] = { entry = e, to = Tr.Owner(e), holder = holder }
            end
        end
    end
    return out
end

-- Record a hand-over here and tell the raid, so everyone's Ledger marks it delivered
-- too. Other clients trust the message only from the recipient; from anyone else it is
-- shown as a claim. The backend ignores it either way.
function Tr.MarkDelivered(auctionID, to)
    deliveries()[auctionID] = { to = to, at = GetServerTime(), by = Group.Me() }
    Comm.Send("DELIVERED", { auctionID, to })
    Events.Fire("LEDGER_CHANGED")
end

Comm.On("DELIVERED", function(sender, f)
    local id, to = f[1], f[2]
    if not id or id == "" or not to or to == "" then return end
    local entry = Book.Find(id)
    local owner = entry and Book.Owner(entry) or nil
    if sender == to or (owner and sender == owner) then
        local d = deliveries()
        if not d[id] then
            d[id] = { to = to, at = GetServerTime(), by = sender }
            claims()[id] = nil
            Events.Fire("LEDGER_CHANGED")
        end
    else
        claims()[id] = { by = sender, to = to, at = GetServerTime() }
        Events.Fire("LEDGER_CHANGED")
    end
end)

-- Where completed trades are announced: "raid" (party when not in a raid, nothing when
-- solo), "whisper" (to the recipient), or "none". Settings owns the choice.
function Tr.AnnounceChannel()
    return (ns.db and ns.db.settings and ns.db.settings.announce) or "raid"
end

function Tr.SetAnnounceChannel(v)
    if ns.db then ns.db.settings.announce = v end
end

-- Tell the raid an item changed hands through the addon, so the trade is on record in chat too.
function Tr.Announce(entry, to)
    local where = Tr.AnnounceChannel()
    if where == "none" or not SendChatMessage or not entry then return end
    local msg = ("FairShare: %s traded to %s (auction #%s, %d points)"):format(entry.itemLink or "item", to, Book.Number(entry), entry.price or 0)
    if where == "whisper" then
        SendChatMessage(msg, "WHISPER", nil, to)
    elseif IsInRaid() then
        SendChatMessage(msg, "RAID")
    elseif IsInGroup() then
        SendChatMessage(msg, "PARTY")
    end
end

-- Unit token for a group member by name, or nil.
function Tr.UnitFor(name)
    if name == Group.Me() then return "player" end
    local prefix, n = "raid", 0
    if IsInRaid() then n = GetNumGroupMembers() elseif IsInGroup() then prefix, n = "party", GetNumGroupMembers() - 1 end
    for i = 1, n do
        local unit = prefix .. i
        local u = UnitName(unit)
        if u and Group.Normalize(u) == name then return unit end
    end
    return nil
end

-- Open a trade with the owner of an entry. Returns ok, err.
function Tr.Initiate(entry)
    local to = Tr.Owner(entry)
    if to == "" then return false, "nothing to trade" end
    if to == Group.Me() then return false, "you are the owner" end
    local unit = Tr.UnitFor(to)
    if not unit then return false, to .. " is not in the group" end
    if not InitiateTrade then return false, "trading is not available here" end
    InitiateTrade(unit)
    return true
end

local function findInBags(itemID)
    if not C_Container or not C_Container.GetContainerNumSlots then return nil end
    for bag = 0, (NUM_BAG_SLOTS or 4) do
        for slot = 1, C_Container.GetContainerNumSlots(bag) or 0 do
            local link = C_Container.GetContainerItemLink(bag, slot)
            if link and Q.ItemID(link) == itemID then return bag, slot end
        end
    end
    return nil
end

-- When a trade opens with someone we owe items to, put them in the trade window.
Events.Register("TRADE_SHOW", function()
    placed = {}
    local partner = UnitName("NPC")
    if not partner then return end
    partner = Group.Normalize(partner)
    local tradeSlot = 1
    for _, o in ipairs(Tr.Owed()) do
        if o.to == partner and tradeSlot <= 6 then
            local bag, slot = findInBags(Q.ItemID(o.entry.itemLink))
            if bag and C_Container.PickupContainerItem and ClickTradeButton then
                C_Container.PickupContainerItem(bag, slot)
                ClickTradeButton(tradeSlot)
                placed[#placed + 1] = o.entry.id
                tradeSlot = tradeSlot + 1
                ns.Print(("trade: added %s for %s"):format(o.entry.itemLink, partner))
            else
                ns.Print(("trade: %s is not in your bags"):format(o.entry.itemLink))
            end
        end
    end
end)

-- Item ids the partner has put in the trade window, so the recipient's own client can
-- confirm a delivery (the only DELIVERED other clients trust).
local incoming = {}

Events.Register("TRADE_TARGET_ITEM_CHANGED", function()
    if not GetTradeTargetItemLink then return end
    for i = 1, 6 do
        local link = GetTradeTargetItemLink(i)
        local id = link and Q.ItemID(link)
        if id then incoming[id] = true end
    end
end)

Events.Register("UI_INFO_MESSAGE", function(_, _, message)
    if message ~= (ERR_TRADE_COMPLETE or "Trade complete.") then return end
    local partner = UnitName("NPC")
    partner = partner and Group.Normalize(partner) or nil
    for _, id in ipairs(placed) do
        local entry, to
        for _, e in ipairs(Tr.Entries()) do if e.id == id then entry, to = e, Tr.Owner(e) end end
        to = to or partner or "?"
        Tr.MarkDelivered(id, to)
        Tr.Announce(entry, to)
    end
    -- As the recipient: anything owed to me that the holder just traded over is delivered,
    -- and my saying so is what every other client accepts.
    local me = Group.Me()
    for _, e in ipairs(Tr.Entries()) do
        if Tr.Status(e) == "owed" and Tr.Owner(e) == me and partner and Tr.Holder(e) == partner then
            local itemID = Q.ItemID(e.itemLink)
            if not GetTradeTargetItemLink or (itemID and incoming[itemID]) then
                Tr.MarkDelivered(e.id, me)
            end
        end
    end
    placed, incoming = {}, {}
end)

Events.Register("TRADE_CLOSED", function()
    Timer.After(1, function() placed, incoming = {}, {} end)
end)

Events.On("AUCTION_RESULT", function() Events.Fire("LEDGER_CHANGED") end)
Events.On("AUCTION_TRANSFER", function() Events.Fire("LEDGER_CHANGED") end)
Events.On("TRANSCRIPT_LINE", function(_, line)
    if line:find("\tloot\t", 1, true) then Events.Fire("LEDGER_CHANGED") end
end)
