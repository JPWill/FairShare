package replay

import (
	"strings"
	"testing"
)

// Two auctions open at once and Alice (50 + 100) bids 150 in both. Her first win charges
// her 100, so when the second result comes her 150 is past what she has left and the item
// goes to the other bidder, with no flag that would dispute the raid.
func TestConcurrentAuctionsCapAtResult(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in(leader, "OPEN", "7-1", "loot", "[x]", "20", "1020")
	b.in(leader, "OPEN", "7-2", "loot", "[y]", "20", "1020")
	c1, r1 := commitAndReveal(b, "7-1", "Alice-Realm", 150, false, strings.Repeat("a", 32))
	cc1, rc1 := commitAndReveal(b, "7-1", "Cara-Realm", 100, false, strings.Repeat("c", 32))
	c2, r2 := commitAndReveal(b, "7-2", "Alice-Realm", 150, false, strings.Repeat("b", 32))
	cb2, rb2 := commitAndReveal(b, "7-2", "Bob-Realm", 30, false, strings.Repeat("d", 32))
	c1()
	cc1()
	c2()
	cb2()
	b.in(leader, "CLOSE", "7-1", "Alice-Realm,Cara-Realm")
	b.in(leader, "CLOSE", "7-2", "Alice-Realm,Bob-Realm")
	r1()
	rc1()
	r2()
	rb2()
	// Results go out in order; the second is judged with the first already charged.
	b.in(leader, "RESULT", "7-1", "Alice-Realm", "100")
	b.in(leader, "RESULT", "7-2", "Bob-Realm", "20")
	rep := run(t, b)

	// Alice's second bid is set aside (informational); nothing disputes the raid.
	if d := rep.Disputing(); len(d) > 0 {
		t.Fatalf("unexpected disputing flags: %v", d)
	}
	for _, f := range rep.Informational() {
		if strings.Contains(f, "invalid_reveal") && !strings.Contains(f, "over_credit") {
			t.Fatalf("unexpected flag: %v", rep.AllFlags())
		}
	}
	a1, a2 := rep.Auctions[0], rep.Auctions[1]
	if a1.Winner != "Alice-Realm" || a1.Price != 100 {
		t.Fatalf("7-1: winner=%s price=%d", a1.Winner, a1.Price)
	}
	if a2.SpentAtResult["Alice"] != 100 {
		t.Fatalf("7-2 should see Alice's 100 already charged: %v", a2.SpentAtResult)
	}
	if a2.Winner != "Bob-Realm" || a2.Price != 20 || contains(a2.Flags, "result_mismatch") {
		t.Fatalf("7-2: winner=%s price=%d flags=%v", a2.Winner, a2.Price, a2.Flags)
	}
	if !contains(a2.Flags, "invalid_reveal:Alice-Realm:over_credit") {
		t.Fatalf("Alice's second 150 must be set aside: %v", a2.Flags)
	}
	d := Settle(rep, SettleConfig{PoolPct: 10})
	if Sum(d) != 0 {
		t.Fatal("not zero-sum")
	}
	paid := map[string]int64{}
	for _, x := range d {
		if x.Reason == "purchase" {
			paid[x.Character] += x.Amount
		}
	}
	if paid["Alice-Realm"] != -100 || paid["Bob-Realm"] != -20 {
		t.Fatalf("purchases: %v", paid)
	}
}
