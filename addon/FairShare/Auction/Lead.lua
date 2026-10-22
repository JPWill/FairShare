-- FairShare/Auction/Lead.lua
-- Who runs the raid, and how the raid carries on when they cannot (protocol §3.9, §3.10).
--
--   SYNC     whispered to the leader by every client that receives a SNAP or a LEAD: the
--            highest auction number it knows and any auction still open. The leader seeds
--            its counter from the maximum, so ids never collide after a crash, and learns
--            about an orphaned auction without needing its own SavedVariables.
--   LEAD     sent by the game's group leader to take over as FairShare leader. Clients accept
--            it only from whoever the game says leads the group, answer ACK on RAID (so the
--            transcript carries the quorum) and SYNC by whisper.
--   ABANDON  leader strikes an auction that never got its RESULT (the old leader crashed
--            mid-auction). Nothing is settled for it; the item goes back in the queue.
--
-- A client also gives up on an auction by itself when no CLOSE or RESULT arrives long after
-- the deadline, so a dead leader never leaves the queue blocked.
local ADDON, ns = ...

local Raid, Auction = ns.Raid, ns.Auction
local Comm, Events, Timer, Group, Book, S = ns.Core.Comm, ns.Core.Events, ns.Core.Timer, ns.Core.Group, Auction.Book, ns.Lib.Serialize

local GRACE = 60            -- seconds past a deadline before a client abandons an auction itself
local SYNC_WINDOW = 60      -- seconds after our own SNAP or LEAD during which SYNC replies count
local LEADER_QUIET = 90     -- seconds of silence before a present leader counts as gone
local votes = {}            -- name -> true: ACKs for the current context.leader
local abandoned = {}        -- auctionIDs this leader already sent ABANDON for
local syncUntil = 0         -- GetTime() until which SYNC replies are expected

----------------------------------------------------------------------------- who leads

-- The FairShare leader: whoever the raid's context names, else the game's group leader.
function Raid.Leader()
    if Raid.context and Raid.context.leader then return Raid.context.leader end
    return Group.LeaderName()
end

-- May this client act as leader: it leads the group in game and the raid context agrees
-- (or there is no context yet).
function Raid.IsLeader()
    if not Group.IsLeader() then return false end
    return not Raid.context or not Raid.context.leader or Raid.context.leader == Group.Me()
end

-- Why the player cannot act as leader right now, or nil.
function Raid.LeaderBlocker()
    if not Group.IsLeader() then
        if Raid.context and Raid.context.leader == Group.Me() then
            return ("%s holds lead in game; ask for it back, or they can take over from the Home tab"):format(Group.LeaderName() or "?")
        end
        return "Only the group leader runs auctions"
    end
    if Raid.context and Raid.context.leader and Raid.context.leader ~= Group.Me() then
        return ("FairShare leader is %s; take over from the Home tab first"):format(Raid.context.leader)
    end
    return nil
end

-- Votes behind the current leader (after a LEAD): count and the number needed.
function Raid.LeadVotes()
    local n = 0
    for _ in pairs(votes) do n = n + 1 end
    local members, c = 0, Raid.context
    if c then
        for _, name in ipairs(Group.Members()) do
            if c.rows[name] and name ~= c.leader then members = members + 1 end
        end
    end
    return n, math.ceil(members / 2)
end

function Raid.LeadConfirmed()
    if not Raid.context or Raid.context.leader ~= Group.Me() then return false end
    if not Raid.context.tookOver then return true end
    local have, need = Raid.LeadVotes()
    return have >= need
end

----------------------------------------------------------------------------- sync

local function highestNumber()
    local n = 0
    for _, e in ipairs(Book.Entries()) do
        local k = tonumber(Book.Number(e)) or 0
        if k > n then n = k end
    end
    return n
end

-- Every auction this client still has open, as "id,id,...", and the phase of the first.
local function unfinished()
    local ids, phase = {}, ""
    for _, a in ipairs(Auction.Running()) do
        ids[#ids + 1] = a.id
        if phase == "" then phase = a.phase end
    end
    return table.concat(ids, ","), phase
end

local function sendSync(to)
    if not to or to == Group.Me() then return end
    local id, phase = unfinished()
    Comm.Send("SYNC", { highestNumber(), id, phase }, { channel = "WHISPER", target = to })
end

Comm.On("SNAP", function(sender)
    if Raid.context and Raid.context.leader == sender then sendSync(sender) end
end)

-- SYNC replies are expected only for a short while after our own SNAP or LEAD.
Events.On("COMM_SENT", function(msgType)
    if msgType == "SNAP" or msgType == "LEAD" then syncUntil = GetTime() + SYNC_WINDOW end
end)

-- An auction id a SYNC may make us abandon: numbered within what we have opened, and
-- either unknown to us or never given a RESULT. A finished sale is never abandoned.
local function abandonable(id)
    local n = tonumber(id:match("%-(%d+)$"))
    if not n or n > Auction.Counter() then return false end
    if Auction.open[id] then return false end
    local e = Book.Find(id)
    if not e then return true end
    if e.abandoned then return true end
    return e.phase ~= nil and e.phase ~= "done"
end

Comm.On("SYNC", function(sender, f)
    if not Raid.IsLeader() then return end
    local c = Raid.context
    -- Only raiders with a row, only while we are waiting for replies: anyone else trying
    -- to renumber the raid or strike an auction is noted and ignored.
    if sender == Group.Me() or not c or not c.rows[sender] or not Group.IsMember(sender) or GetTime() > syncUntil then
        Events.Fire("RAID_NOTE", "unauthorized:SYNC:" .. sender)
        return
    end
    local highest = tostring(f[1] or "")
    if highest:match("^%d+$") and #highest <= 9 then Auction.SeedCounter(tonumber(highest)) end
    for _, id in ipairs(S.SplitCSV(f[2] or "")) do
        if id ~= "" and not abandoned[id] and abandonable(id) then
            abandoned[id] = true
            Comm.Send("ABANDON", { id, "leader lost it" })
        end
    end
end)

----------------------------------------------------------------------------- takeover

-- The game's group leader claims FairShare leadership. Returns ok, err.
function Raid.TakeOver()
    if not Group.IsLeader() then return false, "you do not lead the group in game" end
    if not Raid.context then return false, "no raid in progress to take over" end
    if Raid.context.leader == Group.Me() then return false, "you already lead this raid" end
    Comm.Send("LEAD", { Group.Me() })
    return true
end

-- Is the current FairShare leader gone: no longer in the group, offline, or silent for
-- longer than a leader ever is while running a raid. A present leader cannot be replaced
-- just because the game's lead was passed around for invites.
local function leaderAbsent(c)
    local leader = c.leader
    if not leader or leader == Group.Me() then return true end
    if not Group.IsMember(leader) then return true end
    if UnitIsConnected and Auction.Trade and Auction.Trade.UnitFor then
        local unit = Auction.Trade.UnitFor(leader)
        if unit and UnitIsConnected(unit) == false then return true end
    end
    local seen = Comm.LastSeen(leader)
    return seen == nil or GetTime() - seen > LEADER_QUIET
end

Comm.On("LEAD", function(sender, f)
    if sender ~= Group.LeaderName() then
        Events.Fire("RAID_NOTE", "unauthorized:LEAD:" .. sender)
        return
    end
    local c = Raid.context
    if not c or c.leader == sender then return end
    if not leaderAbsent(c) then
        Events.Fire("RAID_NOTE", "unauthorized:LEAD:" .. sender .. ":leader_present")
        return
    end
    c.leader = sender
    c.tookOver = true
    votes = {}
    for _, a in ipairs(Auction.Running()) do a.leader = sender end
    ns.Print(("%s took over as FairShare leader"):format(sender))
    if sender ~= Group.Me() then
        local fields = { sender }
        local key = Auction.State.MyKey()
        if key then fields[2] = Auction.State.AckTag(key, sender) end
        Comm.Send("ACK", fields)
        sendSync(sender)
    end
    Events.Fire("RAID_LEADER_CHANGED", sender)
    Events.Fire("RAID_CONTEXT", c)
end)

Comm.On("ACK", function(sender, f)
    local c = Raid.context
    if not c or c.leader ~= f[1] or sender == c.leader then return end
    votes[sender] = true
    Events.Fire("RAID_LEADER_CHANGED", c.leader)
end)

----------------------------------------------------------------------------- abandoned auctions

function Auction.Abandon(auctionID, reason)
    if not Raid.IsLeader() then return false, Raid.LeaderBlocker() end
    local a = Auction.Find(auctionID)
    if not a or a.phase == "done" then return false, "nothing to abandon" end
    abandoned[auctionID] = true
    Comm.Send("ABANDON", { auctionID, (reason or "abandoned"):gsub("[%^|]", " ") })
    return true
end

Comm.On("ABANDON", function(sender, f)
    if sender ~= Raid.Leader() then
        Events.Fire("RAID_NOTE", "unauthorized:ABANDON:" .. sender)
        return
    end
    local a = Auction.Find(f[1])
    if a and a.phase ~= "done" then Auction.MarkAbandoned(a, f[2] or "abandoned") end
end)

-- A client gives up on an auction whose leader went quiet. Local only; the next leader
-- (or the same one, back from a crash) sends the ABANDON that settles it for the record.
Timer.Every(5, function()
    if Auction.IsPaused() then return end
    local now = GetServerTime()
    for _, a in ipairs(Auction.Running()) do
        local limit
        if a.phase == "bidding" then
            limit = (a.deadline or now) + GRACE
        else
            limit = (a.closedAt or now) + Auction.REVEAL_SECONDS + GRACE
        end
        if now > limit then Auction.MarkAbandoned(a, "no reply from the leader") end
    end
end)

Events.On("RAID_CONTEXT", function(c)
    if not c then votes, abandoned = {}, {} end
end)
