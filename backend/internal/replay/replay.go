package replay

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// Timing windows the leader's client must honour (addon BID_SECONDS = 20, REVEAL_SECONDS
// = 15); the replay allows a couple of seconds of clock skew on each.
const (
	minBidWindow    = 18 // deadline - OPEN time
	minRevealWindow = 13 // RESULT time - CLOSE time, when a committer has not revealed
	// TestItemID is the Hearthstone, the only item a kind=test auction may carry.
	TestItemID = 6948
)

// Kill is one successful encounter and who was there.
type Kill struct {
	EncounterID   string
	EncounterName string
	Roster        []string // names with a snapshot row and no ban, sorted
	T             int64
	Index         int // position in the line sequence, to order against auctions
}

// Options is what the raids service knows about the raid beyond the snapshot.
type Options struct {
	// LeaderCharacters are the characters registered to the person who opened the raid
	// in the bot. The transcript's leader must be one of them (leader_not_raid_leader).
	LeaderCharacters map[string]bool
	// Keys maps a character name to its owner's addon key (people.addon_key). A sender
	// with a key must tag every COMMIT, REVEAL and ACK (bad_tag); one without is noted
	// untagged.
	Keys map[string]string
	// RequireAddonKey makes untagged lines disputing (config requireAddonKey).
	RequireAddonKey bool
}

// Report is the outcome of replaying one transcript.
type Report struct {
	RaidID        string
	Leader        string
	Snapshot      snapshot.Snapshot
	Context       Context
	Auctions      []*Auction
	Kills         []Kill
	Roster        []string // everyone confirmed by a matching ROWS row, sorted
	Flags         []string // raid-level flags (snapshot_mismatch, row_mismatch:<name>, leader_change, ...)
	LeaderChanges []string // every leader accepted after the first, in order (§3.9)
	Loot          []Line   // loot lines, for the looter check
	// RequireAddonKey mirrors Options.RequireAddonKey so Disputing can classify untagged.
	RequireAddonKey bool
}

// Flagged reports whether any raid-level or auction-level flag exists, of either class.
func (r *Report) Flagged() bool {
	if len(r.Flags) > 0 {
		return true
	}
	for _, a := range r.Auctions {
		if len(a.Flags) > 0 {
			return true
		}
	}
	return false
}

// AllFlags lists every flag with its scope.
func (r *Report) AllFlags() []string {
	out := append([]string(nil), r.Flags...)
	for _, a := range r.Auctions {
		for _, f := range a.Flags {
			out = append(out, a.ID+": "+f)
		}
	}
	return out
}

// Disputing lists the flags that block automatic settlement (protocol §9): the record
// itself cannot be trusted. Empty means the raid may settle.
func (r *Report) Disputing() []string {
	var out []string
	for _, f := range r.Flags {
		if !IsInformational(f, r.RequireAddonKey) {
			out = append(out, f)
		}
	}
	for _, a := range r.Auctions {
		for _, f := range a.Flags {
			if !IsInformational(f, r.RequireAddonKey) {
				out = append(out, a.ID+": "+f)
			}
		}
	}
	return out
}

// Informational lists the flags that are recorded but do not stop settlement: one
// person's misconduct or bad luck, already penalised or set aside by the rules.
func (r *Report) Informational() []string {
	var out []string
	for _, f := range r.Flags {
		if IsInformational(f, r.RequireAddonKey) {
			out = append(out, f)
		}
	}
	for _, a := range r.Auctions {
		for _, f := range a.Flags {
			if IsInformational(f, r.RequireAddonKey) {
				out = append(out, a.ID+": "+f)
			}
		}
	}
	return out
}

// informationalPrefixes are the raid- or auction-level flags that never dispute a raid.
// Anything not listed here (or in informationalReasons for invalid reveals) is disputing:
// an unknown flag fails safe.
var informationalPrefixes = []string{
	"noreveal:", "late_commit:", "late_reveal:", "early_reveal:", "kill_unknown:", "kill_silent:", "unauthorized_snap:",
}

// informationalReasons are the invalid_reveal reasons that only set one bid aside.
var informationalReasons = map[string]bool{
	"over_credit": true, "below_floor": true, "unregistered": true, "banned": true,
	"early_reveal": true, "bad_bid": true, "bad_salt": true, "no_commit": true,
}

// IsInformational classifies one flag (without the "<auctionID>: " scope prefix).
// untagged is informational unless the community requires addon keys.
func IsInformational(flag string, requireAddonKey bool) bool {
	if strings.HasPrefix(flag, "untagged:") {
		return !requireAddonKey
	}
	if strings.HasPrefix(flag, "invalid_reveal:") {
		reason := flag[strings.LastIndex(flag, ":")+1:]
		return informationalReasons[reason]
	}
	for _, p := range informationalPrefixes {
		if strings.HasPrefix(flag, p) {
			return true
		}
	}
	return false
}

// flagOnce adds a raid-level flag unless it is already present.
func (r *Report) flagOnce(f string) {
	for _, x := range r.Flags {
		if x == f {
			return
		}
	}
	r.Flags = append(r.Flags, f)
}

var (
	itemIDRe = regexp.MustCompile(`\|Hitem:(\d+):`)
	bidRe    = regexp.MustCompile(`^[0-9]{1,9}$`)
	saltRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// pendingTransfer is a TRANSFER seen in the line loop, judged after Resolve against the
// computed winner and price (the announced ones may be wrong).
type pendingTransfer struct {
	from, to string
	spent    map[string]int64 // what each owner owed when the TRANSFER arrived
}

// Run replays a transcript against the raid's issued snapshot.
func Run(snap snapshot.Snapshot, checksum string, tr *Transcript, opts Options) (*Report, error) {
	if tr.RaidID != snap.RaidID {
		return nil, fmt.Errorf("transcript is for raid %q, snapshot is for raid %q", tr.RaidID, snap.RaidID)
	}
	rep := &Report{RaidID: snap.RaidID, Snapshot: snap, RequireAddonKey: opts.RequireAddonKey}
	ctx := Context{
		RaidID:     snap.RaidID,
		OffspecPct: snap.OffspecPct,
		Floors:     snap.Floors,
		Banned:     map[string]bool{},
		Rows:       map[string]Row{},
	}
	for _, b := range snap.Banned {
		ctx.Banned[b] = true
	}
	// Rows come from the issued snapshot; ROWS lines only confirm them (E).
	for _, r := range snap.Rows {
		ctx.Rows[r.Character] = Row{Owner: r.Owner, Balance: r.Balance, Credit: r.Credit}
	}
	switch {
	case tr.SnapshotChecksum == "":
		rep.flagOnce("no_snapshot_header")
	case tr.SnapshotChecksum != checksum:
		rep.flagOnce("snapshot_mismatch")
	}
	auctionIDRe := regexp.MustCompile(`^` + regexp.QuoteMeta(snap.RaidID) + `-[0-9]+$`)

	// Everyone who sent anything: a KILL name that never spoke is noted (kill_silent).
	senders := map[string]bool{}
	for _, line := range tr.Lines {
		if line.Dir == "in" {
			senders[line.Sender] = true
		}
	}

	auctions := map[string]*Auction{}
	roster := map[string]bool{}
	claimant, acks := "", map[string]bool{}
	rowFlagged := map[string]bool{}
	transfers := map[string][]pendingTransfer{}
	sawSnap := false

	// acceptLeader installs a new leader from a LEAD quorum; the new leader must have a
	// snapshot row and no ban, else the raid is disputed (B).
	acceptLeader := func(name string) {
		rep.Leader, claimant = name, ""
		rep.LeaderChanges = append(rep.LeaderChanges, name)
		if _, ok := ctx.Rows[name]; !ok || ctx.Banned[name] {
			rep.flagOnce("leader_not_raid_leader:" + name)
		}
	}
	// tagOK applies the addon-key rule (H) to a COMMIT, REVEAL or ACK. It reports whether
	// the line may be processed; a dropped line is flagged on the auction when there is one.
	tagOK := func(sender, tag string, expect func(key string) string, a *Auction) bool {
		key := opts.Keys[sender]
		if key == "" {
			rep.flagOnce("untagged:" + sender)
			return true
		}
		if tag == "" || tag != expect(key) {
			if a != nil {
				a.flagOnce("bad_tag:" + sender)
			} else {
				rep.flagOnce("bad_tag:" + sender)
			}
			return false
		}
		return true
	}

	for idx, line := range tr.Lines {
		if line.Dir == "loot" {
			rep.Loot = append(rep.Loot, line)
			continue
		}
		if line.Dir != "in" {
			continue
		}
		m := Decode(line.Body)
		if !protocolTypes[m.Type] {
			continue
		}
		// Leadership (§3.9): the game's group leader sends LEAD; every other client answers ACK.
		// Once the claimant plus its ACKs reach half the roster, leader-only messages are
		// accepted from the new leader. Anything leader-only from anyone else is flagged.
		switch m.Type {
		case "LEAD":
			if line.Sender == rep.Leader {
				continue
			}
			claimant, acks = line.Sender, map[string]bool{}
			if leadQuorum(rep, roster, claimant, acks) {
				acceptLeader(claimant)
			}
			continue
		case "ACK":
			if claimant == "" || m.field(0) != claimant || line.Sender == claimant {
				continue
			}
			if !tagOK(line.Sender, m.field(1), func(k string) string { return AckTag(k, m.field(0)) }, nil) {
				continue
			}
			acks[line.Sender] = true
			if leadQuorum(rep, roster, claimant, acks) {
				acceptLeader(claimant)
			}
			continue
		}
		// The leader is whoever first broadcast the issued snapshot (its checksum must
		// match); a SNAP from anyone else is noted and ignored. Every other leader-only
		// message needs that leader to be known already.
		leaderOnly := m.Type == "SNAP" || m.Type == "ROWS" || m.Type == "OPEN" || m.Type == "CLOSE" || m.Type == "RESULT" || m.Type == "KILL" || m.Type == "TRANSFER" || m.Type == "VOID" || m.Type == "ABANDON"
		if m.Type == "SNAP" {
			if rep.Leader == "" && m.field(1) == checksum {
				rep.Leader = line.Sender
				if !opts.LeaderCharacters[line.Sender] {
					rep.flagOnce("leader_not_raid_leader:" + line.Sender)
				}
			} else if line.Sender != rep.Leader {
				rep.flagOnce("unauthorized_snap:" + line.Sender)
				continue
			}
		} else if leaderOnly && (rep.Leader == "" || line.Sender != rep.Leader) {
			rep.Flags = append(rep.Flags, "leader_change:"+m.Type+":"+line.Sender)
			continue
		}
		switch m.Type {
		case "SNAP":
			sawSnap = true
			if m.field(0) != snap.RaidID {
				rep.flagOnce("snap_raid_mismatch:" + m.field(0))
			}
			if m.field(1) != checksum {
				rep.flagOnce("snap_checksum_mismatch")
			}
			if pct, err := strconv.ParseInt(m.field(3), 10, 64); err != nil || pct != snap.OffspecPct {
				rep.flagOnce("snap_offspec_mismatch")
			}
			if !sameFloors(m.field(4), snap.Floors) {
				rep.flagOnce("snap_floors_mismatch")
			}
			if !sameSet(m.field(5), snap.Banned) {
				rep.flagOnce("snap_banned_mismatch")
			}
		case "ROWS":
			for _, f := range m.Fields {
				p := strings.Split(f, ",")
				if len(p) != 4 {
					rep.flagOnce("bad_row")
					continue
				}
				bal, e1 := strconv.ParseInt(p[2], 10, 64)
				cr, e2 := strconv.ParseInt(p[3], 10, 64)
				if e1 != nil || e2 != nil {
					rep.flagOnce("bad_row:" + p[0])
					continue
				}
				// The leader's broadcast row must match what the backend issued.
				sr, ok := ctx.Rows[p[0]]
				if !ok || sr.Owner != p[1] || sr.Balance != bal || sr.Credit != cr {
					if !rowFlagged[p[0]] {
						rep.Flags = append(rep.Flags, "row_mismatch:"+p[0])
						rowFlagged[p[0]] = true
					}
					continue
				}
				roster[p[0]] = true
			}
		case "OPEN":
			id := m.field(0)
			if _, dup := auctions[id]; dup {
				rep.Flags = append(rep.Flags, "duplicate_open:"+id)
				continue
			}
			floor, ferr := strconv.ParseInt(m.field(3), 10, 64)
			deadline, _ := strconv.ParseInt(m.field(4), 10, 64)
			a := &Auction{ID: id, Kind: m.field(1), ItemLink: m.field(2), Floor: floor, Deadline: deadline, OpenedBy: line.Sender, SlotKey: m.field(5), OpenT: line.T}
			if mm := itemIDRe.FindStringSubmatch(a.ItemLink); mm != nil {
				a.ItemID, _ = strconv.ParseInt(mm[1], 10, 64)
			}
			if !auctionIDRe.MatchString(id) {
				a.flag("bad_auction_id")
			}
			switch a.Kind {
			case "loot":
			case "test":
				if a.ItemID != TestItemID {
					a.flag("test_real_item")
				}
			default:
				a.flag("bad_kind:" + a.Kind)
			}
			if ferr != nil || floor < 0 {
				a.flag("bad_floor")
			} else if a.SlotKey != "" && a.Kind == "loot" {
				// The floor is the snapshot's for the slot the leader's client named (D).
				// Test auctions run at a minimum of 1 and are never settled, so their floor
				// is not checked.
				want, ok := ctx.Floors[a.SlotKey]
				if !ok {
					want = ctx.Floors["DEFAULT"]
				}
				if floor != want {
					a.flag("floor_mismatch")
				}
			}
			if deadline-line.T < minBidWindow {
				a.flag("short_deadline")
			}
			a.openIndex = idx
			auctions[id] = a
			rep.Auctions = append(rep.Auctions, a)
		case "COMMIT":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "commit_unknown_auction:"+m.field(0))
				continue
			}
			if !tagOK(line.Sender, m.field(2), func(k string) string { return CommitTag(k, a.ID, m.field(1)) }, a) {
				continue
			}
			if a.committedAny(line.Sender) {
				a.flag("duplicate_commit:" + line.Sender)
				continue
			}
			c := Commit{Name: line.Sender, Hash: m.field(1), Late: a.Closed}
			if c.Late {
				a.flag("late_commit:" + line.Sender)
			}
			a.Commits = append(a.Commits, c)
		case "CLOSE":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "close_unknown_auction:"+m.field(0))
				continue
			}
			if a.Closed {
				a.flag("duplicate_close")
				continue
			}
			a.Closed, a.CloseT = true, line.T
			if line.T < a.Deadline {
				a.flag("early_close")
			}
			if m.field(1) != "" {
				a.CloseList = strings.Split(m.field(1), ",")
				sort.Strings(a.CloseList)
			}
		case "REVEAL":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "reveal_unknown_auction:"+m.field(0))
				continue
			}
			if !tagOK(line.Sender, m.field(4), func(k string) string { return RevealTag(k, a.ID, m.field(1), m.field(2), m.field(3)) }, a) {
				continue
			}
			if a.Result != nil {
				a.flag("late_reveal:" + line.Sender)
				continue
			}
			if a.revealedAny(line.Sender) {
				a.flag("duplicate_reveal:" + line.Sender)
				continue
			}
			bid, _ := strconv.ParseInt(m.field(1), 10, 64)
			r := Reveal{Name: line.Sender, Bid: bid, Offspec: m.field(2) == "1", Salt: m.field(3)}
			switch {
			case !bidRe.MatchString(m.field(1)):
				r.preInvalid = "bad_bid"
			case !saltRe.MatchString(r.Salt):
				r.preInvalid = "bad_salt"
			case !a.Closed:
				// A reveal before CLOSE shows the bid while others can still commit.
				r.preInvalid = "early_reveal"
				a.flag("early_reveal:" + line.Sender)
			}
			a.Reveals = append(a.Reveals, r)
		case "RESULT":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "result_unknown_auction:"+m.field(0))
				continue
			}
			if a.Result != nil {
				a.flag("duplicate_result")
				continue
			}
			price, _ := strconv.ParseInt(m.field(2), 10, 64)
			a.SpentAtResult = spentSoFar(&ctx, auctions)
			a.Result = &Result{Winner: m.field(1), Price: price}
			a.ResultT = line.T
			if !a.Closed {
				a.flag("result_before_close")
			} else if line.T < a.CloseT+minRevealWindow && a.unrevealedCommitter() {
				a.flag("early_result")
			}
		case "TRANSFER":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "transfer_unknown_auction:"+m.field(0))
				continue
			}
			from, to := m.field(1), m.field(2)
			spent := spentSoFar(&ctx, auctions)
			transfers[a.ID] = append(transfers[a.ID], pendingTransfer{from: from, to: to, spent: spent})
			// Provisional verdict against the announced result, so later running-cap
			// checks charge the right owner; the final verdict comes after Resolve.
			if a.TransferredTo == "" && transferProblem(&ctx, a, from, to, spent, a.Result) == "" {
				a.TransferredTo = to
			}
		case "VOID":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "void_unknown_auction:"+m.field(0))
				continue
			}
			if a.Result == nil {
				a.flag("void_before_result")
				continue
			}
			if a.Voided {
				a.flag("duplicate_void")
				continue
			}
			a.Voided, a.VoidReason = true, m.field(1)
		case "ABANDON":
			a := auctions[m.field(0)]
			if a == nil {
				rep.Flags = append(rep.Flags, "abandon_unknown_auction:"+m.field(0))
				continue
			}
			if a.Result != nil {
				a.flag("abandon_after_result")
				continue
			}
			// The leader who opened an auction may not abandon it once bids are visible;
			// a successor cleaning up an orphan may (F).
			if len(a.Reveals) > 0 && line.Sender == a.OpenedBy {
				a.flag("abandon_after_reveal")
			}
			a.Abandoned, a.AbandonReason = true, m.field(1)
		case "KILL":
			k := Kill{EncounterID: m.field(0), EncounterName: m.field(1), T: line.T, Index: idx}
			if m.field(2) != "" {
				for _, name := range strings.Split(m.field(2), ",") {
					if _, ok := ctx.Rows[name]; !ok {
						rep.flagOnce("kill_unknown:" + name)
						continue
					}
					if !senders[name] {
						rep.flagOnce("kill_silent:" + name)
					}
					if ctx.Banned[name] {
						continue
					}
					k.Roster = append(k.Roster, name)
				}
				sort.Strings(k.Roster)
			}
			// One record per encounter: a repeated KILL replaces the earlier one (E).
			for i := range rep.Kills {
				if rep.Kills[i].EncounterID == k.EncounterID {
					rep.Kills = append(rep.Kills[:i], rep.Kills[i+1:]...)
					break
				}
			}
			rep.Kills = append(rep.Kills, k)
		}
	}
	if !sawSnap {
		rep.flagOnce("no_snap")
	}

	for _, a := range rep.Auctions {
		Resolve(&ctx, a)
		if a.Result == nil && !a.Abandoned {
			a.flag("no_result")
		}
		// Transfers are judged against the computed winner and price (F).
		a.TransferredTo = ""
		for _, t := range transfers[a.ID] {
			if reason := transferProblem(&ctx, a, t.from, t.to, t.spent, &Result{Winner: a.Winner, Price: a.Price}); reason != "" {
				a.flag("bad_transfer:" + t.to + ":" + reason)
				continue
			}
			a.TransferredTo = t.to
		}
	}
	for n := range roster {
		rep.Roster = append(rep.Roster, n)
	}
	sort.Strings(rep.Roster)
	rep.Context = ctx
	return rep, nil
}

// sameFloors compares a SNAP floorsCSV (KEY=value,...) with the snapshot's floor table.
func sameFloors(csv string, floors map[string]int64) bool {
	got := map[string]int64{}
	if csv != "" {
		for _, kv := range strings.Split(csv, ",") {
			k, v, ok := strings.Cut(kv, "=")
			n, err := strconv.ParseInt(v, 10, 64)
			if !ok || err != nil {
				return false
			}
			got[k] = n
		}
	}
	if len(got) != len(floors) {
		return false
	}
	for k, v := range floors {
		if got[k] != v {
			return false
		}
	}
	return true
}

// sameSet compares a comma-joined name list with a slice, ignoring order.
func sameSet(csv string, want []string) bool {
	var got []string
	if csv != "" {
		got = strings.Split(csv, ",")
	}
	if len(got) != len(want) {
		return false
	}
	a, b := append([]string(nil), got...), append([]string(nil), want...)
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (a *Auction) committedAny(name string) bool {
	for _, c := range a.Commits {
		if c.Name == name {
			return true
		}
	}
	return false
}

func (a *Auction) revealedAny(name string) bool {
	for _, r := range a.Reveals {
		if r.Name == name {
			return true
		}
	}
	return false
}

// unrevealedCommitter reports whether someone who committed before CLOSE has not revealed.
func (a *Auction) unrevealedCommitter() bool {
	for _, c := range a.Commits {
		if !c.Late && !a.revealedAny(c.Name) {
			return true
		}
	}
	return false
}

// Attendees returns the roster the auction settles against: the KILL immediately
// before the OPEN, or the ROWS roster if no kill preceded it. Both are already limited
// to characters with a snapshot row.
func (r *Report) Attendees(a *Auction) []string {
	var last *Kill
	for i := range r.Kills {
		if r.Kills[i].Index < a.openIndex {
			last = &r.Kills[i]
		}
	}
	if last != nil && len(last.Roster) > 0 {
		return last.Roster
	}
	return r.Roster
}

// transferProblem validates a TRANSFER (protocol §3) against the given result and
// returns the rejection reason, or "" when it is acceptable.
func transferProblem(ctx *Context, a *Auction, from, to string, spent map[string]int64, res *Result) string {
	switch {
	case a.TransferredTo != "":
		return "duplicate"
	case a.Kind != "loot":
		return "not_loot"
	case res == nil || a.Result == nil:
		return "not_done"
	case res.Winner == "" || res.Winner != from:
		return "not_winner"
	}
	row, ok := ctx.Rows[to]
	switch {
	case !ok:
		return "unregistered"
	case ctx.Banned[to]:
		return "banned"
	case row.Balance+row.Credit-spent[row.Owner] < res.Price:
		return "over_max"
	}
	return ""
}

// leadQuorum reports whether a LEAD claimant has enough backing: the claimant plus its
// distinct ACKs from roster members must reach half the roster (§3.9). The roster is
// everyone given rows; the latest KILL roster is used instead when there is one, since it
// reflects who is actually still in the group.
func leadQuorum(rep *Report, roster map[string]bool, claimant string, acks map[string]bool) bool {
	members := map[string]bool{}
	if n := len(rep.Kills); n > 0 && len(rep.Kills[n-1].Roster) > 0 {
		for _, name := range rep.Kills[n-1].Roster {
			members[name] = true
		}
	} else {
		for name := range roster {
			members[name] = true
		}
	}
	votes := 1
	for name := range acks {
		if members[name] {
			votes++
		}
	}
	need := (len(members) + 1) / 2
	if need < 1 {
		need = 1
	}
	return votes >= need
}

// spentSoFar sums, per row owner, what is owed for the loot sales announced so far: the
// RESULT price, charged to the transferee when a TRANSFER has already been accepted,
// skipping voided and abandoned auctions. Announced results are what the clients used,
// and Resolve flags any that disagree with the bids. Keyed by owner so one person's
// characters share a cap (E).
func spentSoFar(ctx *Context, auctions map[string]*Auction) map[string]int64 {
	spent := map[string]int64{}
	for _, a := range auctions {
		if a.Kind != "loot" || a.Result == nil || a.Result.Winner == "" || a.Voided || a.Abandoned {
			continue
		}
		payer := a.Result.Winner
		if a.TransferredTo != "" {
			payer = a.TransferredTo
		}
		spent[ctx.OwnerOf(payer)] += a.Result.Price
	}
	return spent
}
