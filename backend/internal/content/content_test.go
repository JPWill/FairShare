package content

import (
	"os"
	"strings"
	"testing"
)

var defaults = Params{FloorDefault: 10, Floor2H: 20, Newcomer: 100, PerRaid: 100, Cap: 500, PoolPct: 10, FreePasses: 1, Penalty: 25, MinAge: 30, Site: DefaultSite}

func TestFAQQuotesSettings(t *testing.T) {
	faq := defaults.FAQ()
	if len(faq) < 15 {
		t.Fatalf("faq has %d entries", len(faq))
	}
	all := ""
	ids := map[string]bool{}
	for _, qa := range faq {
		if qa.ID == "" || qa.Q == "" || qa.A == "" || ids[qa.ID] {
			t.Errorf("bad entry %+v", qa)
		}
		ids[qa.ID] = true
		if len(qa.Q) > 256 || len(qa.A) > 1024 {
			t.Errorf("%s: too long for a Discord embed field (%d/%d)", qa.ID, len(qa.Q), len(qa.A))
		}
		all += qa.A + "\n"
	}
	for _, w := range []string{"10 shares for any item, 20 for a two-handed weapon", "180 is split evenly", "9 each", "credit limit on night one is 100", "10% of every sale", "one free pass", "25 shares", "30 days old", DefaultSite + "/players"} {
		if !strings.Contains(all, w) {
			t.Errorf("faq missing %q", w)
		}
	}
	zero := defaults
	zero.PoolPct = 0
	all = ""
	for _, qa := range zero.FAQ() {
		all += qa.A
	}
	if !strings.Contains(all, "set to 0%") || !strings.Contains(all, "200 is split evenly") {
		t.Error("pool at 0% not reflected")
	}
	with := defaults
	with.Discord = "https://discord.gg/abc"
	for _, qa := range with.FAQ() {
		if qa.ID == "join" && !strings.Contains(qa.A, "https://discord.gg/abc") {
			t.Error("join answer should carry the invite")
		}
	}
}

// The checked-in rules page must say what the bot posts.
func TestRulesMatchDocs(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/rules.md")
	if err != nil {
		t.Skip("docs/rules.md not found: " + err.Error())
	}
	text := string(doc)
	for i, r := range defaults.Rules() {
		if !strings.Contains(text, r) {
			t.Errorf("rule %d not in docs/rules.md:\n%s", i+1, r)
		}
	}
	for _, s := range []string{RulesIntro, RulesOutro} {
		if !strings.Contains(text, s) {
			t.Errorf("docs/rules.md missing: %s", s)
		}
	}
}

func TestTermsAndGetStartedFit(t *testing.T) {
	if n := len(defaults.Terms()); n > 4000 {
		t.Errorf("terms too long for one embed: %d", n)
	}
	if n := len(defaults.GetStarted()); n > 4000 {
		t.Errorf("get started too long: %d", n)
	}
	if !strings.Contains(defaults.Terms(), DefaultSite) {
		t.Error("terms should name the website")
	}
}

func TestSectionsCoverEveryQuestion(t *testing.T) {
	seen := map[string]int{}
	for _, s := range defaults.Sections() {
		if s.Title == "" || len(s.Items) == 0 {
			t.Errorf("empty section %+v", s)
		}
		for _, qa := range s.Items {
			seen[qa.ID]++
		}
	}
	for _, qa := range defaults.FAQ() {
		if seen[qa.ID] != 1 {
			t.Errorf("%s appears %d times in the sections", qa.ID, seen[qa.ID])
		}
	}
}
