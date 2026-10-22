-- FairShare/Auction/Book.lua
-- The raid's book of auctions as plain summaries (saved ones plus the live auction), and
-- a running estimate of what every member's balance looks like after them. The estimate
-- never changes who may bid what: eligibility stays on the snapshot the leader broadcast.
-- The backend settles the real numbers; this only keeps the raid informed without a
-- single extra message, since every client already hears every RESULT.
local ADDON, ns = ...

local Auction = ns.Auction
local Book = {}
Auction.Book = Book

local Group = ns.Core.Group

Book.PAYOUT_PCT = 90   -- share of each sale paid back to the raid; the rest feeds the pool

local function raidID()
    return ns.Raid.context and ns.Raid.context.raidID or nil
end

-- Everything auctioned in this raid, oldest first: saved summaries plus the live auction.
function Book.Entries()
    local out, seen = {}, {}
    local id = raidID()
    if ns.db and ns.db.auctions and id and ns.db.auctions[id] then
        for aid, s in pairs(ns.db.auctions[id]) do
            out[#out + 1] = s
            seen[aid] = true
        end
    end
    for _, a in ipairs(Auction.Running()) do
        if not seen[a.id] then
            local s = a:Summary()
            s.phase = a.phase
            out[#out + 1] = s
        end
    end
    table.sort(out, function(x, y)
        local nx, ny = tonumber(x.id:match("%-(%d+)$")) or 0, tonumber(y.id:match("%-(%d+)$")) or 0
        if nx ~= ny then return nx < ny end
        return x.id < y.id
    end)
    return out
end

function Book.Find(auctionID)
    for _, e in ipairs(Book.Entries()) do
        if e.id == auctionID then return e end
    end
end

function Book.Number(entry)
    return entry.id:match("%-(%d+)$") or entry.id
end

function Book.Name(entry)
    return (entry.itemLink or ""):match("%[(.-)%]") or "?"
end

-- Who ends up paying: the transferee if a transfer happened, else the winner.
function Book.Owner(entry)
    if entry.transfer and entry.transfer.to then return entry.transfer.to end
    return entry.winner or ""
end

-- A finished, non-void loot sale with a winner.
function Book.IsSale(entry)
    return entry.kind == "loot" and (entry.winner or "") ~= "" and not entry.voided and not (entry.phase and entry.phase ~= "done")
end

-- Estimated change per member this raid: owner pays the price, PAYOUT_PCT of it is split
-- evenly over everyone in the group who has a row (standby and kill shares are the
-- backend's call, so this is a floor, not the final figure).
function Book.Deltas()
    local deltas = {}
    local c = ns.Raid.context
    if not c then return deltas end
    local present = {}
    for _, n in ipairs(Group.Members()) do
        if c.rows[n] then present[#present + 1] = n end
    end
    for _, e in ipairs(Book.Entries()) do
        if Book.IsSale(e) then
            local owner, P = Book.Owner(e), e.price or 0
            deltas[owner] = (deltas[owner] or 0) - P
            if #present > 0 then
                local share = math.floor(P * Book.PAYOUT_PCT / 100 / #present)
                for _, n in ipairs(present) do deltas[n] = (deltas[n] or 0) + share end
            end
        end
    end
    return deltas
end

-- What each member already owes for this raid's finished, non-void loot sales (transfers
-- count against the recipient). Fixed into an auction's context at OPEN so every client
-- judges bids with the same figure; the running cap is balance + credit - spent.
function Book.Spent()
    local spent = {}
    for _, e in ipairs(Book.Entries()) do
        if Book.IsSale(e) then
            local o = Book.Owner(e)
            spent[o] = (spent[o] or 0) + (e.price or 0)
        end
    end
    return spent
end
