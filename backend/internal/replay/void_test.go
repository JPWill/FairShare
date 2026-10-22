package replay

import (
	"strings"
	"testing"
)

// A VOID after the RESULT strikes the sale from the books without flagging the raid;
// the item is then auctioned again under a new id and only that sale settles.
func TestVoid(t *testing.T) {
	s, cs := testSnapshot()
	id, id2 := "7-1", "7-2"
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	c, r := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", id, "Alice-Realm")
	r()
	b.in(leader, "RESULT", id, "Alice-Realm", "20")
	b.in(leader, "VOID", id, "re-run")
	b.in(leader, "OPEN", id2, "loot", "[x]", "20", "1040")
	c2, r2 := commitAndReveal(b, id2, "Bob-Realm", 30, false, strings.Repeat("b", 32))
	c2()
	b.in(leader, "CLOSE", id2, "Bob-Realm")
	r2()
	b.in(leader, "RESULT", id2, "Bob-Realm", "20")
	rep := run(t, b)

	if len(rep.Disputing()) > 0 {
		t.Fatalf("a void is not a dispute: %v", rep.AllFlags())
	}
	if !rep.Auctions[0].Voided || rep.Auctions[0].VoidReason != "re-run" || rep.Auctions[1].Voided {
		t.Fatalf("voided=%v/%v reason=%q", rep.Auctions[0].Voided, rep.Auctions[1].Voided, rep.Auctions[0].VoidReason)
	}
	d := Settle(rep, SettleConfig{PoolPct: 10})
	if Sum(d) != 0 {
		t.Fatal("not zero-sum")
	}
	paid := map[string]int64{}
	for _, x := range d {
		if x.AuctionID == id {
			t.Fatalf("voided auction produced a ledger entry: %+v", x)
		}
		if x.Reason == "purchase" {
			paid[x.Character] += x.Amount
		}
	}
	if paid["Alice-Realm"] != 0 || paid["Bob-Realm"] != -20 {
		t.Fatalf("only the re-run should settle: %v", paid)
	}
	if !strings.Contains(Summary(rep), "VOIDED (re-run)") {
		t.Fatal("summary does not mention the void")
	}

	// A VOID before any RESULT, a second VOID, and a VOID from a non-leader are flagged.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	b.in(leader, "VOID", id, "early")
	c, r = commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", id, "Alice-Realm")
	r()
	b.in(leader, "RESULT", id, "Alice-Realm", "20")
	b.in("Alice-Realm", "VOID", id, "not me")
	b.in(leader, "VOID", id, "once")
	b.in(leader, "VOID", id, "twice")
	rep = run(t, b)
	a := rep.Auctions[0]
	if !contains(a.Flags, "void_before_result") || !contains(a.Flags, "duplicate_void") || !contains(rep.Flags, "leader_change:VOID:Alice-Realm") {
		t.Fatalf("flags %v / %v", a.Flags, rep.Flags)
	}
	if !a.Voided || a.VoidReason != "once" {
		t.Fatalf("first valid void should stand: %v %q", a.Voided, a.VoidReason)
	}
}
