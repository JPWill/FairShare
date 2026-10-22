package replay

import (
	"strings"
	"testing"
)

// The leader crashes mid-auction. Bob, promoted by the game, sends LEAD; the others ACK;
// Bob abandons the orphaned auction and runs the next one. Nothing is flagged, the
// orphan settles nothing, and Bob's sale settles normally.
func TestLeadTakeoverAndAbandon(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	// Alice commits to 7-1, the leader closes, then vanishes before any RESULT.
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "1020")
	c, r := commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm")
	r()
	// Bob takes over: roster from ROWS is leader + 4 = 5, so Bob plus two ACKs reach 3.
	b.in("Bob-Realm", "LEAD", "Bob-Realm")
	b.in("Bob-Realm", "OPEN", "7-9", "loot", "[y]", "20", "1100") // too early: not yet confirmed
	b.in("Alice-Realm", "ACK", "Bob-Realm")
	b.in("Cara-Realm", "ACK", "Bob-Realm")
	b.in("Bob-Realm", "ABANDON", "7-1", "leader lost it")
	b.in("Bob-Realm", "OPEN", "7-2", "loot", "[x]", "20", "1200")
	c2, r2 := commitAndReveal(b, "7-2", "Cara-Realm", 30, false, strings.Repeat("c", 32))
	c2()
	b.in("Bob-Realm", "CLOSE", "7-2", "Cara-Realm")
	r2()
	b.in("Bob-Realm", "RESULT", "7-2", "Cara-Realm", "20")
	rep := run(t, b)

	if !contains(rep.Flags, "leader_change:OPEN:Bob-Realm") {
		t.Fatalf("an OPEN before the quorum must be flagged: %v", rep.Flags)
	}
	// Remove that one expected flag and nothing else may be flagged.
	var rest []string
	for _, f := range rep.Flags {
		if f != "leader_change:OPEN:Bob-Realm" {
			rest = append(rest, f)
		}
	}
	rep.Flags = rest
	if len(rep.Disputing()) > 0 {
		t.Fatalf("takeover should not dispute the raid: %v", rep.AllFlags())
	}
	if rep.Leader != "Bob-Realm" || len(rep.LeaderChanges) != 1 {
		t.Fatalf("leader = %q changes = %v", rep.Leader, rep.LeaderChanges)
	}
	var orphan, sale *Auction
	for _, a := range rep.Auctions {
		switch a.ID {
		case "7-1":
			orphan = a
		case "7-2":
			sale = a
		}
	}
	if orphan == nil || !orphan.Abandoned || orphan.AbandonReason != "leader lost it" {
		t.Fatalf("orphan not abandoned: %+v", orphan)
	}
	if sale == nil || !sale.Sale || sale.Winner != "Cara-Realm" {
		t.Fatalf("Bob's sale did not resolve: %+v", sale)
	}
	d := Settle(rep, SettleConfig{PoolPct: 10})
	if Sum(d) != 0 {
		t.Fatal("not zero-sum")
	}
	for _, x := range d {
		if x.AuctionID == "7-1" {
			t.Fatalf("abandoned auction produced a ledger entry: %+v", x)
		}
	}
	if !strings.Contains(Summary(rep), "ABANDONED (leader lost it)") {
		t.Fatal("summary does not mention the abandon")
	}

	// A LEAD from someone the clients did not back stays a pretender: no ACKs, no leadership.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in("Evil-Realm", "LEAD", "Evil-Realm")
	b.in("Evil-Realm", "OPEN", "7-1", "loot", "[x]", "20", "1020")
	rep = run(t, b)
	if rep.Leader != leader || !contains(rep.Flags, "leader_change:OPEN:Evil-Realm") {
		t.Fatalf("pretender accepted: leader=%q flags=%v", rep.Leader, rep.Flags)
	}

	// ABANDON after a RESULT is refused and flagged.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "1020")
	c, r = commitAndReveal(b, "7-1", "Alice-Realm", 50, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm")
	r()
	b.in(leader, "RESULT", "7-1", "Alice-Realm", "20")
	b.in(leader, "ABANDON", "7-1", "oops")
	rep = run(t, b)
	if rep.Auctions[0].Abandoned || !contains(rep.Auctions[0].Flags, "abandon_after_result") {
		t.Fatalf("late abandon: %+v", rep.Auctions[0])
	}
}
