package replay

import (
	"fmt"
	"sort"
)

// SettleConfig holds the economy numbers settlement needs.
type SettleConfig struct {
	PoolPct         int64
	NorevealPenalty int64
	Standby         []string // characters on standby for the raid; they share in every split (brief: "incl. standby")
}

// splitRoster is the KILL roster for the auction plus standby members, one entry per row
// owner (a person's characters take one share), sorted. Each owner is represented by
// their first character by name; standby names need a snapshot row and no ban.
func splitRoster(rep *Report, a *Auction, standby []string) []string {
	names := append([]string{}, rep.Attendees(a)...)
	for _, name := range standby {
		if _, ok := rep.Context.Rows[name]; ok && !rep.Context.Banned[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	seen := map[string]bool{}
	var out []string
	for _, name := range names {
		owner := rep.Context.OwnerOf(name)
		if name != "" && !seen[owner] {
			seen[owner] = true
			out = append(out, name)
		}
	}
	return out
}

// Delta is one ledger movement keyed by character name (or the pool account).
type Delta struct {
	Character string // Name-Realm, or "pool" for the raid's performance pool
	Amount    int64
	Reason    string // purchase | share | pool | penalty
	AuctionID string
}

// Settle turns a clean report into zero-sum ledger deltas (§3 Settlement).
// Test auctions and auctions without a sale produce nothing. Non-reveals are
// penalised into the pool. Shares-per-kill are not ledger movements (points are
// never minted); see KillShares.
func Settle(rep *Report, cfg SettleConfig) []Delta {
	var out []Delta
	for _, a := range rep.Auctions {
		if a.Kind != "loot" {
			continue
		}
		for _, f := range a.Flags {
			if len(f) >= 9 && f[:9] == "noreveal:" && cfg.NorevealPenalty > 0 {
				name := f[9:]
				out = append(out,
					Delta{Character: name, Amount: -cfg.NorevealPenalty, Reason: "penalty", AuctionID: a.ID},
					Delta{Character: "pool", Amount: cfg.NorevealPenalty, Reason: "pool", AuctionID: a.ID})
			}
		}
		if !a.Sale || a.Voided || a.Abandoned {
			continue
		}
		attendees := splitRoster(rep, a, cfg.Standby)
		n := int64(len(attendees))
		if n == 0 {
			continue
		}
		P := a.Price
		pool := P * cfg.PoolPct / 100
		share := (P - pool) / n
		remainder := P - pool - share*n
		out = append(out, Delta{Character: a.EffectiveWinner(), Amount: -P, Reason: "purchase", AuctionID: a.ID})
		for _, name := range attendees {
			out = append(out, Delta{Character: name, Amount: share, Reason: "share", AuctionID: a.ID})
		}
		out = append(out, Delta{Character: "pool", Amount: pool + remainder, Reason: "pool", AuctionID: a.ID})
	}
	return out
}

// Sum of all deltas; must be zero.
func Sum(d []Delta) int64 {
	var s int64
	for _, x := range d {
		s += x.Amount
	}
	return s
}

// Attendance is one row owner's presence across the raid's kills.
type Attendance struct {
	Owner      string
	Characters []string // every character of the owner seen in a KILL roster, in first-seen order
	Kills      int      // distinct encounters attended on any character
	FullRun    bool     // present at every kill of the night
}

// KillShares counts distinct encounters attended per row owner, so a person who
// switches characters mid-raid earns one set of shares and can still complete the run.
// Sorted by owner.
func KillShares(rep *Report) []Attendance {
	byOwner := map[string]*Attendance{}
	chars := map[string]map[string]bool{}
	var owners []string
	for _, k := range rep.Kills {
		present := map[string]bool{}
		for _, n := range k.Roster {
			owner := rep.Context.OwnerOf(n)
			at := byOwner[owner]
			if at == nil {
				at = &Attendance{Owner: owner}
				byOwner[owner] = at
				chars[owner] = map[string]bool{}
				owners = append(owners, owner)
			}
			if !chars[owner][n] {
				chars[owner][n] = true
				at.Characters = append(at.Characters, n) // first-seen order: main, then the alt they swapped to
			}
			if !present[owner] {
				present[owner] = true
				at.Kills++
			}
		}
	}
	var out []Attendance
	for _, owner := range owners {
		at := byOwner[owner]
		at.FullRun = at.Kills == len(rep.Kills) && at.Kills > 0
		out = append(out, *at)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Owner < out[j].Owner })
	return out
}

// Summary is a short human-readable account of a report, for Discord.
func Summary(rep *Report) string {
	s := fmt.Sprintf("Raid %s, leader %s, %d kills, %d auctions, roster %d\n", rep.RaidID, rep.Leader, len(rep.Kills), len(rep.Auctions), len(rep.Roster))
	for _, a := range rep.Auctions {
		item := a.ItemLink
		if a.ItemID != 0 {
			item = fmt.Sprintf("item %d", a.ItemID)
		}
		switch {
		case a.Kind == "test":
			s += fmt.Sprintf("  %s TEST: %d commits, %d valid reveals\n", a.ID, len(a.Commits), len(a.Valid))
		case a.Voided:
			s += fmt.Sprintf("  %s %s: VOIDED (%s), was %s for %d\n", a.ID, item, a.VoidReason, a.Winner, a.Price)
		case a.Abandoned:
			s += fmt.Sprintf("  %s %s: ABANDONED (%s)\n", a.ID, item, a.AbandonReason)
		case a.Sale && a.TransferredTo != "":
			s += fmt.Sprintf("  %s %s: %s for %d (%d bids), transferred to %s who pays\n", a.ID, item, a.Winner, a.Price, len(a.Valid), a.TransferredTo)
		case a.Sale:
			s += fmt.Sprintf("  %s %s: %s for %d (%d bids)\n", a.ID, item, a.Winner, a.Price, len(a.Valid))
		default:
			s += fmt.Sprintf("  %s %s: no sale\n", a.ID, item)
		}
		for _, f := range a.Flags {
			s += "    flag " + f + "\n"
		}
	}
	for _, f := range rep.Flags {
		s += "  flag " + f + "\n"
	}
	return s
}

// SortDeltas orders deltas by auction then character, for stable display.
func SortDeltas(d []Delta) {
	sort.SliceStable(d, func(i, j int) bool {
		if d[i].AuctionID != d[j].AuctionID {
			return d[i].AuctionID < d[j].AuctionID
		}
		return d[i].Character < d[j].Character
	})
}
