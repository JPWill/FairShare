package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/JPWill/FairShare/backend/internal/bans"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/store"
)

type liveFixture struct {
	srv   *httptest.Server
	r     *raids.Service
	lead  string
	other string
	raid  raids.Raid
	t     *testing.T
}

func newLiveFixture(t *testing.T, rateLimit int) *liveFixture {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	p := &people.Service{S: st}
	b := &bans.Service{S: st, People: p}
	p.IsBannedIdentity = b.IsBannedIdentity
	r := &raids.Service{S: st, People: p, Bans: b}
	srv := httptest.NewServer((&Server{People: p, Raids: r, RateLimit: rateLimit}).Handler())
	t.Cleanup(srv.Close)

	old := time.Now().Add(-400 * 24 * time.Hour)
	if _, err := p.Register("L", "Lead", old, "Lead", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Register("A", "Alice", old, "Alice", ""); err != nil {
		t.Fatal(err)
	}
	lead, _ := p.IssueToken("L")
	other, _ := p.IssueToken("A")
	raid, err := r.Open(0, "MC", "L")
	if err != nil {
		t.Fatal(err)
	}
	return &liveFixture{srv: srv, r: r, lead: lead, other: other, raid: raid, t: t}
}

func (f *liveFixture) call(method, path, token string, payload any) (int, map[string]any) {
	f.t.Helper()
	var body *strings.Reader
	if payload != nil {
		enc, _ := json.Marshal(payload)
		body = strings.NewReader(string(enc))
	} else {
		body = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, f.srv.URL+path, body)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func (f *liveFixture) transcript() string {
	return "FAIRSHARE TRANSCRIPT 1\nraid=" + jsonNum(f.raid.ID) + "\nsnapshot=" + f.raid.SnapshotChecksum + "\nplayer=Lead\nversion=t\n1\tin\tLead\tSNAP^" + jsonNum(f.raid.ID) + "^" + f.raid.SnapshotChecksum + "^1^0^DEFAULT=10,INVTYPE_2HWEAPON=20^"
}

func jsonNum(n int64) string { return strings.TrimSpace(string(mustJSON(n))) }

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestLiveUploadDoesNotSettle(t *testing.T) {
	f := newLiveFixture(t, 0)
	// The client sends raidId as a string; the bot sends a number. Both must work.
	code, body := f.call("POST", "/api/v1/transcripts", f.lead, map[string]any{"raidId": jsonNum(f.raid.ID), "text": f.transcript(), "live": true})
	if code != 200 || body["status"] != "live" {
		t.Fatalf("live upload: %d %v", code, body)
	}
	raid, _ := f.r.Get(f.raid.ID)
	if raid.Status != "in_progress" {
		t.Fatalf("status after live upload: %s", raid.Status)
	}
	var lines int64
	if err := f.r.S.DB.QueryRow(`SELECT lines FROM raid_progress WHERE raid_id = ?`, f.raid.ID).Scan(&lines); err != nil || lines != 1 {
		t.Fatalf("progress row: %d %v", lines, err)
	}
	var uploads int
	_ = f.r.S.DB.QueryRow(`SELECT COUNT(*) FROM transcript_upload WHERE raid_id = ?`, f.raid.ID).Scan(&uploads)
	if uploads != 0 {
		t.Fatalf("a live upload must not be a witness upload, got %d", uploads)
	}

	// A second live upload replaces the first.
	code, _ = f.call("POST", "/api/v1/transcripts", f.lead, map[string]any{"raidId": f.raid.ID, "text": f.transcript() + "\n2\tin\tLead\tKILL^1^Boss^Lead", "live": true})
	if code != 200 {
		t.Fatalf("second live upload: %d", code)
	}
	_ = f.r.S.DB.QueryRow(`SELECT lines FROM raid_progress WHERE raid_id = ?`, f.raid.ID).Scan(&lines)
	if lines != 2 {
		t.Fatalf("progress not replaced: %d lines", lines)
	}

	// Finish settles from the stored progress (requiredUploads defaults to 1).
	code, body = f.call("POST", "/api/v1/raids/"+jsonNum(f.raid.ID)+"/finish", f.lead, nil)
	if code != 200 || body["status"] != "settled" {
		t.Fatalf("finish: %d %v", code, body)
	}
	raid, _ = f.r.Get(f.raid.ID)
	if raid.Status != "settled" {
		t.Fatalf("status after finish: %s", raid.Status)
	}
	// Finishing twice has nothing new to do but must not break anything.
	if code, _ := f.call("POST", "/api/v1/raids/"+jsonNum(f.raid.ID)+"/finish", f.lead, nil); code != 200 {
		t.Fatalf("second finish: %d", code)
	}
}

func TestLiveUploadNonLeaderForbidden(t *testing.T) {
	f := newLiveFixture(t, 0)
	code, body := f.call("POST", "/api/v1/transcripts", f.other, map[string]any{"raidId": f.raid.ID, "text": f.transcript(), "live": true})
	if code != 403 {
		t.Fatalf("non-leader live upload: %d %v", code, body)
	}
	if code, _ := f.call("POST", "/api/v1/raids/"+jsonNum(f.raid.ID)+"/finish", f.other, nil); code != 403 {
		t.Fatalf("non-leader finish: %d", code)
	}
	// Nothing to finish from yet for the leader either.
	if code, body := f.call("POST", "/api/v1/raids/"+jsonNum(f.raid.ID)+"/finish", f.lead, nil); code != 400 || !strings.Contains(body["error"].(string), "no live update") {
		t.Fatalf("finish without progress: %d %v", code, body)
	}
	// A witness upload from the non-leader still works as before.
	if code, body := f.call("POST", "/api/v1/transcripts", f.other, map[string]any{"raidId": f.raid.ID, "text": f.transcript()}); code != 200 {
		t.Fatalf("witness upload: %d %v", code, body)
	}
}

func TestRateLimitPerToken(t *testing.T) {
	f := newLiveFixture(t, 3)
	for i := 0; i < 3; i++ {
		if code, _ := f.call("GET", "/api/v1/me", f.lead, nil); code != 200 {
			t.Fatalf("request %d: %d", i+1, code)
		}
	}
	if code, body := f.call("GET", "/api/v1/me", f.lead, nil); code != 429 {
		t.Fatalf("fourth request: %d %v", code, body)
	}
	// Another token has its own bucket.
	if code, _ := f.call("GET", "/api/v1/me", f.other, nil); code != 200 {
		t.Fatalf("other token limited: %d", code)
	}
	// Guessed tokens are limited too, before the database is consulted.
	for i := 0; i < 3; i++ {
		f.call("GET", "/api/v1/me", "guess", nil)
	}
	if code, _ := f.call("GET", "/api/v1/me", "guess", nil); code != 429 {
		t.Fatalf("guessed token not limited: %d", code)
	}
}
