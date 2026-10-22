package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/JPWill/FairShare/backend/internal/verify"
)

func TestBattlenetVerification(t *testing.T) {
	s, st := seed(t)
	discord := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "scope": "identify connections"})
		case "/users/@me":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": idAlice})
		case "/users/@me/connections":
			_ = json.NewEncoder(w).Encode([]verify.Connection{{Type: "battlenet", Name: "Alice#4321", Verified: true}})
		}
	}))
	defer discord.Close()
	s.Verify = &verify.Client{ClientID: "app", ClientSecret: "sec", RedirectURI: "https://example.com/oauth/discord/callback", API: discord.URL}
	s.Signer = verify.NewSigner("sec")
	h := s.Handler()

	// The start link redirects to Discord with the same state.
	state := s.Signer.State(idAlice, time.Now())
	code, body, hdr := get(t, h, "/verify/battlenet?s="+url.QueryEscape(state))
	if code != http.StatusFound || !strings.Contains(hdr.Get("Location"), "discord.com/oauth2/authorize") || !strings.Contains(hdr.Get("Location"), url.QueryEscape(state)) {
		t.Fatalf("start: %d %s %s", code, hdr.Get("Location"), body)
	}
	if code, _, _ := get(t, h, "/verify/battlenet?s=forged.state"); code != http.StatusBadRequest {
		t.Errorf("forged state: %d", code)
	}

	// The callback stores the verified tag for the member the state named.
	code, body, _ = get(t, h, "/oauth/discord/callback?code=good&state="+url.QueryEscape(state))
	if code != http.StatusOK || !strings.Contains(body, "Battle.net verified") {
		t.Fatalf("callback: %d %s", code, body)
	}
	p, err := s.People.Get(idAlice)
	if err != nil || p.BattlenetTag != "Alice#4321" || p.BattlenetVerifiedAt == 0 {
		t.Fatalf("person after verify: %+v %v", p, err)
	}
	if strings.Contains(body, "Alice#4321") {
		t.Error("the result page must not show the tag")
	}
	// A different member's state is refused by the user check.
	other := s.Signer.State(idBob, time.Now())
	if code, body, _ := get(t, h, "/oauth/discord/callback?code=good&state="+url.QueryEscape(other)); code != http.StatusBadGateway || !strings.Contains(body, "different account") {
		t.Errorf("mismatched user: %d %s", code, body)
	}
	// Declining at Discord changes nothing.
	if code, body, _ := get(t, h, "/oauth/discord/callback?error=access_denied&state="+url.QueryEscape(state)); code != http.StatusOK || !strings.Contains(body, "Nothing changed") {
		t.Errorf("declined: %d", code)
	}

	// Credit can be gated on verification.
	_ = st.SetConfig("creditNeedsBattlenet", "true")
	bob, _ := s.People.Get(idBob)
	if c := s.People.Credit(bob); c != st.ConfigInt("newcomerCredit") {
		t.Errorf("unverified Bob credit = %d, want newcomer level", c)
	}
	alice, _ := s.People.Get(idAlice)
	if c := s.People.Credit(alice); c <= st.ConfigInt("newcomerCredit") {
		t.Errorf("verified Alice credit = %d, should exceed newcomer level after a raid", c)
	}

	// Without configuration the pages say so instead of failing.
	s.Verify, s.Signer = nil, nil
	if code, body, _ := get(t, s.Handler(), "/verify/battlenet?s=x"); code != http.StatusNotFound || !strings.Contains(body, "not configured") {
		t.Errorf("unconfigured: %d", code)
	}
}
