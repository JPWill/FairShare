package verify

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStateRoundTrip(t *testing.T) {
	s := NewSigner("secret")
	now := time.Unix(1_800_000_000, 0)
	st := s.State("123456789012345678", now)
	if id, err := s.Parse(st, now.Add(time.Minute)); err != nil || id != "123456789012345678" {
		t.Fatalf("parse: %q %v", id, err)
	}
	if _, err := s.Parse(st, now.Add(StateTTL+time.Second)); err == nil {
		t.Error("expired state accepted")
	}
	if _, err := NewSigner("other").Parse(st, now); err == nil {
		t.Error("state signed with another key accepted")
	}
	tampered := st[:len(st)-3] + "abc"
	if _, err := s.Parse(tampered, now); err == nil {
		t.Error("tampered state accepted")
	}
	for _, bad := range []string{"", "nodot", "a.b", "..", st + "."} {
		if _, err := s.Parse(bad, now); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestBattlenetTagPicksVerified(t *testing.T) {
	conns := []Connection{
		{Type: "steam", Name: "x", Verified: true},
		{Type: "battlenet", Name: "Unverified#1", Verified: false},
		{Type: "battlenet", Name: "Jaemz#1234", Verified: true},
	}
	if tag, ok := BattlenetTag(conns); !ok || tag != "Jaemz#1234" {
		t.Fatalf("got %q %v", tag, ok)
	}
	if _, ok := BattlenetTag(conns[:2]); ok {
		t.Error("unverified tag accepted")
	}
	if tag, ok := BattlenetTag([]Connection{{Type: "battlenet", Name: "NoHash", Verified: true}}); !ok || tag != "NoHash" {
		t.Errorf("tag without #digits rejected: %q %v", tag, ok)
	}
	if _, ok := BattlenetTag([]Connection{{Type: "battlenet", Name: "  ", Verified: true}}); ok {
		t.Error("blank name accepted")
	}
	if d := Describe(conns); strings.Contains(d, "Jaemz") || !strings.Contains(d, "battlenet(verified=true") {
		t.Errorf("Describe leaks names or omits flags: %s", d)
	}
}

// stubDiscord answers the three calls Complete makes.
func stubDiscord(t *testing.T, userID string, conns []Connection, scope string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			if r.Method != "POST" || r.FormValue("grant_type") != "authorization_code" || r.FormValue("code") != "good" || r.FormValue("client_secret") != "sec" {
				http.Error(w, "bad token request", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer", "scope": scope})
		case "/users/@me":
			if r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "no bearer", 401)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": userID})
		case "/users/@me/connections":
			_ = json.NewEncoder(w).Encode(conns)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestCompleteReadsVerifiedTag(t *testing.T) {
	srv := stubDiscord(t, "u1", []Connection{{Type: "battlenet", Name: "Jaemz#1234", Verified: true}}, "identify connections")
	defer srv.Close()
	c := &Client{ClientID: "app", ClientSecret: "sec", RedirectURI: "https://example.test/cb", API: srv.URL}
	res, err := c.Complete(context.Background(), "good", "u1")
	if err != nil || res.Tag != "Jaemz#1234" || res.UserID != "u1" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := c.Complete(context.Background(), "good", "someone-else"); err == nil || !strings.Contains(err.Error(), "different account") {
		t.Errorf("user mismatch not refused: %v", err)
	}
	if _, err := c.Complete(context.Background(), "bad", "u1"); err == nil {
		t.Error("bad code accepted")
	}
}

func TestCompleteRefusesWithoutConnections(t *testing.T) {
	srv := stubDiscord(t, "u1", nil, "identify connections")
	defer srv.Close()
	c := &Client{ClientID: "app", ClientSecret: "sec", API: srv.URL}
	if _, err := c.Complete(context.Background(), "good", "u1"); !errors.Is(err, ErrNoBattlenet) {
		t.Errorf("want ErrNoBattlenet, got %v", err)
	}
	narrow := stubDiscord(t, "u1", nil, "identify")
	defer narrow.Close()
	c.API = narrow.URL
	if _, err := c.Complete(context.Background(), "good", "u1"); err == nil || !strings.Contains(err.Error(), "connections permission") {
		t.Errorf("missing scope not refused: %v", err)
	}
}

func TestAuthorizeURL(t *testing.T) {
	c := &Client{ClientID: "app", RedirectURI: "https://fairsharewow.com/oauth/discord/callback"}
	u := c.AuthorizeURL("st")
	for _, w := range []string{"client_id=app", "scope=identify+connections", "state=st", "redirect_uri=https%3A%2F%2Ffairsharewow.com%2Foauth%2Fdiscord%2Fcallback", "response_type=code"} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %s in %s", w, u)
		}
	}
}
