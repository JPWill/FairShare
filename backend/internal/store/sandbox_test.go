package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxSwitchIsolatesDataAndKeepsGuildIDs(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "fairshare.db")
	s, err := Open(live)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.IsSandbox() {
		t.Fatal("fresh store must start live")
	}
	if err := s.SetConfig("channel.dev", "123"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfig("poolPct", "15"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO person (discord_id, display_name, joined_at) VALUES ('live-user', 'Live', 1)`); err != nil {
		t.Fatal(err)
	}

	if err := s.SetSandbox(true); err != nil {
		t.Fatal(err)
	}
	if !s.IsSandbox() {
		t.Fatal("expected sandbox active")
	}
	if _, err := os.Stat(SandboxPath(live)); err != nil {
		t.Fatalf("sandbox file not created: %v", err)
	}
	// Guild ids and config copied, people not.
	if got := s.ConfigString("channel.dev"); got != "123" {
		t.Fatalf("channel id not copied into sandbox: %q", got)
	}
	if got := s.ConfigString("poolPct"); got != "15" {
		t.Fatalf("config not copied into sandbox: %q", got)
	}
	var n int
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM person`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("sandbox must start with no people, got %d (%v)", n, err)
	}
	if _, err := s.DB.Exec(`INSERT INTO person (discord_id, display_name, joined_at) VALUES ('sandbox-user', 'Sand', 1)`); err != nil {
		t.Fatal(err)
	}

	// Reset wipes sandbox data but keeps guild ids.
	if err := s.ResetSandbox(); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM person`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("reset should empty the sandbox, got %d (%v)", n, err)
	}
	if got := s.ConfigString("channel.dev"); got != "123" {
		t.Fatalf("channel id lost on reset: %q", got)
	}

	// The flag persists: reopening lands in the sandbox; leaving it returns to live data.
	s.Close()
	s2, err := Open(live)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if !s2.IsSandbox() {
		t.Fatal("sandbox flag should persist across restarts")
	}
	if err := s2.SetSandbox(false); err != nil {
		t.Fatal(err)
	}
	if err := s2.DB.QueryRow(`SELECT COUNT(*) FROM person WHERE discord_id = 'live-user'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("live data missing after leaving sandbox: %d (%v)", n, err)
	}
}

func TestSandboxUnavailableInMemory(t *testing.T) {
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetSandbox(true); err == nil {
		t.Fatal("in-memory store should refuse sandbox mode")
	}
}

func TestIsSandboxConcurrentWithSwitch(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "fairshare.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 20; i++ {
			_ = s.SetSandbox(i%2 == 0)
		}
	}()
	for i := 0; i < 2000; i++ {
		_ = s.IsSandbox() // must never observe a torn state or panic
	}
	<-done
	_ = s.SetSandbox(false)
	if s.IsSandbox() {
		t.Fatal("should be live after the last switch")
	}
}
