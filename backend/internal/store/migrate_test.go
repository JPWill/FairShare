package store

import (
	"database/sql"
	"testing"
)

// The v6 migration rebuilds transcript_upload without its UNIQUE(raid_id, person_id):
// existing uploads survive with their ids, and a person may then upload again.
func TestMigrationV6RebuildsUploads(t *testing.T) {
	db, err := sql.Open("sqlite", "file::memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	// Bring the schema to v5 by hand, then seed what a v5 database would hold.
	for i := 0; i < 5; i++ {
		if _, err := db.Exec(migrations[i]); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (5)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO raid (id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status) VALUES (1, 'MC', 1, 'L', '', '', 'settled')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_upload (id, raid_id, person_id, text, uploaded_at) VALUES (7, 1, 'L', 'first', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_upload (raid_id, person_id, text, uploaded_at) VALUES (1, 'L', 'second', 2)`); err == nil {
		t.Fatal("v5 schema should have refused a second upload by the same person")
	}
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
	var version int
	_ = db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&version)
	if version != len(migrations) {
		t.Fatalf("version %d, want %d", version, len(migrations))
	}
	var id int64
	var text string
	if err := db.QueryRow(`SELECT id, text FROM transcript_upload WHERE raid_id = 1`).Scan(&id, &text); err != nil || id != 7 || text != "first" {
		t.Fatalf("upload lost in rebuild: %d %q %v", id, text, err)
	}
	if _, err := db.Exec(`INSERT INTO transcript_upload (raid_id, person_id, text, uploaded_at) VALUES (1, 'L', 'second', 2)`); err != nil {
		t.Fatalf("append-only uploads: %v", err)
	}
	// New columns and tables exist with their defaults.
	var gen, settledUpload int64
	if err := db.QueryRow(`SELECT settle_generation, COALESCE(settled_upload_id, 0) FROM raid WHERE id = 1`).Scan(&gen, &settledUpload); err != nil || gen != 0 || settledUpload != 0 {
		t.Fatalf("raid columns: %d %d %v", gen, settledUpload, err)
	}
	if _, err := db.Exec(`INSERT INTO person (discord_id, display_name, joined_at) VALUES ('P', 'Pat', 1)`); err != nil {
		t.Fatal(err)
	}
	var key string
	if err := db.QueryRow(`SELECT addon_key FROM person WHERE discord_id = 'P'`).Scan(&key); err != nil || key != "" {
		t.Fatalf("addon_key default: %q %v", key, err)
	}
	if _, err := db.Exec(`INSERT INTO settle_grant (raid_id, generation, person_id, raids_attended, lifetime_shares) VALUES (1, 1, 'P', 1, 3)`); err != nil {
		t.Fatal(err)
	}
	if err := AppendLedger(db, []Entry{{PersonID: "P", Delta: 5, Reason: "adjustment", Generation: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT generation FROM ledger WHERE person_id = 'P'`).Scan(&gen); err != nil || gen != 2 {
		t.Fatalf("ledger generation: %d %v", gen, err)
	}
	// Running migrate again is a no-op.
	if err := migrate(db); err != nil {
		t.Fatal(err)
	}
}
