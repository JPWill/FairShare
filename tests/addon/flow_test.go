package addontest

import (
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// pump echoes every message the harness sent back to itself (the server does this for
// RAID and self-whisper), then advances the clock a little. Repeats until `seconds` pass.
func pump(t *testing.T, h *Harness, seconds float64) {
	t.Helper()
	me := mustEval(t, h, `FairShare.Core.Group.Me()`).String()
	for elapsed := 0.0; elapsed < seconds; elapsed += 0.5 {
		sent := h.SentMessages()
		for i := h.Delivered; i < len(sent); i++ {
			if err := h.Deliver(sent[i], me); err != nil {
				t.Fatal(err)
			}
		}
		h.Delivered = len(sent)
		if err := h.AdvanceTime(0.5); err != nil {
			t.Fatal(err)
		}
	}
}

func soloDevLeader(t *testing.T) *Harness {
	t.Helper()
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true})
	h.Set("InRaid", lua.LFalse)
	h.Set("InGroup", lua.LFalse)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		FairShareDB.settings.dev = true
		math.randomseed(42)
		assert(FairShare.Dev.LoadSampleSnapshot())
		assert(FairShare.Raid.Broadcast())
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	return h
}

func TestSnapshotRoundTripAndGolden(t *testing.T) {
	h := mustNew(t)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	// The same fixture the backend's golden test uses; both sides must print the same checksum.
	got := mustEval(t, h, `(function()
		local S = FairShare.Lib.Serialize
		local text = S.BuildSnapshot({ raidID = "1", issued = 1700000000, offspecPct = 0,
			floors = { DEFAULT = 10, INVTYPE_HEAD = 20 }, banned = {},
			rows = { ["Alice"] = { owner = "Alice", balance = 0, credit = 100 }, ["Bob"] = { owner = "Bob", balance = -50, credit = 200 } } })
		local snap = assert(S.ParseSnapshot(text))
		assert(snap.rowCount == 2 and snap.floors.INVTYPE_HEAD == 20 and snap.rows["Bob"].balance == -50)
		assert(S.BuildSnapshot(snap) == text)
		return text .. "\n#" .. FairShare.Lib.Sha256.hex(text)
	end)()`).String()
	want := "FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1700000000\noffspec=0\nfloor:DEFAULT=10\nfloor:INVTYPE_HEAD=20\nrow:Alice\tAlice\t0\t100\nrow:Bob\tBob\t-50\t200"
	text, sum, _ := strings.Cut(got, "\n#")
	if text != want {
		t.Fatalf("canonical text:\n%q\nwant:\n%q", text, want)
	}
	t.Logf("golden snapshot checksum (compare with backend): %s", sum)

	// Import via paste string and via Data.lua, newest issued wins.
	if err := h.Do(`
		local S, B = FairShare.Lib.Serialize, FairShare.Lib.Base64
		local older = S.BuildSnapshot({ raidID = "7", issued = 100, offspecPct = 5, floors = { DEFAULT = 10 }, banned = {}, rows = { ["Leader"] = { owner = "L", balance = 1, credit = 2 } } })
		local newer = S.BuildSnapshot({ raidID = "8", issued = 200, offspecPct = 0, floors = { DEFAULT = 10 }, banned = { ["Bad"] = true }, rows = { ["Leader"] = { owner = "L", balance = 5, credit = 6 } } })
		assert(FairShare.Snapshot.Import(B.encode(older), "paste"))
		assert(FairShare.Snapshot.Current().raidID == "7")
		FairShareData = { version = 1, snapshot = newer, checksum = FairShare.Lib.Sha256.hex(newer) }
		FairShare.Core.Events.Fire("DB_READY")
		assert(FairShare.Snapshot.Current().raidID == "8", "Data.lua should win by issue time")
		assert(FairShare.Snapshot.Current().bannedList[1] == "Bad")
		FairShareData = { version = 1, snapshot = newer, checksum = "deadbeef" }
		FairShare.Core.Events.Fire("DB_READY")
		assert(FairShare.Snapshot.dataError, "bad checksum must be reported")
	`); err != nil {
		t.Fatal(err)
	}
}

// TestSoloAuctionWithVirtualRaiders runs a whole auction alone: real messages whisper
// the player and come back, virtual raiders bid, the leader closes and announces, and
// every check passes.
func TestSoloAuctionWithVirtualRaiders(t *testing.T) {
	h := soloDevLeader(t)
	if got := mustEval(t, h, `FairShare.Raid.context ~= nil`); got != lua.LTrue {
		t.Fatal("broadcast did not establish the raid context")
	}
	if got := mustEval(t, h, `(function() local n = 0 for _ in pairs(FairShare.Raid.context.rows) do n = n + 1 end return n end)()`); got != lua.LNumber(6) {
		t.Fatalf("rows in context = %v, want 6 (leader + 5 raiders)", got)
	}
	if err := h.Do(`assert(FairShare.Auction.Open("|cffa335ee|Hitem:17076::::::::60:::::::::|h[Bonereaver's Edge]|h|r", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if got := mustEval(t, h, `FairShare.Auction.active.floor`); got != lua.LNumber(20) {
		t.Fatalf("floor = %v, want 20 (INVTYPE_2HWEAPON)", got)
	}
	if got := mustEval(t, h, `FairShareBidFrame:IsShown()`); got != lua.LTrue {
		t.Fatal("bid popup did not open")
	}
	// The leader bids too, through the popup's path.
	if err := h.Do(`local card = FairShareBidFrame.FirstBiddable(); assert(card, "no biddable card"); card.box:SetText("60"); card:PlaceBid()`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45) // 20 s bidding + reveals + result
	if got := mustEval(t, h, `FairShare.Auction.active.phase`).String(); got != "done" {
		t.Fatalf("phase = %s, want done", got)
	}
	flags := mustEval(t, h, `table.concat(FairShare.Auction.active.flags, ",")`).String()
	if flags != "" {
		t.Fatalf("unexpected flags: %s", flags)
	}
	winner := mustEval(t, h, `FairShare.Auction.active.result.winner`).String()
	price := mustEval(t, h, `FairShare.Auction.active.result.price`)
	nbids := mustEval(t, h, `#FairShare.Auction.active:Bids()`)
	t.Logf("winner %s price %v from %v bids", winner, price, nbids)
	if winner == "" || int(nbids.(lua.LNumber)) < 2 {
		t.Fatalf("expected a contested sale, got winner %q with %v bids", winner, nbids)
	}
	if got := mustEval(t, h, `#FairShare.Auction.history`); got != lua.LNumber(1) {
		t.Fatalf("history = %v", got)
	}
	// Transcript recorded and exportable; the decoded text parses as a transcript.
	if err := h.Do(`
		local str = assert(FairShare.Transcript.Export())
		local text = FairShare.Lib.Base64.decode(str)
		assert(text:sub(1, 22) == "FAIRSHARE TRANSCRIPT 1", text:sub(1, 40))
		assert(text:find("\nraid=" .. FairShare.Raid.context.raidID .. "\n", 1, true))
		assert(text:find("\tin\t", 1, true))
		assert(text:find("	RESULT^", 1, true))
		local b = FairShareDB.transcript[FairShare.Raid.context.raidID]
		assert(b.snapshot == FairShare.Raid.context.checksum and b.version == "test")
	`); err != nil {
		t.Fatal(err)
	}
}

func TestChaosRaidersAreFlaggedButResultStillAgrees(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.SetChaos(true)
		assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	flags := mustEval(t, h, `table.concat(FairShare.Auction.active.flags, ",")`).String()
	if !strings.Contains(flags, "invalid_reveal:") || !strings.Contains(flags, ":hash_mismatch") {
		t.Fatalf("expected a hash_mismatch flag, got %q", flags)
	}
	if !strings.Contains(flags, "noreveal:") {
		t.Fatalf("expected a noreveal flag, got %q", flags)
	}
	if strings.Contains(flags, "result_mismatch") || strings.Contains(flags, "close_mismatch") {
		t.Fatalf("leader and client disagreed: %q", flags)
	}
}

func TestTestAuctionReportsEveryone(t *testing.T) {
	h := soloDevLeader(t)
	// One virtual raider is not in the snapshot: add a 6th after the snapshot was built.
	if err := h.Do(`
		FairShare.Core.Group.extra["Stranger"] = true
		FairShare.Dev.raiders["Stranger"] = { mode = "normal" }
		assert(FairShare.Auction.OpenTest())
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	report := mustEval(t, h, `(function()
		local out = {}
		for _, r in ipairs(FairShare.Auction.TestReport()) do out[#out+1] = r.name .. "=" .. r.status end
		return table.concat(out, ";")
	end)()`).String()
	if !strings.Contains(report, "Leader=OK") {
		t.Fatalf("leader should auto-bid in a test auction: %s", report)
	}
	if !strings.Contains(report, "Stranger=Not registered") {
		t.Fatalf("unregistered raider not reported: %s", report)
	}
	if strings.Count(report, "=OK") != 6 {
		t.Fatalf("expected 6 OK (leader + 5 raiders): %s", report)
	}
	if got := mustEval(t, h, `FairShare.Auction.active.kind`).String(); got != "test" {
		t.Fatalf("kind = %s", got)
	}
}

func TestEncounterPausesClose(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 5)
	if err := h.Do(`FairShare.Dev.SimulateEncounter(true)`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 30)
	if got := mustEval(t, h, `FairShare.Auction.active.phase`).String(); got != "bidding" {
		t.Fatalf("auction should stay open during the encounter, phase = %s", got)
	}
	if err := h.Do(`FairShare.Dev.SimulateKill()`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 30)
	if got := mustEval(t, h, `FairShare.Auction.active.phase`).String(); got != "done" {
		t.Fatalf("auction should finish after the encounter, phase = %s", got)
	}
	if got := mustEval(t, h, `FairShare.Raid.lastKill and FairShare.Raid.lastKill.name`).String(); got != "Training Dummy" {
		t.Fatalf("KILL not recorded: %s", got)
	}
}

func TestRejectsUnauthorizedOpenAndLootParsing(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Core.Comm.Inject("Simone", FairShare.Core.Comm.Encode("OPEN", { "x-1", "loot", "|Hitem:19019|h[T]|h", 10, GetServerTime() + 20 }))
		assert(FairShare.Auction.active == nil, "non-leader OPEN must be ignored")
		FairShare.Dev.SimulateLoot()
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		local last = lines[#lines]
		assert(last:find("\tloot\tLeader\tLOOT^", 1, true), last)
		local looter, link = FairShare.Transcript.ParseLoot("Simba receives loot: |cff0070dd|Hitem:1::::::::1:::::::::|h[Thing]|h|r.")
		assert(looter == "Simba" and link:find("Thing", 1, true), tostring(looter))
	`); err != nil {
		t.Fatal(err)
	}
}

func TestDevTabAppearsWithDeveloperMode(t *testing.T) {
	h := mustNew(t, Options{Leader: true})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `FairShare.UI.TabNames()`).String(); got != "Home,Ledger,Raid,Roster,Settings" {
		t.Fatalf("tabs = %s", got)
	}
	if err := h.Do(`FairShareDB.settings.dev = true; FairShare.Core.Events.Fire("DEV_MODE", true)`); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `FairShare.UI.TabNames()`).String(); got != "Home,Ledger,Raid,Roster,Settings,Dev" {
		t.Fatalf("tabs = %s", got)
	}
	// Every panel builds without error under the stubs.
	if err := h.Do(`for _, n in ipairs({"Home","Ledger","Raid","Roster","Settings","Dev"}) do FairShare.UI.Show(n) end`); err != nil {
		t.Fatal(err)
	}
}

func TestTransferAtSamePrice(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.bidding = true
		assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.phase == "done" and a.result.winner ~= "", "need a sale")
		Winner = a.result.winner
		-- Pick a recipient who is not the winner and can afford it.
		for name, row in pairs(a.ctx.rows) do
			if name ~= Winner and row.balance + row.credit >= a.result.price then Recipient = name break end
		end
		assert(Recipient)
		assert(#FairShare.Auction.Transferable() == 1)
		-- Rejections before anything is sent.
		assert(not FairShare.Auction.Transfer(a.id, Winner))
		assert(not FairShare.Auction.Transfer(a.id, "Nobody"))
		assert(FairShare.Auction.Transfer(a.id, Recipient))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.transfer and a.transfer.to == Recipient and a.transfer.from == Winner, "transfer not applied")
		assert(a:EffectiveWinner() == Recipient)
		assert(#a.flags == 0, table.concat(a.flags, ","))
		assert(#FairShare.Auction.Transferable() == 0)
		assert(FairShareDB.auctions[FairShare.Raid.context.raidID][a.id].transfer.to == Recipient)
		-- A second transfer from the wire is flagged and ignored.
		FairShare.Core.Comm.Inject(FairShare.Core.Group.Me(), FairShare.Core.Comm.Encode("TRANSFER", { a.id, Winner, Recipient }))
		assert(a.flags[1] == Recipient and false or a.flags[1]:find("bad_transfer:" .. Recipient .. ":duplicate", 1, true), a.flags[1])
		-- Transfer from a non-leader is ignored entirely.
		local before = #a.flags
		FairShare.Core.Comm.Inject("Simone", FairShare.Core.Comm.Encode("TRANSFER", { a.id, Winner, "Simba" }))
		assert(#a.flags == before)
		local text = FairShare.Lib.Base64.decode(FairShare.Transcript.Export())
		assert(text:find("\tTRANSFER^", 1, true))
	`); err != nil {
		t.Fatal(err)
	}
}

func TestNamesStripRealmSuffix(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Thrall Doomhammer"})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `FairShare.Core.Group.Me()`).String(); got != "Thrall Doomhammer" {
		t.Fatalf("Me() = %q", got)
	}
	if got := mustEval(t, h, `FairShare.Core.Group.Normalize("Thrall Doomhammer-Forever")`).String(); got != "Thrall Doomhammer" {
		t.Fatalf("Normalize = %q", got)
	}
	if err := h.Do(`
		Received = nil
		FairShare.Core.Comm.On("T", function(sender, fields) Received = sender end)
	`); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(Sent{Prefix: "FAIRSHARE", Msg: "=T^x", Channel: "RAID"}, "Jaina Proudmoore-Forever"); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `Received`).String(); got != "Jaina Proudmoore" {
		t.Fatalf("sender = %q", got)
	}
}

func TestRosterParseAndInvites(t *testing.T) {
	h := soloDevLeader(t)
	// The sample snapshot carries a roster with one confirmed player who is not present.
	if err := h.Do(`
		local snap = FairShare.Snapshot.Current()
		assert(#snap.rosterList == 7, #snap.rosterList)
		assert(snap.roster["Latecomer"].status == "confirmed" and snap.roster["Latecomer"].role == "ranged")
		-- Round trip through the canonical text keeps roster lines in place and sorted.
		local S = FairShare.Lib.Serialize
		local again = assert(S.ParseSnapshot(S.BuildSnapshot(snap)))
		assert(S.BuildSnapshot(again) == snap.text)
		assert(snap.text:find("\nban:", 1, true) == nil and snap.text:find("\nroster:Latecomer\tranged\tconfirmed\nrow:", 1, true) == nil)
		local missing = FairShare.Roster.Missing("confirmed")
		assert(#missing == 1 and missing[1] == "Latecomer", table.concat(missing, ","))
		assert(FairShare.Roster.Invite(missing) == 1)
	`); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceTime(1); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `table.concat(Invites, ",")`).String(); got != "Latecomer" {
		t.Fatalf("invites = %q", got)
	}
	// Six more invites from solo: four go out, the queue waits for the party, then converts to raid.
	if err := h.Do(`
		Invites = {}
		assert(FairShare.Roster.Invite({ "A", "B", "C", "D", "E", "F" }) == 6)
	`); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceTime(3); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `#Invites`); got != lua.LNumber(4) {
		t.Fatalf("expected 4 invites before the party forms, got %v", got)
	}
	h.Set("InGroup", lua.LTrue)
	h.Set("Roster", mustEval(t, h, `{ {"Leader", 2}, {"A", 0}, {"B", 0}, {"C", 0}, {"D", 0} }`))
	if err := h.FireEvent("GROUP_ROSTER_UPDATE"); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceTime(3); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `#Invites`); got != lua.LNumber(6) {
		t.Fatalf("expected all 6 invites after the party formed, got %v", got)
	}
	if got := mustEval(t, h, `Converted`); got != lua.LNumber(1) {
		t.Fatalf("expected one ConvertToRaid, got %v", got)
	}
	if got := mustEval(t, h, `FairShare.UI.TabNames()`).String(); got != "Home,Ledger,Raid,Roster,Settings,Dev" {
		t.Fatalf("tabs = %s", got)
	}
}

// TestLootQueueAutoRunsAndLedgerTracksDelivery: a loot line queues the item, auto-run
// opens the auction by itself, and the ledger knows the leader still holds the item.
func TestLootQueueAutoRunsAndLedgerTracksDelivery(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.SimulateLoot()
		assert(FairShare.Auction.Queue.Count() == 1, "loot line should land in pending")
		assert(FairShare.Auction.active == nil, "nothing starts by itself")
		assert(FairShare.Auction.Queue.StartAll() == 1, "Start opens the pending item")
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 5)
	if got := mustEval(t, h, `FairShare.Auction.active and FairShare.Auction.active.itemLink or ""`).String(); !strings.Contains(got, "Thunderfury") {
		t.Fatalf("Start did not open the pending item: %q", got)
	}
	if got := mustEval(t, h, `FairShare.Auction.Queue.Count()`); got != lua.LNumber(0) {
		t.Fatalf("queue should be empty once the auction opened, got %v", got)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.phase == "done", a.phase)
		assert(a.result.winner ~= "" and a.result.winner ~= "Leader", "a virtual raider should win: " .. a.result.winner)
		local Tr = FairShare.Auction.Trade
		local entries = Tr.Entries()
		assert(#entries == 1, "ledger entries " .. #entries)
		assert(Tr.Holder(entries[1]) == "Leader", "the loot line says the leader holds it")
		assert(Tr.Status(entries[1]) == "owed", tostring(Tr.Status(entries[1])))
		assert(#Tr.Owed() == 1, "leader owes the item")
		Tr.MarkDelivered(entries[1].id, a.result.winner)
		assert(Tr.Status(entries[1]) == "delivered")
		assert(#Tr.Owed() == 0)
		-- A second loot line is a second drop: it queues another copy.
		FairShare.Dev.SimulateLoot()
		assert(FairShare.Auction.Queue.Count() == 1, "second loot line must queue a second copy")
		FairShare.Auction.Queue.Remove(1)
		-- Void the sale from the ledger side: the entry is marked, the item is no longer owed,
		-- and the estimate no longer charges the winner.
		local before = FairShare.Auction.Book.Deltas()[a.result.winner] or 0
		assert(before < 0, "winner should be charged before the void: " .. before)
		assert(FairShare.Auction.Void(a.id, "re-run"))
	`); err != nil {
		t.Fatal(err)
	}
}

// TestVoidStrikesTheSale: after the leader voids, every client marks the entry, nothing
// is owed for it, and the running estimate no longer charges the winner.
func TestVoidStrikesTheSale(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.phase == "done" and a.result.winner ~= "", "need a sale")
		local Book = FairShare.Auction.Book
		assert((Book.Deltas()[a.result.winner] or 0) < 0, "winner charged before void")
		assert(not FairShare.Auction.Void("nope", "x"), "unknown id must fail")
		assert(FairShare.Auction.Void(a.id, "re-run"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if err := h.Do(`
		local a = FairShare.Auction.active
		local Book, Tr = FairShare.Auction.Book, FairShare.Auction.Trade
		assert(a.voided == "re-run", "void not applied: " .. tostring(a.voided))
		assert(not FairShare.Auction.Void(a.id, "again"), "second void must fail")
		local e = Book.Find(a.id)
		assert(e and e.voided == "re-run", "summary not updated")
		assert(not Book.IsSale(e))
		assert(Tr.Status(e) == nil, "nothing owed for a voided sale")
		assert((Book.Deltas()[a.result.winner] or 0) == 0, "winner still charged after void")
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		local found = false
		for _, l in ipairs(lines) do if l:find("\tVOID^" .. a.id .. "^re-run", 1, true) then found = true end end
		assert(found, "VOID not in transcript")
	`); err != nil {
		t.Fatal(err)
	}
}

// TestQueueBroadcastsSnapshotItself: dropping an item before Broadcast was pressed sends
// the snapshot out and then opens the auction, with no further click from the leader.
func TestQueueBroadcastsSnapshotItself(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true})
	h.Set("InRaid", lua.LFalse)
	h.Set("InGroup", lua.LFalse)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		FairShareDB.settings.dev = true
		math.randomseed(7)
		assert(FairShare.Dev.LoadSampleSnapshot())
		assert(FairShare.Raid.context == nil, "nothing broadcast yet")
		assert(FairShare.Auction.Queue.Add("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "dropped"))
		local n = FairShare.Auction.Queue.StartAll()
		assert(n == 0, "cannot start before the snapshot lands")
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 3)
	if err := h.Do(`
		assert(FairShare.Raid.context ~= nil, "the snapshot should have broadcast itself")
		assert(FairShare.Auction.Queue.StartAll() == 1, "start once the snapshot is in")
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a and a.itemLink:find("Thunderfury", 1, true), "auction should be open")
		assert(FairShare.Auction.Queue.Count() == 0)
	`); err != nil {
		t.Fatal(err)
	}
}

// TestVoidAndTransferSurviveReload: with the live auction objects gone (as after /reload),
// Void and Transfer still work from the saved record, and every client applies them to it.
func TestVoidAndTransferSurviveReload(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.phase == "done" and a.result.winner ~= "", "need a sale")
		FairShareDB_savedID = a.id
		FairShareDB_winner = a.result.winner
		-- Forget the live objects the way a reload does; the summary stays in SavedVariables.
		FairShare.Auction.active = nil
		FairShare.Auction.history = {}
		assert(FairShare.Auction.Find(a.id) == nil)
		local Book = FairShare.Auction.Book
		assert(Book.Find(a.id), "saved record should still be listed")
		-- Transfer to someone who is not the winner.
		local to
		for _, n in ipairs(FairShare.Core.Group.Members()) do
			local row = FairShare.Raid.context.rows[n]
			if n ~= a.result.winner and n ~= "Leader" and row and row.balance + row.credit >= a.result.price then to = n end
		end
		assert(to, "no member can cover " .. a.result.price)
		assert(FairShare.Auction.Transfer(a.id, to))
		FairShareDB_to = to
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if err := h.Do(`
		local Book = FairShare.Auction.Book
		local e = Book.Find(FairShareDB_savedID)
		assert(e.transfer and e.transfer.to == FairShareDB_to, "transfer not applied to the saved record")
		assert(Book.Owner(e) == FairShareDB_to)
		assert(not FairShare.Auction.Transfer(FairShareDB_savedID, FairShareDB_winner), "second transfer must fail")
		assert(FairShare.Auction.Void(FairShareDB_savedID, "re-run"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if err := h.Do(`
		local Book = FairShare.Auction.Book
		local e = Book.Find(FairShareDB_savedID)
		assert(e.voided == "re-run", "void not applied to the saved record: " .. tostring(e.voided))
		assert(not Book.IsSale(e))
		local ok, err = FairShare.Auction.Void(FairShareDB_savedID, "again")
		assert(not ok and err == "Already voided", tostring(err))
	`); err != nil {
		t.Fatal(err)
	}
}

// TestTakeoverAfterLeaderLoss: the old leader opened an auction and vanished. Every client
// abandons it on its own once the grace period passes; the new game leader takes over,
// the raid backs them, a SYNC seeds their numbering past what the raid saw, and the
// orphan is abandoned for the record.
func TestTakeoverAfterLeaderLoss(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		-- Pretend Oldleader ran the raid: the context names them and they opened auction 7.
		FairShare.Raid.context.leader = "Oldleader"
		FairShare.Core.Comm.Inject("Oldleader", FairShare.Core.Comm.Encode("OPEN", { FairShare.Raid.context.raidID .. "-7", "loot", "|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", 40, GetServerTime() + 20 }))
		assert(FairShare.Auction.active and FairShare.Auction.active.phase == "bidding", "OPEN from the FairShare leader must be accepted")
		local ok, err = FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot")
		assert(not ok and err:find("take over", 1, true), "game leader must take over first: " .. tostring(err))
	`); err != nil {
		t.Fatal(err)
	}
	// Nobody closes it. Deadline (20 s) plus grace (60 s) and the client gives up.
	pump(t, h, 95)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.phase == "done" and a.abandoned, "client should abandon a leaderless auction: " .. tostring(a.phase))
		assert(a.result.winner == "")
		assert(FairShare.Raid.TakeOver())
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 4) // LEAD echoes back, virtual raiders ACK
	if err := h.Do(`
		local c = FairShare.Raid.context
		assert(c.leader == "Leader" and c.tookOver, "LEAD should move leadership")
		local have, need = FairShare.Raid.LeadVotes()
		assert(have >= need and FairShare.Raid.LeadConfirmed(), ("votes %d of %d"):format(have, need))
		-- A real client would now whisper SYNC: it saw auction 9 and still has 7-7 open.
		FairShare.Core.Comm.Inject("Simba", FairShare.Core.Comm.Encode("SYNC", { 9, c.raidID .. "-7", "reveal" }), "WHISPER")
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 3)
	if err := h.Do(`
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		local sawAbandon, sawLead, acks = false, false, 0
		for _, l in ipairs(lines) do
			if l:find("\tin\tLeader\tABANDON^" .. FairShare.Raid.context.raidID .. "-7^", 1, true) then sawAbandon = true end
			if l:find("\tin\tLeader\tLEAD^Leader", 1, true) then sawLead = true end
			if l:find("\tACK^Leader", 1, true) then acks = acks + 1 end
		end
		assert(sawLead, "LEAD not in transcript")
		assert(acks >= 3, "ACKs in transcript: " .. acks)
		assert(sawAbandon, "ABANDON for the orphan not sent")
		assert(FairShare.Auction.Counter() >= 9, "counter not seeded from SYNC: " .. FairShare.Auction.Counter())
		assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if got := mustEval(t, h, `FairShare.Auction.active.id`).String(); !strings.HasSuffix(got, "-10") {
		t.Fatalf("next auction should be numbered past the SYNC maximum, got %s", got)
	}
}

// TestRunningCapAcrossAuctions: what a player already owes tonight comes off what they
// may bid next, on every client, so one raider cannot win everything on credit they only
// have once. The leader has 40 + 100 in the sample snapshot.
func TestRunningCapAcrossAuctions(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.bidding = false
		assert(FairShare.Auction.Open("|cffa335ee|Hitem:17076::::::::60:::::::::|h[Bonereaver's Edge]|h|r", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`
		assert(FairShare.Auction.MaxBid() == 140, "fresh cap should be 140: " .. tostring(FairShare.Auction.MaxBid()))
		local ok, err = FairShare.Auction.Bid(141, false)
		assert(not ok, "141 must exceed the cap")
		assert(FairShare.Auction.Bid(140, false))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45) // the leader is the only bidder: wins at the two-hander floor, 20
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(a.phase == "done" and a.result.winner == "Leader" and a.result.price == 20, "expected a 20 sale to the leader")
		assert(FairShare.Auction.Book.Spent()["Leader"] == 20)
		assert(FairShare.Auction.MaxBid() == 120, "cap should drop to 120: " .. tostring(FairShare.Auction.MaxBid()))
		assert(FairShare.Auction.Open("|cffa335ee|Hitem:17076::::::::60:::::::::|h[Bonereaver's Edge]|h|r", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`
		local a = FairShare.Auction.active
		assert(FairShare.Auction.MaxBid(a) == 120, "cap in the new auction: " .. tostring(FairShare.Auction.MaxBid(a)))
		local ok, err = FairShare.Auction.Bid(121, false)
		assert(not ok and err:find("already owe", 1, true), tostring(err))
		assert(FairShare.Auction.Bid(120, false))
	`); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentAuctionsAgree: three auctions open at once with virtual raiders bidding in
// all of them. Results go out one at a time, each judged with the earlier sales charged,
// and every client (here: the leader's own replay of its echoes) agrees with the leader.
func TestConcurrentAuctionsAgree(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		local link = "|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r"
		for i = 1, 3 do assert(FairShare.Auction.Queue.Add(link, "batch")) end
		assert(FairShare.Auction.Queue.StartAll() == 3)
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if got := mustEval(t, h, `#FairShare.Auction.Running()`); got != lua.LNumber(3) {
		t.Fatalf("expected 3 open auctions, got %v", got)
	}
	// The leader bids in two of them; the second bid is capped by the first.
	if err := h.Do(`
		local running = FairShare.Auction.Running()
		assert(FairShare.Auction.Bid(100, false, running[1]))
		local ok, err = FairShare.Auction.Bid(41, false, running[2])
		assert(not ok and err:find("other open bids", 1, true), tostring(err))
		assert(FairShare.Auction.Bid(40, false, running[2]))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 60)
	if err := h.Do(`
		assert(#FairShare.Auction.Running() == 0, "all three should have finished")
		local Book = FairShare.Auction.Book
		local spent = Book.Spent()
		local n = 0
		for _, a in ipairs(FairShare.Auction.history) do
			assert(a.phase == "done", a.id .. " " .. a.phase)
			for _, f in ipairs(a.flags) do
				assert(not f:find("result_mismatch", 1, true) and not f:find("close_mismatch", 1, true), a.id .. ": " .. f)
			end
			n = n + 1
		end
		assert(n == 3, "history " .. n)
		-- Nobody owes more than their cap after the batch.
		for name, owed in pairs(spent) do
			local row = FairShare.Raid.context.rows[name]
			assert(owed <= row.balance + row.credit, name .. " owes " .. owed)
		end
	`); err != nil {
		t.Fatal(err)
	}
}

func TestTranscriptHistoryAndPruning(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		-- 35 old raids plus the current one; pruning keeps the newest 30 and never the current.
		for i = 1, 35 do
			FairShareDB.transcript["old" .. i] = { lines = { "1\tin\tLeader\tSNAP^old" .. i }, startedAt = 1000 + i, player = "Leader" }
		end
		FairShare.Raid.context = { raidID = "cur", checksum = string.rep("a", 64) }
		FairShareDB.transcript["cur"] = { lines = { "1\tin\tLeader\tSNAP^cur" }, startedAt = 1, player = "Leader" }
		local hist = FairShare.Transcript.History()
		assert(#hist == 36, #hist)
		assert(hist[1].raidID == "old35" and hist[36].raidID == "cur", hist[1].raidID)
		assert(FairShare.Transcript.Prune(30) == 5)
		assert(FairShareDB.transcript["cur"] ~= nil, "current raid must survive pruning")
		assert(FairShareDB.transcript["old1"] == nil and FairShareDB.transcript["old6"] ~= nil)
		assert(#FairShare.Transcript.History() == 31)
		-- Any past raid exports, not just the current one.
		local str = assert(FairShare.Transcript.Export("old20"))
		local text = FairShare.Lib.Base64.decode(str)
		assert(text:find("\nraid=old20\n", 1, true), text:sub(1, 60))
		assert(FairShare.Transcript.HistoryLabel(hist[1]):find("Raid old35", 1, true))
	`); err != nil {
		t.Fatal(err)
	}
	// The Settings panel builds with the history dropdown and selects the newest raid.
	if err := h.Do(`FairShare.UI.Show("Settings")`); err != nil {
		t.Fatal(err)
	}
}

// TestNoUnboundedText enforces the addon's no-clipping rule structurally: after every
// panel and popup has been built, no label holding more than a few words may lack a right
// bound (a width or a RIGHT-side anchor). In-game, Dev -> Audit text checks the rendered result.
func TestNoUnboundedText(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		for _, n in ipairs({ "Home", "Ledger", "Raid", "Roster", "Settings", "Dev" }) do FairShare.UI.Show(n) end
		FairShare.UI.Widgets.TextPopup("Transcript", string.rep("x", 5000))
		FairShare.UI.Widgets.Confirm("Already sold", "A fairly long question about whether to void the sale or sell a second copy of the item.", { { "Yes" }, { "No" } })
		assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`FairShare.UI.ShowBid(); FairShare.UI.ShowTranscript().ExpandAll(true)`); err != nil {
		t.Fatal(err)
	}
	report := mustEval(t, h, `table.concat(FairShare.UI.Widgets.AuditStructure(), "\n")`).String()
	if report != "" {
		t.Fatalf("labels without a right bound:\n%s", report)
	}
}

// The transcript window lists every recorded line and expands one into its named fields.
func TestTranscriptWindowShowsLines(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`assert(FairShare.Auction.Open("|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local win = FairShare.UI.ShowTranscript()
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		assert(#lines > 3, "expected a recorded auction")
		local collapsed = #win.list:Items()
		assert(collapsed > 0 and collapsed < #lines, "events are fewer than messages when collapsed")
		local function has(pattern)
			for _, it in ipairs(win.list:Items()) do
				if it.text and it.text:find(pattern) then return true end
			end
			return false
		end
		assert(has("^#1 .*: %S+ for %d+$"), "auction event titled with its result")
		assert(has("^Raid dev0 loaded by "), "snapshot event")
		win.list:Items()[1].toggle()
		assert(#win.list:Items() > collapsed, "expanding adds step rows")
		win.ExpandAll(false)
		assert(#win.list:Items() == collapsed, "collapse all")
		win.ExpandAll(true)
		assert(has(" revealed a bid of %d+"), "reveals read as sentences")
		assert(has(" won for %d+ shares$"), "result reads as a sentence")
		assert(not has("^REVEAL "), "raw hidden by default")
		win.SetRaw(true)
		assert(has("^REVEAL "), "raw messages shown when ticked")
	`); err != nil {
		t.Fatal(err)
	}
}
