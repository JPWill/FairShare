package web

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/JPWill/FairShare/backend/internal/replay"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// Raids that count for history: settled, or settled and then questioned by a later upload.
// Void raids never count; open raids are shown as in progress where it makes sense.
const settledStatuses = `('settled','contested')`

// Player is how a person appears on a page: their Discord display name, linked by their
// first registered character (the URL never carries a Discord id).
type Player struct {
	Name  string
	Char  string
	Class string
}

// mainChar is the person's first registered character, for links.
const mainChar = `(SELECT name FROM character c2 WHERE c2.person_id = %s ORDER BY c2.registered_at, c2.name LIMIT 1)`
const mainClass = `(SELECT COALESCE(class, '') FROM character c2 WHERE c2.person_id = %s ORDER BY c2.registered_at, c2.name LIMIT 1)`

func charOf(col string) string  { return strings.Replace(mainChar, "%s", col, 1) }
func classOf(col string) string { return strings.Replace(mainClass, "%s", col, 1) }

// Sale is one item sold in a settled raid.
type Sale struct {
	RaidID      int64
	Number      int
	Item        string // the item link
	Price       int64
	Owner       Player // who holds it: the transferee when it was transferred
	Transferred bool
	Bids        int
	Date        int64
	Raid        string // event title or instance
}

const saleSelect = `
SELECT a.raid_id, a.auction_id, COALESCE(a.item_link, ''), a.price, a.bids_json,
       COALESCE(a.transferred_to, '') <> '', r.date, COALESCE(NULLIF(e.title, ''), r.instance),
       COALESCE(p.display_name, COALESCE(a.transferred_to, a.winner_character)),
       COALESCE(a.transferred_to, a.winner_character) AS owner, COALESCE(c.class, '')
FROM auction a
JOIN raid r ON r.id = a.raid_id
LEFT JOIN raid_event e ON e.id = r.event_id
LEFT JOIN character c ON c.name = COALESCE(a.transferred_to, a.winner_character)
LEFT JOIN person p ON p.discord_id = c.person_id
WHERE a.kind = 'loot' AND COALESCE(a.winner_character, '') <> '' AND a.flags_json NOT LIKE '%"voided:%'
  AND r.status IN ` + settledStatuses

type bidJSON struct {
	Name    string
	Bid     int64
	Offspec bool
	Valid   bool
	Reason  string
}

func validBids(raw string) []bidJSON {
	var all []bidJSON
	_ = json.Unmarshal([]byte(raw), &all)
	var out []bidJSON
	for _, b := range all {
		if b.Valid {
			out = append(out, b)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Bid > out[j].Bid })
	return out
}

func auctionNumber(id string) int {
	if i := strings.LastIndex(id, "-"); i >= 0 {
		n, _ := strconv.Atoi(id[i+1:])
		return n
	}
	return 0
}

func (s *Server) sales(where string, args []any, order string, limit, offset int) ([]Sale, error) {
	q := saleSelect + where + " ORDER BY " + order + " LIMIT ? OFFSET ?"
	rows, err := s.Store.DB.Query(q, append(args, limit, offset)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Sale
	for rows.Next() {
		var x Sale
		var auctionID, bids string
		if err := rows.Scan(&x.RaidID, &auctionID, &x.Item, &x.Price, &bids, &x.Transferred, &x.Date, &x.Raid, &x.Owner.Name, &x.Owner.Char, &x.Owner.Class); err != nil {
			return nil, err
		}
		x.Number = auctionNumber(auctionID)
		x.Bids = len(validBids(bids))
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Server) count(q string, args ...any) int64 {
	var n sql.NullInt64
	_ = s.Store.DB.QueryRow(q, args...).Scan(&n)
	return n.Int64
}

// ---------------------------------------------------------------- overview

// Stats are the headline numbers on the front page.
type Stats struct {
	Members     int64
	Raids       int64
	Kills       int64
	ItemsSold   int64
	SharesSpent int64
}

func (s *Server) stats() Stats {
	return Stats{
		Members:     s.count(`SELECT COUNT(DISTINCT person_id) FROM character`),
		Raids:       s.count(`SELECT COUNT(*) FROM raid WHERE status IN ` + settledStatuses),
		Kills:       s.count(`SELECT SUM(k) FROM (SELECT MAX(at.kills_present) AS k FROM attendance at JOIN raid r ON r.id = at.raid_id WHERE r.status IN ` + settledStatuses + ` GROUP BY at.raid_id)`),
		ItemsSold:   s.count(`SELECT COUNT(*) FROM (` + saleSelect + `)`),
		SharesSpent: s.count(`SELECT SUM(price) FROM (` + saleSelect + `)`),
	}
}

// ---------------------------------------------------------------- raids

// RaidRow is one raid in a list.
type RaidRow struct {
	ID        int64
	Date      int64
	Title     string
	Status    string
	Leader    Player
	Attendees int64
	Kills     int64
	Items     int64
	Spent     int64
}

func (s *Server) raidRows(order string, limit, offset int) ([]RaidRow, error) {
	if order == "" {
		order = "r.date DESC, r.id DESC"
	}
	// order comes from raidSort (whitelisted keys), never from the request as text.
	rows, err := s.Store.DB.Query(`
SELECT r.id, r.date, COALESCE(NULLIF(e.title, ''), r.instance), r.status,
       COALESCE(lp.display_name, ''), COALESCE(`+charOf("r.leader_person_id")+`, ''), COALESCE(`+classOf("r.leader_person_id")+`, ''),
       (SELECT COUNT(*) FROM attendance at WHERE at.raid_id = r.id AND at.standby = 0),
       (SELECT COALESCE(MAX(at.kills_present), 0) FROM attendance at WHERE at.raid_id = r.id),
       (SELECT COUNT(*) FROM auction a WHERE a.raid_id = r.id AND a.kind = 'loot' AND COALESCE(a.winner_character, '') <> '' AND a.flags_json NOT LIKE '%"voided:%'),
       (SELECT COALESCE(SUM(a.price), 0) FROM auction a WHERE a.raid_id = r.id AND a.kind = 'loot' AND COALESCE(a.winner_character, '') <> '' AND a.flags_json NOT LIKE '%"voided:%')
FROM raid r
LEFT JOIN raid_event e ON e.id = r.event_id
LEFT JOIN person lp ON lp.discord_id = r.leader_person_id
WHERE r.status <> 'void'
ORDER BY `+order+` LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RaidRow
	for rows.Next() {
		var x RaidRow
		if err := rows.Scan(&x.ID, &x.Date, &x.Title, &x.Status, &x.Leader.Name, &x.Leader.Char, &x.Leader.Class, &x.Attendees, &x.Kills, &x.Items, &x.Spent); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RaidDetail is everything the raid page shows.
type RaidDetail struct {
	RaidRow
	Live        bool  // still open: the lists come from the leader's newest live update
	LiveAt      int64 // when that update arrived
	Attendance  []AttendRow
	Auctions    []AuctionRow
	Bosses      []KillRow
	Performance []PerfRow
}

// AttendRow is one person's part in a raid.
type AttendRow struct {
	Player     Player
	Characters []Player // every character they were seen on, first-seen order
	Kills      int64
	FullRun    bool
	Standby    bool
	Net        int64 // shares gained or spent in this raid
	HasNet     bool
}

// AuctionRow is one auction on the raid page.
type AuctionRow struct {
	Number      int
	Item        string
	Test        bool
	Status      string // Sold | No bids | Voided | Abandoned | Bidding
	Winner      Player
	Price       int64
	Transferred Player
	Bids        []BidRow
}

// BidRow is one revealed bid.
type BidRow struct {
	Player  Player
	Bid     int64
	Offspec bool
}

// KillRow is one boss kill.
type KillRow struct {
	Name    string
	At      int64
	Present int
}

// PerfRow is one person's performance score for a raid.
type PerfRow struct {
	Player     Player
	Raid       int64
	RaidTitle  string
	Date       int64
	Character  Player // the character scored; the main when a row predates the column
	Spec       string
	Percentile float64
	Band       string
	Award      int64
}

func (s *Server) raidDetail(id int64) (*RaidDetail, error) {
	var d RaidDetail
	var snapText, snapSum string
	var settledUpload sql.NullInt64
	err := s.Store.DB.QueryRow(`
SELECT r.id, r.date, COALESCE(NULLIF(e.title, ''), r.instance), r.status,
       COALESCE(lp.display_name, ''), COALESCE(`+charOf("r.leader_person_id")+`, ''), COALESCE(`+classOf("r.leader_person_id")+`, ''),
       r.snapshot_text, r.snapshot_checksum, r.settled_upload_id
FROM raid r
LEFT JOIN raid_event e ON e.id = r.event_id
LEFT JOIN person lp ON lp.discord_id = r.leader_person_id
WHERE r.id = ? AND r.status <> 'void'`, id).Scan(&d.ID, &d.Date, &d.Title, &d.Status, &d.Leader.Name, &d.Leader.Char, &d.Leader.Class, &snapText, &snapSum, &settledUpload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	players := s.characterPlayers()
	who := func(char string) Player {
		if p, ok := players[char]; ok {
			return p
		}
		return Player{Name: char}
	}

	settled := d.Status == "settled" || d.Status == "contested"
	var transcriptText string
	if settled {
		if settledUpload.Valid {
			_ = s.Store.DB.QueryRow(`SELECT text FROM transcript_upload WHERE id = ?`, settledUpload.Int64).Scan(&transcriptText)
		}
		if err := s.settledRaid(&d, who); err != nil {
			return nil, err
		}
	} else {
		var at int64
		if err := s.Store.DB.QueryRow(`SELECT text, updated_at FROM raid_progress WHERE raid_id = ?`, id).Scan(&transcriptText, &at); err == nil {
			d.Live, d.LiveAt = true, at
		}
	}

	// Kills (and, for an open raid, everything) come from replaying the transcript against
	// the raid's own snapshot, exactly as settlement does.
	if transcriptText != "" {
		if rep := replayFor(snapText, snapSum, transcriptText); rep != nil {
			for _, k := range rep.Kills {
				d.Bosses = append(d.Bosses, KillRow{Name: k.EncounterName, At: k.T, Present: len(k.Roster)})
			}
			if !settled {
				liveRaid(&d, rep, who)
			}
		}
	}
	return &d, nil
}

func replayFor(snapText, checksum, transcriptText string) *replay.Report {
	snap, err := snapshot.Parse(snapText)
	if err != nil {
		return nil
	}
	tr, err := replay.ParseTranscript(transcriptText)
	if err != nil {
		return nil
	}
	rep, err := replay.Run(snap, checksum, tr, replay.Options{})
	if err != nil {
		return nil
	}
	return rep
}

func (s *Server) settledRaid(d *RaidDetail, who func(string) Player) error {
	rows, err := s.Store.DB.Query(`
SELECT p.display_name, COALESCE(`+charOf("at.person_id")+`, ''), COALESCE(`+classOf("at.person_id")+`, ''),
       at.kills_present, at.full_run, at.standby, COALESCE(at.characters, ''),
       COALESCE((SELECT SUM(l.delta) FROM ledger l WHERE l.raid_id = at.raid_id AND l.person_id = at.person_id), 0)
FROM attendance at JOIN person p ON p.discord_id = at.person_id
WHERE at.raid_id = ?
ORDER BY at.standby, at.kills_present DESC, p.display_name COLLATE NOCASE`, d.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var a AttendRow
		var chars string
		if err := rows.Scan(&a.Player.Name, &a.Player.Char, &a.Player.Class, &a.Kills, &a.FullRun, &a.Standby, &chars, &a.Net); err != nil {
			rows.Close()
			return err
		}
		a.Characters = splitPlayers(chars, who)
		a.HasNet = true
		d.Attendance = append(d.Attendance, a)
	}
	rows.Close()

	rows, err = s.Store.DB.Query(`SELECT auction_id, kind, COALESCE(item_link, ''), COALESCE(winner_character, ''), price, COALESCE(transferred_to, ''), bids_json, flags_json FROM auction WHERE raid_id = ?`, d.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, kind, link, winner, to, bids, flags string
		var price int64
		if err := rows.Scan(&id, &kind, &link, &winner, &price, &to, &bids, &flags); err != nil {
			rows.Close()
			return err
		}
		a := AuctionRow{Number: auctionNumber(id), Item: link, Test: kind == "test"}
		switch {
		case strings.Contains(flags, `"voided:`):
			a.Status = "Voided"
		case strings.Contains(flags, `"abandoned:`):
			a.Status = "Abandoned"
		case winner == "":
			a.Status = "No bids"
		default:
			a.Status = "Sold"
			a.Winner, a.Price = who(winner), price
		}
		if to != "" {
			a.Transferred = who(to)
		}
		for _, b := range validBids(bids) {
			a.Bids = append(a.Bids, BidRow{Player: who(b.Name), Bid: b.Bid, Offspec: b.Offspec})
		}
		d.Auctions = append(d.Auctions, a)
		if !a.Test && a.Status == "Sold" {
			d.Items++
			d.Spent += price
		}
	}
	rows.Close()
	sort.Slice(d.Auctions, func(i, j int) bool { return d.Auctions[i].Number < d.Auctions[j].Number })
	for _, a := range d.Attendance {
		if !a.Standby {
			d.Attendees++
		}
		if a.Kills > d.Kills {
			d.Kills = a.Kills
		}
	}

	perf, err := s.performance(`WHERE pf.raid_id = ?`, d.ID)
	if err != nil {
		return err
	}
	d.Performance = perf
	return nil
}

// liveRaid fills an open raid's page from its newest live update. Provisional: nothing is
// settled until the leader finishes the raid.
func liveRaid(d *RaidDetail, rep *replay.Report, who func(string) Player) {
	present := map[string]int64{}
	for _, k := range rep.Kills {
		for _, n := range k.Roster {
			present[n]++
		}
	}
	names := make([]string, 0, len(present))
	for n := range present {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if present[names[i]] != present[names[j]] {
			return present[names[i]] > present[names[j]]
		}
		return strings.ToLower(names[i]) < strings.ToLower(names[j])
	})
	for _, n := range names {
		d.Attendance = append(d.Attendance, AttendRow{Player: who(n), Kills: present[n], FullRun: present[n] == int64(len(rep.Kills))})
	}
	d.Attendees, d.Kills = int64(len(names)), int64(len(rep.Kills))
	for _, a := range rep.Auctions {
		row := AuctionRow{Number: auctionNumber(a.ID), Item: a.ItemLink, Test: a.Kind == "test"}
		switch {
		case a.Voided:
			row.Status = "Voided"
		case a.Abandoned:
			row.Status = "Abandoned"
		case a.Result == nil:
			row.Status = "Bidding"
		case a.Winner == "":
			row.Status = "No bids"
		default:
			row.Status = "Sold"
			row.Winner, row.Price = who(a.Winner), a.Price
		}
		if a.TransferredTo != "" {
			row.Transferred = who(a.TransferredTo)
		}
		if a.Result != nil {
			for _, r := range a.Reveals {
				if r.Valid {
					row.Bids = append(row.Bids, BidRow{Player: who(r.Name), Bid: r.Bid, Offspec: r.Offspec})
				}
			}
			sort.SliceStable(row.Bids, func(i, j int) bool { return row.Bids[i].Bid > row.Bids[j].Bid })
		}
		d.Auctions = append(d.Auctions, row)
		if !row.Test && row.Status == "Sold" {
			d.Items++
			d.Spent += a.Price
		}
	}
	sort.Slice(d.Auctions, func(i, j int) bool { return d.Auctions[i].Number < d.Auctions[j].Number })
}

// characterPlayers maps every registered character to how its owner is shown.
func (s *Server) characterPlayers() map[string]Player {
	out := map[string]Player{}
	rows, err := s.Store.DB.Query(`SELECT c.name, COALESCE(c.class, ''), p.display_name FROM character c JOIN person p ON p.discord_id = c.person_id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name, class, display string
		if rows.Scan(&name, &class, &display) == nil {
			out[name] = Player{Name: display, Char: name, Class: class}
		}
	}
	return out
}

func (s *Server) performance(where string, args ...any) ([]PerfRow, error) {
	players := s.characterPlayers() // before the query: a second query while rows are open deadlocks a single-connection database
	rows, err := s.Store.DB.Query(`
SELECT p.display_name, COALESCE(`+charOf("pf.person_id")+`, ''), COALESCE(`+classOf("pf.person_id")+`, ''),
       pf.raid_id, COALESCE(NULLIF(e.title, ''), r.instance), r.date,
       COALESCE(pf.spec, ''), COALESCE(pf.percentile, 0), COALESCE(pf.band, ''), pf.award, COALESCE(pf.character, '')
FROM performance pf
JOIN person p ON p.discord_id = pf.person_id
JOIN raid r ON r.id = pf.raid_id
LEFT JOIN raid_event e ON e.id = r.event_id
`+where+` AND r.status IN `+settledStatuses+`
ORDER BY r.date DESC, pf.percentile DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PerfRow
	for rows.Next() {
		var x PerfRow
		var char string
		if err := rows.Scan(&x.Player.Name, &x.Player.Char, &x.Player.Class, &x.Raid, &x.RaidTitle, &x.Date, &x.Spec, &x.Percentile, &x.Band, &x.Award, &char); err != nil {
			return nil, err
		}
		x.Character = x.Player
		if char != "" {
			if p, ok := players[char]; ok {
				x.Character = p
			} else {
				x.Character = Player{Name: x.Player.Name, Char: char}
			}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- leaderboards

// Rank is one line of a leaderboard.
type Rank struct {
	Player Player
	Value  int64
	Extra  string
}

// Board is one leaderboard.
type Board struct {
	Title string
	Note  string // what the number means
	Unit  string
	Rows  []Rank
}

func (s *Server) rank(q string, args ...any) ([]Rank, error) {
	rows, err := s.Store.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rank
	for rows.Next() {
		var x Rank
		if err := rows.Scan(&x.Player.Name, &x.Player.Char, &x.Player.Class, &x.Value, &x.Extra); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

const personCols = `p.display_name, ` + `COALESCE((SELECT name FROM character c2 WHERE c2.person_id = p.discord_id ORDER BY c2.registered_at, c2.name LIMIT 1), ''), COALESCE((SELECT COALESCE(class, '') FROM character c2 WHERE c2.person_id = p.discord_id ORDER BY c2.registered_at, c2.name LIMIT 1), '')`

// registered limits a person query to people with at least one character (excludes the
// placeholder persons the bot creates before registration, and the pool accounts).
const registered = `EXISTS (SELECT 1 FROM character c3 WHERE c3.person_id = p.discord_id)`

func (s *Server) boards(limit int) ([]Board, error) {
	type spec struct {
		title, note, unit, q string
	}
	specs := []spec{
		{"Lifetime earned", "Every share ever paid to them: their cut of each sale plus performance awards. Balance is this minus shares spent on loot.", "shares",
			`SELECT ` + personCols + `, SUM(l.delta), '' FROM ledger l JOIN person p ON p.discord_id = l.person_id WHERE l.reason IN ('share', 'award') GROUP BY p.discord_id HAVING SUM(l.delta) > 0 ORDER BY SUM(l.delta) DESC, p.display_name COLLATE NOCASE LIMIT ?`},
		{"Boss kills", "Boss kills attended, plus a bonus for each full run. Not currency and never spent; decides invite priority and tiebreaks.", "kills",
			`SELECT ` + personCols + `, p.lifetime_shares, '' FROM person p WHERE ` + registered + ` AND p.lifetime_shares > 0 ORDER BY p.lifetime_shares DESC, p.display_name COLLATE NOCASE LIMIT ?`},
		{"Raids attended", "Settled raids with at least one kill or a standby place.", "raids",
			`SELECT ` + personCols + `, p.raids_attended, '' FROM person p WHERE ` + registered + ` AND p.raids_attended > 0 ORDER BY p.raids_attended DESC, p.display_name COLLATE NOCASE LIMIT ?`},
		{"Full runs", "Raids attended from the first boss to the last.", "runs",
			`SELECT ` + personCols + `, SUM(at.full_run), '' FROM attendance at JOIN person p ON p.discord_id = at.person_id JOIN raid r ON r.id = at.raid_id WHERE r.status IN ` + settledStatuses + ` GROUP BY p.discord_id HAVING SUM(at.full_run) > 0 ORDER BY SUM(at.full_run) DESC, p.display_name COLLATE NOCASE LIMIT ?`},
		{"Current balance", "Shares on hand now. Balances sum to zero across the community.", "shares",
			`SELECT ` + personCols + `, p.balance, '' FROM person p WHERE ` + registered + ` AND p.balance > 0 ORDER BY p.balance DESC, p.display_name COLLATE NOCASE LIMIT ?`},
		{"Items won", "Loot bought at auction (transfers count for whoever ended up with the item).", "items",
			`SELECT ` + personCols + `, COUNT(*), '' FROM (` + saleSelect + `) sale JOIN character c4 ON c4.name = sale.owner JOIN person p ON p.discord_id = c4.person_id GROUP BY p.discord_id ORDER BY COUNT(*) DESC, p.display_name COLLATE NOCASE LIMIT ?`},
		{"Shares spent on loot", "Total paid at auction; every share spent was paid out to the raid.", "shares",
			`SELECT ` + personCols + `, SUM(sale.price), '' FROM (` + saleSelect + `) sale JOIN character c4 ON c4.name = sale.owner JOIN person p ON p.discord_id = c4.person_id GROUP BY p.discord_id ORDER BY SUM(sale.price) DESC, p.display_name COLLATE NOCASE LIMIT ?`},
	}
	var out []Board
	for _, sp := range specs {
		rows, err := s.rank(sp.q, limit)
		if err != nil {
			return nil, err
		}
		out = append(out, Board{Title: sp.title, Note: sp.note, Unit: sp.unit, Rows: rows})
	}
	return out, nil
}

// performers ranks by average percentile over scored raids (at least minRaids of them).
func (s *Server) performers(limit, minRaids int) ([]Rank, error) {
	return s.rank(`SELECT `+personCols+`, CAST(ROUND(AVG(pf.percentile)) AS INTEGER), COUNT(*) || ' raids, ' || SUM(pf.award) || ' shares awarded'
FROM performance pf JOIN person p ON p.discord_id = pf.person_id JOIN raid r ON r.id = pf.raid_id
WHERE r.status IN `+settledStatuses+` AND pf.percentile IS NOT NULL
GROUP BY p.discord_id HAVING COUNT(*) >= ?
ORDER BY AVG(pf.percentile) DESC, p.display_name COLLATE NOCASE LIMIT ?`, minRaids, limit)
}

// ---------------------------------------------------------------- players

// earnedCol sums every share ever paid to a person: their cut of each sale and performance
// awards. Balance equals this minus purchases and penalties, which is the question the
// player page answers.
const earnedCol = `COALESCE((SELECT SUM(l.delta) FROM ledger l WHERE l.person_id = p.discord_id AND l.reason IN ('share', 'award')), 0)`

// Directory is one person in the player list.
type Directory struct {
	Player     Player
	Characters []Player
	Raids      int64
	Lifetime   int64 // standing: kills attended plus full-run bonuses
	Earned     int64 // every share ever paid to them (sales and awards)
	Balance    int64
}

func (s *Server) directory(q, order string) ([]Directory, error) {
	if order == "" {
		order = "p.display_name COLLATE NOCASE"
	}
	where := registered
	var args []any
	if q != "" {
		where += ` AND (p.display_name LIKE ? ESCAPE '\' OR EXISTS (SELECT 1 FROM character c5 WHERE c5.person_id = p.discord_id AND c5.name LIKE ? ESCAPE '\'))`
		args = append(args, likeArg(q), likeArg(q))
	}
	rows, err := s.Store.DB.Query(`SELECT p.discord_id, `+personCols+`, p.raids_attended, p.lifetime_shares, p.balance, `+earnedCol+` FROM person p WHERE `+where+` ORDER BY `+order+` LIMIT 500`, args...)
	if err != nil {
		return nil, err
	}
	var out []Directory
	var ids []string
	for rows.Next() {
		var x Directory
		var id string
		if err := rows.Scan(&id, &x.Player.Name, &x.Player.Char, &x.Player.Class, &x.Raids, &x.Lifetime, &x.Balance, &x.Earned); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
		ids = append(ids, id)
	}
	rows.Close()
	for i, id := range ids {
		chars, err := s.characters(id)
		if err != nil {
			return nil, err
		}
		out[i].Characters = chars
	}
	return out, nil
}

func (s *Server) characters(personID string) ([]Player, error) {
	rows, err := s.Store.DB.Query(`SELECT c.name, COALESCE(c.class, ''), p.display_name FROM character c JOIN person p ON p.discord_id = c.person_id WHERE c.person_id = ? ORDER BY c.registered_at, c.name`, personID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Player
	for rows.Next() {
		var x Player
		if err := rows.Scan(&x.Char, &x.Class, &x.Name); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Profile is a player's own page.
type Profile struct {
	Player      Player
	Characters  []Player
	Balance     int64
	Credit      int64
	MaxBid      int64
	Raids       int64
	Lifetime    int64 // standing
	Earned      int64 // every share ever paid to them
	ItemsWon    int
	Spent       int64
	History     []HistoryRow
	Loot        []Sale
	Performance []PerfRow
	AvgPct      float64
	Ledger      []LedgerRow
}

// HistoryRow is one raid in a player's history.
type HistoryRow struct {
	RaidID     int64
	Date       int64
	Title      string
	Characters []Player // which character(s) they attended on
	Kills      int64
	FullRun    bool
	Standby    bool
	Net        int64
}

// LedgerRow is one ledger entry on a player's page.
type LedgerRow struct {
	At        int64
	RaidID    int64
	Reason    string
	Delta     int64
	Character Player // zero when the line was not earned on a character
}

func (s *Server) profile(character string) (*Profile, error) {
	personID, err := s.People.PersonByCharacter(character)
	if err != nil {
		return nil, nil // unknown character: not found
	}
	p, err := s.People.Get(personID)
	if err != nil {
		return nil, nil
	}
	pr := &Profile{Balance: p.Balance, Raids: p.RaidsAttended, Lifetime: p.LifetimeShares}
	_ = s.Store.DB.QueryRow(`SELECT `+earnedCol+` FROM person p WHERE p.discord_id = ?`, personID).Scan(&pr.Earned)
	pr.Credit = s.People.Credit(p)
	pr.MaxBid = pr.Balance + pr.Credit
	if pr.Characters, err = s.characters(personID); err != nil {
		return nil, err
	}
	if len(pr.Characters) == 0 {
		return nil, nil
	}
	pr.Player = pr.Characters[0]
	players := s.characterPlayers()
	who := func(char string) Player {
		if p, ok := players[char]; ok {
			return p
		}
		return Player{Name: char, Char: char}
	}

	rows, err := s.Store.DB.Query(`
SELECT r.id, r.date, COALESCE(NULLIF(e.title, ''), r.instance), at.kills_present, at.full_run, at.standby, COALESCE(at.characters, ''),
       COALESCE((SELECT SUM(l.delta) FROM ledger l WHERE l.raid_id = r.id AND l.person_id = at.person_id), 0)
FROM attendance at JOIN raid r ON r.id = at.raid_id LEFT JOIN raid_event e ON e.id = r.event_id
WHERE at.person_id = ? AND r.status IN `+settledStatuses+`
ORDER BY r.date DESC, r.id DESC LIMIT 200`, personID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h HistoryRow
		var chars string
		if err := rows.Scan(&h.RaidID, &h.Date, &h.Title, &h.Kills, &h.FullRun, &h.Standby, &chars, &h.Net); err != nil {
			rows.Close()
			return nil, err
		}
		h.Characters = splitPlayers(chars, who)
		pr.History = append(pr.History, h)
	}
	rows.Close()

	chars := make([]any, 0, len(pr.Characters))
	marks := make([]string, 0, len(pr.Characters))
	for _, c := range pr.Characters {
		chars = append(chars, c.Char)
		marks = append(marks, "?")
	}
	if pr.Loot, err = s.sales(` AND COALESCE(a.transferred_to, a.winner_character) IN (`+strings.Join(marks, ",")+`)`, chars, "r.date DESC, a.auction_id DESC", 500, 0); err != nil {
		return nil, err
	}
	pr.ItemsWon = len(pr.Loot)
	for _, l := range pr.Loot {
		pr.Spent += l.Price
	}

	if pr.Performance, err = s.performance(`WHERE pf.person_id = ?`, personID); err != nil {
		return nil, err
	}
	var sum float64
	for _, x := range pr.Performance {
		sum += x.Percentile
	}
	if n := len(pr.Performance); n > 0 {
		pr.AvgPct = sum / float64(n)
	}

	lines, err := s.People.Ledger(personID, 100)
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		pr.Ledger = append(pr.Ledger, LedgerRow{At: l.CreatedAt, RaidID: l.RaidID, Reason: l.Reason, Delta: l.Delta, Character: playerOrNone(l.Character, who)})
	}
	return pr, nil
}

// splitPlayers turns attendance.characters ("Main,Alt", first-seen order) into players.
func splitPlayers(csv string, who func(string) Player) []Player {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var out []Player
	for _, n := range strings.Split(csv, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, who(n))
		}
	}
	return out
}

// playerOrNone is who(name), or the zero Player for a line not earned on a character.
func playerOrNone(name string, who func(string) Player) Player {
	if name == "" {
		return Player{}
	}
	return who(name)
}
