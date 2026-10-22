package replay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// hasFlag reports whether any raid- or auction-level flag has the prefix.
func hasFlag(rep *Report, prefix string) bool {
	for _, f := range rep.Flags {
		if strings.HasPrefix(f, prefix) {
			return true
		}
	}
	for _, a := range rep.Auctions {
		for _, f := range a.Flags {
			if strings.HasPrefix(f, prefix) {
				return true
			}
		}
	}
	return false
}

func wantDisputing(t *testing.T, rep *Report, prefix string) {
	t.Helper()
	for _, f := range rep.Disputing() {
		if strings.Contains(f, prefix) {
			return
		}
	}
	t.Fatalf("expected disputing flag %s; disputing=%v all=%v", prefix, rep.Disputing(), rep.AllFlags())
}

func wantInformational(t *testing.T, rep *Report, prefix string) {
	t.Helper()
	for _, f := range rep.Informational() {
		if strings.Contains(f, prefix) {
			return
		}
	}
	t.Fatalf("expected informational flag %s; informational=%v all=%v", prefix, rep.Informational(), rep.AllFlags())
}

// simpleSale runs one clean auction: Alice bids 50 alone and pays the floor 20.
func simpleSale(b *builder, id string) {
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "0")
	c, r := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", id, "Alice-Realm")
	r()
	b.in(leader, "RESULT", id, "Alice-Realm", "20")
}

// B: the transcript's leader must be a character of the person who opened the raid.
func TestLeaderMustBeRaidLeader(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	simpleSale(b, "7-1")
	// The right person: no leader flag.
	rep := run(t, b)
	if hasFlag(rep, "leader_not_raid_leader") {
		t.Fatalf("leader wrongly flagged: %v", rep.Flags)
	}
	// Someone else's character broadcast the snapshot: disputing.
	rep = runWith(t, b, Options{LeaderCharacters: map[string]bool{"Bob-Realm": true}})
	wantDisputing(t, rep, "leader_not_raid_leader:"+leader)
	if rep.Leader != leader {
		t.Fatalf("leader should still be recorded: %q", rep.Leader)
	}
	// No leader characters known at all: also disputing (nil set means nobody).
	rep = runWith(t, b, Options{})
	wantDisputing(t, rep, "leader_not_raid_leader")
}

// Coordinator item 1: the leader is the sender of the first SNAP carrying the issued
// checksum; any other SNAP is noted and ignored.
func TestLeaderFromFirstValidSnap(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	// Evil broadcasts a forged snapshot first, then the real leader the issued one.
	b.in("Evil-Realm", "SNAP", "7", strings.Repeat("0", 64), "1700000000", "50", "DEFAULT=10,INVTYPE_HEAD=20", "")
	b.in("Evil-Realm", "OPEN", "7-1", "loot", "[x]", "20", "0")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in("Bob-Realm", "SNAP", "7", cs, "1700000000", "50", "DEFAULT=10,INVTYPE_HEAD=20", "Evil-Realm")
	simpleSale(b, "7-2")
	rep := run(t, b)
	if rep.Leader != leader {
		t.Fatalf("leader %q, want %q", rep.Leader, leader)
	}
	wantInformational(t, rep, "unauthorized_snap:Evil-Realm")
	wantInformational(t, rep, "unauthorized_snap:Bob-Realm")
	if !contains(rep.Flags, "leader_change:OPEN:Evil-Realm") || len(rep.Auctions) != 1 {
		t.Fatalf("Evil's OPEN before the real SNAP must be dropped: %v / %d auctions", rep.Flags, len(rep.Auctions))
	}
	if hasFlag(rep, "snap_checksum_mismatch") {
		t.Fatalf("a forged SNAP is not the leader's mismatch: %v", rep.Flags)
	}

	// A LEAD quorum may hand leadership to a registered, unbanned character.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in("Bob-Realm", "LEAD", "Bob-Realm")
	b.in("Alice-Realm", "ACK", "Bob-Realm")
	b.in("Cara-Realm", "ACK", "Bob-Realm")
	b.in("Bob-Realm", "SNAP", "7", cs, "1700000000", "50", "DEFAULT=10,INVTYPE_HEAD=20", "Evil-Realm")
	rep = run(t, b)
	if rep.Leader != "Bob-Realm" || hasFlag(rep, "leader_not_raid_leader") || hasFlag(rep, "unauthorized_snap") {
		t.Fatalf("takeover by a registered raider: leader=%q flags=%v", rep.Leader, rep.Flags)
	}
	// ...but not to a banned one.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in("Evil-Realm", "LEAD", "Evil-Realm")
	b.in("Alice-Realm", "ACK", "Evil-Realm")
	b.in("Cara-Realm", "ACK", "Evil-Realm")
	rep = run(t, b)
	wantDisputing(t, rep, "leader_not_raid_leader:Evil-Realm")
}

func TestNoSnapAndNoSnapshotHeader(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	b.in(leader, "KILL", "1", "Boss", leader)
	rep := run(t, b)
	wantDisputing(t, rep, "no_snap")
	if !contains(rep.Flags, "leader_change:KILL:"+leader) {
		t.Fatalf("leader-only lines before a valid SNAP must be dropped: %v", rep.Flags)
	}

	b = newBuilder("7", "", "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm")
	rep = run(t, b)
	wantDisputing(t, rep, "no_snapshot_header")
	if contains(rep.Flags, "no_snap") {
		t.Fatal("SNAP was present")
	}

	// A header naming a different snapshot is a mismatch, not a missing header.
	b = newBuilder("7", strings.Repeat("f", 64), "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm")
	rep = run(t, b)
	wantDisputing(t, rep, "snapshot_mismatch")

	// SNAP fields that disagree with the issued snapshot.
	b = newBuilder("7", cs, "Alice-Realm")
	b.in(leader, "SNAP", "7", cs, "1700000000", "0", "DEFAULT=10", "")
	rep = run(t, b)
	for _, f := range []string{"snap_offspec_mismatch", "snap_floors_mismatch", "snap_banned_mismatch"} {
		wantDisputing(t, rep, f)
	}
}

// D: floors and auction ids.
func TestFloorAndAuctionIDRules(t *testing.T) {
	s, cs := testSnapshot()
	open := func(id, floor, slot string) *Report {
		b := newBuilder("7", cs, "Alice-Realm")
		preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
		b.in(leader, "OPEN", id, "loot", "[x]", floor, "0", slot)
		c, r := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
		c()
		b.in(leader, "CLOSE", id, "Alice-Realm")
		r()
		// Single bidder pays the floor (clamped at 0 when the floor is nonsense).
		var price int64
		if n, err := fmt.Sscanf(floor, "%d", &price); n != 1 || err != nil || price < 0 {
			price = 0
		}
		b.in(leader, "RESULT", id, "Alice-Realm", fmt.Sprint(price))
		return run(t, b)
	}
	// Slot key named and the floor matches the snapshot: clean.
	rep := open("7-1", "20", "INVTYPE_HEAD")
	disputing(t, rep)
	if rep.Auctions[0].SlotKey != "INVTYPE_HEAD" || rep.Auctions[0].Price != 20 {
		t.Fatalf("slot %q price %d", rep.Auctions[0].SlotKey, rep.Auctions[0].Price)
	}
	// Unknown slot falls back to DEFAULT (10).
	disputing(t, open("7-1", "10", "INVTYPE_TRINKET"))
	// Leader lowered the floor for a friend.
	wantDisputing(t, open("7-1", "5", "INVTYPE_HEAD"), "floor_mismatch")
	wantDisputing(t, open("7-1", "15", "INVTYPE_TRINKET"), "floor_mismatch")
	// No slot key (older client): the floor is not checked against the table.
	if rep := open("7-1", "5", ""); hasFlag(rep, "floor_mismatch") {
		t.Fatalf("no slot key, no floor check: %v", rep.AllFlags())
	}
	// Negative or unparsable floors.
	wantDisputing(t, open("7-1", "-5", ""), "bad_floor")
	wantDisputing(t, open("7-1", "ten", ""), "bad_floor")
	rep = open("7-1", "-50", "")
	if rep.Auctions[0].Price < 0 {
		t.Fatalf("price went negative: %d", rep.Auctions[0].Price)
	}
	// Auction ids must be <raidID>-<n>.
	for _, bad := range []string{"8-1", "7-x", "7-1-2", "71", "x7-1"} {
		wantDisputing(t, open(bad, "20", "INVTYPE_HEAD"), "bad_auction_id")
	}
}

// Coordinator item 2: the leader's client must honour the bid and reveal windows.
func TestTimingFlags(t *testing.T) {
	s, cs := testSnapshot()
	mk := func(deadlineOffset, closeAt, resultAt int64, bobReveals bool) *Report {
		b := newBuilder("7", cs, "Alice-Realm")
		preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
		b.raw = true
		openT := b.t + 1
		b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", fmt.Sprint(openT+deadlineOffset))
		ca, ra := commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
		cb, rb := commitAndReveal(b, "7-1", "Bob-Realm", 30, false, strings.Repeat("b", 32))
		ca()
		cb()
		b.at(openT + closeAt - 1)
		b.in(leader, "CLOSE", "7-1", "Alice-Realm,Bob-Realm")
		closeT := b.t
		ra()
		if bobReveals {
			rb()
		}
		b.at(closeT + resultAt - 1)
		if bobReveals {
			b.in(leader, "RESULT", "7-1", "Alice-Realm", "30")
		} else {
			b.in(leader, "RESULT", "7-1", "Alice-Realm", "20")
		}
		return run(t, b)
	}
	// Proper windows: 20 s to bid, close at the deadline, result 15 s later.
	disputing(t, mk(20, 20, 15, true))
	// Everyone revealed quickly: an early RESULT hurts nobody.
	disputing(t, mk(20, 20, 2, true))
	// Bob never revealed and the RESULT came 2 s after CLOSE: he had no chance.
	rep := mk(20, 20, 2, false)
	wantDisputing(t, rep, "early_result")
	wantInformational(t, rep, "noreveal:Bob-Realm")
	// 15 s to bid instead of 20.
	wantDisputing(t, mk(15, 20, 15, true), "short_deadline")
	// CLOSE 5 s before the deadline.
	wantDisputing(t, mk(20, 15, 15, true), "early_close")
	// Two seconds of skew are tolerated.
	disputing(t, mk(18, 20, 13, false))
}

// Coordinator item 3: bids and salts must be well-formed; test auctions carry the Hearthstone.
func TestFieldValidation(t *testing.T) {
	s, cs := testSnapshot()
	reveal := func(bid, salt string) *Report {
		b := newBuilder("7", cs, "Alice-Realm")
		preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
		b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
		b.in("Alice-Realm", "COMMIT", "7-1", sha("7-1|Alice-Realm|"+bid+"|0|"+salt))
		b.in(leader, "CLOSE", "7-1", "Alice-Realm")
		b.in("Alice-Realm", "REVEAL", "7-1", bid, "0", salt)
		b.in(leader, "RESULT", "7-1", "", "0")
		return run(t, b)
	}
	good := strings.Repeat("a", 32)
	for _, bid := range []string{"-5", "1234567890", "+5", "0x10", "5.0", ""} {
		rep := reveal(bid, good)
		wantInformational(t, rep, "invalid_reveal:Alice-Realm:bad_bid")
		if rep.Auctions[0].Sale {
			t.Fatalf("bid %q produced a sale", bid)
		}
	}
	for _, salt := range []string{strings.Repeat("A", 32), strings.Repeat("a", 31), strings.Repeat("a", 33), "", "zz" + strings.Repeat("a", 30)} {
		rep := reveal("50", salt)
		wantInformational(t, rep, "invalid_reveal:Alice-Realm:bad_salt")
		if rep.Auctions[0].Sale {
			t.Fatalf("salt %q produced a sale", salt)
		}
	}
	// Nine digits are fine (and over credit, which is informational too).
	if rep := reveal("123456789", good); !hasFlag(rep, "invalid_reveal:Alice-Realm:over_credit") {
		t.Fatalf("nine-digit bid: %v", rep.AllFlags())
	}

	// A test auction on a real item.
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "test", "|cff0070dd|Hitem:12345:0:0:0|h[Helm]|h|r", "1", "0")
	wantDisputing(t, run(t, b), "test_real_item")
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "test", "[test]", "1", "0")
	wantDisputing(t, run(t, b), "test_real_item")
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "test", hearthstone, "1", "0")
	if rep := run(t, b); hasFlag(rep, "test_real_item") {
		t.Fatalf("hearthstone flagged: %v", rep.AllFlags())
	}
}

// ownerSnapshot has one person (Pat) with two characters, plus Alice.
func ownerSnapshot() (snapshot.Snapshot, string) {
	s := snapshot.Snapshot{
		RaidID: "7", Issued: 1700000000, OffspecPct: 0,
		Floors: map[string]int64{"DEFAULT": 10},
		Banned: []string{"Evil-Realm"},
		Rows: []snapshot.Row{
			{Character: leader, Owner: "Lead", Balance: 0, Credit: 100},
			{Character: "Alice-Realm", Owner: "Alice", Balance: 0, Credit: 100},
			{Character: "Pat One", Owner: "Pat", Balance: 0, Credit: 100},
			{Character: "Pat Two", Owner: "Pat", Balance: 0, Credit: 100},
			{Character: "Mute Guy", Owner: "Mute", Balance: 0, Credit: 100},
			{Character: "Evil-Realm", Owner: "Evil", Balance: 0, Credit: 100},
		},
	}
	return s, snapshot.Checksum(s.Text())
}

func runOwner(t *testing.T, b *builder) *Report {
	t.Helper()
	s, cs := ownerSnapshot()
	tr, err := ParseTranscript(b.text())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(s, cs, tr, leaderOpts())
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// E: KILL rosters are intersected with the snapshot, deduplicated by encounter, and
// shares and caps are per owner.
func TestKillRosterAndPerOwnerRules(t *testing.T) {
	s, cs := ownerSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	b.in(leader, "SNAP", "7", cs, "1700000000", "0", "DEFAULT=10", "Evil-Realm")
	b.in(leader, "ROWS", rowField(s, leader), rowField(s, "Alice-Realm"), rowField(s, "Pat One"), rowField(s, "Evil-Realm"))
	b.in("Pat One", "PING", "x", "1")
	// Ghost has no row; Mute never sends anything; Evil is banned. The first KILL is
	// resent (rebroadcast) with a different roster: the last one counts.
	b.in(leader, "KILL", "100", "Boss", "Alice-Realm,Evil-Realm,Ghost,Lead-Realm,Pat One")
	b.in(leader, "KILL", "100", "Boss", "Alice-Realm,Evil-Realm,Lead-Realm,Pat One")
	// Pat swaps characters for the second boss; Mute Guy is there but never sends a line.
	b.in(leader, "KILL", "200", "Boss Two", "Alice-Realm,Lead-Realm,Mute Guy,Pat Two")
	// Pat One wins 7-1 for 100 (Pat owes 100); Pat Two then bids 50 with only 0 left.
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "10", "0")
	c1, r1 := commitAndReveal(b, "7-1", "Pat One", 100, false, strings.Repeat("a", 32))
	c1()
	b.in(leader, "CLOSE", "7-1", "Pat One")
	r1()
	b.in(leader, "RESULT", "7-1", "Pat One", "10")
	b.in(leader, "OPEN", "7-2", "loot", "[y]", "10", "0")
	c2, r2 := commitAndReveal(b, "7-2", "Pat Two", 100, false, strings.Repeat("b", 32))
	c3, r3 := commitAndReveal(b, "7-2", "Alice-Realm", 20, false, strings.Repeat("c", 32))
	c2()
	c3()
	b.in(leader, "CLOSE", "7-2", "Alice-Realm,Pat Two")
	r2()
	r3()
	b.in(leader, "RESULT", "7-2", "Alice-Realm", "10")
	rep := runOwner(t, b)
	disputing(t, rep)
	wantInformational(t, rep, "kill_unknown:Ghost")
	if hasFlag(rep, "kill_silent:Alice") || hasFlag(rep, "kill_silent:Pat One") {
		t.Fatalf("speakers flagged silent: %v", rep.Flags)
	}
	wantInformational(t, rep, "kill_silent:Mute Guy")
	if len(rep.Kills) != 2 || strings.Join(rep.Kills[0].Roster, ",") != "Alice-Realm,Lead-Realm,Pat One" {
		t.Fatalf("kills %+v", rep.Kills)
	}
	// Pat One paid 10 at the floor for 7-1 (single bidder), so Pat still has 90; the
	// second bid of 100 is over the owner's remaining cap and Alice wins at the floor.
	a2 := rep.Auctions[1]
	if a2.SpentAtResult["Pat"] != 10 || !contains(a2.Flags, "invalid_reveal:Pat Two:over_credit") || a2.Winner != "Alice-Realm" {
		t.Fatalf("per-owner cap: spent=%v winner=%s flags=%v", a2.SpentAtResult, a2.Winner, a2.Flags)
	}
	// Kill shares per owner: Pat attended both kills on two characters, a full run.
	at := KillShares(rep)
	var pat *Attendance
	for i := range at {
		if at[i].Owner == "Pat" {
			pat = &at[i]
		}
	}
	if pat == nil || pat.Kills != 2 || !pat.FullRun || strings.Join(pat.Characters, ",") != "Pat One,Pat Two" {
		t.Fatalf("Pat's attendance: %+v", pat)
	}
	for _, x := range at {
		if x.Owner == "Evil" || x.Owner == "Ghost" {
			t.Fatalf("banned or unknown name earned attendance: %+v", x)
		}
	}
	// Sale split for 7-1 (KILL 100 roster: Alice, Lead, Pat One): three owners, one share each.
	d := Settle(rep, SettleConfig{PoolPct: 10})
	if Sum(d) != 0 {
		t.Fatal("not zero-sum")
	}
	shares := map[string]int{}
	for _, x := range d {
		if x.Reason == "share" && x.AuctionID == "7-2" {
			shares[rep.Context.OwnerOf(x.Character)]++
		}
	}
	// 7-2 settles against KILL 200 (Alice, Lead, Mute Guy, Pat Two): one share per owner.
	if len(shares) != 4 || shares["Pat"] != 1 || shares["Mute"] != 1 || shares["Evil"] != 0 {
		t.Fatalf("shares per owner: %v", shares)
	}
}

// F: early reveals, abandons after reveals, non-reveals in abandoned auctions, and
// transfers judged against the computed result.
func TestRuleGaps(t *testing.T) {
	s, cs := testSnapshot()
	// REVEAL before CLOSE is invalid and noted.
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	ca, ra := commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	cb, rb := commitAndReveal(b, "7-1", "Bob-Realm", 30, false, strings.Repeat("b", 32))
	ca()
	cb()
	ra() // too early
	b.in(leader, "CLOSE", "7-1", "Alice-Realm,Bob-Realm")
	rb()
	b.in(leader, "RESULT", "7-1", "Bob-Realm", "20")
	rep := run(t, b)
	disputing(t, rep)
	wantInformational(t, rep, "early_reveal:Alice-Realm")
	wantInformational(t, rep, "invalid_reveal:Alice-Realm:early_reveal")
	if rep.Auctions[0].Winner != "Bob-Realm" {
		t.Fatalf("early reveal still won: %s", rep.Auctions[0].Winner)
	}

	// The opener abandons after a reveal: disputing. A successor doing it is fine
	// (covered by TestLeadTakeoverAndAbandon). With no reveal it is a plain abandon
	// and nobody is penalised for not revealing.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	ca, ra = commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	ca()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm")
	ra()
	b.in(leader, "ABANDON", "7-1", "changed my mind")
	rep = run(t, b)
	wantDisputing(t, rep, "abandon_after_reveal")

	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	ca, _ = commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	ca()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm")
	b.in(leader, "ABANDON", "7-1", "leader lost")
	rep = run(t, b)
	disputing(t, rep)
	if hasFlag(rep, "noreveal") {
		t.Fatalf("noreveal in an abandoned auction: %v", rep.AllFlags())
	}
	if d := Settle(rep, SettleConfig{PoolPct: 10, NorevealPenalty: 25}); len(d) != 0 {
		t.Fatalf("abandoned auction produced deltas: %v", d)
	}

	// The leader announces the wrong winner, then transfers from the real one: the
	// transfer is judged against the computed result, so only result_mismatch disputes.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	ca, ra = commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	cb, rb = commitAndReveal(b, "7-1", "Bob-Realm", 30, false, strings.Repeat("b", 32))
	ca()
	cb()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm,Bob-Realm")
	ra()
	rb()
	b.in(leader, "RESULT", "7-1", "Bob-Realm", "30")
	b.in(leader, "TRANSFER", "7-1", "Alice-Realm", "Cara-Realm")
	rep = run(t, b)
	wantDisputing(t, rep, "result_mismatch")
	if hasFlag(rep, "bad_transfer") || rep.Auctions[0].TransferredTo != "Cara-Realm" {
		t.Fatalf("transfer should be judged against the computed winner: %v to=%q", rep.AllFlags(), rep.Auctions[0].TransferredTo)
	}
	// And a transfer from the announced-but-wrong winner is not_winner.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	ca, ra = commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	cb, rb = commitAndReveal(b, "7-1", "Bob-Realm", 30, false, strings.Repeat("b", 32))
	ca()
	cb()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm,Bob-Realm")
	ra()
	rb()
	b.in(leader, "RESULT", "7-1", "Bob-Realm", "30")
	b.in(leader, "TRANSFER", "7-1", "Bob-Realm", "Cara-Realm")
	rep = run(t, b)
	if !contains(rep.Auctions[0].Flags, "bad_transfer:Cara-Realm:not_winner") {
		t.Fatalf("flags %v", rep.Auctions[0].Flags)
	}
}

// H: addon-key tags on COMMIT, REVEAL and ACK.
func TestAddonKeyTags(t *testing.T) {
	s, cs := testSnapshot()
	keyA, keyB := strings.Repeat("1", 32), strings.Repeat("2", 32)
	keys := map[string]string{"Alice-Realm": keyA, "Bob-Realm": keyB}
	salt := strings.Repeat("a", 32)
	hashA := CommitHash("7-1", "Alice-Realm", 50, false, salt)
	hashB := CommitHash("7-1", "Bob-Realm", 30, false, strings.Repeat("b", 32))

	// Tagged correctly: clean. Cara has no key: untagged (informational by default).
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	b.in("Alice-Realm", "COMMIT", "7-1", hashA, CommitTag(keyA, "7-1", hashA))
	b.in("Bob-Realm", "COMMIT", "7-1", hashB, CommitTag(keyB, "7-1", hashB))
	hashC := CommitHash("7-1", "Cara-Realm", 25, false, strings.Repeat("c", 32))
	b.in("Cara-Realm", "COMMIT", "7-1", hashC)
	b.in(leader, "CLOSE", "7-1", "Alice-Realm,Bob-Realm,Cara-Realm")
	b.in("Alice-Realm", "REVEAL", "7-1", "50", "0", salt, RevealTag(keyA, "7-1", "50", "0", salt))
	b.in("Bob-Realm", "REVEAL", "7-1", "30", "0", strings.Repeat("b", 32), RevealTag(keyB, "7-1", "30", "0", strings.Repeat("b", 32)))
	b.in("Cara-Realm", "REVEAL", "7-1", "25", "0", strings.Repeat("c", 32))
	b.in(leader, "RESULT", "7-1", "Alice-Realm", "30")
	rep := runWith(t, b, Options{LeaderCharacters: map[string]bool{leader: true}, Keys: keys})
	disputing(t, rep)
	wantInformational(t, rep, "untagged:Cara-Realm")
	if hasFlag(rep, "untagged:Alice") || hasFlag(rep, "untagged:Bob") || rep.Auctions[0].Winner != "Alice-Realm" || rep.Auctions[0].Price != 30 {
		t.Fatalf("tagged bids: %v winner=%s price=%d", rep.AllFlags(), rep.Auctions[0].Winner, rep.Auctions[0].Price)
	}
	// With keys required, Cara's untagged lines dispute the raid.
	rep = runWith(t, b, Options{LeaderCharacters: map[string]bool{leader: true}, Keys: keys, RequireAddonKey: true})
	wantDisputing(t, rep, "untagged:Cara-Realm")

	// A forged COMMIT for Bob (wrong tag) and a forged REVEAL for Alice (no tag) are
	// dropped: Bob never committed in our record (so his real reveal has no commit),
	// Alice never revealed.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "0")
	b.in("Alice-Realm", "COMMIT", "7-1", hashA, CommitTag(keyA, "7-1", hashA))
	b.in("Bob-Realm", "COMMIT", "7-1", hashB, CommitTag(keyA, "7-1", hashB)) // wrong key
	b.in(leader, "CLOSE", "7-1", "Alice-Realm,Bob-Realm")
	b.in("Alice-Realm", "REVEAL", "7-1", "50", "0", salt) // no tag
	b.in("Bob-Realm", "REVEAL", "7-1", "30", "0", strings.Repeat("b", 32), RevealTag(keyB, "7-1", "30", "0", strings.Repeat("b", 32)))
	b.in(leader, "RESULT", "7-1", "Alice-Realm", "30")
	rep = runWith(t, b, Options{LeaderCharacters: map[string]bool{leader: true}, Keys: keys})
	a := rep.Auctions[0]
	wantDisputing(t, rep, "bad_tag:Bob-Realm")
	wantDisputing(t, rep, "bad_tag:Alice-Realm")
	if len(a.Commits) != 1 || a.Commits[0].Name != "Alice-Realm" || len(a.Reveals) != 1 || a.Reveals[0].Name != "Bob-Realm" || a.Sale {
		t.Fatalf("forged lines not dropped: commits=%+v reveals=%+v sale=%v", a.Commits, a.Reveals, a.Sale)
	}
	if !contains(a.Flags, "invalid_reveal:Bob-Realm:no_commit") || !contains(a.Flags, "noreveal:Alice-Realm") {
		t.Fatalf("flags %v", a.Flags)
	}
	wantDisputing(t, rep, "close_mismatch")

	// ACK tags: the KILL roster has four unbanned members, so Cara plus one ACK is a
	// quorum. A forged ACK (wrong key) does not count toward it.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in("Cara-Realm", "LEAD", "Cara-Realm")
	b.in("Alice-Realm", "ACK", "Cara-Realm", AckTag(keyB, "Cara-Realm")) // wrong key: dropped
	rep = runWith(t, b, Options{LeaderCharacters: map[string]bool{leader: true}, Keys: keys})
	if rep.Leader != leader {
		t.Fatalf("quorum reached with a forged ACK: leader=%q", rep.Leader)
	}
	wantDisputing(t, rep, "bad_tag:Alice-Realm")
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in("Cara-Realm", "LEAD", "Cara-Realm")
	b.in("Alice-Realm", "ACK", "Cara-Realm", AckTag(keyA, "Cara-Realm"))
	rep = runWith(t, b, Options{LeaderCharacters: map[string]bool{leader: true}, Keys: keys})
	if rep.Leader != "Cara-Realm" {
		t.Fatalf("properly tagged ACKs should elect Cara: leader=%q flags=%v", rep.Leader, rep.AllFlags())
	}
	disputing(t, rep)

	// Tag formulas are fixed strings (the Lua side computes the same).
	if CommitTag("k", "7-1", "h") != sha("k|7-1|h") || RevealTag("k", "7-1", "5", "1", "s") != sha("k|7-1|5|1|s") || AckTag("k", "Bob") != sha("k|LEAD|Bob") {
		t.Fatal("tag formula changed")
	}
}

// C: the two flag classes.
func TestFlagClasses(t *testing.T) {
	info := []string{
		"noreveal:Bob", "late_commit:Bob", "late_reveal:Bob", "early_reveal:Bob", "kill_unknown:X", "kill_silent:X", "untagged:Bob", "unauthorized_snap:Evil",
		"invalid_reveal:Bob:over_credit", "invalid_reveal:Bob:below_floor", "invalid_reveal:Bob:unregistered", "invalid_reveal:Bob:banned",
		"invalid_reveal:Bob:early_reveal", "invalid_reveal:Bob:bad_bid", "invalid_reveal:Bob:bad_salt", "invalid_reveal:Bob:no_commit",
		"invalid_reveal:Thrall Doomhammer:over_credit",
	}
	for _, f := range info {
		if !IsInformational(f, false) {
			t.Errorf("%s should be informational", f)
		}
	}
	disp := []string{
		"close_mismatch", "result_mismatch", "leader_not_raid_leader:X", "leader_change:OPEN:X", "no_snap", "no_snapshot_header", "snapshot_mismatch",
		"snap_checksum_mismatch", "snap_offspec_mismatch", "snap_floors_mismatch", "row_mismatch:X", "duplicate_open:7-1", "duplicate_commit:X",
		"duplicate_close", "duplicate_reveal:X", "duplicate_result", "invalid_reveal:Bob:hash_mismatch", "floor_mismatch", "bad_floor", "bad_auction_id",
		"abandon_after_reveal", "short_deadline", "early_close", "early_result", "test_real_item", "bad_tag:X", "no_result", "result_before_close",
		"bad_transfer:X:over_max", "something_new",
	}
	for _, f := range disp {
		if IsInformational(f, false) {
			t.Errorf("%s should be disputing", f)
		}
	}
	if IsInformational("untagged:Bob", true) {
		t.Error("untagged must dispute when keys are required")
	}
}

// The addon opens its test auction at a minimum of 1 with the DEFAULT floor key (the
// snapshot's DEFAULT is higher). That must not dispute the raid: test auctions are never
// settled, so their floor is not checked. Found by the demo generator, which runs the
// addon's real test auction through settlement.
func TestTestAuctionFloorIsNotChecked(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", "7-1", "test", "|cffffffff|Hitem:6948::::::::1:::::::::|h[Hearthstone]|h|r", "1", "0", "DEFAULT")
	c, r := commitAndReveal(b, "7-1", "Alice-Realm", 1, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm")
	r()
	b.in(leader, "RESULT", "7-1", "Alice-Realm", "1")
	rep := run(t, b)
	if hasFlag(rep, "floor_mismatch") {
		t.Fatalf("test auction flagged: %v", rep.Disputing())
	}
	disputing(t, rep)
}
