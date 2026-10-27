package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// Fake Discord ids: long digit strings, so a leak would be easy to spot in a page.
const (
	idLead  = "100000000000000001"
	idAlice = "100000000000000002"
	idBob   = "100000000000000003"
)

const thunderfury = "|cffff8000|Hitem:19019::::::::60:::::::::|h[Thunderfury, Blessed Blade]|h|r"
const binding = "|cffa335ee|Hitem:18563::::::::60:::::::::|h[Bindings of the Windseeker]|h|r"

// seed builds a small community: one settled raid with sales, a void and a test auction,
// one raid in progress with a live update, and one voided raid that must never show.
func seed(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO person (discord_id, display_name, joined_at, raids_attended, lifetime_shares, balance) VALUES (?, 'Lead', 1, 1, 8, 9), (?, 'Alice', 1, 1, 8, -141), (?, 'Bob <script>alert(1)</script>', 1, 1, 6, 132)`, idLead, idAlice, idBob)
	exec(`INSERT INTO character (name, person_id, class, registered_at) VALUES ('Leadpal', ?, 'Paladin', 1), ('Alice', ?, 'Mage', 1), ('Alicealt', ?, 'Priest', 2), ('Bob', ?, 'Warrior', 1)`, idLead, idAlice, idAlice, idBob)

	snap := snapshot.Snapshot{RaidID: "1", Issued: 1700000000, Floors: map[string]int64{"DEFAULT": 10}, Rows: []snapshot.Row{
		{Character: "Alice", Owner: "Alice", Credit: 100}, {Character: "Bob", Owner: "Bob", Credit: 100}, {Character: "Leadpal", Owner: "Lead", Credit: 100}}}
	text := snap.Text()
	sum := snapshot.Checksum(text)
	tr := fmt.Sprintf("FAIRSHARE TRANSCRIPT 1\nraid=1\nsnapshot=%s\nplayer=Leadpal\nversion=test\n"+
		"1700000100\tin\tLeadpal\tSNAP^1^%s^1700000000^0^DEFAULT=10^\n"+
		"1700000200\tin\tLeadpal\tKILL^1001^Lucifron^Alice,Bob,Leadpal\n"+
		"1700000900\tin\tLeadpal\tKILL^1002^Magmadar^Alice,Bob,Leadpal\n", sum, sum)
	exec(`INSERT INTO raid_event (id, title, instance, starts_at, created_by, created_at, status, raid_id) VALUES (1, 'Thursday MC', 'Molten Core', 1700000000, ?, 1, 'started', 1)`, idLead)
	exec(`INSERT INTO raid (id, event_id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status, settled_at) VALUES (1, 1, 'Molten Core', 1700000000, ?, ?, ?, 'settled', 1700009000)`, idLead, text, sum)
	exec(`INSERT INTO transcript_upload (id, raid_id, person_id, text, uploaded_at) VALUES (1, 1, ?, ?, 1700009000)`, idLead, tr)
	exec(`UPDATE raid SET settled_upload_id = 1 WHERE id = 1`)
	exec(`INSERT INTO attendance (raid_id, person_id, kills_present, standby, full_run, characters) VALUES (1, ?, 2, 0, 1, 'Leadpal'), (1, ?, 2, 0, 1, 'Alice,Alicealt'), (1, ?, 2, 0, 1, 'Bob')`, idLead, idAlice, idBob)
	exec(`INSERT INTO auction (raid_id, auction_id, kind, item_id, item_link, floor, offspec_pct, winner_person_id, winner_character, price, bids_json, flags_json) VALUES
		(1, '1-1', 'test', 6948, '[Hearthstone]', 1, 0, NULL, '', 0, '[]', '[]'),
		(1, '1-2', 'loot', 19019, ?, 60, 0, ?, 'Alice', 150, '[{"Name":"Alice","Bid":200,"Valid":true,"Salt":"secret-salt"},{"Name":"Bob","Bid":150,"Valid":true},{"Name":"Leadpal","Bid":999,"Valid":false,"Reason":"over_credit"}]', '[]'),
		(1, '1-3', 'loot', 18563, ?, 20, 0, ?, 'Bob', 20, '[{"Name":"Bob","Bid":40,"Valid":true}]', '["voided:re-run"]'),
		(1, '1-4', 'loot', 18563, ?, 20, 0, ?, 'Bob', 25, '[{"Name":"Bob","Bid":25,"Valid":true,"Offspec":true}]', '[]')`,
		thunderfury, idAlice, binding, idBob, binding, idBob)
	exec(`INSERT INTO ledger (raid_id, person_id, delta, reason, auction_id, created_at) VALUES
		(1, ?, -150, 'purchase', '1-2', 1700009000), (1, ?, 45, 'share', '1-2', 1700009000), (1, ?, 45, 'share', '1-2', 1700009000), (1, ?, 45, 'share', '1-2', 1700009000)`,
		idAlice, idAlice, idBob, idLead)
	exec(`INSERT INTO performance (raid_id, person_id, spec, percentile, band, award) VALUES (1, ?, 'Fire', 91.4, 'A', 6)`, idAlice)

	// An open raid with a live update: one kill and an auction still bidding.
	snap2 := snap
	snap2.RaidID = "2"
	text2 := snap2.Text()
	sum2 := snapshot.Checksum(text2)
	live := fmt.Sprintf("FAIRSHARE TRANSCRIPT 1\nraid=2\nsnapshot=%s\nplayer=Leadpal\nversion=test\n"+
		"1700100100\tin\tLeadpal\tSNAP^2^%s^1700000000^0^DEFAULT=10^\n"+
		"1700100200\tin\tLeadpal\tKILL^1003^Gehennas^Alice,Leadpal\n"+
		"1700100300\tin\tLeadpal\tOPEN^2-1^loot^%s^10^1700100320^DEFAULT\n", sum2, sum2, binding)
	exec(`INSERT INTO raid (id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status) VALUES (2, 'Onyxia', 1700100000, ?, ?, ?, 'in_progress')`, idLead, text2, sum2)
	exec(`INSERT INTO raid_progress (raid_id, person_id, text, lines, updated_at) VALUES (2, ?, ?, 3, 1700100400)`, idLead, live)

	exec(`INSERT INTO raid (id, instance, date, leader_person_id, snapshot_text, snapshot_checksum, status) VALUES (3, 'Voided Raid', 1700200000, ?, '', '', 'void')`, idLead)

	s := &Server{Store: st, People: &people.Service{S: st}}
	return s, st
}

func get(t *testing.T, h http.Handler, path string) (int, string, http.Header) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	body, _ := io.ReadAll(rec.Body)
	return rec.Code, string(body), rec.Header()
}

func TestPages(t *testing.T) {
	s, _ := seed(t)
	h := s.Handler()
	shots := os.Getenv("FAIRSHARE_WEB_SHOTS") // folder to save each page's HTML for a visual check
	cases := []struct {
		path string
		want []string
	}{
		{"/", []string{"Raids completed", "Thursday MC", "Thunderfury, Blessed Blade", "Lifetime earned", "Bosses killed"}},
		{"/raids", []string{"Thursday MC", "Onyxia", "In progress", "Settled"}},
		{"/raids/1", []string{"Lucifron", "Magmadar", "Alicealt", ", then", "Thunderfury, Blessed Blade", "Voided", "Sold", "No bids", "off-spec", "&#43;45", "-105", "Fire", "test"}},
		{"/raids/2", []string{"Live:", "Gehennas", "Bidding", "Bindings of the Windseeker"}},
		{"/loot", []string{"Biggest purchases", "Thunderfury, Blessed Blade", "Bindings of the Windseeker"}},
		{"/loot?q=thunder", []string{"Results for", "Thunderfury"}},
		{"/leaderboards", []string{"Lifetime earned", "Boss kills", "Raids attended", "Full runs", "Items won", "Shares spent on loot", "Top performers", "91"}},
		{"/players", []string{"Alice", "Alicealt", "Leadpal"}},
		{"/players/Alice", []string{"Raid history", "Thursday MC", "Alicealt", ", then", "Can bid up to", "Thunderfury", "Bought an item", "Share of a sale", "Average percentile"}},
		{"/players/Alicealt", []string{"Alice", "Thunderfury"}},
		{"/faq", []string{"How It Works", "Is this GDKP?", "two-handed weapon", "second price", "how-bidding-works@1x.png?v="}},
		{"/rules", []string{"The Rules", "One person, one account", "two-handed weapon", "Won it, wear it"}},
	}
	for _, c := range cases {
		code, body, hdr := get(t, h, c.path)
		if code != http.StatusOK {
			t.Errorf("%s: status %d", c.path, code)
			continue
		}
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: missing %q", c.path, w)
			}
		}
		for _, leak := range []string{idLead, idAlice, idBob, "secret-salt", "<script>alert"} {
			if strings.Contains(body, leak) {
				t.Errorf("%s: page contains %q", c.path, leak)
			}
		}
		if !strings.Contains(hdr.Get("Content-Security-Policy"), "default-src 'none'") {
			t.Errorf("%s: no content security policy", c.path)
		}
		if shots != "" {
			name := strings.NewReplacer("/", "_", "?", "_", "=", "_").Replace(strings.TrimPrefix(c.path, "/"))
			if name == "" {
				name = "home"
			}
			body = strings.ReplaceAll(body, `href="/static/`, `href="`+filepath.ToSlash(staticDir(t))+`/`)
			body = strings.ReplaceAll(body, `src="/static/`, `src="`+filepath.ToSlash(staticDir(t))+`/`)
			_ = os.WriteFile(filepath.Join(shots, name+".html"), []byte(body), 0o644)
		}
	}
	// The voided raid's sale must not count, and Bob's escaped name must show.
	_, body, _ := get(t, h, "/raids/1")
	if !strings.Contains(body, "Bob &lt;script&gt;") {
		t.Error("display name not escaped as text")
	}
	_, body, _ = get(t, h, "/loot")
	if strings.Count(body, "Bindings of the Windseeker") != 2 { // the kept sale: once in Biggest, once in the list
		t.Errorf("voided sale counted: %d mentions", strings.Count(body, "Bindings of the Windseeker"))
	}
}

func staticDir(t *testing.T) string {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return "file:///" + filepath.ToSlash(filepath.Join(wd, "static"))
}

func TestNotFoundAndVoid(t *testing.T) {
	s, _ := seed(t)
	h := s.Handler()
	for _, p := range []string{"/raids/3", "/raids/99", "/raids/abc", "/players/Nobody", "/nope"} {
		if code, _, _ := get(t, h, p); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, code)
		}
	}
	if _, body, _ := get(t, h, "/raids"); strings.Contains(body, "Voided Raid") {
		t.Error("voided raid listed")
	}
}

func TestSearchIsLiteral(t *testing.T) {
	s, _ := seed(t)
	h := s.Handler()
	for _, q := range []string{"%", "_", `\`, "'; DROP TABLE raid; --"} {
		code, body, _ := get(t, h, "/loot?q="+url.QueryEscape(q))
		if code != http.StatusOK || !strings.Contains(body, "Nothing matches") {
			t.Errorf("search %q: status %d, matched something", q, code)
		}
		if code, _, _ := get(t, h, "/players?q="+url.QueryEscape(q)); code != http.StatusOK {
			t.Errorf("player search %q: status %d", q, code)
		}
	}
	if _, body, _ := get(t, h, "/raids"); !strings.Contains(body, "Thursday MC") {
		t.Fatal("raid table gone after the injection attempt")
	}
}

func TestStaticAndEmpty(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	h := (&Server{Store: st, People: &people.Service{S: st}}).Handler()
	if code, body, hdr := get(t, h, "/static/site.css"); code != 200 || !strings.Contains(body, "--bronze") || hdr.Get("Cache-Control") == "" {
		t.Fatalf("css: %d", code)
	}
	if code, _, _ := get(t, h, "/static/icon.png"); code != 200 {
		t.Fatalf("icon: %d", code)
	}
	if code, body, hdr := get(t, h, "/static/filter.js"); code != 200 || !strings.Contains(body, "input[type=search]") || !strings.Contains(hdr.Get("Content-Type"), "javascript") {
		t.Fatalf("filter.js: %d %s", code, hdr.Get("Content-Type"))
	}
	// The policy allows the site's own script and nothing else.
	if _, _, hdr := get(t, h, "/"); !strings.Contains(hdr.Get("Content-Security-Policy"), "script-src 'self'") {
		t.Fatal("script-src self missing from the content security policy")
	}
	// A brand-new community: every page renders with its empty state.
	for _, p := range []string{"/", "/raids", "/loot", "/leaderboards", "/players"} {
		code, body, _ := get(t, h, p)
		if code != 200 || !strings.Contains(body, `class="empty"`) {
			t.Errorf("%s: status %d, no empty state", p, code)
		}
	}
}

func TestRateLimit(t *testing.T) {
	l := newLimiter(3, 60_000_000_000) // 3 a minute
	for i := 0; i < 3; i++ {
		if !l.allow("a") {
			t.Fatalf("request %d refused", i+1)
		}
	}
	if l.allow("a") {
		t.Fatal("fourth request allowed")
	}
	if !l.allow("b") {
		t.Fatal("another visitor refused")
	}
}

func TestParseItem(t *testing.T) {
	it := parseItem(thunderfury)
	if it.Name != "Thunderfury, Blessed Blade" || it.Color != "ff8000" || it.ID != 19019 {
		t.Fatalf("%+v", it)
	}
	if it := parseItem("[Hearthstone]"); it.Name != "Hearthstone" {
		t.Fatalf("%+v", it)
	}
}

// A demo site says so on every page, and the sandbox banner never shows instead.
func TestDemoBanner(t *testing.T) {
	s, _ := seed(t)
	s.Demo = true
	h := s.Handler()
	for _, p := range []string{"/", "/raids/1", "/players/Alice", "/nope"} {
		_, body, _ := get(t, h, p)
		if !strings.Contains(body, "Every raider, raid and item here is made up") {
			t.Errorf("%s: no demo banner", p)
		}
	}
	s2, _ := seed(t)
	if _, body, _ := get(t, s2.Handler(), "/"); strings.Contains(body, "made up") {
		t.Error("demo banner on a real site")
	}
}
