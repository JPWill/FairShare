package raids

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/JPWill/FairShare/backend/internal/bans"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/replay"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
	"github.com/JPWill/FairShare/backend/internal/store"
)

func newServices(t *testing.T) (*Service, *people.Service, *bans.Service) {
	t.Helper()
	s, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	p := &people.Service{S: s}
	b := &bans.Service{S: s, People: p}
	p.IsBannedIdentity = b.IsBannedIdentity
	r := &Service{S: s, People: p, Bans: b}
	return r, p, b
}

var old = time.Now().Add(-400 * 24 * time.Hour)

func register(t *testing.T, p *people.Service, id, name, char string) {
	t.Helper()
	if _, err := p.Register(id, name, old, char, "Warrior"); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterRules(t *testing.T) {
	_, p, b := newServices(t)
	if _, err := p.Register("1", "New", time.Now(), "Fresh", ""); err != people.ErrAccountTooNew {
		t.Fatalf("young account: %v", err)
	}
	register(t, p, "1", "Alice", "  alice   ")
	chars, _ := p.Characters("1")
	if len(chars) != 1 || chars[0].Name != "Alice" {
		t.Fatalf("normalized name: %+v", chars)
	}
	if _, err := p.Register("2", "Bob", old, "Alice", ""); err != people.ErrCharacterTaken {
		t.Fatalf("taken: %v", err)
	}
	if _, err := p.Register("2", "Bob", old, "alice-Realm", ""); err != people.ErrCharacterTaken {
		t.Fatalf("old Name-Realm habit must map to the bare name: %v", err)
	}
	for _, bad := range []string{"", "A", "Thrall Doom Hammer", "Thr4ll", "Thrall, Doomhammer"} {
		if _, err := p.Register("2", "Bob", old, bad, ""); err != people.ErrBadName {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
	if n, err := people.NormalizeCharacter("tHRALL   doomHAMMER"); err != nil || n != "Thrall Doomhammer" {
		t.Fatalf("two-word name: %q %v", n, err)
	}
	if _, err := p.Register("2", "Bob", old, "Bobby", "druid", "tank", "ranged", "healer", "melee", "bogus"); err != nil {
		t.Fatal(err)
	}
	if c, _ := p.Character("Bobby"); c.Class != "Druid" || c.Roles != "tank,healer,melee,ranged" {
		t.Fatalf("class/roles stored: %+v", c)
	}
	if _, err := p.Register("2", "Bob", old, "Magey", "Mage", "tank"); err != nil {
		t.Fatal(err)
	}
	if c, _ := p.Character("Magey"); c.Roles != "" {
		t.Fatalf("a mage cannot tank: %+v", c)
	}
	if err := p.LinkBattlenet("1", "Alice", "Alice#1234"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Issue("1", "sold loot", "officer"); err != nil {
		t.Fatal(err)
	}
	// New account tries to bring the banned character or battle tag back.
	if _, err := p.Register("3", "Sneaky", old, "Alice", ""); err != people.ErrBanned {
		t.Fatalf("banned character: %v", err)
	}
	if err := p.LinkBattlenet("3", "Sneaky", "alice#1234"); err != people.ErrBanned {
		t.Fatalf("banned battletag: %v", err)
	}
	bc, _ := b.BannedCharacters()
	if len(bc) != 1 || bc[0] != "Alice" {
		t.Fatalf("banned chars %v", bc)
	}
	bl, _ := b.Active()
	if err := b.Lift(bl[0].ID, "officer", "appeal"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Register("3", "Sneaky", old, "Alice", ""); err != people.ErrCharacterTaken {
		t.Fatalf("after lift the name is merely taken: %v", err)
	}
}

func TestCreditAndTokens(t *testing.T) {
	_, p, _ := newServices(t)
	register(t, p, "1", "Alice", "Alice")
	x, _ := p.Get("1")
	if p.Credit(x) != 100 {
		t.Fatalf("newcomer credit %d", p.Credit(x))
	}
	x.RaidsAttended = 10
	if p.Credit(x) != 500 {
		t.Fatalf("capped credit %d", p.Credit(x))
	}
	tok, err := p.IssueToken("1")
	if err != nil {
		t.Fatal(err)
	}
	if who, err := p.Authenticate(tok); err != nil || who.DiscordID != "1" {
		t.Fatalf("auth %v %v", who, err)
	}
	if _, err := p.Authenticate("nope"); err != people.ErrInvalidToken {
		t.Fatal("bad token accepted")
	}
}

// raidNight builds the transcript of a short raid from a stored raid's snapshot.
func raidNight(raid Raid, player string, resultPrice string) string {
	cs := raid.SnapshotChecksum
	snap, _ := snapshot.Parse(raid.SnapshotText)
	var lines []string
	tm := int64(1000)
	in := func(sender, typ string, fields ...string) {
		tm++
		lines = append(lines, fmt.Sprintf("%d\tin\t%s\t%s", tm, sender, replay.Encode(typ, fields...)))
	}
	lines = append(lines, "FAIRSHARE TRANSCRIPT 1", "raid="+fmt.Sprint(raid.ID), "snapshot="+cs, "player="+player, "version=t")
	in("Lead", "SNAP", fmt.Sprint(raid.ID), cs, fmt.Sprint(snap.Issued), "0", snap.FloorsCSV(), "")
	var rows []string
	for _, r := range snap.Rows {
		rows = append(rows, fmt.Sprintf("%s,%s,%d,%d", r.Character, r.Owner, r.Balance, r.Credit))
	}
	in("Lead", "ROWS", rows...)
	in("Lead", "KILL", "1", "First Boss", "Alice,Bob,Lead")
	id := fmt.Sprint(raid.ID) + "-1"
	// The leader's client gives 20 s to bid, closes at the deadline and announces the
	// result 15 s later (the replay's timing rules).
	in("Lead", "OPEN", id, "loot", "|Hitem:555:0|h[Thing]|h", "10", fmt.Sprint(tm+21), "DEFAULT")
	sa, sb := strings.Repeat("a", 32), strings.Repeat("b", 32)
	in("Alice", "COMMIT", id, replay.CommitHash(id, "Alice", 60, false, sa))
	in("Bob", "COMMIT", id, replay.CommitHash(id, "Bob", 30, false, sb))
	tm += 20
	in("Lead", "CLOSE", id, "Alice,Bob")
	in("Alice", "REVEAL", id, "60", "0", sa)
	in("Bob", "REVEAL", id, "30", "0", sb)
	tm += 15
	in("Lead", "RESULT", id, "Alice", resultPrice)
	in("Lead", "KILL", "2", "Second Boss", "Alice,Lead")
	return strings.Join(lines, "\n")
}

func TestOpenUploadSettle(t *testing.T) {
	r, p, _ := newServices(t)
	_ = r.S.SetConfig("requiredUploads", "2") // this test covers the two-witness path
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	var raiders []string
	r.OnFirstAttendance = func(ids []string) { raiders = append(raiders, ids...) }

	raid, err := r.Open(0, "Molten Core", "L")
	if err != nil {
		t.Fatal(err)
	}
	if raid.Status != "issued" || !strings.Contains(raid.SnapshotText, "row:Alice\tAlice\t0\t100") {
		t.Fatalf("raid %+v", raid)
	}
	if _, err := r.Open(0, "Again", "L"); err == nil {
		t.Fatal("second raid opened while first unsettled")
	}
	cur, err := r.CurrentForLeader("L")
	if err != nil || cur.ID != raid.ID {
		t.Fatalf("current %v %v", cur, err)
	}

	out, err := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "accepted" || out.Uploads != 1 {
		t.Fatalf("first upload: %+v", out.Status)
	}
	// Same person re-uploading does not count twice.
	out, _ = r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30"))
	if out.Status != "accepted" {
		t.Fatalf("re-upload: %s", out.Status)
	}
	out, err = r.Upload(raid.ID, "A", snapshot.Paste(raidNight(raid, "Alice", "30")))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "settled" {
		t.Fatalf("second upload: %s %s %v", out.Status, out.Message, out.Diffs)
	}
	if replay.Sum(out.Deltas) != 0 {
		t.Fatal("not zero sum")
	}
	// Price 30, 3 attendees: pool 3, share 9, remainder 0. Alice -30+9=-21, Bob +9, Lead +9, pool 3.
	a, _ := p.Get("A")
	b, _ := p.Get("B")
	l, _ := p.Get("L")
	if a.Balance != -21 || b.Balance != 9 || l.Balance != 9 {
		t.Fatalf("balances A=%d B=%d L=%d", a.Balance, b.Balance, l.Balance)
	}
	if a.RaidsAttended != 1 || a.LifetimeShares != 2+2 || b.LifetimeShares != 1 {
		t.Fatalf("shares A=%d (attended %d) B=%d", a.LifetimeShares, a.RaidsAttended, b.LifetimeShares)
	}
	if len(raiders) != 3 {
		t.Fatalf("first attendance callback got %v", raiders)
	}
	got, _ := r.Get(raid.ID)
	if got.Status != "settled" {
		t.Fatal(got.Status)
	}
	if out.Balances["Alice"] != -21 {
		t.Fatalf("balances table %v", out.Balances)
	}
	// Now the leader can open another raid, and their credit grew.
	if _, err := r.Open(0, "Next", "L"); err != nil {
		t.Fatal(err)
	}
	l, _ = p.Get("L")
	if p.Credit(l) != 200 {
		t.Fatalf("credit after one raid %d", p.Credit(l))
	}
}

func TestDisputeAndOverride(t *testing.T) {
	r, p, _ := newServices(t)
	_ = r.S.SetConfig("requiredUploads", "2") // this test covers the two-witness path
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	if _, err := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30")); err != nil {
		t.Fatal(err)
	}
	out, err := r.Upload(raid.ID, "A", raidNight(raid, "Alice", "31")) // doctored RESULT
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "disputed" || len(out.Diffs) == 0 {
		t.Fatalf("expected disputed, got %s %v", out.Status, out.Diffs)
	}
	got, _ := r.Get(raid.ID)
	if got.Status != "disputed" {
		t.Fatal(got.Status)
	}
	out, err = r.Override(raid.ID, "officer", "settle", "verified by hand")
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "settled" {
		t.Fatal(out.Status)
	}
	a, _ := p.Get("A")
	if a.Balance != -21 {
		t.Fatalf("override settled with wrong numbers: %d", a.Balance)
	}

	raid2, _ := r.Open(0, "MC", "L")
	if out, err := r.Override(raid2.ID, "officer", "void", "cancelled night"); err != nil || out.Status != "void" {
		t.Fatalf("void %v %v", out, err)
	}
}

func TestEventsAndSignups(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead") // Warrior
	if _, err := p.Register("A", "Alice", old, "Alice", "Druid", "healer"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Register("B", "Bob", old, "Bob", "Mage"); err != nil {
		t.Fatal(err)
	}
	ev, err := r.CreateEvent("Tuesday MC", "Molten Core", 1800000000, "L", 1, 1, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateEvent("x", "y", 1, "L", 1, 1, 3, 0); err == nil {
		t.Fatal("repeat must be 0, 7 or 14")
	}
	if _, err := r.Signup(ev.ID, "B", "Bob", "Bob", "tank", false); err == nil {
		t.Fatal("a mage signed up as tank")
	}
	if _, err := r.Signup(ev.ID, "B", "Bob", "Alice", "ranged", false); err == nil {
		t.Fatal("signed up with someone else's character")
	}
	if st, err := r.Signup(ev.ID, "A", "Alice", "Alice", "healer", false); st != "confirmed" || err != nil {
		t.Fatal(st, err)
	}
	if st, _ := r.Signup(ev.ID, "B", "Bob", "Bob", "ranged", false); st != "standby" {
		t.Fatalf("over cap should be standby: %s", st)
	}
	if _, err := r.Signup(ev.ID, "L", "Lead", "Lead", "tank", false); err == nil {
		t.Fatal("standby cap not enforced")
	}
	if _, err := r.Signup(ev.ID, "X", "Nobody", "Nobody", "melee", false); err != people.ErrNoCharacters {
		t.Fatalf("unregistered: %v", err)
	}
	ups, _ := r.Signups(ev.ID)
	if len(ups) != 2 || ups[0].Class != "Druid" || ups[0].Role != "healer" {
		t.Fatalf("signups %+v", ups)
	}

	// Officers confirm Bob instead of Alice; Alice becomes standby; nobody else can join.
	if err := r.ConfirmRoster(ev.ID, []string{"B"}, "officer"); err != nil {
		t.Fatal(err)
	}
	ev, _ = r.Event(ev.ID)
	if !ev.RosterConfirmed() {
		t.Fatal("roster not confirmed")
	}
	if _, err := r.Signup(ev.ID, "L", "Lead", "Lead", "tank", false); err != ErrRosterConfirmed {
		t.Fatalf("signup after confirm: %v", err)
	}
	raid, err := r.Open(ev.ID, "", "L")
	if err != nil {
		t.Fatal(err)
	}
	if raid.Instance != "Molten Core" || raid.EventID != ev.ID {
		t.Fatalf("raid %+v", raid)
	}
	if !strings.Contains(raid.SnapshotText, "\nroster:Alice\thealer\tstandby\nroster:Bob\tranged\tconfirmed\nrow:") {
		t.Fatalf("roster lines missing or misordered:\n%s", raid.SnapshotText)
	}
	var standby int
	if err := r.S.DB.QueryRow(`SELECT standby FROM attendance WHERE raid_id = ? AND person_id = 'A'`, raid.ID).Scan(&standby); err != nil || standby != 1 {
		t.Fatalf("standby attendance: %d %v", standby, err)
	}
	ev, _ = r.Event(ev.ID)
	if ev.Status != "started" || ev.RaidID != raid.ID {
		t.Fatalf("event %+v", ev)
	}
	if _, err := r.Signup(ev.ID, "A", "Alice", "Alice", "healer", false); err == nil {
		t.Fatal("signup after start")
	}
	// An ad-hoc raid without a confirmed roster carries no roster lines.
	raid2, _ := r.Open(0, "MC", "A")
	if strings.Contains(raid2.SnapshotText, "roster:") {
		t.Fatal("unexpected roster lines")
	}
}

func TestRepeatingEvents(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	ev, err := r.CreateEvent("Weekly MC", "Molten Core", 1800000000, "L", 20, 5, 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if due, _ := r.DueRepeats(1700000000); len(due) != 0 {
		t.Fatalf("not due yet: %+v", due)
	}
	due, _ := r.DueRepeats(1800000001)
	if len(due) != 1 || due[0].ID != ev.ID {
		t.Fatalf("due: %+v", due)
	}
	next, ok, err := r.NextOccurrence(ev)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if next.StartsAt != 1800000000+7*86400 || next.ParentEventID != ev.ID || next.RepeatDays != 7 || next.Title != ev.Title {
		t.Fatalf("next %+v", next)
	}
	if _, ok, _ := r.NextOccurrence(ev); ok {
		t.Fatal("spawned a duplicate")
	}
	if due, _ := r.DueRepeats(1800000001); len(due) != 0 {
		t.Fatalf("still due after spawning: %+v", due)
	}
	if _, ok, _ := r.NextOccurrence(raids0()); ok {
		t.Fatal("non-repeating event spawned")
	}
}

func raids0() Event { return Event{ID: 999, RepeatDays: 0} }

func TestEditAndRescheduleEvent(t *testing.T) {
	r, _, _ := newServices(t)
	ev, err := r.CreateEvent("Tuesday MC", "Molten Core", 1800000000, "L", 20, 5, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateEventDetails(ev.ID, "Wednesday MC", "Molten Core", 25, 3, "Bring fire resist.", "O"); err != nil {
		t.Fatal(err)
	}
	if err := r.RescheduleEvent(ev.ID, 1800003600, 7, "O"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Event(ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Wednesday MC" || got.Cap != 25 || got.StandbyCap != 3 || got.Notes != "Bring fire resist." || got.StartsAt != 1800003600 || got.RepeatDays != 7 {
		t.Fatalf("edit not applied: %+v", got)
	}
	// Notes carry over to the next occurrence of a repeating event.
	next, ok, err := r.NextOccurrence(got)
	if err != nil || !ok {
		t.Fatalf("next occurrence: ok=%v err=%v", ok, err)
	}
	if next.Notes != got.Notes || next.StartsAt != got.StartsAt+7*86400 {
		t.Fatalf("next occurrence lost edits: %+v", next)
	}
	// Cancelled events cannot be edited.
	if err := r.CancelEvent(ev.ID, "O"); err != nil {
		t.Fatal(err)
	}
	if err := r.UpdateEventDetails(ev.ID, "x", "y", 1, 1, "", "O"); err == nil {
		t.Fatal("expected refusal to edit a cancelled event")
	}
	if err := r.RescheduleEvent(ev.ID, 1, 0, "O"); err == nil {
		t.Fatal("expected refusal to reschedule a cancelled event")
	}
}

func TestOpenTestRaidSeedsVirtualRaiders(t *testing.T) {
	r, p, _ := newServices(t)
	if err := p.Ensure("L", "Leader"); err != nil {
		t.Fatal(err)
	}
	raid, err := r.OpenTestRaid("L")
	if err != nil {
		t.Fatal(err)
	}
	snap, err := snapshot.Parse(raid.SnapshotText)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, row := range snap.Rows {
		have[row.Character] = true
	}
	for _, name := range DevRaiderNames {
		if !have[name] {
			t.Fatalf("test snapshot lacks a row for %s", name)
		}
	}
	// A second test raid voids the unsettled first one instead of refusing.
	again, err := r.OpenTestRaid("L")
	if err != nil {
		t.Fatal(err)
	}
	first, _ := r.Get(raid.ID)
	if first.Status != "void" || again.ID == raid.ID {
		t.Fatalf("previous test raid should be voided: %s", first.Status)
	}
	if r.neededUploads() != 1 {
		t.Fatalf("the default is one uploader (the leader), got %d", r.neededUploads())
	}
}

func TestSingleUploadSettlesAndLaterUploadsAudit(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, err := r.Open(0, "Molten Core", "L")
	if err != nil {
		t.Fatal(err)
	}
	// Default: the leader's upload settles.
	out, err := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30"))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "settled" {
		t.Fatalf("leader upload: %s %s", out.Status, out.Message)
	}
	var before int64
	_ = r.S.DB.QueryRow(`SELECT balance FROM person WHERE discord_id = 'A'`).Scan(&before)
	// A matching late upload is a confirmation.
	out, err = r.Upload(raid.ID, "A", raidNight(raid, "Alice", "30"))
	if err != nil || out.Status != "settled" {
		t.Fatalf("matching late upload: %s %v", out.Status, err)
	}
	// A differing late upload contests the raid and leaves the ledger alone.
	out, err = r.Upload(raid.ID, "B", raidNight(raid, "Bob", "50"))
	if err != nil || out.Status != "contested" || len(out.Diffs) == 0 {
		t.Fatalf("differing late upload: %s %v %v", out.Status, err, out.Diffs)
	}
	var after int64
	_ = r.S.DB.QueryRow(`SELECT balance FROM person WHERE discord_id = 'A'`).Scan(&after)
	if after != before {
		t.Fatalf("contest must not move shares: %d -> %d", before, after)
	}
	// Settle override refuses; void reverses every line; then settle applies again.
	if _, err := r.Override(raid.ID, "O", "settle", "x"); err == nil {
		t.Fatal("settle on a settled raid should be refused")
	}
	if _, err := r.Override(raid.ID, "O", "void", "bad transcript"); err != nil {
		t.Fatal(err)
	}
	var sum int64
	_ = r.S.DB.QueryRow(`SELECT COALESCE(SUM(delta),0) FROM ledger WHERE raid_id = ?`, raid.ID).Scan(&sum)
	if sum != 0 {
		t.Fatalf("void should net the raid's ledger to zero, got %d", sum)
	}
	var restored int64
	_ = r.S.DB.QueryRow(`SELECT balance FROM person WHERE discord_id = 'A'`).Scan(&restored)
	if restored != 0 {
		t.Fatalf("Alice's balance should be back to 0, got %d", restored)
	}
	if _, err := r.Override(raid.ID, "O", "settle", "settle from Bob's"); err != nil {
		t.Fatalf("re-settle after void: %v", err)
	}
}

func TestRequiredUploadsTwoNeedsNonLeader(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	_ = r.S.SetConfig("requiredUploads", "2")
	raid, err := r.Open(0, "Molten Core", "L")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30"))
	if out.Status != "accepted" {
		t.Fatalf("leader alone should not settle: %s", out.Status)
	}
	out, _ = r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30"))
	if out.Status != "accepted" {
		t.Fatalf("leader twice should not settle: %s", out.Status)
	}
	out, _ = r.Upload(raid.ID, "A", raidNight(raid, "Alice", "30"))
	if out.Status != "settled" {
		t.Fatalf("leader + raider should settle: %s %s", out.Status, out.Message)
	}
}

// Raids carry their scheduled start, and count a 5-player party only while the officers'
// switch is on; sandbox test raids always allow one.
func TestSnapshotStartsAndParty(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	raid, err := r.Open(0, "Molten Core", "L")
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := snapshot.Parse(raid.SnapshotText)
	if snap.Starts == 0 || snap.Party {
		t.Fatalf("ad-hoc raid: starts=%d party=%v", snap.Starts, snap.Party)
	}
	if _, err := r.Override(raid.ID, "L", "void", "test"); err != nil {
		t.Fatal(err)
	}

	_ = r.S.SetConfig("allowParty", "true")
	raid, err = r.Open(0, "Deadmines", "L")
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ = snapshot.Parse(raid.SnapshotText); !snap.Party {
		t.Fatal("allowParty on, but the snapshot does not allow a party")
	}
	if _, err := r.Override(raid.ID, "L", "void", "test"); err != nil {
		t.Fatal(err)
	}

	_ = r.S.SetConfig("allowParty", "false")
	test, err := r.OpenTestRaid("L")
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ = snapshot.Parse(test.SnapshotText); !snap.Party {
		t.Fatal("sandbox test raid should always allow a party")
	}

	// An event raid starts when the event does, not when it is opened.
	start := store.Now() + 3600
	ev, err := r.CreateEvent("Thursday MC", "Molten Core", start, "L", 20, 5, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Override(test.ID, "L", "void", "test"); err != nil {
		t.Fatal(err)
	}
	raid, err = r.Open(ev.ID, "", "L")
	if err != nil {
		t.Fatal(err)
	}
	if snap, _ = snapshot.Parse(raid.SnapshotText); snap.Starts != start {
		t.Fatalf("event raid starts=%d, want %d", snap.Starts, start)
	}
}
