-- FairShare/Auction/State.lua
-- Pure auction state machine: no UI, no messaging, no timers. One instance per auction.
-- Implements docs/protocol.md §3 exactly; the Go backend replays with the same rules.
local ADDON, ns = ...

local Sha = ns.Lib.Sha256
local State = {}
ns.Auction.State = State

local A = {}
A.__index = A

function State.Hash(auctionID, name, bid, offspec, salt)
    return Sha.hex(auctionID .. "|" .. name .. "|" .. tostring(bid) .. "|" .. (offspec and "1" or "0") .. "|" .. salt)
end

-- Tags: an extra hash over the player's addon key (issued by the bot, pasted into
-- Settings) that rides on COMMIT, REVEAL and ACK. Clients ignore it; the backend checks
-- it against the key it issued, so nobody can write another player's bid into a
-- transcript. A key is 32 lowercase hex characters; anything else means "no key".
function State.ValidKey(key)
    return type(key) == "string" and key:match("^%x+$") ~= nil and #key == 32
end

function State.MyKey()
    local key = ns.db and ns.db.settings and ns.db.settings.addonKey
    if State.ValidKey(key) then return key:lower() end
    return nil
end

function State.CommitTag(key, auctionID, hash)
    return Sha.hex(key .. "|" .. auctionID .. "|" .. hash)
end

function State.RevealTag(key, auctionID, bid, offspec, salt)
    return Sha.hex(key .. "|" .. auctionID .. "|" .. tostring(bid) .. "|" .. (offspec and "1" or "0") .. "|" .. salt)
end

function State.AckTag(key, newLeader)
    return Sha.hex(key .. "|LEAD|" .. newLeader)
end

function State.ApplicableFloor(floor, offspec, offspecPct)
    if not offspec then return floor end
    return math.ceil(floor * (100 - (offspecPct or 0)) / 100)
end

-- What name may still bid: snapshot balance + credit, less what they already owe for sales
-- announced so far this raid (spent). nil when not in the snapshot. Judged when the result
-- is announced, not when the bid is placed, so auctions can run side by side (§3.1).
function State.MaxBid(ctx, name, spent)
    local row = ctx.rows[name]
    if not row then return nil end
    return row.balance + row.credit - ((spent and spent[name]) or 0)
end

-- ctx = { rows = {name = {owner,balance,credit}}, banned = {name=true}, offspecPct }
-- open = { auctionID, kind, itemLink, floor, deadline, leader }
function State.New(ctx, open)
    return setmetatable({
        ctx = ctx,
        id = open.auctionID, kind = open.kind or "loot", itemLink = open.itemLink,
        floor = open.floor, deadline = open.deadline, leader = open.leader,
        phase = "bidding",          -- bidding -> reveal -> done
        commits = {},               -- name -> hash
        reveals = {},               -- name -> {bid, offspec, salt, floor}
        invalid = {},               -- name -> {bid, offspec, salt, reason}
        flags = {},
        closeList = nil,
        result = nil,               -- what the leader announced
        computed = nil,             -- what we computed
    }, A)
end

function A:Flag(f)
    self.flags[#self.flags + 1] = f
end

function A:Commit(sender, hash)
    if self.phase ~= "bidding" then
        self:Flag("late_commit:" .. sender)
        return false
    end
    -- The first commit binds; a second one from the same name is a client trying to
    -- choose later which bid to reveal. The backend keeps the first too.
    if self.commits[sender] then
        self:Flag("duplicate_commit:" .. sender)
        return false
    end
    self.commits[sender] = hash
    return true
end

-- A reveal refused before it is even checked against the commit (wrong phase, malformed
-- fields). Recorded like any other invalid reveal so the Ledger and the backend agree.
function A:RejectReveal(sender, reason, bid, offspec, salt)
    if self.reveals[sender] or self.invalid[sender] then
        self:Flag("invalid_reveal:" .. sender .. ":duplicate")
        return false, "duplicate"
    end
    self.invalid[sender] = { bid = bid, offspec = offspec, salt = salt, reason = reason }
    self:Flag("invalid_reveal:" .. sender .. ":" .. reason)
    return false, reason
end

function A:Committers()
    local list = {}
    for name in pairs(self.commits) do list[#list + 1] = name end
    table.sort(list)
    return list
end

function A:Close(list)
    self.phase = "reveal"
    self.closeList = list
    if table.concat(self:Committers(), ",") ~= table.concat(list, ",") then
        self:Flag("close_mismatch")
    end
end

-- Returns true, or false and a reason. Invalid reveals are kept for the record and excluded.
function A:Reveal(sender, bid, offspec, salt)
    local hash = self.commits[sender]
    local reason
    if not hash then
        reason = "no_commit"
    elseif self.reveals[sender] or self.invalid[sender] then
        reason = "duplicate"
    elseif type(salt) ~= "string" or State.Hash(self.id, sender, bid, offspec, salt) ~= hash then
        reason = "hash_mismatch"
    else
        local row = self.ctx.rows[sender]
        if not row then
            reason = "unregistered"
        elseif self.ctx.banned[sender] then
            reason = "banned"
        elseif type(bid) ~= "number" or bid < 0 or bid ~= math.floor(bid) then
            reason = "bad_bid"
        elseif bid < State.ApplicableFloor(self.floor, offspec, self.ctx.offspecPct) then
            reason = "under_floor"
        end
    end
    if reason then
        self.invalid[sender] = { bid = bid, offspec = offspec, salt = salt, reason = reason }
        self:Flag("invalid_reveal:" .. sender .. ":" .. reason)
        return false, reason
    end
    self.reveals[sender] = { bid = bid, offspec = offspec, salt = salt, floor = State.ApplicableFloor(self.floor, offspec, self.ctx.offspecPct) }
    return true
end

function A:AllRevealed()
    for name in pairs(self.commits) do
        if not self.reveals[name] and not self.invalid[name] then return false end
    end
    return true
end

-- Winner and price from the valid reveals, judged against the running cap: spent is what
-- each name already owes from sales announced before this result, and a bid past
-- balance + credit - spent is set aside (returned as the third value, name -> true).
-- "" and 0 when nothing valid remains.
function A:Compute(spent)
    local names, over = {}, {}
    for n, r in pairs(self.reveals) do
        local cap = State.MaxBid(self.ctx, n, spent) or 0
        if r.bid > cap then over[n] = true else names[#names + 1] = n end
    end
    table.sort(names)
    if #names == 0 then return "", 0, over end

    local top = -1
    for _, n in ipairs(names) do
        if self.reveals[n].bid > top then top = self.reveals[n].bid end
    end
    local tied = {}
    for _, n in ipairs(names) do
        if self.reveals[n].bid == top then tied[#tied + 1] = n end
    end

    local winner
    if #tied == 1 then
        winner = tied[1]
    else
        local salts = {}
        for _, n in ipairs(names) do salts[#salts + 1] = self.reveals[n].salt end
        local T = Sha.hex(table.concat(salts))
        local best
        for _, n in ipairs(tied) do
            local score = Sha.hex(T .. "|" .. n)
            if not best or score < best then best, winner = score, n end
        end
    end

    local second
    for _, n in ipairs(names) do
        if n ~= winner then
            local b = self.reveals[n].bid
            if not second or b > second then second = b end
        end
    end
    local wfloor = self.reveals[winner].floor
    local price = second and math.max(second, wfloor) or wfloor
    return winner, price, over
end

-- Record the leader's RESULT, compare with our own computation under the running cap (spent:
-- what each name owes from sales announced before this one). Returns true when they agree.
function A:Result(winner, price, spent)
    self.phase = "done"
    self.spentAtResult = spent or {}
    for name in pairs(self.commits) do
        if not self.reveals[name] and not self.invalid[name] then self:Flag("noreveal:" .. name) end
    end
    local w, p, over = self:Compute(spent)
    for name in pairs(over) do
        local r = self.reveals[name]
        self.invalid[name] = { bid = r.bid, offspec = r.offspec, salt = r.salt, reason = "over_max" }
        self.reveals[name] = nil
        self:Flag("invalid_reveal:" .. name .. ":over_max")
    end
    self.computed = { winner = w, price = p }
    self.result = { winner = winner or "", price = price or 0 }
    local ok = (w == self.result.winner) and (p == self.result.price)
    if not ok then self:Flag("result_mismatch") end
    return ok
end

-- Leader-announced transfer of the won item to someone else at the same price (§3.7).
-- Returns true, or false and a reason; invalid transfers are flagged and ignored.
function A:Transfer(from, to)
    local reason
    if self.phase ~= "done" then reason = "not_done"
    elseif self.kind ~= "loot" then reason = "not_loot"
    elseif not self.result or self.result.winner == "" or self.result.winner ~= from then reason = "not_winner"
    elseif self.transfer then reason = "duplicate"
    elseif not self.ctx.rows[to] then reason = "unregistered"
    elseif self.ctx.banned[to] then reason = "banned"
    else
        local row = self.ctx.rows[to]
        if State.MaxBid(self.ctx, to, self.spentAtResult) < self.result.price then reason = "over_max" end
    end
    if reason then
        self:Flag("bad_transfer:" .. tostring(to) .. ":" .. reason)
        return false, reason
    end
    self.transfer = { from = from, to = to }
    return true
end

-- Leader-announced void after the result: the sale is struck from the books and the item
-- goes up again under a new id. Returns true, or false and a reason.
function A:Void(reason)
    if self.phase ~= "done" then return false, "not_done" end
    if self.voided then return false, "duplicate" end
    self.voided = reason or "voided"
    return true
end

-- Who ends up paying: the transferee if a valid transfer happened, else the winner.
function A:EffectiveWinner()
    if self.transfer then return self.transfer.to end
    return self.result and self.result.winner or ""
end

-- Sorted list of { name, bid, offspec } for display after reveal (valid bids only).
function A:Bids()
    local out = {}
    for n, r in pairs(self.reveals) do out[#out + 1] = { name = n, bid = r.bid, offspec = r.offspec } end
    table.sort(out, function(x, y)
        if x.bid ~= y.bid then return x.bid > y.bid end
        return x.name < y.name
    end)
    return out
end

-- Plain table safe to store in SavedVariables.
function A:Summary()
    return {
        id = self.id, kind = self.kind, itemLink = self.itemLink, floor = self.floor,
        winner = self.result and self.result.winner or "", price = self.result and self.result.price or 0,
        bids = self:Bids(), invalid = self.invalid, flags = self.flags, committers = self:Committers(),
        closedAt = self.closedAt, resultAt = self.resultAt, transfer = self.transfer, voided = self.voided, abandoned = self.abandoned,
        openedAt = self.openedAt,
    }
end
