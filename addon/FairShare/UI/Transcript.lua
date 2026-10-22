-- FairShare/UI/Transcript.lua
-- A window that reads a raid's transcript back as what happened in the raid, not as the
-- messages that carried it. Lines are grouped into events: the raid being loaded, each
-- boss kill, each auction from open to result (and anything that happened to that item
-- later), leadership changes, loot and notes. A group collapses to one line; expanded it
-- lists every step in plain words with who did it and when. A "Show raw messages" tick
-- adds the protocol message under each step for anyone checking a hash or a salt.
local ADDON, ns = ...

local UI, Comm = ns.UI, ns.Core.Comm

local ROWS, ROW_H = 18, 16

local function parse(line)
    local t, dir, sender, body = line:match("^([^\t]*)\t([^\t]*)\t([^\t]*)\t(.*)$")
    if not t then return { time = 0, dir = "?", sender = "?", msgType = "?", fields = {}, raw = line } end
    local msgType, fields = Comm.Decode(body)
    return { time = tonumber(t) or 0, dir = dir, sender = sender, msgType = msgType or "?", fields = fields or {}, raw = body }
end

local function number(auctionID) return (tostring(auctionID or "")):match("%-(%d+)$") or tostring(auctionID or "?") end
local function count(csv) if not csv or csv == "" then return 0 end local n = 1 for _ in csv:gmatch(",") do n = n + 1 end return n end
local function plural(n, word) return ("%d %s%s"):format(n, word, n == 1 and "" or "s") end

-- Turns the recorded lines into event groups, in the order the events started.
local function groups(lines, t)
    local out, byAuction, lastLead, lastSnap = {}, {}, nil, nil
    local function newGroup(p, title, color)
        local g = { title = title, color = color or t.text, time = p.time, steps = {} }
        out[#out + 1] = g
        return g
    end
    local function step(g, p, text, color)
        g.steps[#g.steps + 1] = { time = p.time, who = p.sender, text = text, color = color or t.text, raw = p.msgType .. " " .. table.concat(p.fields, "  ") }
    end
    local function auctionGroup(p, id)
        local g = byAuction[id]
        if not g then
            g = newGroup(p, ("#%s: opened before this transcript starts"):format(number(id)), t.muted)
            g.item = "?"
            byAuction[id] = g
        end
        return g
    end
    for _, line in ipairs(lines) do
        local p = parse(line)
        local f, m = p.fields, p.msgType
        if m == "SNAP" then
            lastSnap = newGroup(p, ("Raid %s loaded by %s, checksum %s"):format(f[1] or "?", p.sender, (f[2] or ""):sub(1, 8)), t.accent)
            local pct = tonumber(f[4]) or 0
            step(lastSnap, p, ("%s broadcast the snapshot for raid %s (checksum %s), off-spec discount %d%%, %s"):format(p.sender, f[1] or "?", (f[2] or ""):sub(1, 8), pct, f[6] and f[6] ~= "" and (plural(count(f[6]), "banned name")) or "nobody banned"))
        elseif m == "ROWS" then
            local g = lastSnap or newGroup(p, "Balances sent", t.accent)
            step(g, p, ("%s sent balances for %s"):format(p.sender, plural(#f, "raider")))
            for _, row in ipairs(f) do
                local name, owner, bal, credit = row:match("^([^,]*),([^,]*),([^,]*),([^,]*)$")
                if name then
                    g.steps[#g.steps + 1] = { text = ("%s: %s shares, credit %s%s"):format(name, bal, credit, owner ~= name and (" (" .. owner .. ")") or ""), color = t.muted, raw = "ROW " .. row }
                end
            end
        elseif m == "OPEN" then
            local id, kind, item, floor = f[1], f[2], f[3], f[4]
            local g = newGroup(p, "", t.text)
            g.item, g.kind, g.id = item, kind, id
            byAuction[id] = g
            g.title = ("#%s %s: bidding"):format(number(id), item or "?")
            step(g, p, ("%s opened %sauction #%s for %s, min price %s"):format(p.sender, kind == "test" and "test " or "", number(id), item or "?", floor or "?"))
        elseif m == "COMMIT" then
            step(auctionGroup(p, f[1]), p, ("%s placed a sealed bid"):format(p.sender), t.muted)
        elseif m == "CLOSE" then
            local g = auctionGroup(p, f[1])
            local n = count(f[2])
            step(g, p, n == 0 and "Bidding closed with no bids" or ("Bidding closed with %s: %s"):format(plural(n, "bid"), f[2]))
            g.title = ("#%s %s: revealing"):format(number(f[1]), g.item or "?")
        elseif m == "REVEAL" then
            step(auctionGroup(p, f[1]), p, ("%s revealed a bid of %s%s"):format(p.sender, f[2] or "?", f[3] == "1" and " (off-spec)" or ""))
        elseif m == "RESULT" then
            local g = auctionGroup(p, f[1])
            if (f[2] or "") == "" then
                step(g, p, "No valid bid: not sold", t.muted)
                g.title = ("#%s %s: no sale"):format(number(f[1]), g.item or "?")
                g.color = t.muted
            else
                step(g, p, ("%s won for %s shares"):format(f[2], f[3] or "?"), t.good)
                g.title = ("#%s %s: %s for %s"):format(number(f[1]), g.item or "?", f[2], f[3] or "?")
            end
            if g.kind == "test" then g.title = g.title .. " (test, not settled)" g.color = t.muted end
        elseif m == "TRANSFER" then
            local g = auctionGroup(p, f[1])
            step(g, p, ("%s moved the item from %s to %s at the same price"):format(p.sender, f[2] or "?", f[3] or "?"))
            g.title = g.title .. (" > %s"):format(f[3] or "?")
        elseif m == "VOID" then
            local g = auctionGroup(p, f[1])
            step(g, p, ("%s voided the sale (%s): nothing is settled for it"):format(p.sender, f[2] or "?"), t.bad)
            g.title = g.title .. " VOIDED"
            g.color = t.bad
        elseif m == "ABANDON" then
            local g = auctionGroup(p, f[1])
            step(g, p, ("Abandoned (%s): no result was announced, nothing is settled"):format(f[2] or "?"), t.bad)
            g.title = ("#%s %s: abandoned"):format(number(f[1]), g.item or "?")
            g.color = t.muted
        elseif m == "DELIVERED" then
            step(auctionGroup(p, f[1]), p, ("%s handed the item to %s"):format(p.sender, f[2] or "?"), t.good)
        elseif m == "KILL" then
            local g = newGroup(p, ("Boss killed: %s (%s present)"):format(f[2] or "?", plural(count(f[3]), "raider")), t.accent)
            step(g, p, ("%s recorded the kill of %s"):format(p.sender, f[2] or "?"))
            g.steps[#g.steps + 1] = { text = "Present: " .. (f[3] or ""), color = t.muted, raw = "" }
        elseif m == "LEAD" then
            lastLead = newGroup(p, ("Leadership: %s took over"):format(f[1] or p.sender), t.accent)
            step(lastLead, p, ("%s claimed FairShare leadership"):format(f[1] or p.sender))
        elseif m == "ACK" then
            local g = lastLead or newGroup(p, "Leadership", t.accent)
            step(g, p, ("%s backed %s as leader"):format(p.sender, f[1] or "?"), t.muted)
        elseif m == "LOOT" then
            local g = newGroup(p, ("%s looted %s"):format(p.sender, f[1] or "?"), t.text)
            step(g, p, ("%s received %s from the loot window"):format(p.sender, f[1] or "?"))
        elseif p.dir == "note" then
            local g = newGroup(p, p.raw, t.muted)
            step(g, p, p.raw, t.muted)
        else
            local g = newGroup(p, ("%s from %s"):format(m, p.sender), t.muted)
            step(g, p, p.raw, t.muted)
        end
    end
    return out
end

local win

function UI.ShowTranscript(raidID)
    local W, t = UI.Widgets, UI.Skin.Current()
    raidID = raidID or (ns.Raid.context and ns.Raid.context.raidID)
    if not raidID then ns.Print("No raid loaded") return end
    local expanded, render, showRaw = {}, nil, false
    local lines = ns.Transcript.Lines(raidID)
    local events = groups(lines, t)

    if not win then
        win = W.Window("FairShareTranscriptWindow", "Transcript", 600, 30 + ROWS * ROW_H + 40)
        win:SetFrameStrata("DIALOG")
        local c = win.content
        win.summary = W.Label(c, "", 11, t.muted, "LEFT")
        win.summary:SetPoint("TOPLEFT", 8, -6)
        win.summary:SetPoint("RIGHT", -8, 0)
        win.summary:SetWordWrap(false)
        win.list = W.VirtualList(c, ROWS, ROW_H, function(row)
            row.expander = CreateFrame("Button", nil, row)
            row.expander:SetSize(14, 14)
            row.expander:SetNormalTexture("Interface\\Buttons\\UI-PlusButton-Up")
            row.expander:SetHighlightTexture("Interface\\Buttons\\UI-PlusButton-Hilight", "ADD")
            row.expander:SetPoint("LEFT", 2, 0)
            row.expander:SetScript("OnClick", function() if row.OnToggle then row.OnToggle() end end)
            -- Fixed columns so times and names line up whatever the font.
            row.when = W.Label(row, "", 11, t.muted, "LEFT")
            row.when:SetPoint("LEFT", 20, 0)
            row.when:SetWidth(56)
            row.who = W.Label(row, "", 11, t.text, "LEFT")
            row.who:SetPoint("LEFT", 80, 0)
            row.who:SetWidth(110)
            row.who:SetWordWrap(false)
            row.who:SetMaxLines(1)
            row.text = W.Label(row, "", 11, t.text, "LEFT")
            row.text:SetPoint("LEFT", 196, 0)
            row.text:SetPoint("RIGHT", -4, 0)
            row.text:SetWordWrap(false)
            row.text:SetMaxLines(1)
            row:SetScript("OnClick", function() if row.OnToggle then row.OnToggle() end end)
            row:SetScript("OnEnter", function(self)
                if self.full then
                    GameTooltip:SetOwner(self, "ANCHOR_TOP")
                    GameTooltip:SetText(self.full, 1, 1, 1, 1, true)
                    GameTooltip:Show()
                end
            end)
            row:SetScript("OnLeave", function() GameTooltip:Hide() end)
        end, function(row, l)
            row.OnToggle = l.toggle or false
            row.full = l.full or false
            local color = l.color or t.text
            row.when:SetText(l.when or "")
            row.who:SetText(l.who or "")
            row.who:SetTextColor(color[1], color[2], color[3], 1)
            row.text:ClearAllPoints()
            if l.toggle then
                row.expander:Show()
                row.expander:SetNormalTexture(l.open and "Interface\\Buttons\\UI-MinusButton-Up" or "Interface\\Buttons\\UI-PlusButton-Up")
                row.text:SetPoint("LEFT", 80, 0)
            else
                row.expander:Hide()
                row.text:SetPoint("LEFT", l.who and 196 or 196 + (l.indent or 0), 0)
            end
            row.text:SetPoint("RIGHT", -4, 0)
            row.text:SetText(l.text)
            row.text:SetTextColor(color[1], color[2], color[3], 1)
        end)
        win.list:SetPoint("TOPLEFT", 8, -24)
        win.list:SetPoint("RIGHT", -8, 0)
        local strip = CreateFrame("Frame", nil, c)
        strip:SetSize(100 + 6 + 100, 22)
        strip:SetPoint("BOTTOM", 0, 8)
        win.expandAll = W.Button(strip, "Expand all", 100, 22, function() if win.ExpandAll then win.ExpandAll(true) end end)
        win.expandAll:SetPoint("LEFT", 0, 0)
        win.collapseAll = W.Button(strip, "Collapse all", 100, 22, function() if win.ExpandAll then win.ExpandAll(false) end end)
        win.collapseAll:SetPoint("RIGHT", 0, 0)
        win.raw = W.Checkbox(c, "Show raw messages", function(on) if win.SetRaw then win.SetRaw(on) end end)
        win.raw:SetPoint("BOTTOMRIGHT", -8, 8)
        win.raw:SetWidth(170)
    end

    local function clock(time) return date and date("%H:%M:%S", time) or tostring(time) end

    local function build()
        local out = {}
        for i, g in ipairs(events) do
            out[#out + 1] = {
                when = clock(g.time), text = g.title, full = g.title, color = g.color, open = expanded[i] or false,
                toggle = function() expanded[i] = not expanded[i] or nil; render() end,
            }
            if expanded[i] then
                for _, s in ipairs(g.steps) do
                    out[#out + 1] = { when = s.time and clock(s.time) or "", who = s.who or "", text = s.text, full = s.text, color = s.color }
                    if showRaw and s.raw and s.raw ~= "" then
                        out[#out + 1] = { text = s.raw, full = s.raw, color = t.muted, indent = 12 }
                    end
                end
            end
        end
        return out
    end

    render = function()
        local items = build()
        if #items == 0 then items = { { text = "Nothing recorded for this raid", color = t.muted } } end
        win.list:SetItems(items)
        win.summary:SetText(("Raid %s: %s from %s. Click an event to expand it."):format(raidID, plural(#events, "event"), plural(#lines, "message")))
    end
    win.ExpandAll = function(on)
        expanded = {}
        if on then for i = 1, #events do expanded[i] = true end end
        render()
    end
    win.SetRaw = function(on)
        showRaw = on and true or false
        render()
    end
    win.raw:SetChecked(showRaw)
    win.title:SetText(("Transcript - Raid %s"):format(raidID))
    render()
    win:Show()
    return win
end
