// Package store owns the SQLite database: schema, migrations, and the small set of
// typed helpers the service packages share (config values, ledger appends, audit log).
// Service packages add their own queries on top of Store.DB.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps the database handle. DB is the active database: the live file, or the
// sandbox file while sandbox mode is on (see sandbox.go). root is always the live file.
type Store struct {
	DB   *sql.DB
	root *sql.DB
	path string
}

func openDB(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: one writer; keeps transactions simple
	return db, nil
}

// Open opens (creating if needed) the SQLite file at path and runs migrations. If the
// live database says sandbox mode is on, the sandbox file is opened and made active.
func Open(path string) (*Store, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{DB: db, root: db, path: path}
	if configOf(db, "sandbox") == "true" {
		if err := s.SetSandbox(true); err != nil {
			db.Close()
			return nil, fmt.Errorf("reopen sandbox: %w", err)
		}
	}
	return s, nil
}

// OpenMemory opens a private in-memory database (tests).
func OpenMemory() (*Store, error) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db, root: db}, nil
}

// Close closes the active database and, if different, the live one.
func (s *Store) Close() error {
	if s.root != nil && s.root != s.DB {
		s.root.Close()
	}
	return s.DB.Close()
}

var migrations = []string{
	// v1
	`
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);

CREATE TABLE IF NOT EXISTS person (
    discord_id      TEXT PRIMARY KEY,
    display_name    TEXT NOT NULL,
    joined_at       INTEGER NOT NULL,
    raids_attended  INTEGER NOT NULL DEFAULT 0,
    lifetime_shares INTEGER NOT NULL DEFAULT 0,
    balance         INTEGER NOT NULL DEFAULT 0,
    free_passes     INTEGER NOT NULL DEFAULT 1,
    battlenet_tag   TEXT
);

CREATE TABLE IF NOT EXISTS character (
    name          TEXT PRIMARY KEY,
    person_id     TEXT NOT NULL REFERENCES person(discord_id),
    class         TEXT,
    registered_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS character_person ON character(person_id);

CREATE TABLE IF NOT EXISTS ban (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    person_id   TEXT NOT NULL,
    reason      TEXT NOT NULL,
    issued_by   TEXT NOT NULL,
    issued_at   INTEGER NOT NULL,
    lifted_at   INTEGER,
    lifted_by   TEXT,
    lift_reason TEXT
);

CREATE TABLE IF NOT EXISTS ban_identity (
    ban_id INTEGER NOT NULL REFERENCES ban(id),
    kind   TEXT NOT NULL,
    value  TEXT NOT NULL,
    PRIMARY KEY (ban_id, kind, value)
);
CREATE INDEX IF NOT EXISTS ban_identity_value ON ban_identity(kind, value);

CREATE TABLE IF NOT EXISTS raid_event (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    title       TEXT NOT NULL,
    instance    TEXT NOT NULL,
    starts_at   INTEGER NOT NULL,
    created_by  TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    channel_id  TEXT,
    message_id  TEXT,
    cap         INTEGER NOT NULL DEFAULT 20,
    standby_cap INTEGER NOT NULL DEFAULT 5,
    status      TEXT NOT NULL DEFAULT 'open',
    raid_id     INTEGER
);

CREATE TABLE IF NOT EXISTS signup (
    event_id  INTEGER NOT NULL REFERENCES raid_event(id),
    person_id TEXT NOT NULL,
    character TEXT NOT NULL,
    role      TEXT NOT NULL,
    status    TEXT NOT NULL,
    signed_at INTEGER NOT NULL,
    PRIMARY KEY (event_id, person_id)
);

CREATE TABLE IF NOT EXISTS raid (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id          INTEGER,
    instance          TEXT NOT NULL,
    date              INTEGER NOT NULL,
    leader_person_id  TEXT NOT NULL,
    snapshot_text     TEXT NOT NULL,
    snapshot_checksum TEXT NOT NULL,
    status            TEXT NOT NULL,
    settled_at        INTEGER,
    settle_note       TEXT
);

CREATE TABLE IF NOT EXISTS transcript_upload (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    raid_id     INTEGER NOT NULL REFERENCES raid(id),
    person_id   TEXT NOT NULL,
    text        TEXT NOT NULL,
    uploaded_at INTEGER NOT NULL,
    UNIQUE (raid_id, person_id)
);

CREATE TABLE IF NOT EXISTS attendance (
    raid_id       INTEGER NOT NULL REFERENCES raid(id),
    person_id     TEXT NOT NULL,
    kills_present INTEGER NOT NULL DEFAULT 0,
    standby       INTEGER NOT NULL DEFAULT 0,
    full_run      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (raid_id, person_id)
);

CREATE TABLE IF NOT EXISTS auction (
    raid_id          INTEGER NOT NULL REFERENCES raid(id),
    auction_id       TEXT NOT NULL,
    kind             TEXT NOT NULL,
    item_id          INTEGER,
    item_link        TEXT,
    floor            INTEGER NOT NULL,
    offspec_pct      INTEGER NOT NULL,
    winner_person_id TEXT,
    winner_character TEXT,
    price            INTEGER NOT NULL DEFAULT 0,
    transferred_to   TEXT,
    bids_json        TEXT NOT NULL,
    flags_json       TEXT NOT NULL,
    PRIMARY KEY (raid_id, auction_id)
);

CREATE TABLE IF NOT EXISTS ledger (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    raid_id    INTEGER,
    person_id  TEXT NOT NULL,
    delta      INTEGER NOT NULL,
    reason     TEXT NOT NULL,
    auction_id TEXT,
    note       TEXT,
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS ledger_person ON ledger(person_id);

CREATE TABLE IF NOT EXISTS floor_price (
    slot  TEXT PRIMARY KEY,
    value INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS performance (
    raid_id    INTEGER NOT NULL,
    person_id  TEXT NOT NULL,
    spec       TEXT,
    percentile REAL,
    band       TEXT,
    award      INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (raid_id, person_id)
);

CREATE TABLE IF NOT EXISTS guild_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_token (
    hash         TEXT PRIMARY KEY,
    person_id    TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    last_used_at INTEGER
);

CREATE TABLE IF NOT EXISTS audit_log (
    id     INTEGER PRIMARY KEY AUTOINCREMENT,
    at     INTEGER NOT NULL,
    actor  TEXT NOT NULL,
    action TEXT NOT NULL,
    detail TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS scheduled_announcement (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_id TEXT NOT NULL,
    body       TEXT NOT NULL,
    post_at    INTEGER NOT NULL,
    created_by TEXT NOT NULL,
    posted_at  INTEGER
);

INSERT OR IGNORE INTO floor_price (slot, value) VALUES ('DEFAULT', 10);
`,
	// v2: bare character names (docs/protocol.md §1), class roles, repeating events,
	// signup class/role, roster confirmation.
	`
UPDATE character SET name = trim(substr(name, 1, instr(name, '-') - 1)) WHERE instr(name, '-') > 0;
UPDATE ban_identity SET value = trim(substr(value, 1, instr(value, '-') - 1)) WHERE kind = 'character' AND instr(value, '-') > 0;
ALTER TABLE character ADD COLUMN roles TEXT NOT NULL DEFAULT '';
ALTER TABLE raid_event ADD COLUMN repeat_days INTEGER NOT NULL DEFAULT 0;
ALTER TABLE raid_event ADD COLUMN parent_event_id INTEGER;
ALTER TABLE raid_event ADD COLUMN roster_confirmed_at INTEGER;
ALTER TABLE signup ADD COLUMN class TEXT NOT NULL DEFAULT '';
`,
	`
ALTER TABLE raid_event ADD COLUMN notes TEXT NOT NULL DEFAULT '';
`,
	`
-- reserved; nothing uses this column today (a calendar mirror was tried and removed)
ALTER TABLE raid_event ADD COLUMN discord_event_id TEXT NOT NULL DEFAULT '';
`,
	// v5: live progress uploads from the sync client (one per raid, replaced on every
	// /reload) and the Discord message the bot keeps editing with the live ledger.
	`
CREATE TABLE IF NOT EXISTS raid_progress (
    raid_id    INTEGER PRIMARY KEY REFERENCES raid(id),
    person_id  TEXT NOT NULL,
    text       TEXT NOT NULL,
    lines      INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);
ALTER TABLE raid ADD COLUMN live_message_id TEXT NOT NULL DEFAULT '';
`,
	// v6: settlement integrity and addon keys.
	//  - transcript_upload is append-only: the UNIQUE(raid_id, person_id) goes (SQLite
	//    cannot drop a constraint, so the table is rebuilt), uploads are never updated.
	//  - raid.settled_upload_id names the upload a settle was computed from;
	//    raid.settle_generation counts settles, and ledger.generation tags each line with
	//    the settle that produced it so a void reverses exactly that settle's lines.
	//  - settle_grant records the attendance and lifetime shares a settle granted, so a
	//    void can take them back.
	//  - person.addon_key is the bidder's key that signs COMMIT/REVEAL/ACK lines.
	`
CREATE TABLE transcript_upload_v6 (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    raid_id     INTEGER NOT NULL REFERENCES raid(id),
    person_id   TEXT NOT NULL,
    text        TEXT NOT NULL,
    uploaded_at INTEGER NOT NULL
);
INSERT INTO transcript_upload_v6 (id, raid_id, person_id, text, uploaded_at)
    SELECT id, raid_id, person_id, text, uploaded_at FROM transcript_upload;
DROP TABLE transcript_upload;
ALTER TABLE transcript_upload_v6 RENAME TO transcript_upload;
CREATE INDEX IF NOT EXISTS transcript_upload_raid ON transcript_upload(raid_id, person_id);

ALTER TABLE raid ADD COLUMN settled_upload_id INTEGER;
ALTER TABLE raid ADD COLUMN settle_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ledger ADD COLUMN generation INTEGER NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS settle_grant (
    raid_id         INTEGER NOT NULL REFERENCES raid(id),
    generation      INTEGER NOT NULL,
    person_id       TEXT NOT NULL,
    raids_attended  INTEGER NOT NULL DEFAULT 0,
    lifetime_shares INTEGER NOT NULL DEFAULT 0,
    reversed_at     INTEGER,
    PRIMARY KEY (raid_id, generation, person_id)
);

ALTER TABLE person ADD COLUMN addon_key TEXT NOT NULL DEFAULT '';
`,
	// v7: floor prices (decided 2026-10-22): every item 10, two-handed weapons 20.
	// Officers edit both from the panel (Floors); INSERT OR IGNORE keeps an edited value.
	`
INSERT OR IGNORE INTO floor_price (slot, value) VALUES ('INVTYPE_2HWEAPON', 20);
`,
	// v8: the public server. person.tos_accepted_at records the Accept the rules click in
	// #welcome (registration requires it); raid_event.raid_channel_id is the event's own
	// Discord channel (#tuesday-7pm-mc), shared by every occurrence of a repeating event.
	`
ALTER TABLE person ADD COLUMN tos_accepted_at INTEGER NOT NULL DEFAULT 0;
ALTER TABLE raid_event ADD COLUMN raid_channel_id TEXT NOT NULL DEFAULT '';
`,
	// v9: which character did it. attendance.characters lists every character a person was
	// seen on in the raid's kills, first-seen order, comma-joined; ledger.character is the
	// character a share, purchase, penalty or award was earned on.
	`
ALTER TABLE attendance ADD COLUMN characters TEXT NOT NULL DEFAULT '';
ALTER TABLE ledger ADD COLUMN character TEXT NOT NULL DEFAULT '';
`,
	// v10: performance.character is the character the score was earned on.
	`
ALTER TABLE performance ADD COLUMN character TEXT NOT NULL DEFAULT '';
`,
	// v11: person.battlenet_verified_at is set when Discord confirmed the tag (verify package);
	// a typed tag leaves it 0.
	`
ALTER TABLE person ADD COLUMN battlenet_verified_at INTEGER NOT NULL DEFAULT 0;
`,
}

func migrate(db *sql.DB) error {
	var version int
	// schema_version may not exist yet.
	_ = db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&version)
	for i := version; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(`DELETE FROM schema_version`); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Now is the clock used for timestamps; tests may replace it.
var Now = func() int64 { return time.Now().Unix() }

// ---------------------------------------------------------------- guild config

// Config defaults per docs/protocol.md §8.
var ConfigDefaults = map[string]string{
	"poolPct":              "10",
	"newcomerCredit":       "100",
	"creditPerRaid":        "100",
	"creditCap":            "500",
	"sharePerKill":         "1",
	"fullRunBonus":         "2",
	"norevealPenalty":      "25",
	"freePasses":           "1",
	"minAccountAgeDays":    "30",
	"offspecPct":           "0",
	"devPosting":           "true",
	"requiredUploads":      "1",
	"requireAddonKey":      "false",
	"allowParty":           "false",
	"timezone":             "America/New_York",
	"discordInvite":        "", // shown on the website when set; see ValidDiscordInvite
	"websiteURL":           "https://fairsharewow.com",
	"creditNeedsBattlenet": "false", // when true, credit above newcomerCredit needs a verified Battle.net tag
}

// ConfigString returns a guild_config value or its default.
func (s *Store) ConfigString(key string) string {
	var v string
	err := s.DB.QueryRow(`SELECT value FROM guild_config WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return ConfigDefaults[key]
	}
	return v
}

// ConfigInt returns a guild_config integer or its default (0 if neither parses).
func (s *Store) ConfigInt(key string) int64 {
	n, err := strconv.ParseInt(s.ConfigString(key), 10, 64)
	if err != nil {
		n, _ = strconv.ParseInt(ConfigDefaults[key], 10, 64)
	}
	return n
}

// SetConfig stores a guild_config value.
func (s *Store) SetConfig(key, value string) error {
	_, err := s.DB.Exec(`INSERT INTO guild_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ---------------------------------------------------------------- ledger

// Entry is one append-only ledger line.
type Entry struct {
	RaidID    *int64
	PersonID  string // discord id, or "pool:<raidID>" for a raid's performance pool
	Delta     int64
	Reason    string // share | purchase | pool | penalty | adjustment | reversal
	AuctionID string
	Note      string
	Character string // the character the line was earned on; "" for pool and officer lines
	// Generation is the raid's settle_generation that produced (or reversed) the line;
	// 0 for lines outside a settle (adjustments).
	Generation int64
}

// Execer is satisfied by *sql.DB and *sql.Tx.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// AppendLedger inserts entries and updates the cached balance of real persons.
func AppendLedger(ex Execer, entries []Entry) error {
	now := Now()
	for _, e := range entries {
		var raid any
		if e.RaidID != nil {
			raid = *e.RaidID
		}
		var auction any
		if e.AuctionID != "" {
			auction = e.AuctionID
		}
		if _, err := ex.Exec(`INSERT INTO ledger (raid_id, person_id, delta, reason, auction_id, note, created_at, generation, character) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			raid, e.PersonID, e.Delta, e.Reason, auction, e.Note, now, e.Generation, e.Character); err != nil {
			return err
		}
		if _, err := ex.Exec(`UPDATE person SET balance = balance + ? WHERE discord_id = ?`, e.Delta, e.PersonID); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------- audit

// Audit records an officer or system action.
func (s *Store) Audit(actor, action, detail string) {
	_, _ = s.DB.Exec(`INSERT INTO audit_log (at, actor, action, detail) VALUES (?, ?, ?, ?)`, Now(), actor, action, detail)
}

// ErrNotFound is returned by lookups that find nothing.
var ErrNotFound = errors.New("not found")

// Tx runs fn in a transaction.
func (s *Store) Tx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
