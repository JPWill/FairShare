package replay

import (
	"fmt"
	"strings"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// builder assembles a transcript the way the addon would record it. The clock advances
// one second per line and, unless a test disables it with b.raw, honours the timing the
// leader's client must: an OPEN deadline 20 s out, CLOSE no earlier than the deadline,
// RESULT no earlier than 15 s after CLOSE.
type builder struct {
	t        int64
	lines    []string
	raid     string
	raw      bool // record the fields exactly as given; no timing help
	deadline map[string]int64
	closed   map[string]int64
}

func newBuilder(raid, checksum, player string) *builder {
	b := &builder{t: 1000, raid: raid, deadline: map[string]int64{}, closed: map[string]int64{}}
	b.lines = append(b.lines, "FAIRSHARE TRANSCRIPT 1", "raid="+raid, "snapshot="+checksum, "player="+player, "version=test")
	return b
}

// at moves the clock forward to t (never backwards).
func (b *builder) at(t int64) *builder {
	if t > b.t {
		b.t = t
	}
	return b
}

func (b *builder) in(sender, typ string, fields ...string) *builder {
	b.t++
	if !b.raw {
		switch typ {
		case "OPEN":
			if len(fields) >= 5 {
				fields = append([]string(nil), fields...)
				fields[4] = fmt.Sprint(b.t + 20)
				b.deadline[fields[0]] = b.t + 20
			}
		case "CLOSE":
			if len(fields) >= 1 {
				b.at(b.deadline[fields[0]])
				if _, done := b.closed[fields[0]]; !done {
					b.closed[fields[0]] = b.t
				}
			}
		case "RESULT":
			if len(fields) >= 1 {
				b.at(b.closed[fields[0]] + 15)
			}
		}
	}
	b.lines = append(b.lines, fmt.Sprintf("%d\tin\t%s\t%s", b.t, sender, Encode(typ, fields...)))
	return b
}

func (b *builder) text() string { return strings.Join(b.lines, "\n") }

const leader = "Lead-Realm"

// hearthstone is the only item a test auction may carry.
const hearthstone = "|cffffffff|Hitem:6948::::::::1::::::::|h[Hearthstone]|h|r"

// leaderOpts says Lead-Realm is a character of the person who opened the raid.
func leaderOpts() Options {
	return Options{LeaderCharacters: map[string]bool{leader: true}}
}

func testSnapshot() (snapshot.Snapshot, string) {
	s := snapshot.Snapshot{
		RaidID: "7", Issued: 1700000000, OffspecPct: 50,
		Floors: map[string]int64{"DEFAULT": 10, "INVTYPE_HEAD": 20},
		Banned: []string{"Evil-Realm"},
		Rows: []snapshot.Row{
			{Character: leader, Owner: "Lead", Balance: 0, Credit: 100},
			{Character: "Alice-Realm", Owner: "Alice", Balance: 50, Credit: 100},
			{Character: "Bob-Realm", Owner: "Bob", Balance: -20, Credit: 100},
			{Character: "Cara-Realm", Owner: "Cara", Balance: 0, Credit: 100},
			{Character: "Evil-Realm", Owner: "Evil", Balance: 0, Credit: 100},
		},
	}
	return s, snapshot.Checksum(s.Text())
}

func rowField(s snapshot.Snapshot, name string) string {
	for _, r := range s.Rows {
		if r.Character == name {
			return fmt.Sprintf("%s,%s,%d,%d", r.Character, r.Owner, r.Balance, r.Credit)
		}
	}
	return name + ",?,0,0"
}

// preamble: SNAP + ROWS for the given members + one KILL with the same roster.
func preamble(b *builder, s snapshot.Snapshot, cs string, members ...string) {
	b.in(leader, "SNAP", s.RaidID, cs, "1700000000", fmt.Sprint(s.OffspecPct), "DEFAULT=10,INVTYPE_HEAD=20", "Evil-Realm")
	rows := make([]string, len(members))
	for i, m := range members {
		rows[i] = rowField(s, m)
	}
	b.in(leader, "ROWS", rows...)
	b.in(leader, "KILL", "100", "Boss", strings.Join(members, ","))
}

func commitAndReveal(b *builder, id, name string, bid int64, offspec bool, salt string) (commit func(), reveal func()) {
	off := "0"
	if offspec {
		off = "1"
	}
	return func() { b.in(name, "COMMIT", id, CommitHash(id, name, bid, offspec, salt)) },
		func() { b.in(name, "REVEAL", id, fmt.Sprint(bid), off, salt) }
}

func run(t *testing.T, b *builder) *Report {
	t.Helper()
	return runWith(t, b, leaderOpts())
}

func runWith(t *testing.T, b *builder, opts Options) *Report {
	t.Helper()
	s, cs := testSnapshot()
	tr, err := ParseTranscript(b.text())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(s, cs, tr, opts)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// disputing fails the test when the report carries disputing flags.
func disputing(t *testing.T, rep *Report) {
	t.Helper()
	if d := rep.Disputing(); len(d) > 0 {
		t.Fatalf("unexpected disputing flags: %v (all: %v)", d, rep.AllFlags())
	}
}

func TestHappyPathSecondPrice(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "loot", "|cff0070dd|Hitem:12345:0:0:0|h[Helm]|h|r", "20", "1020")
	ca, ra := commitAndReveal(b, id, "Alice-Realm", 120, false, strings.Repeat("a", 32))
	cb, rb := commitAndReveal(b, id, "Bob-Realm", 70, false, strings.Repeat("b", 32))
	ca()
	cb()
	b.in(leader, "CLOSE", id, "Alice-Realm,Bob-Realm")
	ra()
	rb()
	b.in(leader, "RESULT", id, "Alice-Realm", "70")
	rep := run(t, b)
	if len(rep.Disputing()) > 0 {
		t.Fatalf("unexpected flags: %v", rep.AllFlags())
	}
	if rep.Leader != leader {
		t.Fatalf("leader %q", rep.Leader)
	}
	a := rep.Auctions[0]
	if a.Winner != "Alice-Realm" || a.Price != 70 || a.ItemID != 12345 {
		t.Fatalf("winner %s price %d item %d", a.Winner, a.Price, a.ItemID)
	}
	d := Settle(rep, SettleConfig{PoolPct: 10, NorevealPenalty: 25})
	if Sum(d) != 0 {
		t.Fatalf("not zero-sum: %d", Sum(d))
	}
	// Standby members share in the split too when they have a snapshot row; duplicates
	// of present members, unregistered names and banned names are ignored.
	ds := Settle(rep, SettleConfig{PoolPct: 10, NorevealPenalty: 25, Standby: []string{"Carol", "Bob-Realm", "Evil-Realm"}})
	shares := 0
	for _, x := range ds {
		if x.Reason == "share" {
			shares++
		}
	}
	if shares != 4 || Sum(ds) != 0 {
		t.Fatalf("with standby: %d shares (want 4), sum %d", shares, Sum(ds))
	}
	// P=70, pool=7, share=63/4=15, remainder=3 -> pool 10
	got := map[string]int64{}
	for _, x := range d {
		got[x.Character] += x.Amount
	}
	if got["Alice-Realm"] != -70+15 || got["Bob-Realm"] != 15 || got["pool"] != 10 {
		t.Fatalf("deltas %v", got)
	}
}

func TestSingleBidderPaysFloor(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	c, r := commitAndReveal(b, id, "Alice-Realm", 90, false, strings.Repeat("a", 32))
	c()
	b.in(leader, "CLOSE", id, "Alice-Realm")
	r()
	b.in(leader, "RESULT", id, "Alice-Realm", "20")
	rep := run(t, b)
	if len(rep.Disputing()) > 0 || rep.Auctions[0].Price != 20 {
		t.Fatalf("price %d flags %v", rep.Auctions[0].Price, rep.AllFlags())
	}
}

func TestOffspecFloorAndPriceClamp(t *testing.T) {
	s, cs := testSnapshot()
	// offspec 50%: floor 20 -> 10 for off-spec. Off-spec sole bidder pays 10.
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	c, r := commitAndReveal(b, id, "Bob-Realm", 12, true, strings.Repeat("b", 32))
	c()
	b.in(leader, "CLOSE", id, "Bob-Realm")
	r()
	b.in(leader, "RESULT", id, "Bob-Realm", "10")
	rep := run(t, b)
	if len(rep.Disputing()) > 0 || rep.Auctions[0].Price != 10 {
		t.Fatalf("offspec price %d flags %v", rep.Auctions[0].Price, rep.AllFlags())
	}

	// Main-spec winner over an off-spec second bid below the main floor: price clamps to 20.
	b = newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	id = "7-2"
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	ca, ra := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
	cb, rb := commitAndReveal(b, id, "Bob-Realm", 12, true, strings.Repeat("b", 32))
	ca()
	cb()
	b.in(leader, "CLOSE", id, "Alice-Realm,Bob-Realm")
	ra()
	rb()
	b.in(leader, "RESULT", id, "Alice-Realm", "20")
	rep = run(t, b)
	if len(rep.Disputing()) > 0 || rep.Auctions[0].Price != 20 {
		t.Fatalf("clamped price %d flags %v", rep.Auctions[0].Price, rep.AllFlags())
	}
	if ApplicableFloor(25, 50, true) != 13 {
		t.Fatal("ceil rounding")
	}
}

func TestInvalidReveals(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm", "Cara-Realm", "Evil-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	salt := strings.Repeat("c", 32)
	// Alice: hash mismatch (reveals a different bid than committed)
	b.in("Alice-Realm", "COMMIT", id, CommitHash(id, "Alice-Realm", 30, false, salt))
	// Bob: over credit (balance -20 + credit 100 = 80)
	cb, rb := commitAndReveal(b, id, "Bob-Realm", 81, false, salt)
	cb()
	// Cara: below floor
	cc, rc := commitAndReveal(b, id, "Cara-Realm", 5, false, salt)
	cc()
	// Evil: banned
	ce, re := commitAndReveal(b, id, "Evil-Realm", 40, false, salt)
	ce()
	// Ghost: never in ROWS (unregistered)
	cg, rg := commitAndReveal(b, id, "Ghost-Realm", 40, false, salt)
	cg()
	b.in(leader, "CLOSE", id, "Alice-Realm,Bob-Realm,Cara-Realm,Evil-Realm,Ghost-Realm")
	b.in("Alice-Realm", "REVEAL", id, "31", "0", salt)
	rb()
	rc()
	re()
	rg()
	b.in("Nobody-Realm", "REVEAL", id, "99", "0", salt) // no commit
	b.in(leader, "RESULT", id, "", "0")
	rep := run(t, b)
	a := rep.Auctions[0]
	if a.Sale {
		t.Fatalf("sale with no valid bids: %s %d", a.Winner, a.Price)
	}
	want := []string{
		"invalid_reveal:Alice-Realm:hash_mismatch",
		"invalid_reveal:Bob-Realm:over_credit",
		"invalid_reveal:Cara-Realm:below_floor",
		"invalid_reveal:Evil-Realm:banned",
		"invalid_reveal:Ghost-Realm:unregistered",
		"invalid_reveal:Nobody-Realm:no_commit",
	}
	for _, w := range want {
		if !contains(a.Flags, w) {
			t.Errorf("missing flag %s in %v", w, a.Flags)
		}
	}
	if contains(a.Flags, "result_mismatch") {
		t.Error("RESULT of no-sale should match")
	}
}

func TestNorevealPenaltyAndFlag(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	ca, ra := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
	cb, _ := commitAndReveal(b, id, "Bob-Realm", 40, false, strings.Repeat("b", 32))
	ca()
	cb()
	b.in(leader, "CLOSE", id, "Alice-Realm,Bob-Realm")
	ra()
	b.in(leader, "RESULT", id, "Alice-Realm", "20")
	rep := run(t, b)
	a := rep.Auctions[0]
	if !contains(a.Flags, "noreveal:Bob-Realm") {
		t.Fatalf("flags %v", a.Flags)
	}
	if a.Price != 20 || a.Winner != "Alice-Realm" {
		t.Fatalf("winner %s price %d", a.Winner, a.Price)
	}
	d := Settle(rep, SettleConfig{PoolPct: 10, NorevealPenalty: 25})
	if Sum(d) != 0 {
		t.Fatal("not zero-sum")
	}
	var bob int64
	for _, x := range d {
		if x.Character == "Bob-Realm" && x.Reason == "penalty" {
			bob += x.Amount
		}
	}
	if bob != -25 {
		t.Fatalf("penalty %d", bob)
	}
}

func TestTieBreakIsDeterministicAndSaltDependent(t *testing.T) {
	s, cs := testSnapshot()
	build := func(saltA, saltB string) *Report {
		b := newBuilder("7", cs, "Alice-Realm")
		preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
		id := "7-1"
		b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
		ca, ra := commitAndReveal(b, id, "Alice-Realm", 60, false, saltA)
		cb, rb := commitAndReveal(b, id, "Bob-Realm", 60, false, saltB)
		ca()
		cb()
		b.in(leader, "CLOSE", id, "Alice-Realm,Bob-Realm")
		ra()
		rb()
		return run(t, b)
	}
	r1 := build(strings.Repeat("1", 32), strings.Repeat("2", 32))
	r2 := build(strings.Repeat("1", 32), strings.Repeat("2", 32))
	if r1.Auctions[0].Winner != r2.Auctions[0].Winner {
		t.Fatal("tie-break not deterministic")
	}
	if r1.Auctions[0].Price != 60 {
		t.Fatalf("tie price %d", r1.Auctions[0].Price)
	}
	// Find a salt pair that flips the winner, proving the salts matter.
	flipped := false
	for i := 0; i < 50 && !flipped; i++ {
		r := build(fmt.Sprintf("%032d", i), fmt.Sprintf("%032d", i+1000))
		if r.Auctions[0].Winner != r1.Auctions[0].Winner {
			flipped = true
		}
	}
	if !flipped {
		t.Fatal("tie-break never depended on salts")
	}
}

func TestCloseAndResultMismatch(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
	ca, ra := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
	ca()
	b.in(leader, "CLOSE", id, "Alice-Realm,Bob-Realm") // Bob never committed here
	ra()
	cb, _ := commitAndReveal(b, id, "Bob-Realm", 40, false, strings.Repeat("b", 32))
	cb()                                          // late
	b.in(leader, "RESULT", id, "Bob-Realm", "40") // wrong
	rep := run(t, b)
	a := rep.Auctions[0]
	for _, w := range []string{"close_mismatch", "late_commit:Bob-Realm", "result_mismatch"} {
		if !contains(a.Flags, w) {
			t.Errorf("missing %s in %v", w, a.Flags)
		}
	}
}

func TestTestAuctionsNeverSettle(t *testing.T) {
	s, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
	id := "7-1"
	b.in(leader, "OPEN", id, "test", hearthstone, "1", "1020")
	ca, ra := commitAndReveal(b, id, "Alice-Realm", 1, false, strings.Repeat("a", 32))
	ca()
	b.in(leader, "CLOSE", id, "Alice-Realm")
	ra()
	b.in(leader, "RESULT", id, "Alice-Realm", "1")
	rep := run(t, b)
	if len(rep.Disputing()) > 0 {
		t.Fatalf("flags %v", rep.AllFlags())
	}
	if d := Settle(rep, SettleConfig{PoolPct: 10}); len(d) != 0 {
		t.Fatalf("test auction settled: %v", d)
	}
}

func TestCompareTranscripts(t *testing.T) {
	s, cs := testSnapshot()
	mk := func(price string) *Transcript {
		b := newBuilder("7", cs, "Alice-Realm")
		preamble(b, s, cs, leader, "Alice-Realm", "Bob-Realm")
		id := "7-1"
		b.in(leader, "OPEN", id, "loot", "[x]", "20", "1020")
		ca, ra := commitAndReveal(b, id, "Alice-Realm", 50, false, strings.Repeat("a", 32))
		ca()
		b.in(leader, "CLOSE", id, "Alice-Realm")
		ra()
		b.in(leader, "RESULT", id, "Alice-Realm", price)
		tr, err := ParseTranscript(b.text())
		if err != nil {
			t.Fatal(err)
		}
		return tr
	}
	if d := Compare(mk("20"), mk("20")); len(d) != 0 {
		t.Fatalf("identical transcripts differ: %v", d)
	}
	if d := Compare(mk("20"), mk("21")); len(d) == 0 {
		t.Fatal("differing transcripts compared equal")
	}
	// Timestamps and non-protocol lines are ignored.
	a := mk("20")
	a.Lines[0].T += 500
	a.Lines = append(a.Lines, Line{T: 9, Dir: "note", Sender: "x", Body: "hello"})
	if d := Compare(a, mk("20")); len(d) != 0 {
		t.Fatalf("time/note should be ignored: %v", d)
	}
}

func TestLeaderChangeAndRowMismatch(t *testing.T) {
	_, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	b.in(leader, "SNAP", "7", cs, "1700000000", "50", "DEFAULT=10", "")
	b.in(leader, "ROWS", "Alice-Realm,Alice,999,100") // doctored balance
	b.in("Alice-Realm", "OPEN", "7-1", "loot", "[x]", "10", "1020")
	rep := run(t, b)
	if !contains(rep.Flags, "row_mismatch:Alice-Realm") || !contains(rep.Flags, "leader_change:OPEN:Alice-Realm") {
		t.Fatalf("flags %v", rep.Flags)
	}
	if len(rep.Auctions) != 0 {
		t.Fatal("unauthorized OPEN created an auction")
	}
}

func TestParseBase64AndEscapes(t *testing.T) {
	_, cs := testSnapshot()
	b := newBuilder("7", cs, "Alice-Realm")
	b.in(leader, "KILL", "1", "Boss^With~Chars", "A-R,B-R")
	tr, err := ParseTranscript(snapshot.Paste(b.text()))
	if err != nil {
		t.Fatal(err)
	}
	m := Decode(tr.Lines[0].Body)
	if m.Type != "KILL" || m.Fields[1] != "Boss^With~Chars" {
		t.Fatalf("decoded %+v", m)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
