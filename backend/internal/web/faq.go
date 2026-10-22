package web

import (
	"net/http"
	"strings"

	"github.com/JPWill/FairShare/backend/internal/content"
)

// faq renders How It Works from the shared content package with the live settings, so
// the page, the #faq channel and the rules the bot enforces can never disagree.
func (s *Server) faq(w http.ResponseWriter, r *http.Request) {
	floor := func(slot string, fallback int64) int64 {
		var v int64
		if err := s.Store.DB.QueryRow(`SELECT value FROM floor_price WHERE slot = ?`, slot).Scan(&v); err != nil {
			return fallback
		}
		return v
	}
	p := content.Params{
		FloorDefault: floor("DEFAULT", 10), Floor2H: floor("INVTYPE_2HWEAPON", 20),
		Newcomer: s.Store.ConfigInt("newcomerCredit"), PerRaid: s.Store.ConfigInt("creditPerRaid"), Cap: s.Store.ConfigInt("creditCap"),
		PoolPct: s.Store.ConfigInt("poolPct"), FreePasses: s.Store.ConfigInt("freePasses"), Penalty: s.Store.ConfigInt("norevealPenalty"),
		MinAge: s.Store.ConfigInt("minAccountAgeDays"), OffspecPct: s.Store.ConfigInt("offspecPct"),
		Site: siteFromRequest(r), Discord: s.discordInvite(),
	}
	s.render(w, http.StatusOK, "faq", "How It Works", "faq", map[string]any{
		"Summary": p.Summary(), "Sections": p.Sections(),
	})
}

// siteFromRequest is the address this page was asked for, so links inside the shared
// text stay on the site the visitor is on: the demo links to demo.fairsharewow.com and
// never sends anyone to the live ledger. Plain HTTP only for a local run.
func siteFromRequest(r *http.Request) string {
	host := r.Host
	if host == "" {
		return content.DefaultSite
	}
	scheme := "https"
	if strings.HasPrefix(host, "localhost") || strings.HasPrefix(host, "127.") || strings.HasPrefix(host, "[::1]") {
		scheme = "http"
	}
	return scheme + "://" + host
}

// rules renders the community rules on their own page, from the same text the bot
// pins in #rules and docs/rules.md carries.
func (s *Server) rules(w http.ResponseWriter, r *http.Request) {
	floor := func(slot string, fallback int64) int64 {
		var v int64
		if err := s.Store.DB.QueryRow(`SELECT value FROM floor_price WHERE slot = ?`, slot).Scan(&v); err != nil {
			return fallback
		}
		return v
	}
	p := content.Params{FloorDefault: floor("DEFAULT", 10), Floor2H: floor("INVTYPE_2HWEAPON", 20), PoolPct: s.Store.ConfigInt("poolPct")}
	s.render(w, http.StatusOK, "rules", "Rules", "rules", map[string]any{"Intro": content.RulesIntro, "Rules": p.Rules(), "Outro": content.RulesOutro})
}
