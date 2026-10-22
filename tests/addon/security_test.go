package addontest

// Guards against a modified client in the raid: every message must arrive on the group
// channel, chatter cannot push auction traffic out of its window, the leader's floor is
// checked against the snapshot, bids are signed with the player's addon key, rule edge
// cases are recorded the way the backend records them, and the dev simulator cannot
// speak into a real raid.

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

const thunderfury = `|cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r`
const bonereaver = `|cffa335ee|Hitem:17076::::::::60:::::::::|h[Bonereaver's Edge]|h|r`

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestOffChannelMessagesAreDropped(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true}) // in a raid by default
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		Received = {}
		FairShare.Core.Comm.On("T", function(sender, fields, meta) Received[#Received + 1] = sender .. "@" .. meta.channel end)
	`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		channel, sender string
		want            bool
	}{
		{"RAID", "Other", true},
		{"PARTY", "Other", false},
		{"WHISPER", "Other", false},
		{"WHISPER", "Leader", true}, // our own echo
		{"SAY", "Other", false},
	}
	for _, c := range cases {
		if err := h.Deliver(Sent{Prefix: "FAIRSHARE", Msg: "=T^x", Channel: c.channel}, c.sender); err != nil {
			t.Fatal(err)
		}
	}
	got := mustEval(t, h, `table.concat(Received, ",")`).String()
	if got != "Other@RAID,Leader@WHISPER" {
		t.Fatalf("delivered = %q", got)
	}
	// SYNC may be whispered by anyone (it is the reply the leader asked for).
	if err := h.Do(`FairShare.Core.Comm.On("SYNC", function(sender) Received[#Received + 1] = "sync:" .. sender end)`); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(Sent{Prefix: "FAIRSHARE", Msg: "=SYNC^0^^", Channel: "WHISPER"}, "Other"); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `Received[#Received]`).String(); got != "sync:Other" {
		t.Fatalf("whispered SYNC should pass, last = %q", got)
	}
}

func TestPongIsRateLimitedPerSender(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Bob"})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	ping := Sent{Prefix: "FAIRSHARE", Msg: "=PING^123^test", Channel: "RAID"}
	for i := 0; i < 5; i++ {
		if err := h.Deliver(ping, "Alice"); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.AdvanceTime(0.05); err != nil {
		t.Fatal(err)
	}
	pongs := 0
	for _, s := range h.SentMessages() {
		if strings.HasPrefix(s.Msg, "=PONG^") {
			pongs++
		}
	}
	if pongs != 1 {
		t.Fatalf("five PINGs in a burst should get one PONG, got %d", pongs)
	}
	if err := h.AdvanceTime(11); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(ping, "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceTime(0.05); err != nil {
		t.Fatal(err)
	}
	pongs = 0
	for _, s := range h.SentMessages() {
		if strings.HasPrefix(s.Msg, "=PONG^") {
			pongs++
		}
	}
	if pongs != 2 {
		t.Fatalf("a PING after the window should be answered, got %d PONGs", pongs)
	}
}

func TestSendQueuePutsAuctionTrafficFirst(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Bob"})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	// Lockdown: nothing leaves the queue until ENCOUNTER_END.
	h.Set("SendResult", lua.LNumber(11))
	if err := h.Do(`
		local Comm = FairShare.Core.Comm
		Comm.Send("PING", { "1", "v" })
		Comm.Send("PONG", { "1", "v" })
		Comm.Send("COMMIT", { "r-1", "hash" })
		Comm.Send("PONG", { "2", "v" })
		Comm.Send("REVEAL", { "r-1", "5", "0", "salt" })
	`); err != nil {
		t.Fatal(err)
	}
	h.Set("SendResult", lua.LNumber(0))
	if err := h.FireEvent("ENCOUNTER_END", lua.LNumber(1), lua.LString("Boss"), lua.LNumber(1), lua.LNumber(1), lua.LNumber(1)); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, s := range h.SentMessages() {
		order = append(order, strings.SplitN(strings.TrimPrefix(s.Msg, "="), "^", 2)[0])
	}
	if got := strings.Join(order, ","); got != "COMMIT,REVEAL,PING,PONG,PONG" {
		t.Fatalf("send order = %s", got)
	}
}

func TestChunkHeadersAreBoundsChecked(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Bob"})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"!5:3:2^x", "!5:0:1^x", "!5:1:100^x", "!5:2:2^x", "!6:1:2^a"} {
		if err := h.Deliver(Sent{Prefix: "FAIRSHARE", Msg: msg, Channel: "RAID"}, "Other"); err != nil {
			t.Fatalf("%s: %v", msg, err)
		}
	}
	// A partial message expires instead of living forever.
	if err := h.AdvanceTime(31); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`Received = nil; FairShare.Core.Comm.On("Z", function(sender) Received = sender end)`); err != nil {
		t.Fatal(err)
	}
	if err := h.Deliver(Sent{Prefix: "FAIRSHARE", Msg: "!6:2:2^b", Channel: "RAID"}, "Other"); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `Received == nil`); got != lua.LTrue {
		t.Fatal("an expired first half must not complete a message")
	}
}

func TestOpenFloorMustMatchSnapshot(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.bidding = false
		local Comm, c = FairShare.Core.Comm, FairShare.Raid.context
		local function open(floor, key, kind, link)
			Comm.Inject("Leader", Comm.Encode("OPEN", { c.raidID .. "-50", kind or "loot", link or "` + bonereaver + `", floor, GetServerTime() + 20, key }))
		end
		open(1, "INVTYPE_2HWEAPON")
		assert(FairShare.Auction.open[c.raidID .. "-50"] == nil, "floor 1 for a 20 two-hander must be refused")
		open(-5, "DEFAULT")
		assert(FairShare.Auction.open[c.raidID .. "-50"] == nil, "negative floor must be refused")
		local lines = FairShare.Transcript.Lines(c.raidID)
		local noted = false
		for _, l in ipairs(lines) do if l:find("\tnote\t", 1, true) and l:find("floor_mismatch:" .. c.raidID .. "-50", 1, true) then noted = true end end
		assert(noted, "floor_mismatch note missing")
		open(20, "INVTYPE_2HWEAPON")
		assert(FairShare.Auction.open[c.raidID .. "-50"], "the snapshot's own floor opens")
		-- Our own OPEN carries the key it looked the floor up by.
		assert(FairShare.Auction.Open("` + bonereaver + `", "loot"))
		local last = SentMessages[#SentMessages].msg
		assert(last:find("^=OPEN%^") and last:find("%^20%^%d+%^INVTYPE_2HWEAPON$"), last)
		-- A test auction with a real item is flagged and nobody auto-bids it.
		local before = #SentMessages
		Comm.Inject("Leader", Comm.Encode("OPEN", { c.raidID .. "-51", "test", "` + thunderfury + `", 1, GetServerTime() + 20, "DEFAULT" }))
		local a = FairShare.Auction.open[c.raidID .. "-51"]
		assert(a and a.flags[1] == "test_real_item", "test_real_item flag missing")
		for i = before + 1, #SentMessages do assert(not SentMessages[i].msg:find("^=COMMIT"), "auto-bid on a real item in a test auction") end
		-- A deadline shorter than the protocol's window is flagged.
		Comm.Inject("Leader", Comm.Encode("OPEN", { c.raidID .. "-52", "loot", "` + thunderfury + `", 10, GetServerTime() + 5, "DEFAULT" }))
		local b = FairShare.Auction.open[c.raidID .. "-52"]
		assert(b and b.flags[1] == "short_deadline", "short_deadline flag missing")
	`); err != nil {
		t.Fatal(err)
	}
}

func TestAddonKeySignsCommitRevealAndAck(t *testing.T) {
	h := soloDevLeader(t)
	key := strings.Repeat("ab", 16)
	if err := h.Do(`
		FairShare.Dev.bidding = false
		FairShareDB.settings.addonKey = "` + key + `"
		assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`assert(FairShare.Auction.Bid(77, false))`); err != nil {
		t.Fatal(err)
	}
	sent := h.SentMessages()
	commit := sent[len(sent)-1].Msg
	parts := strings.Split(strings.TrimPrefix(commit, "="), "^")
	if len(parts) != 4 || parts[0] != "COMMIT" {
		t.Fatalf("COMMIT with a key should carry a tag: %s", commit)
	}
	if want := sha(key + "|" + parts[1] + "|" + parts[2]); parts[3] != want {
		t.Fatalf("commit tag = %s, want %s", parts[3], want)
	}
	pump(t, h, 23) // deadline, CLOSE, our REVEAL
	var reveal string
	for _, s := range h.SentMessages() {
		if strings.HasPrefix(s.Msg, "=REVEAL^") {
			reveal = s.Msg
		}
	}
	rp := strings.Split(strings.TrimPrefix(reveal, "="), "^")
	if len(rp) != 6 {
		t.Fatalf("REVEAL with a key should have 5 fields: %s", reveal)
	}
	if want := sha(key + "|" + rp[1] + "|" + rp[2] + "|" + rp[3] + "|" + rp[4]); rp[5] != want {
		t.Fatalf("reveal tag = %s, want %s", rp[5], want)
	}
	// Receivers ignore the tag: the reveal is still valid on this client.
	pump(t, h, 20)
	if got := mustEval(t, h, `FairShare.Auction.history[#FairShare.Auction.history].result.winner`).String(); got != "Leader" {
		t.Fatalf("tagged reveal should still win: %s", got)
	}
	// Without a key the old shapes go out.
	if err := h.Do(`
		FairShareDB.settings.addonKey = ""
		assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`assert(FairShare.Auction.Bid(40, false))`); err != nil {
		t.Fatal(err)
	}
	sent = h.SentMessages()
	if n := len(strings.Split(sent[len(sent)-1].Msg, "^")); n != 3 {
		t.Fatalf("COMMIT without a key should have 2 fields, got %d", n-1)
	}
	// ACK carries a tag too when the key is set.
	if err := h.Do(`
		FairShareDB.settings.addonKey = "` + key + `"
		FairShare.Raid.context.leader = "Oldleader"
		FairShare.Core.Comm.Inject("Leader", FairShare.Core.Comm.Encode("LEAD", { "Leader" }))
	`); err != nil {
		t.Fatal(err)
	}
	// Our own LEAD is for ourselves, so no ACK from us; simulate another client's view instead.
	if got := mustEval(t, h, `FairShare.Auction.State.AckTag("`+key+`", "Leader")`).String(); got != sha(key+"|LEAD|Leader") {
		t.Fatalf("ack tag = %s", got)
	}
}

func TestDuplicateCommitKeepsTheFirst(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.bidding = false
		assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`
		local Comm, a = FairShare.Core.Comm, FairShare.Auction.active
		Comm.Inject("Simone", Comm.Encode("COMMIT", { a.id, "h1" }))
		Comm.Inject("Simone", Comm.Encode("COMMIT", { a.id, "h2" }))
		assert(a.commits.Simone == "h1", "first commit must bind: " .. tostring(a.commits.Simone))
		assert(a.flags[#a.flags] == "duplicate_commit:Simone", table.concat(a.flags, ","))
	`); err != nil {
		t.Fatal(err)
	}
}

func TestRevealFieldsAreValidatedLikeTheBackend(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		FairShare.Dev.bidding = false
		assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`
		local Comm, State, a = FairShare.Core.Comm, FairShare.Auction.State, FairShare.Auction.active
		Salt = string.rep("0f", 16)
		Comm.Inject("Simone", Comm.Encode("COMMIT", { a.id, State.Hash(a.id, "Simone", 100, false, Salt) }))
		Comm.Inject("Simon", Comm.Encode("COMMIT", { a.id, State.Hash(a.id, "Simon", 100, false, Salt) }))
		Comm.Inject("Simba", Comm.Encode("COMMIT", { a.id, State.Hash(a.id, "Simba", 100, false, "zz") }))
		-- Revealing before CLOSE leaks the bid to the others: recorded as invalid, not ignored.
		Comm.Inject("Simone", Comm.Encode("REVEAL", { a.id, 100, 0, Salt }))
		assert(a.invalid.Simone and a.invalid.Simone.reason == "early_reveal", "early reveal not recorded")
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 21) // CLOSE
	if err := h.Do(`
		local Comm, a = FairShare.Core.Comm, FairShare.Auction.active
		assert(a.phase == "reveal", a.phase)
		Comm.Inject("Simon", Comm.Encode("REVEAL", { a.id, "1e2", 0, Salt }))
		assert(a.invalid.Simon and a.invalid.Simon.reason == "bad_bid", "1e2 must be a bad bid: " .. tostring(a.invalid.Simon and a.invalid.Simon.reason))
		Comm.Inject("Simba", Comm.Encode("REVEAL", { a.id, 100, 0, "zz" }))
		assert(a.invalid.Simba and a.invalid.Simba.reason == "bad_salt", "short salt must be rejected")
	`); err != nil {
		t.Fatal(err)
	}
}

func TestSyncRepliesAreGuarded(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		local Comm, A = FairShare.Core.Comm, FairShare.Auction
		local c = FairShare.Raid.context
		local function sync(from, n) Comm.Inject(from, Comm.Encode("SYNC", { n, "", "" }), "WHISPER") end
		local base = A.Counter()
		sync("Nobody", base + 3)
		assert(A.Counter() == base, "a stranger must not renumber the raid")
		sync("Simba", "1e9"); sync("Simba", "inf"); sync("Simba", "-1"); sync("Simba", "7.5")
		assert(A.Counter() == base, "only whole numbers count: " .. A.Counter())
		sync("Simba", base + 999)
		assert(A.Counter() == base, "a leap past 50 is ignored")
		sync("Simba", base + 3)
		assert(A.Counter() == base + 3, "a plausible number seeds the counter")
		assert(not A.SeedCounter(math.huge) and not A.SeedCounter(0/0), "inf and nan refused")
	`); err != nil {
		t.Fatal(err)
	}
	// Run one auction to the end, then a SYNC naming it must not make the leader abandon it.
	if err := h.Do(`FairShare.Dev.bidding = false; assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`assert(FairShare.Auction.Bid(50, false))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local Comm, A = FairShare.Core.Comm, FairShare.Auction
		local done = A.history[#A.history]
		assert(done.phase == "done" and done.result.winner == "Leader", "auction should have finished")
		assert(FairShare.Raid.Broadcast()) -- reopens the SYNC window
		Before = #SentMessages
		Comm.Inject("Simba", Comm.Encode("SYNC", { A.Counter(), done.id, "reveal" }), "WHISPER")
		for i = Before + 1, #SentMessages do assert(not SentMessages[i].msg:find("^=ABANDON"), "finished sale must never be abandoned") end
	`); err != nil {
		t.Fatal(err)
	}
	// Outside the window a SYNC is just noted.
	if err := h.AdvanceTime(61); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		local Comm, A = FairShare.Core.Comm, FairShare.Auction
		local base = A.Counter()
		Comm.Inject("Simba", Comm.Encode("SYNC", { base + 2, "", "" }), "WHISPER")
		assert(A.Counter() == base, "SYNC outside the window must be ignored")
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		assert(lines[#lines]:find("unauthorized:SYNC:Simba", 1, true), lines[#lines])
	`); err != nil {
		t.Fatal(err)
	}
}

func TestLeadRefusedWhileLeaderIsPresent(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		local Comm = FairShare.Core.Comm
		FairShare.Raid.context.leader = "Simba"
		Comm.Inject("Simba", Comm.Encode("PONG", { "1", "sim" })) -- Simba is here and talking
		assert(FairShare.Raid.TakeOver())
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if err := h.Do(`
		assert(FairShare.Raid.context.leader == "Simba", "a present leader must not be replaced: " .. FairShare.Raid.context.leader)
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		local noted = false
		for _, l in ipairs(lines) do if l:find("unauthorized:LEAD:Leader:leader_present", 1, true) then noted = true end end
		assert(noted, "refusal must be noted")
	`); err != nil {
		t.Fatal(err)
	}
	if err := h.AdvanceTime(91); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`assert(FairShare.Raid.TakeOver())`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 2)
	if got := mustEval(t, h, `FairShare.Raid.context.leader`).String(); got != "Leader" {
		t.Fatalf("a leader silent for 90 s can be replaced, got %s", got)
	}
}

func TestDeliveredIsTrustedOnlyFromTheRecipient(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`FairShare.Dev.bidding = false; assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`
		local Comm, State, a = FairShare.Core.Comm, FairShare.Auction.State, FairShare.Auction.active
		Salt = string.rep("0a", 16)
		Comm.Inject("Simba", Comm.Encode("COMMIT", { a.id, State.Hash(a.id, "Simba", 60, false, Salt) }))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 21)
	if err := h.Do(`
		local Comm, a = FairShare.Core.Comm, FairShare.Auction.active
		Comm.Inject("Simba", Comm.Encode("REVEAL", { a.id, 60, 0, Salt }))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 20)
	if err := h.Do(`
		local Comm, Tr, Book = FairShare.Core.Comm, FairShare.Auction.Trade, FairShare.Auction.Book
		local e = Book.Entries()[#Book.Entries()]
		assert(e.winner == "Simba", "Simba should win: " .. tostring(e.winner))
		assert(Tr.Status(e) == "owed")
		Comm.Inject("Simone", Comm.Encode("DELIVERED", { e.id, "Simba" }))
		assert(Tr.Status(e) == "owed", "a third party's DELIVERED must not mark it delivered")
		assert(Tr.Claim(e) == "Simone", "the claim is kept for the Ledger")
		Comm.Inject("Simba", Comm.Encode("DELIVERED", { e.id, "Simba" }))
		assert(Tr.Status(e) == "delivered", "the recipient's word is final")
		assert(Tr.Claim(e) == nil)
	`); err != nil {
		t.Fatal(err)
	}
}

func TestResultMismatchIsLoudAndBlocksTrading(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`FairShare.Dev.bidding = false; assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if err := h.Do(`
		local Comm, State, a = FairShare.Core.Comm, FairShare.Auction.State, FairShare.Auction.active
		Salt = string.rep("0b", 16)
		Comm.Inject("Simba", Comm.Encode("COMMIT", { a.id, State.Hash(a.id, "Simba", 60, false, Salt) }))
		Comm.Inject("Simon", Comm.Encode("COMMIT", { a.id, State.Hash(a.id, "Simon", 50, false, Salt) }))
	`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 21)
	if err := h.Do(`
		local Comm, a = FairShare.Core.Comm, FairShare.Auction.active
		Comm.Inject("Simba", Comm.Encode("REVEAL", { a.id, 60, 0, Salt }))
		-- Simon never reveals; the leader announces Simon as winner anyway, early.
		Comm.Inject("Leader", Comm.Encode("RESULT", { a.id, "Simon", 1 }))
		assert(a.phase == "done")
		local flags = table.concat(a.flags, ",")
		assert(flags:find("result_mismatch", 1, true), flags)
		assert(flags:find("early_result", 1, true), flags)
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		local noted = false
		for _, l in ipairs(lines) do if l:find("result_mismatch:" .. a.id, 1, true) then noted = true end end
		assert(noted, "mismatch must be noted in the transcript")
		local Tr, Book = FairShare.Auction.Trade, FairShare.Auction.Book
		local e = Book.Find(a.id)
		assert(Tr.Disputed(e), "entry should be disputed")
		assert(#Tr.Owed() == 0, "a disputed sale is not auto-traded")
	`); err != nil {
		t.Fatal(err)
	}
}

func TestForeignSnapNeverStartsATranscript(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`
		local Comm = FairShare.Core.Comm
		Comm.Inject("Simone", Comm.Encode("SNAP", { "evil1", string.rep("0", 64), "1", "0", "DEFAULT=10", "" }))
		assert(FairShareDB.transcript["evil1"] == nil, "a non-leader SNAP must not create a raid")
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		local noted = false
		for _, l in ipairs(lines) do if l:find("unauthorized_snap:Simone", 1, true) then noted = true end end
		assert(noted, "foreign SNAP should be noted in the current raid")
		-- Malformed leader messages are dropped, not crashed on.
		Comm.Inject("Leader", Comm.Encode("SNAP", { "", "x" }))
		Comm.Inject("Leader", Comm.Encode("OPEN", { "", "loot", "x", 1, GetServerTime() + 20 }))
		assert(FairShare.Raid.context ~= nil and FairShare.Raid.context.raidID ~= "")
	`); err != nil {
		t.Fatal(err)
	}
}

func TestReopeningAFinishedIdIsRefused(t *testing.T) {
	h := soloDevLeader(t)
	if err := h.Do(`FairShare.Dev.bidding = false; assert(FairShare.Auction.Open("` + thunderfury + `", "loot"))`); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 45)
	if err := h.Do(`
		local Comm, A = FairShare.Core.Comm, FairShare.Auction
		local done = A.history[#A.history]
		assert(done.phase == "done")
		Comm.Inject("Leader", Comm.Encode("OPEN", { done.id, "loot", "` + thunderfury + `", 10, GetServerTime() + 20, "DEFAULT" }))
		assert(A.open[done.id] == nil, "a finished id must not reopen")
		local lines = FairShare.Transcript.Lines(FairShare.Raid.context.raidID)
		assert(lines[#lines]:find("duplicate_open:" .. done.id, 1, true), lines[#lines])
	`); err != nil {
		t.Fatal(err)
	}
}

func TestSimulatorIsConfinedToDevRaids(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		FairShareDB.settings.dev = true
		FairShare.Dev.AddRaiders(3)
		Received = 0
		FairShare.Core.Events.On("COMM_RECEIVED", function() Received = Received + 1 end)
		assert(FairShare.Core.Comm.Inject("Simone", "PING^1^x") == false, "Inject must refuse outside a dev raid")
		assert(Received == 0, "nothing may enter the receive path")
		for _, n in ipairs(FairShare.Core.Group.Members()) do assert(n:sub(1, 3) ~= "Sim", "virtual raiders must not count as members: " .. n) end
	`); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsAddonKeyValidation(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		FairShare.UI.Show("Settings")
		local panel = FairShare.UI.PanelFrame("Settings")
		assert(panel and panel.SaveAddonKey, "settings panel exposes SaveAddonKey")
		local State = FairShare.Auction.State
		assert(State.ValidKey(string.rep("ab", 16)) and not State.ValidKey("abc") and not State.ValidKey(string.rep("zz", 16)))
		assert(State.MyKey() == nil, "no key by default")
	`); err != nil {
		t.Fatal(err)
	}
	// Drive the box through the panel: a bad key is refused, a good one saved lowercase.
	if err := h.Do(`
		local panel = FairShare.UI.PanelFrame("Settings")
		panel.AddonKeyBox:SetText("not a key")
		panel.SaveAddonKey()
		assert(FairShareDB.settings.addonKey == "", "a bad key must not be saved: " .. tostring(FairShareDB.settings.addonKey))
		panel.AddonKeyBox:SetText("  " .. string.rep("AB", 16) .. " ")
		panel.SaveAddonKey()
		assert(FairShareDB.settings.addonKey == string.rep("ab", 16), "a good key is trimmed and lowercased")
		assert(FairShare.Auction.State.MyKey() == string.rep("ab", 16))
		panel.AddonKeyBox:SetText("")
		panel.SaveAddonKey()
		assert(FairShareDB.settings.addonKey == "", "an empty box clears the key")
		FairShareDB.settings.addonKey = "not a key"
		assert(FairShare.Auction.State.MyKey() == nil, "garbage in SavedVariables is not a key")
	`); err != nil {
		t.Fatal(err)
	}
}
