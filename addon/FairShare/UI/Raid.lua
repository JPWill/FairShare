-- FairShare/UI/Raid.lua
-- The raid itself, for everyone: which snapshot is in force (compare the checksum with
-- Discord), the off-spec discount, who is registered, and the transcript. Raiders can
-- export; the leader can also import, clear the raid, rebroadcast, and run the test
-- auction that proves everyone can bid before real loot drops.
local ADDON, ns = ...

local UI, Events, Group, Auction, Roster = ns.UI, ns.Core.Events, ns.Core.Group, ns.Auction, ns.Roster

UI.RegisterPanel("Raid", function(panel)
    local W, t = UI.Widgets, UI.Skin.Current()
    local COL = W.LABEL_COLUMN + 4

    -- Current Raid: the raid's name on the field line, its checksum underneath, then the
    -- Snapshot line says how many rows arrived and from whom.
    local snapshot = W.Field(panel, "Current Raid", -4)
    local checksum = W.Label(panel, "", nil, t.text, "LEFT")
    checksum:SetPoint("TOPLEFT", COL, -20)
    checksum:SetPoint("RIGHT", 0, 0)
    checksum:SetWordWrap(false)
    checksum:SetMaxLines(1)
    local snapshotInfo = W.Field(panel, "Snapshot", -38)
    local offspec = W.Field(panel, "Off-spec", -60)
    local roster = W.Field(panel, "Roster", -82)
    local transcript = W.Field(panel, "Transcript", -104)
    local addonKey = W.Field(panel, "Addon key", -126)

    W.Separator(panel, -150)

    -- Leader checks: the test auction and a rebroadcast.
    local checks = CreateFrame("Frame", nil, panel)
    checks:SetPoint("TOPLEFT", 0, -154)
    checks:SetPoint("RIGHT", 0, 0)
    checks:SetHeight(96)
    local testBtn = W.Button(checks, "Test Auction", 110, 22, function()
        local ok, err = Auction.OpenTest()
        if not ok then ns.Print(err) end
    end)
    testBtn:SetPoint("TOPLEFT", 0, -4)
    local testSummary = W.Label(checks, "", 11, t.muted, "LEFT")
    testSummary:SetPoint("LEFT", testBtn, "RIGHT", 10, 0)
    testSummary:SetHeight(22)
    testSummary:SetJustifyH("CENTER")
    testSummary:SetJustifyV("MIDDLE")
    testSummary:SetWordWrap(false)
    testSummary:SetMaxLines(2)
    testSummary:SetHeight(28)
    testSummary:SetJustifyV("TOP")
    local rebroadcastBtn = W.Button(checks, "Re-Broadcast", 100, 22, function()
        local ok, err = ns.Raid.Broadcast()
        if not ok then ns.Print(err) end
    end)
    rebroadcastBtn:SetPoint("TOPRIGHT", 0, -4)
    testSummary:SetPoint("RIGHT", rebroadcastBtn, "LEFT", -10, 0)
    local testList = W.List(checks, 4, 14, 220)
    testList:SetPoint("TOPLEFT", 0, -32)
    testList:SetPoint("RIGHT", 0, 0)
    local checksSep = W.Separator(checks, -94)

    -- Buttons: raiders export; the leader also imports and clears.
    -- The buttons sit in a strip centred on the panel; the hint goes under them, centred too.
    local buttons = CreateFrame("Frame", nil, panel)
    buttons:SetPoint("RIGHT", 0, 0)
    buttons:SetHeight(48)
    local strip = CreateFrame("Frame", nil, buttons)
    strip:SetSize(90 * 3 + 110 + 6 * 3, 22)
    strip:SetPoint("TOP", 0, -2)
    local exportBtn = W.Button(strip, "Export", 90, 22, function()
        local str, err = ns.Transcript.Export()
        if not str then ns.Print(err) return end
        W.TextPopup("Transcript (copy and upload to the bot)", str)
    end)
    exportBtn:SetPoint("RIGHT", 0, 0)
    local showBtn = W.Button(strip, "Show Transcript", 110, 22, function()
        local c = ns.Raid.context
        if c then UI.ShowTranscript(c.raidID) else ns.Print("No raid loaded") end
    end)
    showBtn:SetPoint("RIGHT", exportBtn, "LEFT", -6, 0)
    local clearBtn = W.Button(strip, "Clear raid", 90, 22, function()
        W.Confirm("Clear raid", "Forget this raid's context on your client. The transcript stays saved. Everyone else keeps theirs until you broadcast again.", {
            { "Clear", function() ns.Raid.Clear() end },
            { "Cancel" },
        })
    end)
    clearBtn:SetPoint("RIGHT", showBtn, "LEFT", -6, 0)
    local importBtn = W.Button(strip, "Import", 90, 22, function()
        W.TextPopup("Import snapshot", "", function(text)
            local snap, err = ns.Snapshot.Import(text, "paste")
            if not snap then ns.Print("Import failed: " .. tostring(err)) end
        end, "Import")
    end)
    importBtn:SetPoint("RIGHT", clearBtn, "LEFT", -6, 0)
    local hint = W.Label(buttons, "", 11, t.muted, "CENTER")
    hint:SetPoint("TOPLEFT", 0, -28)
    hint:SetPoint("RIGHT", 0, 0)
    hint:SetWordWrap(true)
    hint:SetMaxLines(2)
    hint:SetHeight(28)
    hint:SetJustifyV("TOP")

    local function refresh()
        local leader = UI.IsLeader()
        local c = ns.Raid.context
        local snap = ns.Snapshot.Current()

        if c then
            local n = 0
            for _ in pairs(c.rows) do n = n + 1 end
            snapshot:SetText(("Raid Name: %s"):format(c.raidID))
            snapshot:SetTextColor(unpack(t.text))
            checksum:SetText(("Checksum: %s"):format(c.short))
            snapshotInfo:SetText(("%d row%s from %s"):format(n, n == 1 and "" or "s", c.leader or "the leader"))
        elseif snap and leader then
            snapshot:SetText(("Raid Name: %s"):format(snap.raidID))
            snapshot:SetTextColor(unpack(t.text))
            checksum:SetText(("Checksum: %s"):format(snap.short))
            local blocker = ns.Raid.BroadcastBlocker(snap)
            snapshotInfo:SetText(("%d row%s from %s, %s%s"):format(snap.rowCount, snap.rowCount == 1 and "" or "s", snap.source or "?",
                blocker and ("not sent: " .. blocker) or "broadcasting",
                ns.Snapshot.dataError and (" (" .. ns.Snapshot.dataError .. ")") or ""))
        else
            snapshot:SetText(leader and "None loaded" or "Waiting for the leader")
            snapshot:SetTextColor(unpack(t.muted))
            checksum:SetText("")
            snapshotInfo:SetText(leader and (ns.Snapshot.dataError or "Run the sync client, or Import a pasted string.") or "")
        end

        local pct = (c and c.offspecPct) or (snap and snap.offspecPct) or 0
        offspec:SetText(pct > 0 and ("%d%% off the min price for off-spec bids (set by the bot per raid)"):format(pct) or "No discount (set by the bot per raid)")

        local members = Group.Members()
        local have = 0
        if c then for _, n in ipairs(members) do if c.rows[n] then have = have + 1 end end end
        local parts = { ("%d in the group, %d registered"):format(#members, have) }
        if leader and Roster and Roster.Entries then
            local entries = Roster.Entries()
            local confirmed, present = 0, 0
            for _, e in ipairs(entries) do
                if e.status == "confirmed" then
                    confirmed = confirmed + 1
                    if e.present then present = present + 1 end
                end
            end
            if #entries > 0 then parts[#parts + 1] = ("%d of %d confirmed are here (Roster tab)"):format(present, confirmed) end
        end
        roster:SetText(table.concat(parts, "; "))
        roster:SetTextColor(unpack((c and have < #members) and t.bad or t.text))

        local lines = c and ns.Transcript.Lines(c.raidID) or {}
        transcript:SetText(#lines > 0 and ("%d lines recorded"):format(#lines) or "Nothing recorded yet")
        if Auction.State.MyKey() then
            addonKey:SetText("Set: your bids are signed")
            addonKey:SetTextColor(unpack(t.good))
        else
            addonKey:SetText("Not set: bids can be contested, see Settings")
            addonKey:SetTextColor(unpack(t.bad))
        end
        exportBtn:SetEnabled(#lines > 0)
        showBtn:SetEnabled(#lines > 0)

        if leader then
            checks:Show()
            buttons:ClearAllPoints()
            buttons:SetPoint("TOPLEFT", 0, -254)
            buttons:SetPoint("RIGHT", 0, 0)
            importBtn:Show(); clearBtn:Show()
            strip:SetWidth(90 * 3 + 110 + 6 * 3)
            clearBtn:SetEnabled(c ~= nil)
            hint:SetText("Import a snapshot, clear this raid, or export the transcript.")
            rebroadcastBtn:SetEnabled(snap ~= nil and ns.Raid.IsLeader())
            local a = Auction.FindTest()
            local running = a and a.phase ~= "done"
            testBtn:SetEnabled(c ~= nil and not running and ns.Raid.IsLeader())
            if a then
                local report = Auction.TestReport(a)
                local ok, rows = 0, {}
                for _, r in ipairs(report) do
                    if r.ok then ok = ok + 1 else rows[#rows + 1] = { text = r.name, right = r.status, color = t.bad } end
                end
                local now = GetServerTime()
                if a.phase == "bidding" then
                    testSummary:SetText(("Running: bidding, %ds left, %d of %d replied%s"):format(math.max(0, (a.deadline or now) - now), #a:Committers(), #report, Auction.IsPaused() and " (paused)" or ""))
                    testSummary:SetTextColor(unpack(t.muted))
                elseif a.phase == "reveal" then
                    local revealed = 0
                    for _ in pairs(a.reveals) do revealed = revealed + 1 end
                    for _ in pairs(a.invalid) do revealed = revealed + 1 end
                    testSummary:SetText(("Running: revealing, %d of %d"):format(revealed, #a:Committers()))
                    testSummary:SetTextColor(unpack(t.muted))
                else
                    local r = a.result or { winner = "", price = 0 }
                    local outcome = r.winner == "" and "No bids" or ("%s won for %d shares"):format(r.winner, r.price)
                    testSummary:SetText(("%d of %d OK. %s%s"):format(ok, #report, outcome, #a.flags > 0 and " (flagged)" or ""))
                    testSummary:SetTextColor(unpack(ok == #report and t.good or t.bad))
                end
                testList:SetRows(rows)
            else
                testSummary:SetText("")
                testSummary:SetTextColor(unpack(t.muted))
                testList:SetRows({})
            end
        else
            checks:Hide()
            buttons:ClearAllPoints()
            buttons:SetPoint("TOPLEFT", 0, -154)
            buttons:SetPoint("RIGHT", 0, 0)
            importBtn:Hide(); clearBtn:Hide()
            strip:SetWidth(90 + 6 + 110)
            hint:SetText("The leader exports and uploads after the last boss. Keep yours: Settings lets you export any past raid if a result is disputed.")
        end
    end

    for _, e in ipairs({ "SNAPSHOT_CHANGED", "RAID_CONTEXT", "RAID_LEADER_CHANGED", "GROUP_CHANGED", "AUCTION_OPEN", "AUCTION_UPDATE", "AUCTION_CLOSE", "AUCTION_RESULT", "TRANSCRIPT_LINE", "ADDON_KEY_CHANGED" }) do
        Events.On(e, refresh)
    end
    Events.Register("GROUP_ROSTER_UPDATE", refresh)
    ns.Core.Timer.Every(1, function()
        if panel:IsShown() and Auction.IsRunning() then refresh() end
    end)
    panel.OnShow = refresh
    refresh()
end)
