-- FairShare/Auction/Protocol.lua
-- Wires the state machine to messages and timers. The leader opens, closes, and announces;
-- every client (leader included) runs the same state machine and checks the leader's work.
-- Any number of auctions may be open at once: every message carries its auction id, each
-- auction has its own state and timers, and only the RESULTs go out one at a time, in
-- order, because each result is judged against what earlier results already charged.
local ADDON, ns = ...

local Auction = ns.Auction
local State = Auction.State
local Comm, Events, Timer, Group, S = ns.Core.Comm, ns.Core.Events, ns.Core.Timer, ns.Core.Group, ns.Lib.Serialize

Auction.BID_SECONDS = 20
Auction.REVEAL_SECONDS = 15
Auction.open = {}          -- auctionID -> state, for auctions that have not reached a result
Auction.active = nil       -- the most recently opened auction (convenience for panels and tests)
Auction.history = {}       -- finished auctions this session, oldest first

local counter = 0          -- leader's auction number within the raid
local myBids = {}          -- auctionID -> { bid, offspec, salt }
local paused = false       -- inside a boss encounter
local awaitingEcho = nil   -- auctionID whose RESULT the leader sent and has not yet heard back

local TEST_ITEM = "|cffffffff|Hitem:6948::::::::1:::::::::|h[Hearthstone]|h|r"

local function randomSalt()
    local out = {}
    for i = 1, 16 do out[i] = string.format("%02x", math.random(0, 255)) end
    return table.concat(out)
end

local function ctx()
    local c = ns.Raid.context
    if not c then return nil end
    return { rows = c.rows, banned = c.banned, offspecPct = c.offspecPct }
end

local function spent()
    return Auction.Book and Auction.Book.Spent() or {}
end

local function number(id)
    return tonumber((id or ""):match("%-(%d+)$")) or 0
end

function Auction.IsPaused()
    return paused
end

-- Open auctions, lowest number first.
function Auction.Running()
    local out = {}
    for _, a in pairs(Auction.open) do out[#out + 1] = a end
    table.sort(out, function(x, y) return number(x.id) < number(y.id) end)
    return out
end

function Auction.IsRunning()
    return next(Auction.open) ~= nil
end

function Auction.Find(auctionID)
    if Auction.open[auctionID] then return Auction.open[auctionID] end
    if Auction.active and Auction.active.id == auctionID then return Auction.active end
    for i = #Auction.history, 1, -1 do
        if Auction.history[i].id == auctionID then return Auction.history[i] end
    end
end

-- The test auction in progress, else the most recent finished one.
function Auction.FindTest()
    for _, a in ipairs(Auction.Running()) do
        if a.kind == "test" then return a end
    end
    for i = #Auction.history, 1, -1 do
        if Auction.history[i].kind == "test" then return Auction.history[i] end
    end
end

----------------------------------------------------------------------------- leader

local leaderClose, leaderResult

leaderClose = function(a)
    if a.phase ~= "bidding" or Auction.open[a.id] ~= a then return end
    if paused then
        Timer.After(5, function() leaderClose(a) end)
        return
    end
    Comm.Send("CLOSE", { a.id, S.JoinCSV(a:Committers()) })
    Timer.After(Auction.REVEAL_SECONDS, function() leaderResult(a) end)
end

-- Results go out one at a time: the next is computed only once the previous one has echoed
-- back, so the running cap it is judged against already includes that sale on every client.
-- Auctions that become ready while a RESULT is in flight wait their turn, lowest number
-- first, and the next goes out the instant the echo lands: one server round trip apiece.
local waiting = {}
local nextResult

leaderResult = function(a)
    if a.phase ~= "reveal" or a.resultSent or Auction.open[a.id] ~= a then return end
    if paused then
        Timer.After(5, function() leaderResult(a) end)
        return
    end
    if awaitingEcho and awaitingEcho ~= a.id then
        waiting[a.id] = a
        return
    end
    a.resultSent = true
    awaitingEcho = a.id
    local w, p = a:Compute(spent())
    Comm.Send("RESULT", { a.id, w, p })
    -- An echo that never comes (the message was dropped) must not stall the batch forever.
    local id = a.id
    Timer.After(20, function()
        if awaitingEcho == id then
            awaitingEcho = nil
            Events.Fire("RAID_NOTE", "result_echo_timeout:" .. id)
            nextResult()
        end
    end)
end

nextResult = function()
    local best
    for _, a in pairs(waiting) do
        if not best or number(a.id) < number(best.id) then best = a end
    end
    if best then
        waiting[best.id] = nil
        leaderResult(best)
    end
end

-- Open an auction for an item. kind = "loot" (default) or "test". Returns ok, err.
function Auction.Open(itemLink, kind)
    if not ns.Raid.IsLeader() then return false, ns.Raid.LeaderBlocker() end
    local c = ns.Raid.context
    if not c then return false, "the snapshot has not reached the raid yet" end
    kind = kind or "loot"
    if kind == "test" then itemLink = TEST_ITEM end
    if not itemLink or itemLink == "" then return false, "no item" end
    -- The floor travels with the key it was looked up by, so every client and the backend
    -- can check it against the snapshot's table instead of trusting the leader's number.
    local slotKey = kind == "test" and "DEFAULT" or ns.Raid.FloorKeyFor(itemLink)
    local floor = kind == "test" and 1 or (ns.Raid.FloorForKey(slotKey) or 1)
    counter = counter + 1
    local id = c.raidID .. "-" .. counter
    Comm.Send("OPEN", { id, kind, itemLink, floor, GetServerTime() + Auction.BID_SECONDS, slotKey })
    return true, id
end

-- Open several at once. Returns how many opened, and the first error if any.
function Auction.OpenMany(links)
    local n, firstErr = 0, nil
    for _, link in ipairs(links) do
        local ok, err = Auction.Open(link, "loot")
        if ok then n = n + 1 elseif not firstErr then firstErr = err end
    end
    return n, firstErr
end

function Auction.OpenTest()
    return Auction.Open(nil, "test")
end

----------------------------------------------------------------------------- bidder

-- Bids this client has placed in other auctions still open: counted against the cap so a
-- raider cannot promise the same credit twice while results are pending.
local function committedElsewhere(a)
    local total = 0
    for id, b in pairs(myBids) do
        local o = Auction.open[id]
        if o and o ~= a then total = total + b.bid end
    end
    return total
end

-- The most this client may bid in a (or in general when a is nil).
function Auction.MaxBid(a)
    local me = Group.Me()
    local c = a and a.ctx or ctx()
    if not c then return nil end
    local cap = State.MaxBid(c, me, spent())
    if not cap then return nil end
    return cap - committedElsewhere(a)
end

-- Why the player cannot bid in a right now, or nil if they can.
function Auction.BidBlocker(a)
    if not a or a.phase ~= "bidding" then return "No auction open" end
    local me = Group.Me()
    if a.ctx.banned[me] then return "You are banned from bidding" end
    if not a.ctx.rows[me] then return "You are not registered in this snapshot" end
    if myBids[a.id] then return "Bid already placed" end
    return nil
end

-- Place a bid. a may be omitted when exactly one auction is open.
function Auction.Bid(amount, offspec, a)
    if not a then
        local running = Auction.Running()
        a = running[1]
        if #running > 1 then return false, "say which auction" end
    end
    local blocker = Auction.BidBlocker(a)
    if blocker then return false, blocker end
    -- A commit sent after the deadline would reach the raid after the CLOSE and be flagged
    -- late; refuse it here instead so the bid never looks counted when it was not.
    if GetServerTime() >= (a.deadline or 0) then return false, "bidding has closed" end
    local me = Group.Me()
    local row = a.ctx.rows[me]
    amount = tonumber(amount)
    if not amount or amount ~= math.floor(amount) or amount < 0 then return false, "enter a whole number" end
    local floor = State.ApplicableFloor(a.floor, offspec, a.ctx.offspecPct)
    if amount < floor then return false, "minimum is " .. floor end
    local max = Auction.MaxBid(a)
    if amount > max then
        local why = ""
        if committedElsewhere(a) > 0 then why = " with your other open bids"
        elseif max < row.balance + row.credit then why = " after what you already owe this raid" end
        return false, "maximum is " .. max .. why
    end
    local salt = randomSalt()
    myBids[a.id] = { bid = amount, offspec = offspec and true or false, salt = salt }
    local hash = State.Hash(a.id, me, amount, offspec, salt)
    local fields = { a.id, hash }
    local key = State.MyKey()
    if key then fields[3] = State.CommitTag(key, a.id, hash) end
    Comm.Send("COMMIT", fields)
    Events.Fire("AUCTION_UPDATE", a)
    return true
end

function Auction.MyBid(a)
    return a and myBids[a.id] or nil
end

-- Never number below what the raid has already seen (SYNC replies, saved records). Only
-- whole numbers, and never a leap of more than 50: a raider cannot push the numbering
-- out of reach (or to infinity) with one SYNC.
local SEED_LEAP = 50
function Auction.SeedCounter(n)
    if type(n) ~= "number" or n ~= n or n ~= math.floor(n) or n == math.huge then return false end
    if n <= counter then return false end
    if n > counter + SEED_LEAP then return false end
    counter = n
    return true
end

function Auction.Counter()
    return counter
end

----------------------------------------------------------------------------- handlers

local function finish(a)
    a.resultAt = GetServerTime()
    Auction.open[a.id] = nil
    Auction.history[#Auction.history + 1] = a
    if ns.db and ns.Raid.context then
        ns.db.auctions = ns.db.auctions or {}
        local raid = ns.db.auctions[ns.Raid.context.raidID] or {}
        ns.db.auctions[ns.Raid.context.raidID] = raid
        raid[a.id] = a:Summary()
    end
    Events.Fire("AUCTION_RESULT", a)
end

-- The auction ends without a result: the leader abandoned it, or this client gave up
-- waiting for one. Recorded, never settled.
function Auction.MarkAbandoned(a, reason)
    if a.phase == "done" then return end
    a.phase = "done"
    a.abandoned = reason or "abandoned"
    a.result = { winner = "", price = 0 }
    a.computed = { winner = "", price = 0 }
    if awaitingEcho == a.id then awaitingEcho = nil end
    waiting[a.id] = nil
    finish(a)
    nextResult()
end

-- Does this client already know an auction by this id: open, finished this session, or
-- saved from before a reload. The leader's client never re-opens one.
local savedRecord
local function knownAuction(id)
    return Auction.Find(id) ~= nil or savedRecord(id) ~= nil
end

Comm.On("OPEN", function(sender, f)
    if sender ~= ns.Raid.Leader() then
        Events.Fire("RAID_NOTE", "unauthorized:OPEN:" .. sender)
        return
    end
    local c = ctx()
    if not c then
        Events.Fire("AUCTION_ERROR", "auction opened before the snapshot was broadcast")
        return
    end
    local id = f[1]
    if not id or id == "" then
        Events.Fire("RAID_NOTE", "malformed:OPEN:" .. sender)
        return
    end
    if Auction.open[id] then return end
    if knownAuction(id) then
        Events.Fire("RAID_NOTE", "duplicate_open:" .. id)
        return
    end
    local kind, itemLink, floor, deadline, slotKey = f[2], f[3], tonumber(f[4]), tonumber(f[5]), f[6]
    -- The floor must be the snapshot's own number for the key the leader looked it up by.
    -- A leader's client that says otherwise is modified: the raid does not bid.
    if kind == "loot" and slotKey and slotKey ~= "" then
        local expected = ns.Raid.FloorForKey(slotKey)
        if not floor or floor < 0 or floor ~= expected then
            Events.Fire("RAID_NOTE", "floor_mismatch:" .. id)
            ns.Print(("|cffff4040Auction %s refused: min price %s does not match the snapshot's %s for %s|r"):format(id, tostring(f[4]), tostring(expected), slotKey))
            return
        end
    end
    if not floor or floor < 0 then
        Events.Fire("RAID_NOTE", "bad_floor:" .. id)
        return
    end
    local now = GetServerTime()
    local a = State.New(c, { auctionID = id, kind = kind, itemLink = itemLink, floor = floor, deadline = deadline, leader = sender })
    a.openedAt = now
    if deadline and deadline - now < Auction.BID_SECONDS - 2 then a:Flag("short_deadline") end
    local realItemInTest = a.kind == "test" and itemLink ~= TEST_ITEM
    if realItemInTest then
        a:Flag("test_real_item")
        Events.Fire("RAID_NOTE", "test_real_item:" .. id)
    end
    Auction.open[a.id] = a
    Auction.active = a
    if sender == Group.Me() then
        -- Close on the deadline every client was shown, plus a second for commits already in
        -- flight, rather than a fixed delay from when our own OPEN echoed back. Scheduled
        -- before anyone hears about the auction, so a UI error can never leave it unclosed.
        Timer.After(math.max(1, (a.deadline or now) - now + 1), function() leaderClose(a) end)
    end
    Events.Fire("AUCTION_OPEN", a)
    -- Test auctions: everyone registered bids the floor automatically, so the leader learns
    -- who has the addon and who is missing from the snapshot without anyone clicking.
    if a.kind == "test" and not realItemInTest and not Auction.BidBlocker(a) then
        Auction.Bid(State.ApplicableFloor(a.floor, false, 0), false, a)
    end
end)

Comm.On("COMMIT", function(sender, f)
    local a = Auction.open[f[1]]
    if not a then return end
    a:Commit(sender, f[2])
    Events.Fire("AUCTION_UPDATE", a)
end)

Comm.On("CLOSE", function(sender, f)
    local a = Auction.open[f[1]]
    if not a or sender ~= a.leader or a.phase ~= "bidding" then return end
    local now = GetServerTime()
    if a.deadline and now < a.deadline then a:Flag("early_close") end
    a:Close(S.SplitCSV(f[2]))
    a.closedAt = now
    Events.Fire("AUCTION_CLOSE", a)
    local mine = myBids[a.id]
    if mine and a.commits[Group.Me()] then
        local fields = { a.id, mine.bid, mine.offspec and 1 or 0, mine.salt }
        local key = State.MyKey()
        if key then fields[5] = State.RevealTag(key, a.id, mine.bid, mine.offspec, mine.salt) end
        Comm.Send("REVEAL", fields)
    end
end)

Comm.On("REVEAL", function(sender, f)
    local a = Auction.open[f[1]]
    if not a or a.phase == "done" then return end
    local bidText, offspec, salt = tostring(f[2] or ""), f[3] == "1", f[4]
    -- Field formats are checked before anything else, with the same rules as the backend,
    -- so a reveal that one side would read differently is invalid on both.
    if a.phase == "bidding" then
        a:RejectReveal(sender, "early_reveal", tonumber(bidText), offspec, salt)
    elseif not bidText:match("^%d+$") or #bidText > 9 then
        a:RejectReveal(sender, "bad_bid", tonumber(bidText), offspec, salt)
    elseif type(salt) ~= "string" or not salt:match("^%x+$") or #salt ~= 32 then
        a:RejectReveal(sender, "bad_salt", tonumber(bidText), offspec, salt)
    else
        a:Reveal(sender, tonumber(bidText), offspec, salt)
    end
    Events.Fire("AUCTION_UPDATE", a)
    if a.phase == "reveal" and a.leader == Group.Me() and a:AllRevealed() then
        Timer.After(1, function() leaderResult(a) end)
    end
end)

Comm.On("RESULT", function(sender, f)
    local a = Auction.open[f[1]]
    if not a or sender ~= a.leader or a.phase == "done" then return end
    local wasAwaited = awaitingEcho == a.id
    if wasAwaited then awaitingEcho = nil end
    local now = GetServerTime()
    if a.closedAt and now < a.closedAt + Auction.REVEAL_SECONDS and not a:AllRevealed() then a:Flag("early_result") end
    local ok = a:Result(f[2], tonumber(f[3]) or 0, spent())
    if not ok then
        -- Every client is a referee: say so where the whole raid can see it on this screen,
        -- and keep the item out of the trade helper until an officer has looked.
        local c, r = a.computed or {}, a.result or {}
        ns.Print(("|cffff4040RESULT for %s does not match: announced %s for %d, this client computed %s for %d. Not trading it until an officer checks.|r"):format(
            a.id, (r.winner or "") ~= "" and r.winner or "no sale", r.price or 0, (c.winner or "") ~= "" and c.winner or "no sale", c.price or 0))
        if PlaySound and SOUNDKIT and SOUNDKIT.RAID_WARNING then pcall(PlaySound, SOUNDKIT.RAID_WARNING) end
        Events.Fire("RAID_NOTE", ("result_mismatch:%s:announced %s %d:computed %s %d"):format(a.id, r.winner or "", r.price or 0, c.winner or "", c.price or 0))
    end
    finish(a)
    if wasAwaited then nextResult() end
end)

Events.Register("ENCOUNTER_START", function()
    paused = true
    for _, a in ipairs(Auction.Running()) do Events.Fire("AUCTION_UPDATE", a) end
end)
Events.Register("ENCOUNTER_END", function()
    paused = false
    for _, a in ipairs(Auction.Running()) do Events.Fire("AUCTION_UPDATE", a) end
end)
Events.On("RAID_CONTEXT", function(c)
    -- A new raid id restarts numbering on the leader.
    if c and c.raidID ~= Auction.raidID then
        Auction.raidID = c.raidID
        counter = 0
        -- Seed from what this client already saved for the raid, so numbering continues after a reload.
        for _, e in ipairs(Auction.Book and Auction.Book.Entries() or {}) do
            counter = math.max(counter, number(e.id))
        end
    end
end)

----------------------------------------------------------------------------- saved records

-- After a reload the live state objects are gone but the summaries in SavedVariables are
-- not. Transfers and voids work from either: the live auction when it exists, else the
-- saved record, so the Ledger's controls keep working for everything in the raid.
savedRecord = function(auctionID)
    local c = ns.Raid.context
    if not ns.db or not c or not ns.db.auctions or not ns.db.auctions[c.raidID] then return nil end
    return ns.db.auctions[c.raidID][auctionID]
end

local function saveSummary(a)
    if ns.db and ns.Raid.context and ns.db.auctions and ns.db.auctions[ns.Raid.context.raidID] then
        ns.db.auctions[ns.Raid.context.raidID][a.id] = a:Summary()
    end
end

-- A uniform view of an auction for validation: kind, winner, price, transfer, voided, done.
local function view(auctionID)
    local a = Auction.Find(auctionID)
    if a then
        return a, {
            kind = a.kind, done = a.phase == "done", winner = a.result and a.result.winner or "",
            price = a.result and a.result.price or 0, transfer = a.transfer, voided = a.voided,
        }
    end
    local s = savedRecord(auctionID)
    if s then
        return nil, { kind = s.kind, done = true, winner = s.winner or "", price = s.price or 0, transfer = s.transfer, voided = s.voided, saved = s }
    end
    return nil, nil
end

----------------------------------------------------------------------------- transfers

-- Finished loot auctions that can still be transferred, newest first.
function Auction.Transferable()
    local out = {}
    for i = #Auction.history, 1, -1 do
        local a = Auction.history[i]
        if a.kind == "loot" and a.phase == "done" and a.result and a.result.winner ~= "" and not a.transfer and not a.voided then
            out[#out + 1] = a
        end
    end
    return out
end

-- Why a transfer of this auction to toName would be refused, or nil. Shared by the sender
-- and the receiver so both sides agree.
local function transferProblem(v, toName)
    local c = ns.Raid.context
    if not v then return "Unknown auction" end
    if not v.done or v.kind ~= "loot" or v.winner == "" then return "Nothing to transfer" end
    if v.voided then return "Sale was voided" end
    if v.transfer then return "Already transferred" end
    if not toName or toName == "" then return "Pick a recipient" end
    if toName == v.winner then return "That is already the winner" end
    if not c then return "No raid context" end
    local row = c.rows[toName]
    if not row then return toName .. " is not in the snapshot" end
    if c.banned[toName] then return toName .. " is banned" end
    if row.balance + row.credit - (spent()[toName] or 0) < v.price then return toName .. " cannot cover " .. v.price end
    return nil
end

-- Leader hands the won item to someone else at the same price. Returns ok, err.
function Auction.Transfer(auctionID, toName)
    if not ns.Raid.IsLeader() then return false, ns.Raid.LeaderBlocker() end
    toName = Group.Normalize(toName)
    local _, v = view(auctionID)
    local problem = transferProblem(v, toName)
    if problem then return false, problem end
    Comm.Send("TRANSFER", { auctionID, v.winner, toName })
    return true
end

Comm.On("TRANSFER", function(sender, f)
    if sender ~= ns.Raid.Leader() then
        Events.Fire("RAID_NOTE", "unauthorized:TRANSFER:" .. sender)
        return
    end
    local a, v = view(f[1])
    if a then
        a:Transfer(f[2], f[3])
        saveSummary(a)
        Events.Fire("AUCTION_TRANSFER", a)
        Events.Fire("AUCTION_UPDATE", a)
    elseif v and v.saved then
        if v.winner ~= f[2] or transferProblem(v, f[3]) then
            Events.Fire("RAID_NOTE", "bad_transfer:" .. tostring(f[3]))
            return
        end
        v.saved.transfer = { from = f[2], to = f[3] }
        Events.Fire("AUCTION_TRANSFER", v.saved)
    end
    Events.Fire("LEDGER_CHANGED")
end)

----------------------------------------------------------------------------- voids

local function voidProblem(v)
    if not v then return "Unknown auction" end
    if not v.done or v.kind ~= "loot" or v.winner == "" then return "Nothing to void" end
    if v.voided then return "Already voided" end
    return nil
end

-- Leader strikes a finished loot sale from the books (protocol §3.8). The item can then be
-- auctioned again under a new auction id. Returns ok, err.
function Auction.Void(auctionID, reason)
    if not ns.Raid.IsLeader() then return false, ns.Raid.LeaderBlocker() end
    local _, v = view(auctionID)
    local problem = voidProblem(v)
    if problem then return false, problem end
    Comm.Send("VOID", { auctionID, (reason or "voided"):gsub("[%^|]", " ") })
    return true
end

Comm.On("VOID", function(sender, f)
    if sender ~= ns.Raid.Leader() then
        Events.Fire("RAID_NOTE", "unauthorized:VOID:" .. sender)
        return
    end
    local a, v = view(f[1])
    if voidProblem(v) then return end
    if a then
        a:Void(f[2])
        saveSummary(a)
        Events.Fire("AUCTION_VOID", a)
        Events.Fire("AUCTION_UPDATE", a)
    else
        v.saved.voided = f[2] or "voided"
        Events.Fire("AUCTION_VOID", v.saved)
    end
    Events.Fire("LEDGER_CHANGED")
end)

----------------------------------------------------------------------------- test report

-- For a test auction: one line per group member and what it proved.
function Auction.TestReport(a)
    a = a or Auction.FindTest()
    if not a then return {} end
    local out = {}
    for _, name in ipairs(Group.Members()) do
        local status
        if a.ctx.banned[name] then status = "Banned"
        elseif not a.ctx.rows[name] then status = "Not registered"
        elseif not a.commits[name] then status = "No addon or no reply"
        elseif a.invalid[name] then status = "Bad reveal: " .. a.invalid[name].reason
        elseif not a.reveals[name] then status = (a.phase == "done") and "Did not reveal" or "Waiting"
        else status = "OK" end
        out[#out + 1] = { name = name, status = status, ok = status == "OK" }
    end
    return out
end
