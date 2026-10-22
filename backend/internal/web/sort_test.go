package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// order reports whether a appears before b in body (both must be present).
func order(t *testing.T, body, a, b string) bool {
	t.Helper()
	i, j := strings.Index(body, a), strings.Index(body, b)
	if i < 0 || j < 0 {
		t.Fatalf("missing %q or %q", a, b)
	}
	return i < j
}

func TestSortableTables(t *testing.T) {
	s, _ := seed(t)
	h := s.Handler()

	// Loot: Thunderfury sold for more than the Bindings. Only the table counts: the
	// Biggest purchases cards above it are always by price.
	table := func(body string) string {
		if i := strings.Index(body, "All loot"); i >= 0 {
			return body[i:]
		}
		return body
	}
	_, body, _ := get(t, h, "/loot?sort=price&dir=desc")
	if !order(t, table(body), "Thunderfury", "Bindings of the Windseeker") {
		t.Error("price desc: cheaper item first")
	}
	_, body, _ = get(t, h, "/loot?sort=price&dir=asc")
	if !order(t, table(body), "Bindings of the Windseeker", "Thunderfury") {
		t.Error("price asc: dearer item first")
	}
	// The current column is marked and its header link flips the direction.
	if !strings.Contains(body, `class="sort on asc" href="/loot?dir=desc&amp;sort=price"`) {
		t.Error("price header not marked as the current ascending sort with a flip link")
	}
	// The pager and the search carry the order along.
	_, body, _ = get(t, h, "/loot?q=t&sort=price&dir=asc")
	if !strings.Contains(body, `href="/loot?dir=desc&amp;q=t&amp;sort=price"`) {
		t.Error("header link dropped the search text")
	}

	// Unknown keys and directions fall back to the default order, never an error.
	for _, p := range []string{"/loot?sort=drop%20table&dir=up", "/raids?sort=;--", "/players?sort=x&dir=y"} {
		if code, _, _ := get(t, h, p); code != http.StatusOK {
			t.Errorf("%s: status %d", p, code)
		}
	}

	// Players: balance descending puts Bob (132) before Alice (-141).
	_, body, _ = get(t, h, "/players?sort=balance&dir=desc")
	if !order(t, body, "Bob", "Alice") {
		t.Error("balance desc: Alice before Bob")
	}
	_, body, _ = get(t, h, "/players?sort=balance&dir=asc")
	if !order(t, body, "Alice", "Bob") {
		t.Error("balance asc: Bob before Alice")
	}

	// Raids: every key renders.
	for _, k := range []string{"date", "raid", "leader", "status", "raiders", "bosses", "items", "spent"} {
		if code, body, _ := get(t, h, "/raids?sort="+k+"&dir=asc"); code != http.StatusOK || !strings.Contains(body, "Thursday MC") {
			t.Errorf("raids sort %s: status %d", k, code)
		}
	}

	// The front page's previews are plain headers, not links.
	_, body, _ = get(t, h, "/")
	if strings.Contains(body, `class="sort`) {
		t.Error("front page tables should not be sortable")
	}
}

func TestFAQAndDiscordLink(t *testing.T) {
	s, st := seed(t)
	h := s.Handler()

	code, body, _ := get(t, h, "/faq")
	if code != http.StatusOK {
		t.Fatalf("faq: %d", code)
	}
	for _, w := range []string{"How It Works", "10 shares for any item, 20 for a two-handed weapon", "180 is split evenly", "9 each", "credit limit on night one is 100", "Is this GDKP?"} {
		if !strings.Contains(body, w) {
			t.Errorf("faq: missing %q", w)
		}
	}
	if strings.Contains(body, "discord.gg") {
		t.Error("faq: Discord link shown while unset")
	}

	// The pool text follows the setting.
	if err := st.SetConfig("poolPct", "0"); err != nil {
		t.Fatal(err)
	}
	_, body, _ = get(t, h, "/faq")
	if !strings.Contains(body, "set to 0%") || !strings.Contains(body, "200 is split evenly") {
		t.Error("faq: pool at 0% not reflected")
	}

	// A valid invite appears in the header, footer and FAQ; anything else is dropped.
	if err := st.SetConfig("discordInvite", "https://discord.gg/abc-123"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/faq", "/players"} {
		_, body, _ = get(t, h, p)
		if strings.Count(body, `href="https://discord.gg/abc-123"`) == 0 {
			t.Errorf("%s: Discord link missing", p)
		}
	}
	_ = st.SetConfig("discordInvite", "javascript:alert(1)")
	_, body, _ = get(t, h, "/")
	if strings.Contains(body, "javascript:") || strings.Contains(body, `class="discord"`) {
		t.Error("non-Discord link rendered")
	}
	_ = st.SetConfig("discordInvite", "https://discord.gg/abc?x=<script>")
	_, body, _ = get(t, h, "/")
	if strings.Contains(body, `class="discord"`) {
		t.Error("invite with extra characters rendered")
	}
}

func TestValidDiscordInvite(t *testing.T) {
	ok := []string{"", "https://discord.gg/wowforever", "https://discord.com/invite/foreverclassic", "https://discord.gg/4h3VCp7uxx"}
	bad := []string{"discord.gg/x", "http://discord.gg/x", "https://discord.gg/", "https://discord.gg/a b", "https://evil.example/discord.gg/x", "https://discord.gg/x/../y", "https://discord.gg/" + strings.Repeat("a", 65)}
	for _, v := range ok {
		if !store.ValidDiscordInvite(v) {
			t.Errorf("%q should be accepted", v)
		}
	}
	for _, v := range bad {
		if store.ValidDiscordInvite(v) {
			t.Errorf("%q should be refused", v)
		}
	}
}

func TestFAQEmptyCommunity(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := (&Server{Store: st, People: &people.Service{S: st}}).Handler()
	if code, body, _ := get(t, h, "/faq"); code != 200 || !strings.Contains(body, "20 for a two-handed weapon") {
		t.Fatalf("faq on an empty database: %d", code)
	}
}
