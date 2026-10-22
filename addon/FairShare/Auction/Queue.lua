-- FairShare/Auction/Queue.lua
-- Loot waiting to be auctioned. Items arrive from the loot window, from loot lines in chat
-- (whoever picked it up), or from the Ledger (re-run or second copy), and sit as pending
-- rows on the Ledger until the leader ticks a batch and presses Auction checked: every
-- ticked item opens at once and the auctions run side by side. Nothing starts by itself.
--
-- Two of the same item are two entries. The only thing suppressed is the same physical drop
-- seen twice: a loot-window slot is queued once per slot, and when the leader hands that
-- item out the loot line that follows is matched to the window entry instead of adding one.
local ADDON, ns = ...

local Auction = ns.Auction
local Q = {}
Auction.Queue = Q

local Events, Group = ns.Core.Events, ns.Core.Group

Q.items = {}                -- array of { link, itemID, source, addedAt }
local MATCH_WINDOW = 300    -- seconds a loot-window entry waits for its matching loot line
local seenSlots = {}        -- "<lootSourceGUID>:<slot>" already queued from a loot window
local windowCopies = {}     -- { itemID, at, matched } for entries that came from a loot window

local QUALITY_BY_COLOR = { ["9d9d9d"] = 0, ["ffffff"] = 1, ["1eff00"] = 2, ["0070dd"] = 3, ["a335ee"] = 4, ["ff8000"] = 5, ["e6cc80"] = 6 }

function Q.ItemID(link)
    return tonumber((link or ""):match("|Hitem:(%d+)"))
end

-- Item quality from the client's cache, or from the link colour when it is not cached yet.
function Q.QualityOf(link)
    if not link then return nil end
    local info = (C_Item and C_Item.GetItemInfo) or GetItemInfo
    if info then
        local _, _, quality = info(link)
        if quality then return quality end
    end
    local color = link:match("|cff(%x%x%x%x%x%x)")
    return color and QUALITY_BY_COLOR[color:lower()] or nil
end

function Q.MinQuality()
    return (ns.db and ns.db.settings and ns.db.settings.minQuality) or 3
end

function Q.AutoQueue()
    return not (ns.db and ns.db.settings and ns.db.settings.autoQueue == false)
end

function Q.SetAutoQueue(on)
    if ns.db then ns.db.settings.autoQueue = on and true or false end
    Events.Fire("QUEUE_CHANGED")
end

-- Add a pending item. Returns true, or false and why not. source is free text for the Ledger.
function Q.Add(link, source)
    if not ns.Raid.IsLeader() then return false, ns.Raid.LeaderBlocker() end
    if type(link) ~= "string" or not link:find("|Hitem:", 1, true) then return false, "not an item link" end
    Q.items[#Q.items + 1] = { link = link, itemID = Q.ItemID(link), source = source or "", addedAt = GetServerTime() }
    Events.Fire("QUEUE_CHANGED")
    return true
end

function Q.Remove(index)
    local it = table.remove(Q.items, index or 1)
    if it then Events.Fire("QUEUE_CHANGED") end
    return it
end

function Q.Clear()
    Q.items = {}
    Events.Fire("QUEUE_CHANGED")
end

function Q.Count()
    return #Q.items
end

-- How many pending entries carry this item id (the loot strip shows "pending x2").
function Q.CountOf(itemID)
    local n = 0
    for _, it in ipairs(Q.items) do
        if it.itemID == itemID then n = n + 1 end
    end
    return n
end

-- A snapshot that is loaded but not yet broadcast goes out by itself the moment the leader
-- tries to start something; the start is then retried once the SNAP lands. Returns true if sent.
local function broadcastIfNeeded()
    if ns.Raid.context or not ns.Raid.IsLeader() or not ns.Snapshot.Current() then return false end
    local ok = ns.Raid.Broadcast()
    if ok then ns.Print("Broadcasting the snapshot; try again in a moment") end
    return ok
end

-- Open auctions for the pending items at these indices (all when nil), all at once.
-- Returns how many opened, and an error if none could.
function Q.Start(indices)
    if not ns.Raid.IsLeader() then return 0, ns.Raid.LeaderBlocker() end
    if #Q.items == 0 then return 0, "nothing pending" end
    if Auction.IsPaused() then return 0, "wait for the boss to end" end
    if not ns.Raid.context then
        broadcastIfNeeded()
        return 0, "the snapshot has not reached the raid yet"
    end
    local picked = {}
    if indices then
        for _, i in ipairs(indices) do if Q.items[i] then picked[#picked + 1] = i end end
    else
        for i = 1, #Q.items do picked[#picked + 1] = i end
    end
    table.sort(picked)
    local opened, firstErr = 0, nil
    for k = #picked, 1, -1 do
        local i = picked[k]
        local ok, err = Auction.Open(Q.items[i].link, "loot")
        if ok then
            table.remove(Q.items, i)
            opened = opened + 1
        elseif not firstErr then
            firstErr = err
        end
    end
    Events.Fire("QUEUE_CHANGED")
    if opened == 0 then return 0, firstErr or "Nothing opened" end
    return opened, firstErr
end

function Q.StartAll()
    return Q.Start(nil)
end

----------------------------------------------------------------------------- feeds

local function slotKey(slot)
    local guid
    if GetLootSourceInfo then guid = GetLootSourceInfo(slot) end
    return tostring(guid or "loot") .. ":" .. slot
end

-- Loot window: queue every item at or above the quality threshold, once per slot.
Events.Register("LOOT_OPENED", function()
    if not Q.AutoQueue() or not ns.Raid.IsLeader() or not ns.Raid.context or not GetNumLootItems then return end
    if not ns.Raid.InRaidInstance() then return end
    for slot = 1, GetNumLootItems() do
        local link = GetLootSlotLink(slot)
        local _, _, _, _, quality = GetLootSlotInfo(slot)
        local key = slotKey(slot)
        if link and not seenSlots[key] and (quality or Q.QualityOf(link) or 0) >= Q.MinQuality() then
            seenSlots[key] = true
            if Q.Add(link, "loot window") then
                windowCopies[#windowCopies + 1] = { itemID = Q.ItemID(link), at = GetServerTime(), matched = false }
            end
        end
    end
end)

-- A loot line for an item the leader just queued from the loot window is the same drop
-- being handed out, not a second one. Returns true when it was absorbed.
local function matchWindowCopy(itemID)
    local now = GetServerTime()
    for i = #windowCopies, 1, -1 do
        local c = windowCopies[i]
        if now - c.at > MATCH_WINDOW then
            table.remove(windowCopies, i)
        elseif not c.matched and c.itemID == itemID then
            c.matched = true
            return true
        end
    end
    return false
end

-- Loot lines in chat: whoever picked the item up, it still goes up for auction. Only inside
-- an instance (or in developer mode), so world drops and crafting never land in the queue.
Events.Register("CHAT_MSG_LOOT", function(_, text)
    if not Q.AutoQueue() or not ns.Raid.IsLeader() or not ns.Raid.context then return end
    if not ns.Raid.InRaidInstance() then return end
    local looter, link = ns.Transcript.ParseLoot(text)
    if not looter or not link then return end
    if (Q.QualityOf(link) or 0) < Q.MinQuality() then return end
    if matchWindowCopy(Q.ItemID(link)) then return end
    Q.Add(link, "looted by " .. looter)
end)

Events.On("RAID_CONTEXT", function(c)
    if not c then
        Q.Clear()
        seenSlots, windowCopies = {}, {}
    end
end)
