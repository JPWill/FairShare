package replay

import (
	"strings"
	"testing"
)

func TestTransfer(t *testing.T) {
	s, cs := testSnapshot()
	id := "7-1"
	// base: Alice wins for 20 (single bidder), then whatever extra adds.
	base := func(extra func(b *builder)) *Report {
		b := newBuilder("7", cs, "Alice-Realm")
		preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
		b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
		c, r := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
		c()
		b.in(leader, "CLOSE", id, "Alice-Realm")
		r()
		b.in(leader, "RESULT", id, "Alice-Realm", "20")
		extra(b)
		return run(t, b)
	}

	rep := base(func(b *builder) { b.in(leader, "TRANSFER", id, "Alice-Realm", "Bob-Realm") })
	a := rep.Auctions[0]
	if len(rep.Disputing()) > 0 || a.TransferredTo != "Bob-Realm" || a.Winner != "Alice-Realm" {
		t.Fatalf("valid transfer: to=%q winner=%q flags=%v", a.TransferredTo, a.Winner, rep.AllFlags())
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
	if paid["Bob-Realm"] != -20 || paid["Alice-Realm"] != 0 {
		t.Fatalf("purchase should move to Bob: %v", paid)
	}
	if !strings.Contains(Summary(rep), "transferred to Bob-Realm") {
		t.Fatal("summary does not mention the transfer")
	}

	cases := map[string]func(b *builder){
		"duplicate": func(b *builder) {
			b.in(leader, "TRANSFER", id, "Alice-Realm", "Bob-Realm")
			b.in(leader, "TRANSFER", id, "Alice-Realm", "Cara-Realm")
		},
		"not_winner":   func(b *builder) { b.in(leader, "TRANSFER", id, "Bob-Realm", "Cara-Realm") },
		"unregistered": func(b *builder) { b.in(leader, "TRANSFER", id, "Alice-Realm", "Ghost-Realm") },
		"banned":       func(b *builder) { b.in(leader, "TRANSFER", id, "Alice-Realm", "Evil-Realm") },
	}
	wantTo := map[string]string{"duplicate": "Cara-Realm", "not_winner": "Cara-Realm", "unregistered": "Ghost-Realm", "banned": "Evil-Realm"}
	for reason, extra := range cases {
		rep := base(extra)
		if !contains(rep.Auctions[0].Flags, "bad_transfer:"+wantTo[reason]+":"+reason) {
			t.Errorf("%s: flags %v", reason, rep.Auctions[0].Flags)
		}
	}
	// Transfer from a non-leader is ignored and flagged as a leader change.
	rep = base(func(b *builder) { b.in("Alice-Realm", "TRANSFER", id, "Alice-Realm", "Bob-Realm") })
	if rep.Auctions[0].TransferredTo != "" || !contains(rep.Flags, "leader_change:TRANSFER:Alice-Realm") {
		t.Errorf("non-leader transfer: to=%q flags=%v", rep.Auctions[0].TransferredTo, rep.Flags)
	}

	// over_max: Bob has balance -20 + credit 100 = 80; a 90-point sale is beyond him.
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm")
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	ca, ra := commitAndReveal(b, id, "Alice-Realm", 100, false, strings.Repeat("a", 32))
	cc, rc := commitAndReveal(b, id, "Cara-Realm", 90, false, strings.Repeat("c", 32))
	ca()
	cc()
	b.in(leader, "CLOSE", id, "Alice-Realm,Cara-Realm")
	ra()
	rc()
	b.in(leader, "RESULT", id, "Alice-Realm", "90")
	b.in(leader, "TRANSFER", id, "Alice-Realm", "Bob-Realm")
	rep = run(t, b)
	if !contains(rep.Auctions[0].Flags, "bad_transfer:Bob-Realm:over_max") {
		t.Errorf("over_max: flags %v", rep.Auctions[0].Flags)
	}

	// not_done: TRANSFER before RESULT.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	b.in(leader, "TRANSFER", id, "Alice-Realm", "Bob-Realm")
	rep = run(t, b)
	if !contains(rep.Auctions[0].Flags, "bad_transfer:Bob-Realm:not_done") {
		t.Errorf("not_done: flags %v", rep.Auctions[0].Flags)
	}

	// not_loot: a test auction cannot be transferred.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	b.in(leader, "OPEN", id, "test", hearthstone, "1", "1020")
	ct, rt := commitAndReveal(b, id, "Alice-Realm", 1, false, strings.Repeat("a", 32))
	ct()
	b.in(leader, "CLOSE", id, "Alice-Realm")
	rt()
	b.in(leader, "RESULT", id, "Alice-Realm", "1")
	b.in(leader, "TRANSFER", id, "Alice-Realm", "Bob-Realm")
	rep = run(t, b)
	if !contains(rep.Auctions[0].Flags, "bad_transfer:Bob-Realm:not_loot") {
		t.Errorf("not_loot: flags %v", rep.Auctions[0].Flags)
	}
}
