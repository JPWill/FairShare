-- FairShare/UI/Roster.lua
-- Leader-only tab: the roster officers confirmed in Discord, who is already in the group,
-- and one click to invite everyone who is not. Scrolls, so a 40-player roster fits.
local ADDON, ns = ...

local UI, Events, Roster = ns.UI, ns.Core.Events, ns.Roster

local ROLE_ORDER = { tank = 1, healer = 2, melee = 3, ranged = 4 }
local ROLE_LABEL = { tank = "Tank", healer = "Healer", melee = "Melee", ranged = "Ranged" }
local ROWS, ROW_H = 16, 18

UI.RegisterPanel("Roster", function(panel)
    local W, t = UI.Widgets, UI.Skin.Current()

    local summary = W.Field(panel, "Roster", -4)
    -- The invite buttons sit on their own row so the summary never runs under them.
    local inviteBtn = W.Button(panel, "Invite Rostered", 120, 22, function()
        local n = Roster.Invite(Roster.Missing("confirmed"))
        ns.Print(n > 0 and ("Inviting %d"):format(n) or "Everyone confirmed is already here")
    end)
    inviteBtn:SetPoint("TOP", panel, "TOP", 63, -26)
    local standbyBtn = W.Button(panel, "Invite Standby", 120, 22, function()
        local n = Roster.Invite(Roster.Missing("standby"))
        ns.Print(n > 0 and ("Inviting %d standby"):format(n) or "No standby missing")
    end)
    standbyBtn:SetPoint("RIGHT", inviteBtn, "LEFT", -6, 0)
    local counts = W.Label(panel, "", 11, t.muted, "LEFT")
    counts:SetPoint("TOPLEFT", 0, -31)
    counts:SetPoint("RIGHT", standbyBtn, "LEFT", -8, 0)
    counts:SetWordWrap(false)

    W.Separator(panel, -54)

    local list = W.VirtualList(panel, ROWS, ROW_H, function(r)
        r.entryName = false
        r.name = W.Label(r, "", nil, t.text, "LEFT")
        r.name:SetPoint("LEFT", 0, 0)
        r.name:SetWidth(200)
        r.name:SetWordWrap(false)
        r.role = W.Label(r, "", nil, t.muted, "LEFT")
        r.role:SetPoint("LEFT", r.name, "RIGHT", 6, 0)
        r.role:SetWidth(70)
        r.state = W.Label(r, "", nil, t.muted, "LEFT")
        r.state:SetPoint("LEFT", r.role, "RIGHT", 6, 0)
        r.state:SetWidth(80)
        r.btn = W.Button(r, "Invite", 56, 16, function() if r.entryName then Roster.Invite({ r.entryName }) end end)
        r.btn:SetPoint("RIGHT", 0, 0)
    end, function(r, e)
        r.entryName = e.name
        r.name:SetText(e.name)
        r.role:SetText(ROLE_LABEL[e.role] or e.role)
        if e.present then
            r.state:SetText("Here")
            r.state:SetTextColor(unpack(t.good))
            r.btn:Hide()
        else
            r.state:SetText(e.status == "standby" and "standby" or "missing")
            r.state:SetTextColor(unpack(e.status == "standby" and t.muted or t.bad))
            r.btn:Show()
        end
    end)
    list:SetPoint("TOPLEFT", 0, -62)
    list:SetPoint("RIGHT", 0, 0)

    local function refresh()
        local entries = Roster.Entries()
        table.sort(entries, function(a, b)
            if a.status ~= b.status then return a.status == "confirmed" end
            if a.present ~= b.present then return not a.present end
            local ra, rb = ROLE_ORDER[a.role] or 9, ROLE_ORDER[b.role] or 9
            if ra ~= rb then return ra < rb end
            return a.name < b.name
        end)
        local confirmed, present, byRole = 0, 0, {}
        for _, e in ipairs(entries) do
            if e.status == "confirmed" then
                confirmed = confirmed + 1
                if e.present then present = present + 1 end
                byRole[e.role] = (byRole[e.role] or 0) + 1
            end
        end
        if #entries == 0 then
            summary:SetText("No roster in this snapshot (officers confirm one in Discord)")
            summary:SetTextColor(unpack(t.muted))
            counts:SetText("")
        else
            summary:SetText(("%d of %d confirmed are here, %d standby%s"):format(present, confirmed, #entries - confirmed,
                Roster.Pending() > 0 and (", %d invites pending"):format(Roster.Pending()) or ""))
            summary:SetTextColor(unpack(present == confirmed and t.good or t.text))
            counts:SetText("")
        end
        inviteBtn:SetEnabled(#entries > 0 and UI.IsLeader())
        standbyBtn:SetEnabled(#entries > 0 and UI.IsLeader())
        list:SetItems(entries)
    end

    for _, e in ipairs({ "SNAPSHOT_CHANGED", "GROUP_CHANGED", "ROSTER_INVITED", "ROSTER_INVITES_DONE" }) do Events.On(e, refresh) end
    Events.Register("GROUP_ROSTER_UPDATE", refresh)
    panel.OnShow = refresh
    refresh()
end, { leaderOnly = true })
