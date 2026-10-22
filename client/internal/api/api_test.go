package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newServer(t *testing.T) (*httptest.Server, *Client) {
	t.Helper()
	mux := http.NewServeMux()
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Authorization") != "Bearer good" {
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "bad token"})
			return false
		}
		return true
	}
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		json.NewEncoder(w).Encode(Me{DiscordID: "1", DisplayName: "Alice"})
	})
	mux.HandleFunc("/api/v1/snapshot/current", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if r.URL.Query().Get("none") == "1" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(Snapshot{RaidID: "7", Text: "FAIRSHARE SNAPSHOT 1", Checksum: "x", IssuedAt: 5})
	})
	mux.HandleFunc("/api/v1/transcripts", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["raidId"] != "7" || body["text"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "bad body"})
			return
		}
		if body["live"] == true {
			json.NewEncoder(w).Encode(UploadResult{Status: "live", Message: "live update recorded"})
			return
		}
		json.NewEncoder(w).Encode(UploadResult{Status: "accepted", Message: "waiting for a second upload"})
	})
	mux.HandleFunc("/api/v1/raids/7/finish", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) || r.Method != http.MethodPost {
			return
		}
		json.NewEncoder(w).Encode(UploadResult{Status: "settled", Message: "Settled."})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, New(srv.URL+"/", "good")
}

func TestClient(t *testing.T) {
	srv, c := newServer(t)
	me, err := c.Me()
	if err != nil || me.DisplayName != "Alice" {
		t.Fatalf("Me = %+v, %v", me, err)
	}
	s, err := c.CurrentSnapshot()
	if err != nil || s.RaidID != "7" || s.Text != "FAIRSHARE SNAPSHOT 1" {
		t.Fatalf("CurrentSnapshot = %+v, %v", s, err)
	}
	r, err := c.UploadTranscript("7", "FAIRSHARE TRANSCRIPT 1\nraid=7")
	if err != nil || r.Status != "accepted" {
		t.Fatalf("Upload = %+v, %v", r, err)
	}
	if _, err := c.UploadTranscript("8", "x"); err == nil {
		t.Fatal("expected error from 400")
	}
	if r, err := c.UploadProgress("7", "FAIRSHARE TRANSCRIPT 1\nraid=7"); err != nil || r.Status != "live" {
		t.Fatalf("UploadProgress = %+v, %v", r, err)
	}
	if r, err := c.Finish("7"); err != nil || r.Status != "settled" {
		t.Fatalf("Finish = %+v, %v", r, err)
	}
	if _, err := c.Finish("8"); err == nil {
		t.Fatal("expected error finishing an unknown raid")
	}

	bad := New(srv.URL, "wrong")
	if _, err := bad.Me(); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token error = %v", err)
	}

	down := New("http://127.0.0.1:1", "good")
	if _, err := down.Me(); err == nil {
		t.Fatal("expected error for unreachable backend")
	}
}

func TestNoRaid(t *testing.T) {
	srv, _ := newServer(t)
	c := New(srv.URL, "good")
	// Simulate 404 by hitting the handler's "none" switch through a custom base path.
	c.HTTP.Transport = &rewrite{base: http.DefaultTransport}
	_, err := c.CurrentSnapshot()
	if !errors.Is(err, ErrNoRaid) {
		t.Fatalf("err = %v, want ErrNoRaid", err)
	}
}

// rewrite adds ?none=1 to every request so the test server returns 404.
type rewrite struct{ base http.RoundTripper }

func (r *rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	q := req.URL.Query()
	q.Set("none", "1")
	req.URL.RawQuery = q.Encode()
	return r.base.RoundTrip(req)
}
