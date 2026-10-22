// Package web is the community website: raid history, loot history, leaderboards and a
// page per player, read straight from the bot's database on every request. It has no
// state of its own and no admin side, so it never needs updating by hand: whatever the
// bot has settled, the site shows. Read-only, no login, no third-party code.
//
// What it never shows: Discord ids, Battle.net tags, bans, tokens or addon keys, dispute
// notes, bid salts. Everything shown is already public to the raid (bids are revealed in
// game) or to the community (the ledger is public by design).
package web

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JPWill/FairShare/backend/internal/content"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/store"
	"github.com/JPWill/FairShare/backend/internal/verify"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static
var staticFS embed.FS

// Server renders the site.
type Server struct {
	Store  *store.Store
	People *people.Service
	// TrustedProxy: take the client address from X-Forwarded-For (only behind a proxy
	// that overwrites it), for the per-address rate limit.
	TrustedProxy bool
	// Demo puts a banner on every page: the data is made up (fairshare-demo).
	Demo bool

	// Verify and Signer enable Battle.net verification through Discord (verify.go); both
	// nil when no client secret is configured.
	Verify *verify.Client
	Signer *verify.Signer
	// DiscordLink overrides the store's invite; the demo site has no bot of its own.
	DiscordLink string

	once  sync.Once
	pages map[string]*template.Template
	lim   *limiter
}

var pageNames = []string{"home", "raids", "raid", "loot", "leaderboards", "players", "player", "faq", "rules", "verify", "notfound"}

func (s *Server) init() {
	s.once.Do(func() {
		s.pages = map[string]*template.Template{}
		for _, name := range pageNames {
			t := template.Must(template.New("layout.html").Funcs(funcs).ParseFS(templateFS, "templates/layout.html", "templates/"+name+".html"))
			s.pages[name] = t
		}
		s.lim = newLimiter(240, time.Minute) // generous: pages are cheap, this only stops a flood
	})
}

// Handler serves the site. Mount it at "/" next to the API.
func (s *Server) Handler() http.Handler {
	s.init()
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(24*time.Hour, http.FileServer(http.FS(static)))))
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("GET /raids", s.raids)
	mux.HandleFunc("GET /raids/{id}", s.raid)
	mux.HandleFunc("GET /loot", s.loot)
	mux.HandleFunc("GET /leaderboards", s.leaderboards)
	mux.HandleFunc("GET /players", s.players)
	mux.HandleFunc("GET /players/{name}", s.player)
	mux.HandleFunc("GET /faq", s.faq)
	mux.HandleFunc("GET /rules", s.rules)
	mux.HandleFunc("GET /verify/battlenet", s.verifyStart)
	mux.HandleFunc("GET /oauth/discord/callback", s.verifyCallback)
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { s.notFound(w, r) })
	return s.guard(mux)
}

// guard adds the security headers every page gets and the per-address rate limit.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; img-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if !s.lim.allow(clientIP(r, s.TrustedProxy)) {
			h.Set("Retry-After", "60")
			http.Error(w, "Too many requests. Try again in a minute.", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func cacheFor(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		next.ServeHTTP(w, r)
	})
}

// view is what every page template receives.
type view struct {
	Title   string
	Nav     string
	Sandbox bool
	Demo    bool
	// Discord is the community invite: DiscordLink when set (the demo site borrows the live
	// server's), else guild_config; empty when unset or not a Discord link (checked again
	// here: the row could have been edited outside the bot).
	Discord string
	// Repo is the public source code, for the footer links.
	Repo string
	Data any
}

func (s *Server) render(w http.ResponseWriter, status int, page, title, nav string, data any) {
	t := s.pages[page]
	var b strings.Builder
	if err := t.Execute(&b, view{Title: title, Nav: nav, Sandbox: s.Store.IsSandbox(), Demo: s.Demo, Discord: s.discordInvite(), Repo: content.Repo, Data: data}); err != nil {
		log.Printf("web: render %s: %v", page, err)
		http.Error(w, "Something went wrong rendering this page.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(b.String()))
}

func (s *Server) discordInvite() string {
	if s.DiscordLink != "" && store.ValidDiscordInvite(s.DiscordLink) {
		return s.DiscordLink
	}
	if v := s.Store.ConfigString("discordInvite"); store.ValidDiscordInvite(v) {
		return v
	}
	return ""
}

// fail logs the real error and shows a plain page; database text never reaches the visitor.
func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("web: %v", err)
	http.Error(w, "Something went wrong reading the ledger. Try again shortly.", http.StatusInternalServerError)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.render(w, http.StatusNotFound, "notfound", "Not found", "", nil)
}

// ---------------------------------------------------------------- template helpers

var itemRe = regexp.MustCompile(`^\|c(?:ff)?([0-9a-fA-F]{6,8})\|Hitem:(\d+)[^|]*\|h\[([^\]]*)\]\|h\|r$`)

// Item is an item link reduced to what a web page can show.
type Item struct {
	Name  string
	Color string // hex without '#', from the link's quality colour
	ID    int64
}

// parseItem reads a WoW item link ("|cffa335ee|Hitem:19019:...|h[Thunderfury]|h|r").
func parseItem(link string) Item {
	m := itemRe.FindStringSubmatch(link)
	if m == nil {
		name := strings.Trim(link, "[]")
		if name == "" {
			name = "Unknown item"
		}
		return Item{Name: name, Color: "e6d7b8"}
	}
	color := m[1]
	if len(color) == 8 {
		color = color[2:]
	}
	id, _ := strconv.ParseInt(m[2], 10, 64)
	return Item{Name: m[3], Color: strings.ToLower(color), ID: id}
}

var funcs = template.FuncMap{
	"item":         parseItem,
	"dict":         dict,
	"sortURL":      sortURL,
	"sortClass":    sortClass,
	"sortQuery":    sortQuery,
	"linkify":      linkify,
	"markdownBold": markdownBold,
	// The colour classes are fixed names so the stylesheet can hold them (no inline styles,
	// which the content security policy forbids).
	"qualityClass": func(hex string) string {
		switch strings.ToLower(hex) {
		case "9d9d9d":
			return "q0"
		case "ffffff":
			return "q1"
		case "1eff00":
			return "q2"
		case "0070dd":
			return "q3"
		case "a335ee":
			return "q4"
		case "ff8000":
			return "q5"
		case "e6cc80":
			return "q6"
		}
		return "q1"
	},
	"classClass": func(class string) string {
		return "c-" + strings.ToLower(strings.ReplaceAll(class, " ", ""))
	},
	"date":     func(t int64) string { return time.Unix(t, 0).UTC().Format("Mon 2 Jan 2006") },
	"dateTime": func(t int64) string { return time.Unix(t, 0).UTC().Format("2 Jan 2006 15:04 UTC") },
	"signed": func(n int64) string {
		if n > 0 {
			return fmt.Sprintf("+%d", n)
		}
		return fmt.Sprint(n)
	},
	"sign": func(n int64) string {
		switch {
		case n > 0:
			return "pos"
		case n < 0:
			return "neg"
		}
		return "zero"
	},
	"playerURL": func(character string) string { return "/players/" + url.PathEscape(character) },
	"plural": func(n int, one, many string) string {
		if n == 1 {
			return one
		}
		return many
	},
	"pct": func(f float64) string { return fmt.Sprintf("%.0f", f) },
	"add": func(a, b int) int { return a + b },
	"statusLabel": func(s string) string {
		switch s {
		case "settled":
			return "Settled"
		case "contested":
			return "Under review"
		case "disputed":
			return "Awaiting officer"
		case "issued", "in_progress":
			return "In progress"
		}
		return s
	},
	"reasonLabel": reasonLabel,
}

func reasonLabel(r string) string {
	switch r {
	case "share":
		return "Share of a sale"
	case "purchase":
		return "Bought an item"
	case "penalty":
		return "Missed reveal"
	case "reversal":
		return "Reversed by an officer"
	case "adjustment":
		return "Officer adjustment"
	case "pool":
		return "Performance pool"
	case "award":
		return "Performance award"
	}
	return r
}

// ---------------------------------------------------------------- query helpers

func pageParam(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	if n > 10000 {
		return 10000
	}
	return n
}

// search trims a search box value to something sane for LIKE.
func search(r *http.Request) string {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) > 40 {
		q = q[:40]
	}
	return q
}

// likeArg escapes LIKE wildcards so a search for "100%" means the text "100%".
func likeArg(q string) string {
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%"
}

// ---------------------------------------------------------------- rate limit

type limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perWindow int, window time.Duration) *limiter {
	return &limiter{rate: float64(perWindow) / window.Seconds(), burst: float64(perWindow), buckets: map[string]*bucket{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= 4096 {
			l.evict(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.rate
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evict drops full buckets (idle visitors), or the oldest if none are full.
func (l *limiter) evict(now time.Time) {
	var oldestKey string
	var oldest time.Time
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
			continue
		}
		if oldestKey == "" || b.last.Before(oldest) {
			oldestKey, oldest = k, b.last
		}
	}
	if len(l.buckets) >= 4096 && oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		// Same rule as the API: the trusted proxy overwrites the header, so its first
		// entry is the visitor.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
