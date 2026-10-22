// Package raids covers the raid lifecycle: events and signups, opening a raid (issuing
// its snapshot), accepting transcript uploads, and settling or disputing the result.
package raids

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/JPWill/FairShare/backend/internal/bans"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/replay"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// Service is the raids service.
type Service struct {
	S      *store.Store
	People *people.Service
	Bans   *bans.Service
	// OnFirstAttendance is called with persons who just got their first settled raid
	// (the bot grants the Raider role).
	OnFirstAttendance func(personIDs []string)
}

// Event is a scheduled raid with signups.
type Event struct {
	ID                int64
	Title             string
	Instance          string
	StartsAt          int64
	CreatedBy         string
	ChannelID         string
	MessageID         string
	Cap               int64
	StandbyCap        int64
	Status            string // open | started | cancelled
	RaidID            int64
	RepeatDays        int64  // 0, 7 or 14
	ParentEventID     int64  // the occurrence this one was spawned from
	RosterConfirmedAt int64  // 0 until officers confirmed the roster
	Notes             string // officer notes shown on the signup embed
	RaidChannelID     string // the event's own Discord channel (#tuesday-7pm-mc), "" before the bot makes it
}

// RosterConfirmed reports whether officers locked the roster.
func (e Event) RosterConfirmed() bool { return e.RosterConfirmedAt != 0 }

// Signup is one person's signup on an event.
type Signup struct {
	PersonID  string
	Character string
	Class     string
	Role      string // tank | healer | melee | ranged ("" for a pure standby signup)
	Status    string // confirmed | standby | withdrawn
	SignedAt  int64
}

// Raid is one opened raid.
type Raid struct {
	ID               int64
	EventID          int64
	Instance         string
	Date             int64
	LeaderPersonID   string
	SnapshotText     string
	SnapshotChecksum string
	Status           string // issued | in_progress | settled | disputed | contested | void
	SettledAt        int64
	SettleNote       string
	LiveMessageID    string // Discord message the bot keeps editing with the live ledger
	SettledUploadID  int64  // the transcript_upload row the current settle was computed from (0 if none)
	SettleGeneration int64  // how many times the raid has been settled; ledger lines carry it
}

// Outcome of a transcript upload.
type Outcome struct {
	Status   string // accepted | settled | disputed
	Message  string
	Report   *replay.Report
	Deltas   []replay.Delta
	Diffs    []string
	Uploads  int
	Balances map[string]int64 // display name -> balance after settle (settled only)
}

var ErrUnsettled = errors.New("an unsettled raid blocks this")

// ---------------------------------------------------------------- events

// CreateEvent stores a raid event. repeatDays is 0, 7 or 14; parentID links a spawned
// occurrence to the one it came from (0 for a fresh event).
func (r *Service) CreateEvent(title, instance string, startsAt int64, by string, cap, standbyCap, repeatDays, parentID int64) (Event, error) {
	if cap <= 0 {
		cap = 20
	}
	if standbyCap < 0 {
		standbyCap = 0
	}
	if repeatDays != 0 && repeatDays != 7 && repeatDays != 14 {
		return Event{}, errors.New("repeat must be none, weekly or biweekly")
	}
	res, err := r.S.DB.Exec(`INSERT INTO raid_event (title, instance, starts_at, created_by, created_at, cap, standby_cap, status, repeat_days, parent_event_id) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?)`,
		strings.TrimSpace(title), strings.TrimSpace(instance), startsAt, by, store.Now(), cap, standbyCap, repeatDays, nullInt(parentID))
	if err != nil {
		return Event{}, err
	}
	id, _ := res.LastInsertId()
	r.S.Audit(by, "event_create", fmt.Sprintf("event %d %q %s at %d repeat %d", id, title, instance, startsAt, repeatDays))
	return r.Event(id)
}

// NextOccurrence spawns the following occurrence of a repeating event, once. It returns
// the new event, or (Event{}, false) when the event does not repeat or the next one
// already exists.
func (r *Service) NextOccurrence(ev Event) (Event, bool, error) {
	if ev.RepeatDays <= 0 {
		return Event{}, false, nil
	}
	var n int
	if err := r.S.DB.QueryRow(`SELECT COUNT(*) FROM raid_event WHERE parent_event_id = ?`, ev.ID).Scan(&n); err != nil {
		return Event{}, false, err
	}
	if n > 0 {
		return Event{}, false, nil
	}
	next, err := r.CreateEvent(ev.Title, ev.Instance, ev.StartsAt+ev.RepeatDays*86400, ev.CreatedBy, ev.Cap, ev.StandbyCap, ev.RepeatDays, ev.ID)
	if err != nil {
		return Event{}, false, err
	}
	if ev.Notes != "" {
		_, _ = r.S.DB.Exec(`UPDATE raid_event SET notes = ? WHERE id = ?`, ev.Notes, next.ID)
		next.Notes = ev.Notes
	}
	if ev.RaidChannelID != "" {
		_, _ = r.S.DB.Exec(`UPDATE raid_event SET raid_channel_id = ? WHERE id = ?`, ev.RaidChannelID, next.ID)
		next.RaidChannelID = ev.RaidChannelID
	}
	return next, true, nil
}

// UpdateEventDetails changes the words and sizes of an open event.
func (r *Service) UpdateEventDetails(id int64, title, instance string, cap, standbyCap int64, notes, by string) error {
	title, instance = strings.TrimSpace(title), strings.TrimSpace(instance)
	if title == "" || instance == "" {
		return errors.New("title and instance are required")
	}
	if cap <= 0 {
		cap = 20
	}
	if standbyCap < 0 {
		standbyCap = 0
	}
	res, err := r.S.DB.Exec(`UPDATE raid_event SET title = ?, instance = ?, cap = ?, standby_cap = ?, notes = ? WHERE id = ? AND status = 'open'`,
		title, instance, cap, standbyCap, strings.TrimSpace(notes), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("that event has already started or was cancelled")
	}
	r.S.Audit(by, "event_edit", fmt.Sprintf("%d: %s (%s) cap %d standby %d", id, title, instance, cap, standbyCap))
	return nil
}

// RescheduleEvent moves an open event and/or changes how it repeats.
func (r *Service) RescheduleEvent(id, startsAt, repeatDays int64, by string) error {
	res, err := r.S.DB.Exec(`UPDATE raid_event SET starts_at = ?, repeat_days = ? WHERE id = ? AND status = 'open'`, startsAt, repeatDays, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("that event has already started or was cancelled")
	}
	r.S.Audit(by, "event_reschedule", fmt.Sprintf("%d: %d repeat %d", id, startsAt, repeatDays))
	return nil
}

// DueRepeats lists repeating events whose start has passed (or that started/cancelled)
// and that have no next occurrence yet.
func (r *Service) DueRepeats(now int64) ([]Event, error) {
	rows, err := r.S.DB.Query(`SELECT `+evCols+` FROM raid_event e WHERE repeat_days > 0 AND (starts_at <= ? OR status <> 'open')
		AND NOT EXISTS (SELECT 1 FROM raid_event c WHERE c.parent_event_id = e.id) ORDER BY starts_at`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

const evCols = "id, title, instance, starts_at, created_by, COALESCE(channel_id,''), COALESCE(message_id,''), cap, standby_cap, status, COALESCE(raid_id,0), repeat_days, COALESCE(parent_event_id,0), COALESCE(roster_confirmed_at,0), COALESCE(notes,''), COALESCE(raid_channel_id,'')"

// SetEventMessage remembers where the signup embed lives.
func (r *Service) SetEventMessage(id int64, channelID, messageID string) error {
	_, err := r.S.DB.Exec(`UPDATE raid_event SET channel_id = ?, message_id = ? WHERE id = ?`, channelID, messageID, id)
	return err
}

// Event loads one event.
func (r *Service) Event(id int64) (Event, error) {
	return scanEvent(r.S.DB.QueryRow(`SELECT `+evCols+` FROM raid_event WHERE id = ?`, id))
}

type scanner interface{ Scan(dest ...any) error }

func scanEvent(s scanner) (Event, error) {
	var e Event
	err := s.Scan(&e.ID, &e.Title, &e.Instance, &e.StartsAt, &e.CreatedBy, &e.ChannelID, &e.MessageID, &e.Cap, &e.StandbyCap, &e.Status, &e.RaidID, &e.RepeatDays, &e.ParentEventID, &e.RosterConfirmedAt, &e.Notes, &e.RaidChannelID)
	if errors.Is(err, sql.ErrNoRows) {
		return e, store.ErrNotFound
	}
	return e, err
}

// OpenEvents lists events that have not started, soonest first.
func (r *Service) OpenEvents() ([]Event, error) {
	rows, err := r.S.DB.Query(`SELECT ` + evCols + ` FROM raid_event WHERE status = 'open' ORDER BY starts_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CancelEvent marks an event cancelled.
func (r *Service) CancelEvent(id int64, by string) error {
	_, err := r.S.DB.Exec(`UPDATE raid_event SET status = 'cancelled' WHERE id = ? AND status = 'open'`, id)
	r.S.Audit(by, "event_cancel", fmt.Sprint(id))
	return err
}

var ErrRosterConfirmed = errors.New("the roster is confirmed; ask an officer to add you")

// Signup adds or changes a person's signup with one of their characters in a role the
// character's class can play. standby requests the standby list regardless of room.
// Returns the resulting status.
func (r *Service) Signup(eventID int64, discordID, displayName, character, role string, standby bool) (string, error) {
	e, err := r.Event(eventID)
	if err != nil {
		return "", err
	}
	if e.Status != "open" {
		return "", errors.New("signups are closed for this raid")
	}
	if e.RosterConfirmed() {
		return "", ErrRosterConfirmed
	}
	if r.Bans.IsBannedPerson(discordID) {
		return "", people.ErrBanned
	}
	chars, err := r.People.Characters(discordID)
	if err != nil {
		return "", err
	}
	if len(chars) == 0 {
		return "", people.ErrNoCharacters
	}
	var chosen *people.Character
	for k := range chars {
		if chars[k].Name == character {
			chosen = &chars[k]
		}
	}
	if chosen == nil {
		return "", errors.New("that character is not registered to you")
	}
	if role != "" && !people.RoleAllowed(chosen.Class, role) {
		return "", fmt.Errorf("a %s cannot sign up as %s", chosen.Class, role)
	}
	if role == "" && !standby {
		return "", errors.New("pick a role")
	}
	ups, err := r.Signups(eventID)
	if err != nil {
		return "", err
	}
	confirmed, standbyCount := 0, 0
	for _, s := range ups {
		if s.PersonID == discordID {
			continue
		}
		switch s.Status {
		case "confirmed":
			confirmed++
		case "standby":
			standbyCount++
		}
	}
	status := "confirmed"
	switch {
	case standby:
		status = "standby"
	case int64(confirmed) >= e.Cap:
		status = "standby"
	}
	if status == "standby" && int64(standbyCount) >= e.StandbyCap && !standby {
		return "", errors.New("the raid and its standby list are full")
	}
	if status == "standby" && int64(standbyCount) >= e.StandbyCap {
		return "", errors.New("the standby list is full")
	}
	_, err = r.S.DB.Exec(`INSERT INTO signup (event_id, person_id, character, class, role, status, signed_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(event_id, person_id) DO UPDATE SET character = excluded.character, class = excluded.class, role = excluded.role, status = excluded.status, signed_at = excluded.signed_at`,
		eventID, discordID, chosen.Name, chosen.Class, role, status, store.Now())
	return status, err
}

// ConfirmRoster locks the roster: the given persons become confirmed, every other
// non-withdrawn signup becomes standby. Calling it again re-confirms with a new set.
func (r *Service) ConfirmRoster(eventID int64, confirmed []string, by string) error {
	e, err := r.Event(eventID)
	if err != nil {
		return err
	}
	if e.Status != "open" {
		return errors.New("that event has already started or was cancelled")
	}
	set := map[string]bool{}
	for _, id := range confirmed {
		set[id] = true
	}
	ups, err := r.Signups(eventID)
	if err != nil {
		return err
	}
	for _, s := range ups {
		status := "standby"
		if set[s.PersonID] {
			status = "confirmed"
		}
		if _, err := r.S.DB.Exec(`UPDATE signup SET status = ? WHERE event_id = ? AND person_id = ?`, status, eventID, s.PersonID); err != nil {
			return err
		}
	}
	if _, err := r.S.DB.Exec(`UPDATE raid_event SET roster_confirmed_at = ? WHERE id = ?`, store.Now(), eventID); err != nil {
		return err
	}
	r.S.Audit(by, "roster_confirm", fmt.Sprintf("event %d: %d confirmed of %d", eventID, len(set), len(ups)))
	return nil
}

// Withdraw removes a person's signup.
func (r *Service) Withdraw(eventID int64, discordID string) error {
	_, err := r.S.DB.Exec(`UPDATE signup SET status = 'withdrawn' WHERE event_id = ? AND person_id = ?`, eventID, discordID)
	return err
}

// Signups lists non-withdrawn signups in order.
func (r *Service) Signups(eventID int64) ([]Signup, error) {
	rows, err := r.S.DB.Query(`SELECT person_id, character, COALESCE(class,''), role, status, signed_at FROM signup WHERE event_id = ? AND status != 'withdrawn' ORDER BY signed_at`, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Signup
	for rows.Next() {
		var s Signup
		if err := rows.Scan(&s.PersonID, &s.Character, &s.Class, &s.Role, &s.Status, &s.SignedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- raids

// UnsettledFor lists raids this person leads or attended (as signup) that are not
// settled: issued, in progress, disputed, or contested after a settle.
func (r *Service) UnsettledFor(discordID string) ([]int64, error) {
	rows, err := r.S.DB.Query(`
		SELECT DISTINCT raid.id FROM raid
		LEFT JOIN raid_event ev ON ev.raid_id = raid.id
		LEFT JOIN signup s ON s.event_id = ev.id AND s.status != 'withdrawn'
		WHERE raid.status IN ('issued','in_progress','disputed','contested') AND (raid.leader_person_id = ? OR s.person_id = ?)
		ORDER BY raid.id`, discordID, discordID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// BuildSnapshot assembles the standings for a new raid. roster may be nil.
func (r *Service) BuildSnapshot(raidID string, roster []snapshot.RosterEntry) (snapshot.Snapshot, error) {
	s := snapshot.Snapshot{RaidID: raidID, Issued: store.Now(), OffspecPct: r.S.ConfigInt("offspecPct"), Floors: map[string]int64{}, Roster: roster}
	rows, err := r.S.DB.Query(`SELECT slot, value FROM floor_price`)
	if err != nil {
		return s, err
	}
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return s, err
		}
		s.Floors[k] = v
	}
	rows.Close()
	if _, ok := s.Floors["DEFAULT"]; !ok {
		s.Floors["DEFAULT"] = 10
	}
	banned, err := r.Bans.BannedCharacters()
	if err != nil {
		return s, err
	}
	s.Banned = banned
	chars, err := r.People.AllCharacters()
	if err != nil {
		return s, err
	}
	persons := map[string]people.Person{}
	for _, c := range chars {
		p, ok := persons[c.PersonID]
		if !ok {
			p, err = r.People.Get(c.PersonID)
			if err != nil {
				return s, err
			}
			persons[c.PersonID] = p
		}
		s.Rows = append(s.Rows, snapshot.Row{Character: c.Name, Owner: p.DisplayName, Balance: p.Balance, Credit: r.People.Credit(p)})
	}
	return s, nil
}

// Open creates a raid and issues its snapshot. eventID may be 0 for an ad-hoc raid.
// A 5-player party counts as the raid group only while the officers' "5-player groups"
// switch (config allowParty) is on, for testing before raids unlock.
func (r *Service) Open(eventID int64, instance, leaderDiscordID string) (Raid, error) {
	return r.open(eventID, instance, leaderDiscordID, strings.EqualFold(r.S.ConfigString("allowParty"), "true"))
}

func (r *Service) open(eventID int64, instance, leaderDiscordID string, party bool) (Raid, error) {
	if ids, err := r.UnsettledFor(leaderDiscordID); err != nil {
		return Raid{}, err
	} else if len(ids) > 0 {
		return Raid{}, fmt.Errorf("%w: raid %d led or attended by you is not settled", ErrUnsettled, ids[0])
	}
	var roster []snapshot.RosterEntry
	var standbyPersons []string
	starts := store.Now() // an ad-hoc raid starts when it is opened
	if eventID != 0 {
		e, err := r.Event(eventID)
		if err != nil {
			return Raid{}, err
		}
		if e.Status != "open" {
			return Raid{}, errors.New("that event has already started or was cancelled")
		}
		starts = e.StartsAt
		if instance == "" {
			instance = e.Instance
		}
		ups, err := r.Signups(eventID)
		if err != nil {
			return Raid{}, err
		}
		for _, s := range ups {
			if ids, err := r.UnsettledFor(s.PersonID); err != nil {
				return Raid{}, err
			} else if len(ids) > 0 {
				return Raid{}, fmt.Errorf("%w: attendee %s has unsettled raid %d", ErrUnsettled, s.Character, ids[0])
			}
			if e.RosterConfirmed() {
				roster = append(roster, snapshot.RosterEntry{Name: s.Character, Role: s.Role, Status: s.Status})
				if s.Status == "standby" {
					standbyPersons = append(standbyPersons, s.PersonID)
				}
			}
		}
	}
	// Reads and writes are kept out of one transaction on purpose: the pool has a
	// single connection, so a query inside a transaction would deadlock.
	res, err := r.S.DB.Exec(`INSERT INTO raid (event_id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status) VALUES (?, ?, ?, ?, '', '', 'issued')`,
		nullInt(eventID), instance, store.Now(), leaderDiscordID)
	if err != nil {
		return Raid{}, err
	}
	id, _ := res.LastInsertId()
	snap, err := r.BuildSnapshot(fmt.Sprint(id), roster)
	if err != nil {
		_, _ = r.S.DB.Exec(`DELETE FROM raid WHERE id = ?`, id)
		return Raid{}, err
	}
	snap.Starts, snap.Party = starts, party
	for _, pid := range standbyPersons {
		_, _ = r.S.DB.Exec(`INSERT INTO attendance (raid_id, person_id, kills_present, standby, full_run) VALUES (?, ?, 0, 1, 0) ON CONFLICT(raid_id, person_id) DO UPDATE SET standby = 1`, id, pid)
	}
	text := snap.Text()
	cs := snapshot.Checksum(text)
	if _, err := r.S.DB.Exec(`UPDATE raid SET snapshot_text = ?, snapshot_checksum = ? WHERE id = ?`, text, cs, id); err != nil {
		return Raid{}, err
	}
	if eventID != 0 {
		if _, err := r.S.DB.Exec(`UPDATE raid_event SET status = 'started', raid_id = ? WHERE id = ?`, id, eventID); err != nil {
			return Raid{}, err
		}
	}
	raid := Raid{ID: id, EventID: eventID, Instance: instance, Date: store.Now(), LeaderPersonID: leaderDiscordID, SnapshotText: text, SnapshotChecksum: cs, Status: "issued"}
	r.S.Audit(leaderDiscordID, "raid_open", fmt.Sprintf("raid %d %s checksum %s", raid.ID, instance, snapshot.Short(raid.SnapshotChecksum)))
	return raid, nil
}

// DevRaiderNames are the virtual raiders the addon's Developer mode uses, in the order
// its "Add 5 raiders" button adds them, plus the one it leaves out of the group so the
// Roster tab has someone to invite. A test snapshot carries rows for all of them.
var DevRaiderNames = []string{"Simone", "Simba", "Simon", "Simran", "Simeon", "Sima", "Simkin", "Simza", "Latecomer"}

// OpenTestRaid opens an ad-hoc raid for rehearsing the copy/paste loop without an event:
// it makes sure fake persons and characters exist for the addon's virtual raiders (so
// their bids verify against real rows), voids any unsettled raid this leader still has,
// and issues the snapshot. The bot only offers it in sandbox mode.
func (r *Service) OpenTestRaid(leaderDiscordID string) (Raid, error) {
	for _, name := range DevRaiderNames {
		pid := "dev:" + strings.ToLower(name)
		if _, err := r.S.DB.Exec(`INSERT INTO person (discord_id, display_name, joined_at, free_passes) VALUES (?, ?, ?, 1) ON CONFLICT(discord_id) DO NOTHING`, pid, "Sim "+name, store.Now()); err != nil {
			return Raid{}, err
		}
		if _, err := r.S.DB.Exec(`INSERT INTO character (name, person_id, class, roles, registered_at) VALUES (?, ?, 'Warrior', 'melee', ?) ON CONFLICT(name) DO NOTHING`, name, pid, store.Now()); err != nil {
			return Raid{}, err
		}
	}
	ids, err := r.UnsettledFor(leaderDiscordID)
	if err != nil {
		return Raid{}, err
	}
	for _, id := range ids {
		if _, err := r.Override(id, leaderDiscordID, "void", "superseded by a new test raid"); err != nil {
			return Raid{}, err
		}
	}
	// Sandbox rehearsals run solo or in a small group, so a party always counts here.
	return r.open(0, "Dev test", leaderDiscordID, true)
}

// neededUploads is how many distinct uploaders settle a raid: guild_config
// requiredUploads (default 1, the leader's own upload), always 1 in the sandbox. With
// 2 or more, one uploader must not be the leader. Extra uploads are always accepted and
// compared; a later disagreement marks a settled raid "contested" for the officers.
func (r *Service) neededUploads() int {
	if r.S.IsSandbox() {
		return 1
	}
	n := r.S.ConfigInt("requiredUploads")
	switch {
	case n < 1:
		return 1
	case n > 40: // a raid has at most 40 members, so no larger value can ever be met
		return 40
	}
	return int(n)
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

// Get loads a raid.
func (r *Service) Get(id int64) (Raid, error) {
	var x Raid
	var settled sql.NullInt64
	var note sql.NullString
	var ev sql.NullInt64
	err := r.S.DB.QueryRow(`SELECT id, event_id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status, settled_at, settle_note, live_message_id, COALESCE(settled_upload_id, 0), settle_generation FROM raid WHERE id = ?`, id).
		Scan(&x.ID, &ev, &x.Instance, &x.Date, &x.LeaderPersonID, &x.SnapshotText, &x.SnapshotChecksum, &x.Status, &settled, &note, &x.LiveMessageID, &x.SettledUploadID, &x.SettleGeneration)
	if errors.Is(err, sql.ErrNoRows) {
		return x, store.ErrNotFound
	}
	x.EventID, x.SettledAt, x.SettleNote = ev.Int64, settled.Int64, note.String
	return x, err
}

// CurrentForLeader returns the newest unsettled raid led by the person.
func (r *Service) CurrentForLeader(discordID string) (Raid, error) {
	var id int64
	err := r.S.DB.QueryRow(`SELECT id FROM raid WHERE leader_person_id = ? AND status IN ('issued','in_progress','disputed','contested') ORDER BY id DESC LIMIT 1`, discordID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Raid{}, store.ErrNotFound
	}
	if err != nil {
		return Raid{}, err
	}
	return r.Get(id)
}

// Unsettled lists raids that still need settlement, newest first.
func (r *Service) Unsettled() ([]Raid, error) {
	rows, err := r.S.DB.Query(`SELECT id FROM raid WHERE status IN ('issued','in_progress','disputed','contested') ORDER BY id DESC LIMIT 25`)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	var out []Raid
	for _, id := range ids {
		x, err := r.Get(id)
		if err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, nil
}

// ---------------------------------------------------------------- uploads and settlement

// Sentinel errors the API layer maps to HTTP codes.
var (
	// ErrNotAllowed: the caller may not upload for this raid (banned, or neither its
	// leader nor the owner of a character with a snapshot row). HTTP 403.
	ErrNotAllowed = errors.New("you are not allowed to upload for this raid")
	// ErrAlreadySettled: the raid was settled by a concurrent request; nothing changed. HTTP 409.
	ErrAlreadySettled = errors.New("raid is already settled")
)

// uploadAllowed decides whether a person may upload a transcript for the raid: the
// leader, or the owner of a character that has a row in the stored snapshot, and never
// someone banned. Attackers read the code: a transcript from anyone else is refused
// before it is parsed.
func (r *Service) uploadAllowed(raid Raid, discordID string) error {
	if r.banned(discordID) {
		return fmt.Errorf("%w: banned", ErrNotAllowed)
	}
	if raid.LeaderPersonID == discordID {
		return nil
	}
	snap, err := snapshot.Parse(raid.SnapshotText)
	if err != nil {
		return fmt.Errorf("stored snapshot is unreadable: %w", err)
	}
	chars, err := r.People.CharactersOf(discordID)
	if err != nil {
		return err
	}
	for _, row := range snap.Rows {
		for _, c := range chars {
			if row.Character == c {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: not the leader and no character of yours is in the raid's snapshot", ErrNotAllowed)
}

func (r *Service) banned(discordID string) bool {
	return r.Bans != nil && r.Bans.IsBannedPerson(discordID)
}

// replayOptions is what the replay needs to know about the raid beyond its snapshot:
// who the leader's characters are, which characters sign with an addon key, and whether
// keys are required.
func (r *Service) replayOptions(raid Raid) (replay.Options, error) {
	opts := replay.Options{LeaderCharacters: map[string]bool{}, RequireAddonKey: r.S.ConfigString("requireAddonKey") == "true"}
	chars, err := r.People.CharactersOf(raid.LeaderPersonID)
	if err != nil {
		return opts, err
	}
	for _, c := range chars {
		opts.LeaderCharacters[c] = true
	}
	opts.Keys, err = r.People.CharacterKeys()
	return opts, err
}

// replayText parses and replays a transcript against the raid's stored snapshot.
func (r *Service) replayText(raid Raid, text string) (*replay.Transcript, *replay.Report, error) {
	tr, err := replay.ParseTranscript(text)
	if err != nil {
		return nil, nil, err
	}
	if tr.RaidID != fmt.Sprint(raid.ID) {
		return nil, nil, fmt.Errorf("transcript is for raid %s, not %d", tr.RaidID, raid.ID)
	}
	snap, err := snapshot.Parse(raid.SnapshotText)
	if err != nil {
		return nil, nil, fmt.Errorf("stored snapshot is unreadable: %w", err)
	}
	opts, err := r.replayOptions(raid)
	if err != nil {
		return nil, nil, err
	}
	rep, err := replay.Run(snap, raid.SnapshotChecksum, tr, opts)
	if err != nil {
		return nil, nil, err
	}
	return tr, rep, nil
}

// Upload stores a transcript as a witness statement and settles, disputes, or records
// it. Uploads are append-only: a person's earlier uploads are kept, and the newest one
// per person is what counts.
func (r *Service) Upload(raidID int64, discordID, text string) (Outcome, error) {
	raid, err := r.Get(raidID)
	if err != nil {
		return Outcome{}, err
	}
	if raid.Status == "void" {
		return Outcome{}, fmt.Errorf("raid %d was voided", raidID)
	}
	if err := r.uploadAllowed(raid, discordID); err != nil {
		return Outcome{}, err
	}
	tr, rep, err := r.replayText(raid, text)
	if err != nil {
		return Outcome{}, err
	}
	res, err := r.S.DB.Exec(`INSERT INTO transcript_upload (raid_id, person_id, text, uploaded_at) VALUES (?, ?, ?, ?)`,
		raidID, discordID, tr.Text(), store.Now())
	if err != nil {
		return Outcome{}, err
	}
	uploadID, _ := res.LastInsertId()
	if raid.Status == "issued" {
		_, _ = r.S.DB.Exec(`UPDATE raid SET status = 'in_progress' WHERE id = ?`, raidID)
	}
	if raid.Status == "settled" || raid.Status == "contested" {
		return r.audit(raid, rep, tr)
	}
	return r.evaluate(raid, rep, uploadID)
}

// ErrNotLeader is returned when someone other than the raid's leader sends a live update.
var ErrNotLeader = errors.New("only the leader's client can send live updates")

// ErrFinishNotLeader is returned when someone other than the raid's leader tries to finish it.
var ErrFinishNotLeader = errors.New("only the raid's leader can finish it")

// Progress stores a mid-raid transcript from the leader's sync client and replays it for
// a live view. It never settles: the same validations as Upload run, the text replaces
// the previous progress upload, and the outcome carries provisional deltas. Finish turns
// the last progress upload into a real upload.
func (r *Service) Progress(raidID int64, discordID, text string) (Outcome, error) {
	raid, err := r.Get(raidID)
	if err != nil {
		return Outcome{}, err
	}
	if raid.LeaderPersonID != discordID {
		return Outcome{}, ErrNotLeader
	}
	if r.banned(discordID) {
		return Outcome{}, fmt.Errorf("%w: banned", ErrNotAllowed)
	}
	if raid.Status != "issued" && raid.Status != "in_progress" {
		return Outcome{}, fmt.Errorf("raid %d is %s; live updates are only for a raid in progress", raidID, raid.Status)
	}
	tr, rep, err := r.replayText(raid, text)
	if err != nil {
		return Outcome{}, err
	}
	if _, err := r.S.DB.Exec(`INSERT INTO raid_progress (raid_id, person_id, text, lines, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(raid_id) DO UPDATE SET person_id = excluded.person_id, text = excluded.text, lines = excluded.lines, updated_at = excluded.updated_at`,
		raidID, discordID, tr.Text(), len(tr.Lines), store.Now()); err != nil {
		return Outcome{}, err
	}
	if raid.Status == "issued" {
		_, _ = r.S.DB.Exec(`UPDATE raid SET status = 'in_progress' WHERE id = ?`, raidID)
	}
	out := Outcome{Report: rep, Status: "live", Uploads: 1}
	out.Deltas = replay.Settle(rep, r.settleConfig(raid.ID))
	replay.SortDeltas(out.Deltas)
	out.Message = fmt.Sprintf("Live update recorded (%d lines, %d kills, %d auctions). Nothing is settled until you finish the raid. %s", len(tr.Lines), len(rep.Kills), len(rep.Auctions), flagNote(rep))
	return out, nil
}

// Finish settles a raid from its last live update: the stored progress text goes through
// the normal Upload path, so the witness rules apply exactly as for a pasted transcript.
func (r *Service) Finish(raidID int64, discordID string) (Outcome, error) {
	raid, err := r.Get(raidID)
	if err != nil {
		return Outcome{}, err
	}
	if raid.LeaderPersonID != discordID {
		return Outcome{}, ErrFinishNotLeader
	}
	if r.banned(discordID) {
		return Outcome{}, fmt.Errorf("%w: banned", ErrNotAllowed)
	}
	var text, by string
	err = r.S.DB.QueryRow(`SELECT text, person_id FROM raid_progress WHERE raid_id = ?`, raidID).Scan(&text, &by)
	if errors.Is(err, sql.ErrNoRows) {
		return Outcome{}, fmt.Errorf("raid %d has no live update to finish from; /reload in game with the sync client running, or upload the transcript by hand", raidID)
	}
	if err != nil {
		return Outcome{}, err
	}
	out, err := r.Upload(raidID, by, text)
	if err != nil {
		return Outcome{}, err
	}
	r.S.Audit(discordID, "raid_finish", fmt.Sprintf("raid %d from live update by %s -> %s", raidID, by, out.Status))
	return out, nil
}

// SetLiveMessage remembers the Discord message that shows the raid's live ledger.
func (r *Service) SetLiveMessage(raidID int64, messageID string) error {
	_, err := r.S.DB.Exec(`UPDATE raid SET live_message_id = ? WHERE id = ?`, messageID, raidID)
	return err
}

// audit compares a late upload with the transcript the raid was settled on. A match is
// confirmation; a difference marks the raid contested so officers look at it (the ledger
// stays as settled until an officer voids or re-settles).
func (r *Service) audit(raid Raid, rep *replay.Report, mine *replay.Transcript) (Outcome, error) {
	var settledText string
	if raid.SettledUploadID == 0 {
		return Outcome{Report: rep, Status: raid.Status, Message: "Recorded."}, nil
	}
	if err := r.S.DB.QueryRow(`SELECT text FROM transcript_upload WHERE id = ? AND raid_id = ?`, raid.SettledUploadID, raid.ID).Scan(&settledText); err != nil {
		return Outcome{Report: rep, Status: raid.Status, Message: "Recorded."}, nil
	}
	first, err := replay.ParseTranscript(settledText)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Report: rep, Status: raid.Status}
	if d := replay.Compare(first, mine); len(d) > 0 {
		out.Diffs = d
		out.Status = "contested"
		note := "contested by a later upload:\n" + strings.Join(d, "\n")
		_, _ = r.S.DB.Exec(`UPDATE raid SET status = 'contested', settle_note = ? WHERE id = ?`, note, raid.ID)
		out.Message = "This transcript disagrees with the one the raid was settled on. The raid is now contested; officers can void it (reversing the shares) or re-settle from a chosen transcript."
		r.S.Audit("system", "raid_contested", fmt.Sprintf("raid %d: %s", raid.ID, truncate(note, 500)))
		return out, nil
	}
	out.Message = "Matches the transcript the raid was settled on. Thank you for the second witness."
	return out, nil
}

// upload is one transcript_upload row.
type upload struct {
	id     int64
	person string
	text   string
}

// latestUploads returns each person's newest upload for the raid, in upload order.
func (r *Service) latestUploads(raidID int64) ([]upload, error) {
	rows, err := r.S.DB.Query(`SELECT id, person_id, text FROM transcript_upload WHERE raid_id = ? ORDER BY id`, raidID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byPerson := map[string]int{}
	var ups []upload
	for rows.Next() {
		var u upload
		if err := rows.Scan(&u.id, &u.person, &u.text); err != nil {
			return nil, err
		}
		if i, seen := byPerson[u.person]; seen {
			ups[i] = u // newer upload by the same person replaces the older one
			continue
		}
		byPerson[u.person] = len(ups)
		ups = append(ups, u)
	}
	return ups, rows.Err()
}

// evaluate decides settled / disputed / accepted from the raid's uploads. latest is the
// replay of the upload that just arrived (row latestID). People are counted, not files:
// one person uploading twice is one witness. With one upload required, only the leader's
// settles; with more, at least one witness must not be the leader. The settle is computed
// from the leader's newest upload when there is one.
func (r *Service) evaluate(raid Raid, latest *replay.Report, latestID int64) (Outcome, error) {
	ups, err := r.latestUploads(raid.ID)
	if err != nil {
		return Outcome{}, err
	}
	out := Outcome{Report: latest, Uploads: len(ups)}
	out.Deltas = replay.Settle(latest, r.settleConfig(raid.ID))
	replay.SortDeltas(out.Deltas)

	need := r.neededUploads()
	var ref *upload
	nonLeader := false
	for i := range ups {
		if ups[i].person == raid.LeaderPersonID {
			ref = &ups[i]
		} else {
			nonLeader = true
		}
	}
	switch {
	case need == 1 && ref == nil:
		out.Status = "accepted"
		out.Message = fmt.Sprintf("Transcript recorded as a witness (%d uploader(s) so far). The raid leader's upload, or finishing the raid from the sync client, settles it. %s", len(ups), flagNote(latest))
		return out, nil
	case need > 1 && (len(ups) < need || !nonLeader):
		out.Status = "accepted"
		out.Message = fmt.Sprintf("Transcript recorded (%d of %d uploaders so far; one must be someone other than the leader). %s", len(ups), need, flagNote(latest))
		return out, nil
	}
	if ref == nil {
		ref = &ups[len(ups)-1]
	}
	refTr, err := replay.ParseTranscript(ref.text)
	if err != nil {
		return Outcome{}, err
	}
	var diffs []string
	for _, u := range ups {
		if u.id == ref.id {
			continue
		}
		tr, err := replay.ParseTranscript(u.text)
		if err != nil {
			return Outcome{}, err
		}
		if d := replay.Compare(refTr, tr); len(d) > 0 {
			diffs = append(diffs, fmt.Sprintf("uploads from %s and %s differ:", ref.person, u.person))
			diffs = append(diffs, d...)
		}
	}
	out.Diffs = diffs
	refRep := latest
	if ref.id != latestID {
		if _, refRep, err = r.replayText(raid, ref.text); err != nil {
			return Outcome{}, err
		}
	}
	if disputing := refRep.Disputing(); len(diffs) > 0 || len(disputing) > 0 {
		note := strings.Join(append(diffs, disputing...), "\n")
		_, _ = r.S.DB.Exec(`UPDATE raid SET status = 'disputed', settle_note = ? WHERE id = ?`, note, raid.ID)
		out.Status = "disputed"
		out.Message = "Transcripts disagree or auctions are flagged. An officer must review. " + flagNote(refRep)
		r.S.Audit("system", "raid_disputed", fmt.Sprintf("raid %d: %s", raid.ID, truncate(note, 500)))
		return out, nil
	}
	deltas := replay.Settle(refRep, r.settleConfig(raid.ID))
	replay.SortDeltas(deltas)
	applied, err := r.settle(raid, refRep, deltas, fmt.Sprintf("automatic: %d+ matching uploads", need), ref.id)
	if err != nil {
		return out, err
	}
	out.Status = "settled"
	out.Report, out.Deltas = refRep, applied
	out.Message = "Settled. " + flagNote(refRep)
	out.Balances, _ = r.balancesFor(refRep)
	return out, nil
}

// flagNote summarises a report's flags for a reply: disputing ones first, then the
// informational notes that settle anyway.
func flagNote(rep *replay.Report) string {
	var parts []string
	if d := rep.Disputing(); len(d) > 0 {
		parts = append(parts, "Disputing: "+strings.Join(d, "; "))
	}
	if i := rep.Informational(); len(i) > 0 {
		parts = append(parts, "Notes: "+strings.Join(i, "; "))
	}
	if len(parts) == 0 {
		return "No flags."
	}
	return strings.Join(parts, ". ")
}

// settleConfig loads the economy numbers plus the raid's standby characters (signups the
// officers left on standby for the event this raid was opened from), who share in payouts.
func (r *Service) settleConfig(raidID int64) replay.SettleConfig {
	cfg := replay.SettleConfig{PoolPct: r.S.ConfigInt("poolPct"), NorevealPenalty: r.S.ConfigInt("norevealPenalty")}
	rows, err := r.S.DB.Query(`SELECT s.character FROM signup s JOIN raid_event e ON e.id = s.event_id WHERE e.raid_id = ? AND s.status = 'standby' AND s.character <> ''`, raidID)
	if err != nil {
		return cfg
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if rows.Scan(&name) == nil {
			cfg.Standby = append(cfg.Standby, name)
		}
	}
	return cfg
}

// settleStatuses are the raid states a settle may start from. The UPDATE that moves the
// raid to settled is the first statement of the transaction and must change a row, so
// two concurrent settles cannot both apply.
const settleStatuses = `'issued','in_progress','disputed','void','contested'`

// settle applies ledger deltas, attendance, lifetime shares, and auction records, and
// returns the deltas actually written (a waived penalty is left out). uploadID is the
// transcript the settle was computed from.
func (r *Service) settle(raid Raid, rep *replay.Report, deltas []replay.Delta, note string, uploadID int64) ([]replay.Delta, error) {
	// Resolve every character to its owner before the transaction (single connection).
	owner := map[string]string{}
	lookup := func(character string) {
		if character == "" || character == "pool" {
			return
		}
		if _, done := owner[character]; done {
			return
		}
		if id, err := r.People.PersonByCharacter(character); err == nil {
			owner[character] = id
		}
	}
	for _, d := range deltas {
		lookup(d.Character)
	}
	attendance := replay.KillShares(rep)
	for _, at := range attendance {
		for _, c := range at.Characters {
			lookup(c)
		}
	}
	for _, a := range rep.Auctions {
		lookup(a.Winner)
		lookup(a.TransferredTo)
	}
	poolAccount := "pool:" + fmt.Sprint(raid.ID)
	resolve := func(character string) (string, bool) {
		if character == "pool" {
			return poolAccount, true
		}
		id, ok := owner[character]
		return id, ok
	}
	sharePerKill, fullRunBonus := r.S.ConfigInt("sharePerKill"), r.S.ConfigInt("fullRunBonus")
	var newlyAttended []string
	var applied []replay.Delta
	var unresolved []string

	err := r.S.Tx(context.Background(), func(tx *sql.Tx) error {
		raidID := raid.ID
		// Guard: only one settle may move the raid to settled.
		res, err := tx.Exec(`UPDATE raid SET status = 'settled', settled_at = ?, settled_upload_id = ?, settle_note = ?, settle_generation = settle_generation + 1 WHERE id = ? AND status IN (`+settleStatuses+`)`,
			store.Now(), nullInt(uploadID), note, raidID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAlreadySettled
		}
		var generation int64
		if err := tx.QueryRow(`SELECT settle_generation FROM raid WHERE id = ?`, raidID).Scan(&generation); err != nil {
			return err
		}

		// Free passes: the first non-reveal per person in the raid is forgiven when they
		// have a pass left; the pool then does not receive that penalty.
		waived := map[string]bool{}
		waivedPool := map[string]int64{}
		skip := map[int]bool{}
		for i, d := range deltas {
			if d.Reason != "penalty" {
				continue
			}
			pid, ok := resolve(d.Character)
			if !ok || waived[pid] {
				continue
			}
			var passes int64
			if err := tx.QueryRow(`SELECT free_passes FROM person WHERE discord_id = ?`, pid).Scan(&passes); err != nil {
				return err
			}
			if passes <= 0 {
				continue
			}
			if _, err := tx.Exec(`UPDATE person SET free_passes = free_passes - 1 WHERE discord_id = ?`, pid); err != nil {
				return err
			}
			waived[pid] = true
			skip[i] = true
			waivedPool[d.AuctionID] += -d.Amount
		}
		var entries []store.Entry
		for i, d := range deltas {
			if skip[i] {
				continue
			}
			if d.Reason == "pool" && d.Character == "pool" && waivedPool[d.AuctionID] > 0 {
				d.Amount -= waivedPool[d.AuctionID]
				waivedPool[d.AuctionID] = 0
				if d.Amount == 0 {
					continue
				}
			}
			pid, ok := resolve(d.Character)
			lineNote := d.Character
			if !ok {
				// A character that no longer resolves to a person (M12): the movement goes
				// to the raid's pool so the books stay zero-sum and the settle completes.
				pid, lineNote = poolAccount, "unresolved:"+d.Character
				unresolved = append(unresolved, d.Character)
			}
			entries = append(entries, store.Entry{RaidID: &raidID, PersonID: pid, Delta: d.Amount, Reason: d.Reason, AuctionID: d.AuctionID, Note: lineNote, Generation: generation, Character: d.Character})
			applied = append(applied, d)
		}
		if err := store.AppendLedger(tx, entries); err != nil {
			return err
		}
		// Attendance per person: one set of kill shares however many characters they used.
		for _, at := range attendance {
			pid, ok := "", false
			for _, c := range at.Characters {
				if pid, ok = resolve(c); ok {
					break
				}
			}
			if !ok {
				continue // unregistered raider: no shares
			}
			var attended int64
			if err := tx.QueryRow(`SELECT raids_attended FROM person WHERE discord_id = ?`, pid).Scan(&attended); err != nil {
				return err
			}
			if attended == 0 {
				newlyAttended = append(newlyAttended, pid)
			}
			shares := int64(at.Kills) * sharePerKill
			fr := 0
			if at.FullRun {
				shares += fullRunBonus
				fr = 1
			}
			if _, err := tx.Exec(`INSERT INTO attendance (raid_id, person_id, kills_present, standby, full_run, characters) VALUES (?, ?, ?, 0, ?, ?) ON CONFLICT(raid_id, person_id) DO UPDATE SET kills_present = excluded.kills_present, full_run = excluded.full_run, characters = excluded.characters`, raidID, pid, at.Kills, fr, strings.Join(at.Characters, ",")); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE person SET raids_attended = raids_attended + 1, lifetime_shares = lifetime_shares + ? WHERE discord_id = ?`, shares, pid); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO settle_grant (raid_id, generation, person_id, raids_attended, lifetime_shares) VALUES (?, ?, ?, 1, ?)`, raidID, generation, pid, shares); err != nil {
				return err
			}
		}
		for _, a := range rep.Auctions {
			bids, _ := json.Marshal(a.Reveals)
			storeFlags := a.Flags
			if a.Voided {
				storeFlags = append(append([]string{}, a.Flags...), "voided:"+a.VoidReason)
			}
			if a.Abandoned {
				storeFlags = append(append([]string{}, storeFlags...), "abandoned:"+a.AbandonReason)
			}
			flags, _ := json.Marshal(storeFlags)
			var winnerPID any
			if a.Winner != "" {
				if pid, ok := resolve(a.Winner); ok {
					winnerPID = pid
				}
			}
			var transferred any
			if a.TransferredTo != "" {
				transferred = a.TransferredTo
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO auction (raid_id, auction_id, kind, item_id, item_link, floor, offspec_pct, winner_person_id, winner_character, price, transferred_to, bids_json, flags_json) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				raidID, a.ID, a.Kind, a.ItemID, a.ItemLink, a.Floor, rep.Context.OffspecPct, winnerPID, a.Winner, a.Price, transferred, string(bids), string(flags)); err != nil {
				return err
			}
		}
		if len(unresolved) > 0 {
			_, err = tx.Exec(`UPDATE raid SET settle_note = ? WHERE id = ?`, note+"\nunresolved characters (moved to pool): "+strings.Join(unresolved, ", "), raidID)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	r.S.Audit("system", "raid_settled", fmt.Sprintf("raid %d: %s", raid.ID, note))
	if len(newlyAttended) > 0 && r.OnFirstAttendance != nil {
		r.OnFirstAttendance(newlyAttended)
	}
	return applied, nil
}

// balancesFor maps display names of everyone in the report's roster to their balance.
func (r *Service) balancesFor(rep *replay.Report) (map[string]int64, error) {
	out := map[string]int64{}
	for _, name := range rep.Roster {
		pid, err := r.People.PersonByCharacter(name)
		if err != nil {
			continue
		}
		p, err := r.People.Get(pid)
		if err != nil {
			continue
		}
		out[p.DisplayName] = p.Balance
	}
	return out, nil
}

// Override lets an officer force a settle (flags ignored) or void the raid.
//
// settle takes an optional transcript_upload id to settle from; by default it is the
// leader's newest upload, or the newest upload of all when the leader has none.
//
// void on a settled or contested raid reverses the ledger lines, attendance and
// lifetime shares of the current settle generation only, so a raid settled, voided and
// settled again is reversed exactly once per settle.
func (r *Service) Override(raidID int64, officer, action, reason string, uploadID ...int64) (Outcome, error) {
	raid, err := r.Get(raidID)
	if err != nil {
		return Outcome{}, err
	}
	switch action {
	case "void":
		msg := "Raid voided; no shares moved."
		if raid.Status == "settled" || raid.Status == "contested" {
			reversed, err := r.reverseSettle(raid, reason)
			if err != nil {
				return Outcome{}, err
			}
			msg = fmt.Sprintf("Raid voided; %d ledger lines reversed, balances and attendance restored.", reversed)
		}
		_, err := r.S.DB.Exec(`UPDATE raid SET status = 'void', settled_at = ?, settle_note = ? WHERE id = ?`, store.Now(), "void by officer: "+reason, raidID)
		r.S.Audit(officer, "raid_void", fmt.Sprintf("raid %d: %s", raidID, reason))
		return Outcome{Status: "void", Message: msg}, err
	case "settle":
		if raid.Status == "settled" || raid.Status == "contested" {
			return Outcome{}, errors.New("already settled; void it first (which reverses the shares), then settle again")
		}
		up, err := r.pickUpload(raid, uploadID)
		if err != nil {
			return Outcome{}, err
		}
		_, rep, err := r.replayText(raid, up.text)
		if err != nil {
			return Outcome{}, err
		}
		deltas := replay.Settle(rep, r.settleConfig(raid.ID))
		replay.SortDeltas(deltas)
		applied, err := r.settle(raid, rep, deltas, fmt.Sprintf("officer %s: %s (upload %d by %s)", officer, reason, up.id, up.person), up.id)
		if err != nil {
			return Outcome{}, err
		}
		r.S.Audit(officer, "raid_settle_override", fmt.Sprintf("raid %d from upload %d: %s", raidID, up.id, reason))
		out := Outcome{Status: "settled", Message: "Settled by officer override. " + flagNote(rep), Report: rep, Deltas: applied}
		out.Balances, _ = r.balancesFor(rep)
		return out, nil
	default:
		return Outcome{}, fmt.Errorf("unknown action %q", action)
	}
}

// pickUpload chooses the upload an override settles from.
func (r *Service) pickUpload(raid Raid, uploadID []int64) (upload, error) {
	var up upload
	if len(uploadID) > 0 && uploadID[0] != 0 {
		err := r.S.DB.QueryRow(`SELECT id, person_id, text FROM transcript_upload WHERE id = ? AND raid_id = ?`, uploadID[0], raid.ID).Scan(&up.id, &up.person, &up.text)
		if errors.Is(err, sql.ErrNoRows) {
			return up, fmt.Errorf("upload %d is not a transcript of raid %d", uploadID[0], raid.ID)
		}
		return up, err
	}
	err := r.S.DB.QueryRow(`SELECT id, person_id, text FROM transcript_upload WHERE raid_id = ? AND person_id = ? ORDER BY id DESC LIMIT 1`, raid.ID, raid.LeaderPersonID).Scan(&up.id, &up.person, &up.text)
	if errors.Is(err, sql.ErrNoRows) {
		err = r.S.DB.QueryRow(`SELECT id, person_id, text FROM transcript_upload WHERE raid_id = ? ORDER BY id DESC LIMIT 1`, raid.ID).Scan(&up.id, &up.person, &up.text)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return up, errors.New("no transcript uploaded for this raid")
	}
	return up, err
}

// reverseSettle undoes the current settle generation: every ledger line of that
// generation is countered by a reversal line, and the attendance and lifetime shares it
// granted are taken back. Returns the number of ledger lines reversed.
func (r *Service) reverseSettle(raid Raid, reason string) (int, error) {
	var count int
	err := r.S.Tx(context.Background(), func(tx *sql.Tx) error {
		var generation int64
		if err := tx.QueryRow(`SELECT settle_generation FROM raid WHERE id = ?`, raid.ID).Scan(&generation); err != nil {
			return err
		}
		rows, err := tx.Query(`SELECT person_id, delta, COALESCE(auction_id, ''), COALESCE(note, '') FROM ledger WHERE raid_id = ? AND generation = ? AND reason <> 'reversal'`, raid.ID, generation)
		if err != nil {
			return err
		}
		var entries []store.Entry
		for rows.Next() {
			var e store.Entry
			var auction, note string
			if err := rows.Scan(&e.PersonID, &e.Delta, &auction, &note); err != nil {
				rows.Close()
				return err
			}
			rid := raid.ID
			e.RaidID, e.Delta, e.Reason, e.AuctionID, e.Note, e.Generation = &rid, -e.Delta, "reversal", auction, "void: "+reason, generation
			entries = append(entries, e)
		}
		rows.Close()
		if err := store.AppendLedger(tx, entries); err != nil {
			return err
		}
		count = len(entries)
		grants, err := tx.Query(`SELECT person_id, raids_attended, lifetime_shares FROM settle_grant WHERE raid_id = ? AND generation = ? AND reversed_at IS NULL`, raid.ID, generation)
		if err != nil {
			return err
		}
		type grant struct {
			pid             string
			attended, share int64
		}
		var gs []grant
		for grants.Next() {
			var g grant
			if err := grants.Scan(&g.pid, &g.attended, &g.share); err != nil {
				grants.Close()
				return err
			}
			gs = append(gs, g)
		}
		grants.Close()
		for _, g := range gs {
			if _, err := tx.Exec(`UPDATE person SET raids_attended = MAX(raids_attended - ?, 0), lifetime_shares = lifetime_shares - ? WHERE discord_id = ?`, g.attended, g.share, g.pid); err != nil {
				return err
			}
			// Standby rows were written when the raid opened and stay; the kill counts go.
			if _, err := tx.Exec(`DELETE FROM attendance WHERE raid_id = ? AND person_id = ? AND standby = 0`, raid.ID, g.pid); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE attendance SET kills_present = 0, full_run = 0 WHERE raid_id = ? AND person_id = ?`, raid.ID, g.pid); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`UPDATE settle_grant SET reversed_at = ? WHERE raid_id = ? AND generation = ? AND reversed_at IS NULL`, store.Now(), raid.ID, generation)
		return err
	})
	return count, err
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}
