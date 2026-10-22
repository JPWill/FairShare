package raids

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/replay"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// nightOpts shapes the transcript buildNight writes.
type nightOpts struct {
	price      string            // the RESULT price the leader announces
	keys       map[string]string // character -> addon key; tagged lines for these
	bobSilent  bool              // Bob commits but never reveals
	leaderChar string            // who broadcasts (default Lead)
}

// buildNight is raidNight with knobs: one kill, one auction (Alice 60 vs Bob 30), one
// more kill. Timing follows the leader's client (20 s to bid, 15 s to reveal).
func buildNight(raid Raid, player string, o nightOpts) string {
	cs := raid.SnapshotChecksum
	snap, _ := snapshot.Parse(raid.SnapshotText)
	lead := o.leaderChar
	if lead == "" {
		lead = "Lead"
	}
	var lines []string
	tm := int64(1000)
	in := func(sender, typ string, fields ...string) {
		tm++
		lines = append(lines, fmt.Sprintf("%d\tin\t%s\t%s", tm, sender, replay.Encode(typ, fields...)))
	}
	tag := func(name string, f func(key string) string) []string {
		if k := o.keys[name]; k != "" {
			return []string{f(k)}
		}
		return nil
	}
	lines = append(lines, "FAIRSHARE TRANSCRIPT 1", "raid="+fmt.Sprint(raid.ID), "snapshot="+cs, "player="+player, "version=t")
	in(lead, "SNAP", fmt.Sprint(raid.ID), cs, fmt.Sprint(snap.Issued), "0", snap.FloorsCSV(), "")
	var rows []string
	for _, r := range snap.Rows {
		rows = append(rows, fmt.Sprintf("%s,%s,%d,%d", r.Character, r.Owner, r.Balance, r.Credit))
	}
	in(lead, "ROWS", rows...)
	in(lead, "KILL", "1", "First Boss", "Alice,Bob,"+lead)
	id := fmt.Sprint(raid.ID) + "-1"
	in(lead, "OPEN", id, "loot", "|Hitem:555:0|h[Thing]|h", "10", fmt.Sprint(tm+21), "DEFAULT")
	sa, sb := strings.Repeat("a", 32), strings.Repeat("b", 32)
	ha, hb := replay.CommitHash(id, "Alice", 60, false, sa), replay.CommitHash(id, "Bob", 30, false, sb)
	in("Alice", "COMMIT", append([]string{id, ha}, tag("Alice", func(k string) string { return replay.CommitTag(k, id, ha) })...)...)
	in("Bob", "COMMIT", append([]string{id, hb}, tag("Bob", func(k string) string { return replay.CommitTag(k, id, hb) })...)...)
	tm += 20
	in(lead, "CLOSE", id, "Alice,Bob")
	in("Alice", "REVEAL", append([]string{id, "60", "0", sa}, tag("Alice", func(k string) string { return replay.RevealTag(k, id, "60", "0", sa) })...)...)
	if !o.bobSilent {
		in("Bob", "REVEAL", append([]string{id, "30", "0", sb}, tag("Bob", func(k string) string { return replay.RevealTag(k, id, "30", "0", sb) })...)...)
	}
	tm += 15
	in(lead, "RESULT", id, "Alice", o.price)
	in(lead, "KILL", "2", "Second Boss", "Alice,"+lead)
	return strings.Join(lines, "\n")
}

func count(t *testing.T, r *Service, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := r.S.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// A: who may upload.
func TestUploadAuthorization(t *testing.T) {
	r, p, b := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, err := r.Open(0, "MC", "L")
	if err != nil {
		t.Fatal(err)
	}
	// Registered after the raid opened: no snapshot row, so no say in it.
	register(t, p, "S", "Stranger", "Stranger")
	text := raidNight(raid, "Lead", "30")
	if _, err := r.Upload(raid.ID, "S", text); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("stranger upload: %v", err)
	}
	if _, err := r.Upload(raid.ID, "nobody", text); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("unregistered upload: %v", err)
	}
	if count(t, r, `SELECT COUNT(*) FROM transcript_upload WHERE raid_id = ?`, raid.ID) != 0 {
		t.Fatal("refused uploads must not be stored")
	}
	// An attendee (owner of a snapshot row) is a witness.
	if out, err := r.Upload(raid.ID, "B", text); err != nil || out.Status != "accepted" {
		t.Fatalf("attendee upload: %v %+v", err, out.Status)
	}
	// Banned people are refused everywhere, the leader included.
	if _, err := b.Issue("B", "sold loot", "officer"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upload(raid.ID, "B", text); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("banned upload: %v", err)
	}
	if _, err := b.Issue("L", "fraud", "officer"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Upload(raid.ID, "L", text); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("banned leader upload: %v", err)
	}
	if _, err := r.Progress(raid.ID, "L", text); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("banned leader progress: %v", err)
	}
	if _, err := r.Finish(raid.ID, "L"); !errors.Is(err, ErrNotAllowed) {
		t.Fatalf("banned leader finish: %v", err)
	}
	// Non-leaders still get the leader errors first.
	if _, err := r.Progress(raid.ID, "A", text); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("non-leader progress: %v", err)
	}
	if _, err := r.Finish(raid.ID, "A"); !errors.Is(err, ErrFinishNotLeader) {
		t.Fatalf("non-leader finish: %v", err)
	}
}

// A: with one upload required, only the leader's settles.
func TestLeaderUploadRequiredToSettle(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	out, err := r.Upload(raid.ID, "A", raidNight(raid, "Alice", "30"))
	if err != nil || out.Status != "accepted" || !strings.Contains(out.Message, "leader") {
		t.Fatalf("witness first: %v %s %q", err, out.Status, out.Message)
	}
	out, _ = r.Upload(raid.ID, "B", raidNight(raid, "Bob", "30"))
	if out.Status != "accepted" || out.Uploads != 2 {
		t.Fatalf("two witnesses, no leader: %s (%d)", out.Status, out.Uploads)
	}
	if got, _ := r.Get(raid.ID); got.Status != "in_progress" {
		t.Fatalf("status %s", got.Status)
	}
	out, err = r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30"))
	if err != nil || out.Status != "settled" {
		t.Fatalf("leader upload: %v %s %s", err, out.Status, out.Message)
	}
	got, _ := r.Get(raid.ID)
	var leaderUpload int64
	_ = r.S.DB.QueryRow(`SELECT id FROM transcript_upload WHERE raid_id = ? AND person_id = 'L'`, raid.ID).Scan(&leaderUpload)
	if got.SettledUploadID != leaderUpload || got.SettleGeneration != 1 {
		t.Fatalf("settled from upload %d (leader's is %d), generation %d", got.SettledUploadID, leaderUpload, got.SettleGeneration)
	}
	// A disputing flag in the leader's transcript blocks the settle even when witnesses agree.
	raid2, _ := r.Open(0, "MC", "L")
	bad := strings.Replace(raidNight(raid2, "Lead", "30"), "\tin\tLead\tCLOSE^", "\tin\tLead\tCLOSE^"+fmt.Sprint(raid2.ID)+"-1^Alice^", 1)
	bad = strings.Replace(bad, "^Alice^"+fmt.Sprint(raid2.ID)+"-1^Alice,Bob", "^Alice", 1)
	out, err = r.Upload(raid2.ID, "L", bad)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "disputed" || !strings.Contains(out.Message, "close_mismatch") {
		t.Fatalf("doctored CLOSE list: %s %s", out.Status, out.Message)
	}
}

// C: informational flags are noted but settle.
func TestInformationalFlagsSettle(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	// Bob never reveals: noreveal (informational), untagged for everyone (no keys yet).
	out, err := r.Upload(raid.ID, "L", buildNight(raid, "Lead", nightOpts{price: "10", bobSilent: true}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "settled" || !strings.Contains(out.Message, "Notes:") || !strings.Contains(out.Message, "noreveal:Bob") {
		t.Fatalf("%s: %s", out.Status, out.Message)
	}
	var flags string
	_ = r.S.DB.QueryRow(`SELECT flags_json FROM auction WHERE raid_id = ?`, raid.ID).Scan(&flags)
	if !strings.Contains(flags, "noreveal:Bob") {
		t.Fatalf("informational flags must be recorded: %s", flags)
	}
}

// F: the first non-reveal per person per raid consumes a free pass.
func TestFreePassThenPenalty(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	night := func() (Raid, Outcome) {
		raid, err := r.Open(0, "MC", "L")
		if err != nil {
			t.Fatal(err)
		}
		out, err := r.Upload(raid.ID, "L", buildNight(raid, "Lead", nightOpts{price: "10", bobSilent: true}))
		if err != nil || out.Status != "settled" {
			t.Fatalf("%v %s %s", err, out.Status, out.Message)
		}
		return raid, out
	}
	raid, out := night()
	bob, _ := p.Get("B")
	if bob.FreePasses != 0 {
		t.Fatalf("free pass not consumed: %d", bob.FreePasses)
	}
	if count(t, r, `SELECT COUNT(*) FROM ledger WHERE raid_id = ? AND reason = 'penalty'`, raid.ID) != 0 {
		t.Fatal("penalty charged despite the free pass")
	}
	for _, d := range out.Deltas {
		if d.Reason == "penalty" {
			t.Fatalf("waived penalty still reported: %+v", d)
		}
	}
	if replay.Sum(out.Deltas) != 0 {
		t.Fatalf("applied deltas not zero-sum: %d", replay.Sum(out.Deltas))
	}
	// Price 10, three attendees: pool 1, share 3 each. Bob: +3, no penalty.
	if bob.Balance != 3 {
		t.Fatalf("Bob's balance %d", bob.Balance)
	}
	// Second night: no pass left, the penalty applies.
	raid2, _ := night()
	bob, _ = p.Get("B")
	if bob.Balance != 3+3-25 {
		t.Fatalf("Bob's balance after penalty %d", bob.Balance)
	}
	if count(t, r, `SELECT COUNT(*) FROM ledger WHERE raid_id = ? AND reason = 'penalty'`, raid2.ID) != 1 {
		t.Fatal("penalty missing")
	}
	if count(t, r, `SELECT COALESCE(SUM(delta),0) FROM ledger WHERE raid_id = ?`, raid2.ID) != 0 {
		t.Fatal("raid ledger not zero-sum")
	}
}

// G: uploads are append-only and audited against the upload the raid settled on.
func TestAppendOnlyUploadsAndAudit(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	if out, _ := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30")); out.Status != "settled" {
		t.Fatalf("first: %s", out.Status)
	}
	// The leader uploads again with a different RESULT: the first upload is kept and the
	// raid is contested, not quietly re-settled.
	out, err := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "31"))
	if err != nil || out.Status != "contested" {
		t.Fatalf("second leader upload: %v %s", err, out.Status)
	}
	if count(t, r, `SELECT COUNT(*) FROM transcript_upload WHERE raid_id = ? AND person_id = 'L'`, raid.ID) != 2 {
		t.Fatal("uploads must be append-only")
	}
	got, _ := r.Get(raid.ID)
	var firstText string
	_ = r.S.DB.QueryRow(`SELECT text FROM transcript_upload WHERE id = ?`, got.SettledUploadID).Scan(&firstText)
	if !strings.Contains(firstText, "RESULT^"+fmt.Sprint(raid.ID)+"-1^Alice^30") {
		t.Fatal("settled upload text was changed")
	}
	// Contested raids block the leader from opening the next one and are listed as unsettled.
	if _, err := r.Open(0, "Next", "L"); !errors.Is(err, ErrUnsettled) {
		t.Fatalf("contested raid should block opening: %v", err)
	}
	if ids, _ := r.UnsettledFor("L"); len(ids) != 1 || ids[0] != raid.ID {
		t.Fatalf("unsettled for leader: %v", ids)
	}
	if cur, err := r.CurrentForLeader("L"); err != nil || cur.ID != raid.ID {
		t.Fatalf("current for leader: %v %v", cur.ID, err)
	}
	if list, _ := r.Unsettled(); len(list) != 1 {
		t.Fatalf("unsettled list: %d", len(list))
	}
}

// G: void reverses only the current settle generation, including attendance.
func TestVoidReversesCurrentGenerationOnly(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	if out, _ := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30")); out.Status != "settled" {
		t.Fatalf("settle: %s", out.Status)
	}
	lines := count(t, r, `SELECT COUNT(*) FROM ledger WHERE raid_id = ?`, raid.ID)
	check := func(stage string, wantLines int64, alice, attended, shares int64, attendanceRows int64) {
		t.Helper()
		if n := count(t, r, `SELECT COUNT(*) FROM ledger WHERE raid_id = ?`, raid.ID); n != wantLines {
			t.Fatalf("%s: %d ledger lines, want %d", stage, n, wantLines)
		}
		a, _ := p.Get("A")
		if a.Balance != alice || a.RaidsAttended != attended || a.LifetimeShares != shares {
			t.Fatalf("%s: Alice balance=%d attended=%d shares=%d", stage, a.Balance, a.RaidsAttended, a.LifetimeShares)
		}
		if n := count(t, r, `SELECT COUNT(*) FROM attendance WHERE raid_id = ?`, raid.ID); n != attendanceRows {
			t.Fatalf("%s: %d attendance rows, want %d", stage, n, attendanceRows)
		}
		if s := count(t, r, `SELECT COALESCE(SUM(delta),0) FROM ledger WHERE raid_id = ?`, raid.ID); s != 0 {
			t.Fatalf("%s: raid ledger sums to %d", stage, s)
		}
	}
	check("settled", lines, -21, 1, 4, 3)
	if _, err := r.Override(raid.ID, "O", "void", "first void"); err != nil {
		t.Fatal(err)
	}
	check("voided", 2*lines, 0, 0, 0, 0)
	if _, err := r.Override(raid.ID, "O", "settle", "again"); err != nil {
		t.Fatal(err)
	}
	check("re-settled", 3*lines, -21, 1, 4, 3)
	if got, _ := r.Get(raid.ID); got.SettleGeneration != 2 {
		t.Fatalf("generation %d", got.SettleGeneration)
	}
	// The second void reverses generation 2 only: not the already-reversed first settle.
	if _, err := r.Override(raid.ID, "O", "void", "second void"); err != nil {
		t.Fatal(err)
	}
	check("voided again", 4*lines, 0, 0, 0, 0)
	if count(t, r, `SELECT COUNT(*) FROM ledger WHERE raid_id = ? AND generation = 2 AND reason = 'reversal'`, raid.ID) != lines {
		t.Fatal("reversal lines must carry the generation they reverse")
	}
}

// G: an officer may settle from a chosen upload; the default is the leader's newest.
func TestOverrideSettleFromUpload(t *testing.T) {
	r, p, _ := newServices(t)
	_ = r.S.SetConfig("requiredUploads", "2")
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	if _, err := r.Upload(raid.ID, "A", raidNight(raid, "Alice", "31")); err != nil {
		t.Fatal(err)
	}
	if out, _ := r.Upload(raid.ID, "L", raidNight(raid, "Lead", "30")); out.Status != "disputed" {
		t.Fatalf("expected disputed: %s", out.Status)
	}
	var aliceUpload, leadUpload int64
	_ = r.S.DB.QueryRow(`SELECT id FROM transcript_upload WHERE raid_id = ? AND person_id = 'A'`, raid.ID).Scan(&aliceUpload)
	_ = r.S.DB.QueryRow(`SELECT id FROM transcript_upload WHERE raid_id = ? AND person_id = 'L'`, raid.ID).Scan(&leadUpload)
	if _, err := r.Override(raid.ID, "O", "settle", "by hand", 999999); err == nil {
		t.Fatal("unknown upload id accepted")
	}
	out, err := r.Override(raid.ID, "O", "settle", "trust Alice", aliceUpload)
	if err != nil || out.Status != "settled" {
		t.Fatalf("%v %s", err, out.Status)
	}
	if got, _ := r.Get(raid.ID); got.SettledUploadID != aliceUpload || !strings.Contains(got.SettleNote, "by A") {
		t.Fatalf("settled from %d note %q", got.SettledUploadID, got.SettleNote)
	}
	if _, err := r.Override(raid.ID, "O", "void", "redo"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Override(raid.ID, "O", "settle", "default"); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Get(raid.ID); got.SettledUploadID != leadUpload {
		t.Fatalf("default should be the leader's upload %d, got %d", leadUpload, got.SettledUploadID)
	}
	// The settle guard: a settled raid cannot be settled again underneath.
	if _, err := r.settle(Raid{ID: raid.ID}, out.Report, nil, "race", 0); !errors.Is(err, ErrAlreadySettled) {
		t.Fatalf("concurrent settle: %v", err)
	}
}

// B: the leader in the transcript must be a character of the person who opened the raid.
func TestTranscriptLeaderMustMatchRaidLeader(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	raid, _ := r.Open(0, "MC", "L")
	// Alice's client ran the raid although L opened it in the bot.
	out, err := r.Upload(raid.ID, "L", buildNight(raid, "Lead", nightOpts{price: "30", leaderChar: "Alice"}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "disputed" || !strings.Contains(out.Message, "leader_not_raid_leader:Alice") {
		t.Fatalf("%s: %s", out.Status, out.Message)
	}
}

// H: addon keys end to end.
func TestAddonKeys(t *testing.T) {
	r, p, _ := newServices(t)
	register(t, p, "L", "Lead", "Lead")
	register(t, p, "A", "Alice", "Alice")
	register(t, p, "B", "Bob", "Bob")
	if _, err := p.IssueAddonKey("nobody"); err == nil {
		t.Fatal("key issued to an unknown person")
	}
	key, err := p.IssueAddonKey("A")
	if err != nil || len(key) != 32 || strings.ToLower(key) != key || strings.Trim(key, "0123456789abcdef") != "" {
		t.Fatalf("key %q %v", key, err)
	}
	if k, err := p.AddonKey("A"); err != nil || k != key {
		t.Fatalf("AddonKey %q %v", k, err)
	}
	if k, _ := p.AddonKey("B"); k != "" {
		t.Fatalf("Bob has no key: %q", k)
	}
	if keys, _ := p.CharacterKeys(); len(keys) != 1 || keys["Alice"] != key {
		t.Fatalf("CharacterKeys %v", keys)
	}
	again, _ := p.IssueAddonKey("A")
	if again == key {
		t.Fatal("re-issue must replace the key")
	}
	key = again

	// Alice tags her lines; Bob has no key: settled, Bob noted untagged.
	raid, _ := r.Open(0, "MC", "L")
	out, err := r.Upload(raid.ID, "L", buildNight(raid, "Lead", nightOpts{price: "30", keys: map[string]string{"Alice": key}}))
	if err != nil || out.Status != "settled" || !strings.Contains(out.Message, "untagged:Bob") || strings.Contains(out.Message, "untagged:Alice") {
		t.Fatalf("%v %s %s", err, out.Status, out.Message)
	}
	// A leader forging Alice's lines without her key: dropped and disputed.
	raid2, _ := r.Open(0, "MC", "L")
	out, _ = r.Upload(raid2.ID, "L", buildNight(raid2, "Lead", nightOpts{price: "30"}))
	if out.Status != "disputed" || !strings.Contains(out.Message, "bad_tag:Alice") {
		t.Fatalf("%s %s", out.Status, out.Message)
	}
	if _, err := r.Override(raid2.ID, "O", "void", "forged"); err != nil {
		t.Fatal(err)
	}
	// Keys required: Bob's untagged lines now dispute.
	_ = r.S.SetConfig("requireAddonKey", "true")
	raid3, _ := r.Open(0, "MC", "L")
	out, _ = r.Upload(raid3.ID, "L", buildNight(raid3, "Lead", nightOpts{price: "30", keys: map[string]string{"Alice": key}}))
	if out.Status != "disputed" || !strings.Contains(out.Message, "untagged:Bob") {
		t.Fatalf("%s %s", out.Status, out.Message)
	}
	// Revoking access clears the key, so nothing tagged with it verifies any more.
	if err := p.RevokeAccess("A"); err != nil {
		t.Fatal(err)
	}
	if k, _ := p.AddonKey("A"); k != "" {
		t.Fatalf("key survived revoke: %q", k)
	}
}
