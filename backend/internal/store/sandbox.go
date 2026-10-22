package store

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
)

// Sandbox mode: a second SQLite file next to the live one. While it is active every
// service reads and writes the sandbox, and the bot routes every channel post to
// #fairshare-dev, so a whole raid night can be rehearsed without touching the live
// ledger or the public channels. The flag lives in the LIVE database's guild_config
// (key "sandbox") so it survives restarts; guild ids (roles, channels) are copied into
// the sandbox the first time it is created so the bot still knows where to post.
//
// sandboxMu guards the switch itself and every read of IsSandbox, so a handler asking
// "am I sandboxed?" while an officer flips the switch sees one state or the other,
// never a torn pointer.

var sandboxMu sync.Mutex

// SandboxPath is the sandbox file for a live database path.
func SandboxPath(livePath string) string {
	return strings.TrimSuffix(livePath, ".db") + "-sandbox.db"
}

// IsSandbox reports whether the sandbox database is active.
func (s *Store) IsSandbox() bool {
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	return s.isSandbox()
}

// isSandbox is IsSandbox for callers that already hold sandboxMu.
func (s *Store) isSandbox() bool {
	return s.root != nil && s.DB != s.root
}

// SandboxPath is the path of this store's sandbox file ("" for in-memory stores).
func (s *Store) SandboxPath() string {
	if s.path == "" {
		return ""
	}
	return SandboxPath(s.path)
}

// SetSandbox switches the active database to the sandbox (on) or back to live (off)
// and records the choice in the live database. A request already holding the old
// handle finishes on it; this is an officer-only dev action, not a hot path.
func (s *Store) SetSandbox(on bool) error {
	sandboxMu.Lock()
	defer sandboxMu.Unlock()
	if s.path == "" {
		return fmt.Errorf("sandbox is not available for in-memory databases")
	}
	if on == s.isSandbox() {
		return nil
	}
	if on {
		db, err := openDB(s.SandboxPath())
		if err != nil {
			return err
		}
		if err := migrate(db); err != nil {
			db.Close()
			return err
		}
		if err := copyGuildIDs(s.root, db); err != nil {
			db.Close()
			return err
		}
		s.DB = db
	} else {
		old := s.DB
		s.DB = s.root
		old.Close()
	}
	return setConfig(s.root, "sandbox", fmt.Sprint(on))
}

// ResetSandbox deletes the sandbox database and starts a fresh one. Only valid while
// the sandbox is active.
func (s *Store) ResetSandbox() error {
	if !s.IsSandbox() {
		return fmt.Errorf("sandbox is not active")
	}
	if err := s.SetSandbox(false); err != nil {
		return err
	}
	p := s.SandboxPath()
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		_ = os.Remove(p + suffix)
	}
	return s.SetSandbox(true)
}

// copyGuildIDs seeds a fresh sandbox with the live role/channel ids and config values,
// without overwriting anything the sandbox already has.
func copyGuildIDs(from, to *sql.DB) error {
	rows, err := from.Query(`SELECT key, value FROM guild_config WHERE key <> 'sandbox'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		if _, err := to.Exec(`INSERT INTO guild_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO NOTHING`, k, v); err != nil {
			return err
		}
	}
	return rows.Err()
}

func setConfig(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO guild_config (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func configOf(db *sql.DB, key string) string {
	var v string
	if err := db.QueryRow(`SELECT value FROM guild_config WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}
