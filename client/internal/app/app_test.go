package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	stdsync "sync"
	"testing"
	"time"

	"github.com/JPWill/FairShare/client/internal/config"
	"github.com/JPWill/FairShare/client/internal/datafile"
)

const snapText = "FAIRSHARE SNAPSHOT 1\nraid=7\n"

const savedVars = `
FairShareDB = {
	["transcript"] = {
		["7"] = {
			["player"] = "Alice",
			["snapshot"] = "cafe",
			["lines"] = {
				"1700000100\tin\tAlice\tSNAP^7^cafe^1700000000^0^DEFAULT=10^",
			},
		},
	},
}
`

// backend is a fake FairShare API that counts what it was sent.
type backend struct {
	mu       stdsync.Mutex
	raid     bool // an open raid led by the caller exists
	live     int
	witness  int
	finished int
}

func (b *backend) server(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			json.NewEncoder(w).Encode(map[string]string{"discordId": "1", "displayName": "Alice"})
		}
	})
	mux.HandleFunc("/api/v1/snapshot/current", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		b.mu.Lock()
		open := b.raid
		b.mu.Unlock()
		if !open {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "no open raid led by you"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"raidId": "7", "text": snapText, "checksum": datafile.Checksum(snapText), "issuedAt": 1})
	})
	mux.HandleFunc("/api/v1/transcripts", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		b.mu.Lock()
		defer b.mu.Unlock()
		if body["live"] == true {
			b.live++
			json.NewEncoder(w).Encode(map[string]string{"status": "live", "message": "Live update recorded."})
			return
		}
		b.witness++
		json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "message": "Recorded as a witness."})
	})
	mux.HandleFunc("/api/v1/raids/7/finish", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		b.mu.Lock()
		b.finished++
		b.raid = false
		b.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"status": "settled", "message": "Settled."})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func (b *backend) counts() (live, witness, finished int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.live, b.witness, b.finished
}

// wowDir builds a game folder with the addon installed and one account's SavedVariables.
func wowDir(t *testing.T) string {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "_retail_", "Interface", "AddOns", "FairShare"), 0o755); err != nil {
		t.Fatal(err)
	}
	sv := filepath.Join(dir, "_retail_", "WTF", "Account", "ACC", "SavedVariables")
	if err := os.MkdirAll(sv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sv, "FairShare.lua"), []byte(savedVars), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newController(t *testing.T, cfg config.Config) *Controller {
	data := t.TempDir()
	c, err := New(cfg, Options{ConfigPath: filepath.Join(data, "config.json"), DataDir: data, Version: "test", PollEvery: 20 * time.Millisecond, SnapshotEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Stop(2 * time.Second) })
	return c
}

// eventually waits for cond, failing with the last status and log if it never holds.
func eventually(t *testing.T, c *Controller, what string, cond func(Status) bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond(c.Status()) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	lines, _ := c.Lines(0)
	t.Fatalf("%s never happened; status %+v\nlog:\n%s", what, c.Status(), strings.Join(lines, "\n"))
}

func TestLeaderNight(t *testing.T) {
	b := &backend{raid: true}
	srv := b.server(t)
	wow := wowDir(t)
	cfg := config.Config{BackendURL: srv.URL, Token: "good", WoWDir: wow, Flavor: "_retail_"}
	c := newController(t, cfg)
	c.Start()

	eventually(t, c, "sign in", func(s Status) bool { return s.Connected && s.Account == "Alice" })
	eventually(t, c, "snapshot written", func(s Status) bool {
		return s.RaidID == "7" && s.Snapshot == datafile.Checksum(snapText)[:8] && !s.SnapshotAt.IsZero()
	})
	if got := datafile.ExistingChecksum(cfg.DataLuaPath()); got != datafile.Checksum(snapText) {
		t.Fatalf("Data.lua checksum = %q", got)
	}
	eventually(t, c, "live update", func(s Status) bool { return s.LastLiveLines == 1 })
	if live, witness, _ := b.counts(); live != 1 || witness != 0 {
		t.Fatalf("live=%d witness=%d after the first scan", live, witness)
	}

	// The same file again is not re-sent.
	if !c.SyncNow() {
		t.Fatal("SyncNow queue full")
	}
	time.Sleep(100 * time.Millisecond)
	if live, _, _ := b.counts(); live != 1 {
		t.Fatalf("identical transcript re-sent: live=%d", live)
	}

	if !c.Finish() {
		t.Fatal("Finish queue full")
	}
	eventually(t, c, "raid cleared after settle", func(s Status) bool { return s.RaidID == "" })
	if _, err := os.Stat(cfg.DataLuaPath()); !os.IsNotExist(err) {
		t.Fatalf("Data.lua still present after the raid settled: %v", err)
	}
	if _, _, finished := b.counts(); finished != 1 {
		t.Fatalf("finished=%d", finished)
	}

	// A witness upload sends the raid once, then reports nothing new.
	c.UploadWitness()
	eventually(t, c, "witness upload", func(s Status) bool { return strings.Contains(s.Note, "uploaded as a witness") })
	c.UploadWitness()
	eventually(t, c, "nothing new", func(s Status) bool { return strings.HasPrefix(s.Note, "Nothing new") })
	if _, witness, _ := b.counts(); witness != 1 {
		t.Fatalf("witness=%d", witness)
	}
}

func TestNotSetUpThenApply(t *testing.T) {
	b := &backend{}
	srv := b.server(t)
	c := newController(t, config.Config{})
	c.Start()
	eventually(t, c, "not set up", func(s Status) bool { return !s.Configured && strings.HasPrefix(s.Note, "Not set up") })

	cfg := config.Config{BackendURL: srv.URL, Token: "good", WoWDir: wowDir(t), Flavor: "_retail_"}
	if name, err := Validate(cfg); err != nil || name != "Alice" {
		t.Fatalf("Validate = %q, %v", name, err)
	}
	c.Apply(cfg)
	eventually(t, c, "connected after apply", func(s Status) bool { return s.Connected })
	eventually(t, c, "no raid", func(s Status) bool { return strings.HasPrefix(s.Note, "No open raid") })
	saved, err := config.Load(c.opt.ConfigPath)
	if err != nil || saved.Token != "good" {
		t.Fatalf("config not saved: %+v, %v", saved, err)
	}
}

func TestBadTokenAndValidation(t *testing.T) {
	b := &backend{}
	srv := b.server(t)
	wow := wowDir(t)
	c := newController(t, config.Config{BackendURL: srv.URL, Token: "bad", WoWDir: wow, Flavor: "_retail_"})
	c.Start()
	eventually(t, c, "token rejected", func(s Status) bool { return strings.Contains(s.Err, "rejected your sync token") && !s.Connected })

	cases := map[string]config.Config{
		"https://":        {BackendURL: "http://fairshare.example.com", Token: "good", WoWDir: wow, Flavor: "_retail_"},
		"paste your":      {BackendURL: srv.URL, Token: " ", WoWDir: wow, Flavor: "_retail_"},
		"not installed":   {BackendURL: srv.URL, Token: "good", WoWDir: t.TempDir(), Flavor: "_retail_"},
		"rejected this":   {BackendURL: srv.URL, Token: "bad", WoWDir: wow, Flavor: "_retail_"},
		"cannot reach":    {BackendURL: "http://127.0.0.1:1", Token: "good", WoWDir: wow, Flavor: "_retail_"},
		"pick your World": {BackendURL: srv.URL, Token: "good"},
	}
	for want, cfg := range cases {
		if _, err := Validate(cfg); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Validate(%+v) = %v, want error containing %q", cfg, err, want)
		}
	}
}

func TestLogLinesAndFile(t *testing.T) {
	c := newController(t, config.Config{})
	c.Start()
	eventually(t, c, "start line", func(Status) bool { l, _ := c.Lines(0); return len(l) > 0 })
	lines, seq := c.Lines(0)
	if !strings.Contains(lines[0], "FairShare sync test started") {
		t.Fatalf("first line %q", lines[0])
	}
	if more, seq2 := c.Lines(seq); len(more) != 0 || seq2 != seq {
		t.Fatalf("Lines(seq) = %v, %d", more, seq2)
	}
	c.Stop(time.Second)
	data, err := os.ReadFile(c.LogPath())
	if err != nil || !strings.Contains(string(data), "started") || !strings.Contains(string(data), "stopped") {
		t.Fatalf("log file: %q, %v", data, err)
	}
}
