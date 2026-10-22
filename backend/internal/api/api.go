// Package api is the HTTP API the sync client uses (docs/protocol.md §7).
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// Server serves the API.
type Server struct {
	People *people.Service
	Raids  *raids.Service
	// OnUpload, if set, is called after every accepted upload (the bot posts summaries).
	OnUpload func(raidID int64, by string, out raids.Outcome)
	// OnProgress, if set, is called after every accepted live update (the bot edits the
	// live ledger message).
	OnProgress func(raidID int64, by string, out raids.Outcome)
	// RateLimit is the number of requests one token may make per minute (0 = default 60).
	// Each remote address gets ipLimitFactor times that, shared by every token behind it.
	RateLimit int
	// TrustedProxy makes the limiter take the client address from X-Forwarded-For.
	// Only set it when a reverse proxy in front of the API overwrites that header;
	// otherwise any caller could pick its own bucket.
	TrustedProxy bool

	limiter *limiter
}

// ipLimitFactor: how many times the per-token rate one address may use in total, so a
// household or VPN exit shared by several raiders is not throttled as one client.
const ipLimitFactor = 10

// maxBuckets caps the limiter's memory; the oldest-touched bucket goes when exceeded.
const maxBuckets = 4096

// Handler returns the router. The limiter is built here, before any request can
// arrive, so there is no lazy initialisation to race on.
func (s *Server) Handler() http.Handler {
	per := s.RateLimit
	if per <= 0 {
		per = 60
	}
	s.limiter = newLimiter(float64(per), time.Now)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", s.auth(s.me))
	mux.HandleFunc("GET /api/v1/snapshot/current", s.auth(s.currentSnapshot))
	mux.HandleFunc("POST /api/v1/transcripts", s.auth(s.uploadTranscript))
	mux.HandleFunc("POST /api/v1/raids/{id}/finish", s.auth(s.finishRaid))
	mux.HandleFunc("GET /api/v1/raids/{id}", s.auth(s.raid))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	return mux
}

// auth resolves the bearer token to a person and applies the rate limits: one bucket
// per remote address (checked first, so a flood from one place is cut before the
// token is even hashed) and one per token. Bad tokens are limited too (by the token's
// hash), so guessing is slow.
func (s *Server) auth(next func(http.ResponseWriter, *http.Request, people.Person)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow("ip:"+s.clientIP(r), ipLimitFactor) {
			tooMany(w)
			return
		}
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			fail(w, 401, "missing bearer token")
			return
		}
		tok := strings.TrimSpace(h[7:])
		sum := sha256.Sum256([]byte(tok))
		if !s.limiter.allow("tok:"+hex.EncodeToString(sum[:8]), 1) {
			tooMany(w)
			return
		}
		p, err := s.People.Authenticate(tok)
		if err != nil {
			fail(w, 401, "invalid token")
			return
		}
		next(w, r, p)
	}
}

func tooMany(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	fail(w, 429, "too many requests; try again in a minute")
}

// clientIP is the address the limiter keys on: the connection's remote host, or the
// first X-Forwarded-For entry when a trusted proxy sits in front.
func (s *Server) clientIP(r *http.Request) string {
	if s.TrustedProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			first, _, _ := strings.Cut(xff, ",")
			if ip := strings.TrimSpace(first); ip != "" {
				return ip
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter is a token bucket per key: `perMinute × factor` requests, refilling continuously.
type limiter struct {
	mu        sync.Mutex
	perMinute float64
	buckets   map[string]*bucket
	now       func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(perMinute float64, now func() time.Time) *limiter {
	return &limiter{perMinute: perMinute, buckets: map[string]*bucket{}, now: now}
}

// allow takes one request from the key's bucket, whose capacity is perMinute × factor.
func (l *limiter) allow(key string, factor float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	capacity := l.perMinute * factor
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) >= maxBuckets {
			l.evictOldest()
		}
		b = &bucket{tokens: capacity, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Minutes() * capacity
	if b.tokens > capacity {
		b.tokens = capacity
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// evictOldest drops the least recently touched bucket (and anything idle over an hour,
// which costs nothing extra while we are scanning). Called with the lock held.
func (l *limiter) evictOldest() {
	now := l.now()
	var oldestKey string
	var oldest time.Time
	for k, v := range l.buckets {
		if now.Sub(v.last) > time.Hour {
			delete(l.buckets, k)
			continue
		}
		if oldestKey == "" || v.last.Before(oldest) {
			oldestKey, oldest = k, v.last
		}
	}
	if len(l.buckets) >= maxBuckets && oldestKey != "" {
		delete(l.buckets, oldestKey)
	}
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// internalError logs the real error and answers with a generic message: database and
// file paths are nobody's business on the wire.
func internalError(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("api %s %s: %v", r.Method, r.URL.Path, err)
	fail(w, 500, "internal error")
}

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// forbidden reports whether a raids error means the caller may not do this (403):
// not the leader for live updates or finish, or not allowed to upload for the raid at
// all (banned, or neither its leader nor present in it).
func forbidden(err error) bool {
	return errors.Is(err, raids.ErrNotLeader) || errors.Is(err, raids.ErrFinishNotLeader) || errors.Is(err, raids.ErrNotAllowed)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, p people.Person) {
	ok(w, map[string]any{"discordId": p.DiscordID, "displayName": p.DisplayName})
}

func (s *Server) currentSnapshot(w http.ResponseWriter, r *http.Request, p people.Person) {
	raid, err := s.Raids.CurrentForLeader(p.DiscordID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, "no open raid led by you")
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	// raidId is a string on the wire: the client matches it against transcript headers.
	ok(w, map[string]any{"raidId": fmt.Sprint(raid.ID), "text": raid.SnapshotText, "checksum": raid.SnapshotChecksum, "issuedAt": raid.Date, "status": raid.Status})
}

// uploadTranscript takes {raidId, text[, live]}. A plain upload is a witness statement
// that can settle the raid. With live=true it is a mid-raid progress update from the
// leader's sync client: stored for the live ledger, never settled, replaced by the next.
func (s *Server) uploadTranscript(w http.ResponseWriter, r *http.Request, p people.Person) {
	var body struct {
		RaidID raidID `json:"raidId"`
		Text   string `json:"text"`
		Live   bool   `json:"live"`
	}
	buf, err := io.ReadAll(io.LimitReader(r.Body, 8*1024*1024))
	if err != nil || json.Unmarshal(buf, &body) != nil {
		fail(w, 400, "bad json")
		return
	}
	id := int64(body.RaidID)
	if id == 0 || body.Text == "" {
		fail(w, 400, "raidId and text are required")
		return
	}
	if body.Live {
		out, err := s.Raids.Progress(id, p.DiscordID, body.Text)
		if forbidden(err) {
			fail(w, 403, err.Error())
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			fail(w, 404, fmt.Sprintf("raid %d not found", id))
			return
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if s.OnProgress != nil {
			s.OnProgress(id, p.DiscordID, out)
		}
		ok(w, map[string]any{"status": out.Status, "message": out.Message, "uploads": out.Uploads, "flags": flagsOf(out)})
		return
	}
	out, err := s.Raids.Upload(id, p.DiscordID, body.Text)
	if forbidden(err) {
		fail(w, 403, err.Error())
		return
	}
	if errors.Is(err, raids.ErrAlreadySettled) {
		fail(w, 409, "the raid was settled by another upload moments ago; nothing changed")
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if s.OnUpload != nil {
		s.OnUpload(id, p.DiscordID, out)
	}
	ok(w, map[string]any{"status": out.Status, "message": out.Message, "uploads": out.Uploads, "flags": flagsOf(out)})
}

// raidID accepts a JSON number or a numeric string: the sync client carries raid ids as
// strings (they come from transcript headers), the bot and tests send numbers.
type raidID int64

func (r *raidID) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		*r = 0
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("raidId must be a number: %w", err)
	}
	*r = raidID(n)
	return nil
}

// finishRaid settles a raid from its last live update (leader only).
func (s *Server) finishRaid(w http.ResponseWriter, r *http.Request, p people.Person) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "bad id")
		return
	}
	out, err := s.Raids.Finish(id, p.DiscordID)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, fmt.Sprintf("raid %d not found", id))
		return
	}
	if forbidden(err) {
		fail(w, 403, err.Error())
		return
	}
	if errors.Is(err, raids.ErrAlreadySettled) {
		fail(w, 409, "the raid was settled moments ago; nothing changed")
		return
	}
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if s.OnUpload != nil {
		s.OnUpload(id, p.DiscordID, out)
	}
	ok(w, map[string]any{"status": out.Status, "message": out.Message, "uploads": out.Uploads, "flags": flagsOf(out)})
}

func flagsOf(out raids.Outcome) []string {
	if out.Report == nil {
		return nil
	}
	return out.Report.AllFlags()
}

// raid returns a raid's status. The settle note (flags, transcript differences, officer
// reasons) is only for the raid's leader; everyone else gets status and checksum.
func (s *Server) raid(w http.ResponseWriter, r *http.Request, p people.Person) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		fail(w, 400, "bad id")
		return
	}
	raid, err := s.Raids.Get(id)
	if errors.Is(err, store.ErrNotFound) {
		fail(w, 404, fmt.Sprintf("raid %d not found", id))
		return
	}
	if err != nil {
		internalError(w, r, err)
		return
	}
	resp := map[string]any{"raidId": fmt.Sprint(raid.ID), "instance": raid.Instance, "status": raid.Status, "checksum": raid.SnapshotChecksum}
	if raid.LeaderPersonID == p.DiscordID {
		resp["note"] = raid.SettleNote
	}
	ok(w, resp)
}
