package api

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestRaidNoteOnlyForLeader(t *testing.T) {
	f := newLiveFixture(t, 0)
	if _, err := f.r.S.DB.Exec(`UPDATE raid SET settle_note = 'secret officer reason' WHERE id = ?`, f.raid.ID); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/raids/" + jsonNum(f.raid.ID)
	if code, body := f.call("GET", path, f.lead, nil); code != 200 || body["note"] != "secret officer reason" {
		t.Fatalf("leader should see the note: %d %v", code, body)
	}
	code, body := f.call("GET", path, f.other, nil)
	if code != 200 {
		t.Fatalf("other: %d %v", code, body)
	}
	if _, has := body["note"]; has {
		t.Fatalf("non-leader must not see the settle note: %v", body)
	}
}

func TestLimiterPerAddress(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newLimiter(2, func() time.Time { return now })
	// A token bucket holds perMinute; an address bucket holds ipLimitFactor times that.
	for i := 0; i < 2; i++ {
		if !l.allow("tok:a", 1) {
			t.Fatalf("token request %d refused", i+1)
		}
	}
	if l.allow("tok:a", 1) {
		t.Fatal("third token request allowed")
	}
	for i := 0; i < 2*ipLimitFactor; i++ {
		if !l.allow("ip:1.2.3.4", ipLimitFactor) {
			t.Fatalf("address request %d refused", i+1)
		}
	}
	if l.allow("ip:1.2.3.4", ipLimitFactor) {
		t.Fatal("address bucket did not run out")
	}
	// Refill after a minute.
	now = now.Add(time.Minute)
	if !l.allow("tok:a", 1) {
		t.Fatal("token bucket did not refill")
	}
}

func TestLimiterEvictsOldestBucket(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	l := newLimiter(60, func() time.Time { return now })
	for i := 0; i < maxBuckets; i++ {
		l.allow("tok:"+strconv.Itoa(i), 1)
		now = now.Add(time.Millisecond)
	}
	if len(l.buckets) != maxBuckets {
		t.Fatalf("buckets: %d", len(l.buckets))
	}
	l.allow("tok:new", 1)
	if len(l.buckets) != maxBuckets {
		t.Fatalf("cap not held: %d", len(l.buckets))
	}
	if _, kept := l.buckets["tok:0"]; kept {
		t.Fatal("the oldest bucket should have been evicted")
	}
	if _, kept := l.buckets["tok:new"]; !kept {
		t.Fatal("the new bucket should exist")
	}
}

func TestRateLimitPerAddressBeforeToken(t *testing.T) {
	// With a per-token limit of 1 the address bucket holds ipLimitFactor requests, so a
	// flood of distinct guessed tokens from one address is cut off by the address bucket
	// before any of them reaches the database.
	f := newLiveFixture(t, 1)
	limited := false
	for i := 0; i < ipLimitFactor+1; i++ {
		if code, _ := f.call("GET", "/api/v1/me", "guess"+strconv.Itoa(i), nil); code == 429 {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("distinct guessed tokens from one address were never throttled")
	}
}

func TestClientIPHonoursProxyOnlyWhenTrusted(t *testing.T) {
	req, _ := http.NewRequest("GET", "/api/v1/me", nil)
	req.RemoteAddr = "10.0.0.9:5555"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	if ip := (&Server{}).clientIP(req); ip != "10.0.0.9" {
		t.Fatalf("untrusted proxy: %s", ip)
	}
	if ip := (&Server{TrustedProxy: true}).clientIP(req); ip != "203.0.113.7" {
		t.Fatalf("trusted proxy: %s", ip)
	}
}
