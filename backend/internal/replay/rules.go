package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Row is a bidder's standing as broadcast by the leader (ROWS).
type Row struct {
	Owner   string
	Balance int64
	Credit  int64
}

// Context is everything an auction is judged against.
type Context struct {
	RaidID     string
	OffspecPct int64
	Floors     map[string]int64
	Banned     map[string]bool
	Rows       map[string]Row
}

// OwnerOf is the row owner a character belongs to, or the name itself when it has no
// row. Caps and shares are per owner (protocol §9).
func (c *Context) OwnerOf(name string) string {
	if r, ok := c.Rows[name]; ok && r.Owner != "" {
		return r.Owner
	}
	return name
}

// Commit is one COMMIT as received.
type Commit struct {
	Name string
	Hash string
	Late bool // arrived after CLOSE
}

// Reveal is one REVEAL and its verdict.
type Reveal struct {
	Name    string
	Bid     int64
	Offspec bool
	Salt    string
	Valid   bool
	Reason  string // why invalid
	Floor   int64  // applicable floor for this bidder

	preInvalid string // set by the parser when the bid field was not an integer
}

// Auction is one auction's full state after resolution.
type Auction struct {
	ID       string
	Kind     string // loot | test
	ItemLink string
	ItemID   int64
	Floor    int64
	Deadline int64
	OpenedBy string
	SlotKey  string // floor-table key the leader's client used ("" from older clients)
	// Line times of the first OPEN, CLOSE and RESULT, for the timing rules.
	OpenT, CloseT, ResultT int64

	Commits   []Commit
	CloseList []string // from CLOSE
	Closed    bool
	Reveals   []Reveal
	Result    *Result // from RESULT
	Flags     []string
	// TransferredTo is set by a valid TRANSFER: the item went to this registered
	// raider instead of the winner, and they pay for it.
	TransferredTo string
	// Voided is set by a VOID from the leader after the RESULT: the sale is struck from
	// the books (no purchase, shares or pool) and the item is auctioned again under a
	// new id. Not a flag: a void is a normal leader action, not a dispute.
	Voided     bool
	VoidReason string
	// Abandoned is set by an ABANDON from the leader before any RESULT: the auction never
	// concluded (the leader who opened it was lost) and settles nothing. Not a flag.
	Abandoned     bool
	AbandonReason string
	// SpentAtResult is what each member already owed for this raid's loot sales announced
	// before this auction's RESULT (transfers count against the recipient; voids and
	// abandons do not). The running cap for a bid is balance + credit - SpentAtResult
	// (§3.1); judging at the result, not at the bid, lets auctions run side by side.
	SpentAtResult map[string]int64

	// Computed
	Winner string
	Price  int64
	Sale   bool
	Valid  []Reveal // the valid reveals, highest bid first

	openIndex int // position of OPEN in the transcript, to order against KILLs
}

// Result is the leader's announced outcome.
type Result struct {
	Winner string
	Price  int64
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// CommitHash is sha256 hex of "auctionID|name|bid|offspec|salt".
func CommitHash(auctionID, name string, bid int64, offspec bool, salt string) string {
	off := "0"
	if offspec {
		off = "1"
	}
	return sha(fmt.Sprintf("%s|%s|%d|%s|%s", auctionID, name, bid, off, salt))
}

// CommitTag is the addon-key signature on a COMMIT: sha256 hex of "key|auctionID|hash".
func CommitTag(key, auctionID, hash string) string {
	return sha(key + "|" + auctionID + "|" + hash)
}

// RevealTag is the addon-key signature on a REVEAL: sha256 hex of
// "key|auctionID|bid|offspec|salt", with bid and offspec exactly as sent.
func RevealTag(key, auctionID, bid, offspec, salt string) string {
	return sha(key + "|" + auctionID + "|" + bid + "|" + offspec + "|" + salt)
}

// AckTag is the addon-key signature on an ACK: sha256 hex of "key|LEAD|newLeader".
func AckTag(key, newLeader string) string {
	return sha(key + "|LEAD|" + newLeader)
}

// ApplicableFloor is the floor for a bidder: main spec pays floor, off-spec pays
// ceil(floor × (100 − pct) / 100).
func ApplicableFloor(floor, offspecPct int64, offspec bool) int64 {
	if !offspec {
		return floor
	}
	return (floor*(100-offspecPct) + 99) / 100
}

func (a *Auction) flag(f string) { a.Flags = append(a.Flags, f) }

// flagOnce adds an auction flag unless it is already present.
func (a *Auction) flagOnce(f string) {
	for _, x := range a.Flags {
		if x == f {
			return
		}
	}
	a.Flags = append(a.Flags, f)
}

func (a *Auction) committed(name string) *Commit {
	for i := range a.Commits {
		if a.Commits[i].Name == name && !a.Commits[i].Late {
			return &a.Commits[i]
		}
	}
	return nil
}

// Resolve applies §3 to the auction: judges every reveal, picks winner and price,
// adds flags for anything that disagrees with CLOSE and RESULT.
func Resolve(ctx *Context, a *Auction) {
	// 3.5 close list vs our own committer list
	mine := make([]string, 0, len(a.Commits))
	for _, c := range a.Commits {
		if !c.Late {
			mine = append(mine, c.Name)
		}
	}
	sort.Strings(mine)
	if a.Closed && strings.Join(mine, ",") != strings.Join(a.CloseList, ",") {
		a.flag("close_mismatch")
	}

	// 3.1 eligibility
	revealed := map[string]bool{}
	for i := range a.Reveals {
		r := &a.Reveals[i]
		r.Floor = ApplicableFloor(a.Floor, ctx.OffspecPct, r.Offspec)
		r.Valid, r.Reason = judge(ctx, a, r)
		revealed[r.Name] = true
		if !r.Valid {
			a.flag("invalid_reveal:" + r.Name + ":" + r.Reason)
		}
	}
	// A non-reveal is only a non-reveal when the auction ran to its end: closed, and
	// neither abandoned nor voided (F).
	if a.Closed && !a.Abandoned && !a.Voided {
		for _, c := range a.Commits {
			if !c.Late && !revealed[c.Name] {
				a.flag("noreveal:" + c.Name)
			}
		}
	}

	// 3.3 winner
	var valid []Reveal
	for _, r := range a.Reveals {
		if r.Valid {
			valid = append(valid, r)
		}
	}
	sort.SliceStable(valid, func(i, j int) bool { return valid[i].Bid > valid[j].Bid })
	a.Valid = valid
	if len(valid) == 0 {
		a.Sale, a.Winner, a.Price = false, "", 0
	} else {
		top := valid[0].Bid
		var tied []Reveal
		for _, r := range valid {
			if r.Bid == top {
				tied = append(tied, r)
			}
		}
		winner := tied[0]
		if len(tied) > 1 {
			winner = breakTie(valid, tied)
		}
		a.Sale, a.Winner = true, winner.Name
		// 3.4 price
		if len(valid) >= 2 {
			second := secondHighest(valid, winner.Name)
			a.Price = max64(second, winner.Floor)
		} else {
			a.Price = winner.Floor
		}
		if a.Price < 0 {
			// Only reachable with a negative floor, already flagged bad_floor.
			a.Price = 0
		}
	}

	// 3.5 result vs ours
	if a.Result != nil && (a.Result.Winner != a.Winner || a.Result.Price != a.Price) {
		a.flag("result_mismatch")
	}
}

func judge(ctx *Context, a *Auction, r *Reveal) (bool, string) {
	if r.preInvalid != "" {
		return false, r.preInvalid
	}
	c := a.committed(r.Name)
	if c == nil {
		return false, "no_commit"
	}
	if CommitHash(a.ID, r.Name, r.Bid, r.Offspec, r.Salt) != c.Hash {
		return false, "hash_mismatch"
	}
	row, ok := ctx.Rows[r.Name]
	if !ok {
		return false, "unregistered"
	}
	if ctx.Banned[r.Name] {
		return false, "banned"
	}
	if r.Bid < 0 {
		return false, "bad_bid"
	}
	if r.Bid > row.Balance+row.Credit-a.SpentAtResult[row.Owner] {
		return false, "over_credit"
	}
	if r.Bid < r.Floor {
		return false, "below_floor"
	}
	return true, ""
}

// breakTie: T = sha256(concat of salts of all valid reveals ordered by name);
// each tied bidder scores sha256(T|name); lowest hex wins.
func breakTie(valid []Reveal, tied []Reveal) Reveal {
	byName := append([]Reveal(nil), valid...)
	sort.Slice(byName, func(i, j int) bool { return byName[i].Name < byName[j].Name })
	var salts strings.Builder
	for _, r := range byName {
		salts.WriteString(r.Salt)
	}
	T := sha(salts.String())
	best, bestScore := tied[0], ""
	for _, r := range tied {
		score := sha(T + "|" + r.Name)
		if bestScore == "" || score < bestScore {
			best, bestScore = r, score
		}
	}
	return best
}

func secondHighest(valid []Reveal, winner string) int64 {
	var second int64 = -1
	for _, r := range valid {
		if r.Name == winner {
			continue
		}
		if r.Bid > second {
			second = r.Bid
		}
	}
	return second
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// EffectiveWinner is who pays: the transfer target if a valid TRANSFER was recorded,
// otherwise the computed winner.
func (a *Auction) EffectiveWinner() string {
	if a.TransferredTo != "" {
		return a.TransferredTo
	}
	return a.Winner
}
