package replay

import (
	"strings"
	"testing"
)

// The running cap (§3.1): a bid is judged against balance + credit minus what the bidder
// already owes for earlier sales in the raid, so one player cannot win every auction on
// credit they only have once. Alice has 50 + 100 in testSnapshot.
func TestRunningCap(t *testing.T) {
	s, cs := testSnapshot()
	first := func(b *builder) {
		// 7-1: Alice bids 150 against Cara's 100 and pays the second price, 100: she now owes 100.
		b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "1020")
		c, r := commitAndReveal(b, "7-1", "Alice-Realm", 150, false, strings.Repeat("a", 32))
		cc, rc := commitAndReveal(b, "7-1", "Cara-Realm", 100, false, strings.Repeat("d", 32))
		c()
		cc()
		b.in(leader, "CLOSE", "7-1", "Alice-Realm,Cara-Realm")
		r()
		rc()
		b.in(leader, "RESULT", "7-1", "Alice-Realm", "100")
	}

	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	first(b)
	// 7-2: Alice bids 90. Her cap is 150 - 100 = 50, so the bid is invalid; Bob's 30 wins at the floor.
	b.in(leader, "OPEN", "7-2", "loot", "[y]", "20", "1100")
	c2, r2 := commitAndReveal(b, "7-2", "Alice-Realm", 90, false, strings.Repeat("b", 32))
	c3, r3 := commitAndReveal(b, "7-2", "Bob-Realm", 30, false, strings.Repeat("c", 32))
	c2()
	c3()
	b.in(leader, "CLOSE", "7-2", "Alice-Realm,Bob-Realm")
	r2()
	r3()
	b.in(leader, "RESULT", "7-2", "Bob-Realm", "20")
	rep := run(t, b)

	a1, a2 := rep.Auctions[0], rep.Auctions[1]
	if a1.SpentAtResult["Alice"] != 0 || a2.SpentAtResult["Alice"] != 100 {
		t.Fatalf("spent at open: %v / %v", a1.SpentAtResult, a2.SpentAtResult)
	}
	if !contains(a2.Flags, "invalid_reveal:Alice-Realm:over_credit") {
		t.Fatalf("Alice's 90 should exceed her remaining 50: %v", a2.Flags)
	}
	if a2.Winner != "Bob-Realm" || a2.Price != 20 || contains(a2.Flags, "result_mismatch") {
		t.Fatalf("7-2 should go to Bob at the floor: winner=%s price=%d flags=%v", a2.Winner, a2.Price, a2.Flags)
	}

	// A 50 bid is within what she has left.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	first(b)
	b.in(leader, "OPEN", "7-2", "loot", "[y]", "20", "1100")
	c2, r2 = commitAndReveal(b, "7-2", "Alice-Realm", 50, false, strings.Repeat("b", 32))
	c2()
	b.in(leader, "CLOSE", "7-2", "Alice-Realm")
	r2()
	b.in(leader, "RESULT", "7-2", "Alice-Realm", "20")
	rep = run(t, b)
	if len(rep.Disputing()) > 0 {
		t.Fatalf("50 is within the remaining cap: %v", rep.AllFlags())
	}
}
