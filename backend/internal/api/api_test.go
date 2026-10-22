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

func TestAPI(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p := &people.Service{S: st}
	b := &bans.Service{S: st, People: p}
	p.IsBannedIdentity = b.IsBannedIdentity
	r := &raids.Service{S: st, People: p, Bans: b}
	_ = st.SetConfig("requiredUploads", "2") // this test covers the two-witness path
	srv := httptest.NewServer((&Server{People: p, Raids: r}).Handler())
	defer srv.Close()

	old := time.Now().Add(-400 * 24 * time.Hour)
	if _, err := p.Register("L", "Lead", old, "Lead", ""); err != nil {
		t.Fatal(err)
	}
	tok, _ := p.IssueToken("L")

	get := func(path, token string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", srv.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}

	if code, _ := get("/api/v1/me", ""); code != 401 {
		t.Fatalf("no token: %d", code)
	}
	if code, _ := get("/api/v1/me", "bogus"); code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	if code, body := get("/api/v1/me", tok); code != 200 || body["discordId"] != "L" {
		t.Fatalf("me: %d %v", code, body)
	}
	if code, _ := get("/api/v1/snapshot/current", tok); code != 404 {
		t.Fatalf("no raid yet: %d", code)
	}
	raid, err := r.Open(0, "MC", "L")
	if err != nil {
		t.Fatal(err)
	}
	code, body := get("/api/v1/snapshot/current", tok)
	if code != 200 || body["checksum"] != raid.SnapshotChecksum || !strings.HasPrefix(body["text"].(string), "FAIRSHARE SNAPSHOT 1") {
		t.Fatalf("snapshot: %d %v", code, body)
	}

	// Upload: a wrong raid id is rejected; a valid transcript is accepted.
	post := func(payload string) (int, map[string]any) {
		req, _ := http.NewRequest("POST", srv.URL+"/api/v1/transcripts", strings.NewReader(payload))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	if code, _ := post(`{"raidId": 999, "text": "x"}`); code != 400 {
		t.Fatalf("bad raid: %d", code)
	}
	transcript := "FAIRSHARE TRANSCRIPT 1\nraid=1\nsnapshot=" + raid.SnapshotChecksum + "\nplayer=Lead\nversion=t\n1\tin\tLead\tSNAP^1^" + raid.SnapshotChecksum + "^1^0^DEFAULT=10,INVTYPE_2HWEAPON=20^"
	enc, _ := json.Marshal(map[string]any{"raidId": raid.ID, "text": transcript})
	if code, body := post(string(enc)); code != 200 || body["status"] != "accepted" {
		t.Fatalf("upload: %d %v", code, body)
	}
	if code, body := get("/api/v1/raids/1", tok); code != 200 || body["status"] != "in_progress" {
		t.Fatalf("raid status: %d %v", code, body)
	}
}
