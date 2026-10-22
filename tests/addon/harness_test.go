package addontest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/rand"
	"strings"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func mustNew(t *testing.T, opts ...Options) *Harness {
	t.Helper()
	h, err := New(opts...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h
}

func mustEval(t *testing.T, h *Harness, expr string) lua.LValue {
	t.Helper()
	v, err := h.Eval(expr)
	if err != nil {
		t.Fatalf("eval %q: %v", expr, err)
	}
	return v
}

// TestSmoke proves every TOC file compiles and runs, and that login (SavedVariables
// defaults, minimap button, window build) completes under the stubs.
func TestSmoke(t *testing.T) {
	h := mustNew(t)
	if got := h.NS.RawGetString("NAME"); got != lua.LString("FairShare") {
		t.Fatalf("ns.NAME = %v", got)
	}
	if got := h.NS.RawGetString("PREFIX"); got != lua.LString("FAIRSHARE") {
		t.Fatalf("ns.PREFIX = %v", got)
	}
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `FairShareDB.settings.theme`); got != lua.LString("auto") {
		t.Fatalf("settings default not applied: %v", got)
	}
	if got := mustEval(t, h, `FairShareMinimapButton ~= nil`); got != lua.LTrue {
		t.Fatal("minimap button not created on login")
	}
	if got := mustEval(t, h, `FairShareMainWindow ~= nil`); got != lua.LTrue {
		t.Fatal("main window not built on login")
	}
	if err := h.Do(`FairShare_ToggleWindow()`); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `FairShareMainWindow:IsShown()`); got != lua.LTrue {
		t.Fatal("toggle did not show the window")
	}
}

func TestLeaderTabHiddenForNonLeaders(t *testing.T) {
	h := mustNew(t, Options{Leader: false})
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	// In a group (stub default) and not leader: no Leader tab.
	if err := h.Do(`FairShare.UI.Show()`); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h, `FairShare.UI.IsLeader()`); got != lua.LFalse {
		t.Fatalf("IsLeader() = %v for a grouped non-leader", got)
	}
	if got := mustEval(t, h, `FairShare.UI.TabNames()`).String(); got != "Home,Ledger,Raid,Settings" {
		t.Fatalf("non-leader tabs = %q", got)
	}
	h2 := mustNew(t, Options{Leader: true})
	if err := h2.Login(); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, h2, `FairShare.UI.IsLeader()`); got != lua.LTrue {
		t.Fatalf("IsLeader() = %v for the group leader", got)
	}
	if got := mustEval(t, h2, `FairShare.UI.TabNames()`).String(); got != "Home,Ledger,Raid,Roster,Settings" {
		t.Fatalf("leader tabs = %q", got)
	}
}

func luaSha(t *testing.T, h *Harness, input []byte) string {
	t.Helper()
	h.Set("Input", lua.LString(string(input)))
	return mustEval(t, h, `FairShare.Lib.Sha256.hex(Input)`).String()
}

func TestSha256KnownVectors(t *testing.T) {
	h := mustNew(t)
	cases := map[string]string{
		"":    "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		"abc": "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
		"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq": "248d6a61d20638b8e5c026930c3e6039a33ce45964ff2167f6ecedd419db06c1",
	}
	for in, want := range cases {
		if got := luaSha(t, h, []byte(in)); got != want {
			t.Errorf("sha256(%q) = %s, want %s", in, got, want)
		}
	}
	if got := mustEval(t, h, `FairShare.Lib.Sha256.short("abc")`).String(); got != "ba7816bf" {
		t.Errorf("short = %s", got)
	}
}

// TestSha256MatchesGo hashes random byte strings of many lengths (including every
// padding boundary around 55/56/64 bytes) in Lua and in Go and requires identical output.
func TestSha256MatchesGo(t *testing.T) {
	h := mustNew(t)
	rng := rand.New(rand.NewSource(20261020))
	lengths := []int{1, 2, 3, 31, 32, 33, 54, 55, 56, 57, 63, 64, 65, 100, 119, 120, 121, 127, 128, 129, 255, 256, 300, 511, 512, 1000}
	for _, n := range lengths {
		for rep := 0; rep < 3; rep++ {
			buf := make([]byte, n)
			rng.Read(buf)
			want := sha256.Sum256(buf)
			if got := luaSha(t, h, buf); got != hex.EncodeToString(want[:]) {
				t.Fatalf("len %d: lua %s != go %x", n, got, want)
			}
		}
	}
}

// TestSha256Timing is informational only: gopher-lua is far slower than the game's
// interpreter, so the number here is an upper bound, never a benchmark.
func TestSha256Timing(t *testing.T) {
	h := mustNew(t)
	in := []byte(strings.Repeat("x", 100))
	start := time.Now()
	for i := 0; i < 20; i++ {
		luaSha(t, h, in)
	}
	t.Logf("100-byte hash under gopher-lua: %v each (in-game will be much faster)", time.Since(start)/20)
}

func TestBase64MatchesGo(t *testing.T) {
	h := mustNew(t)
	rng := rand.New(rand.NewSource(7))
	for n := 0; n <= 40; n++ {
		buf := make([]byte, n)
		rng.Read(buf)
		h.Set("Input", lua.LString(string(buf)))
		want := base64.StdEncoding.EncodeToString(buf)
		if got := mustEval(t, h, `FairShare.Lib.Base64.encode(Input)`).String(); got != want {
			t.Fatalf("len %d: encode lua %q != go %q", n, got, want)
		}
		if got := mustEval(t, h, `FairShare.Lib.Base64.decode(FairShare.Lib.Base64.encode(Input))`).String(); got != string(buf) {
			t.Fatalf("len %d: decode roundtrip mismatch", n)
		}
	}
	// Whitespace inside a pasted string is ignored; junk is rejected.
	if got := mustEval(t, h, `FairShare.Lib.Base64.decode("aGVs\nbG8g d29y bGQ=")`).String(); got != "hello world" {
		t.Fatalf("decode with whitespace = %q", got)
	}
	if got := mustEval(t, h, `FairShare.Lib.Base64.decode("abc")`); got != lua.LNil {
		t.Fatalf("decode of bad length returned %v, want nil", got)
	}
	if got := mustEval(t, h, `FairShare.Lib.Base64.decode("ab$=")`); got != lua.LNil {
		t.Fatalf("decode of bad char returned %v, want nil", got)
	}
}

func TestCommEncodeDecodeEscapes(t *testing.T) {
	h := mustNew(t)
	got := mustEval(t, h, `(function()
		local Comm = FairShare.Core.Comm
		local body = Comm.Encode("T", { "a^b", "c~d", "", 42, "~1^0" })
		local typ, f = Comm.Decode(body)
		return typ .. "|" .. table.concat(f, "|") .. "|" .. #f
	end)()`).String()
	if got != "T|a^b|c~d||42|~1^0|5" {
		t.Fatalf("roundtrip = %q", got)
	}
}

func TestCommChunkingAndReassembly(t *testing.T) {
	h := mustNew(t)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`
		Received = nil
		FairShare.Core.Comm.On("BIG", function(sender, fields) Received = { sender = sender, fields = fields } end)
		FairShare.Core.Comm.Send("BIG", { string.rep("x", 600), "tail^with~specials" })
	`); err != nil {
		t.Fatal(err)
	}
	sent := h.SentMessages()
	if len(sent) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(sent))
	}
	for i, s := range sent {
		if len(s.Msg) > 255 {
			t.Fatalf("chunk %d is %d bytes", i, len(s.Msg))
		}
		if s.Channel != "RAID" {
			t.Fatalf("chunk %d channel %q", i, s.Channel)
		}
	}
	// Deliver out of order from a sender with no realm suffix: must reassemble and normalize the name.
	for _, i := range []int{2, 0, 1} {
		if err := h.Deliver(sent[i], "Other"); err != nil {
			t.Fatal(err)
		}
	}
	if got := mustEval(t, h, `Received and Received.sender`).String(); got != "Other" {
		t.Fatalf("sender = %q", got)
	}
	if got := mustEval(t, h, `#Received.fields[1]`); got != lua.LNumber(600) {
		t.Fatalf("field 1 length = %v", got)
	}
	if got := mustEval(t, h, `Received.fields[2]`).String(); got != "tail^with~specials" {
		t.Fatalf("field 2 = %q", got)
	}
}

func TestCommThrottle(t *testing.T) {
	h := mustNew(t)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`for i = 1, 15 do FairShare.Core.Comm.Send("N", { i }) end`); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 9 {
		t.Fatalf("burst: sent %d, want 9", n)
	}
	if err := h.AdvanceTime(1); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 10 {
		t.Fatalf("after 1s: sent %d, want 10", n)
	}
	if err := h.AdvanceTime(10); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 15 {
		t.Fatalf("after 11s: sent %d, want 15", n)
	}
	if got := mustEval(t, h, `FairShare.Core.Comm.QueueLength()`); got != lua.LNumber(0) {
		t.Fatalf("queue not drained: %v", got)
	}
	// Order preserved.
	if got := h.SentMessages()[14].Msg; got != "=N^15" {
		t.Fatalf("last message = %q", got)
	}
}

func TestCommLockdownRetry(t *testing.T) {
	h := mustNew(t)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	h.Set("SendResult", lua.LNumber(11))
	if err := h.Do(`FairShare.Core.Comm.Send("A", {})`); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 0 {
		t.Fatalf("sent during lockdown: %d", n)
	}
	if got := mustEval(t, h, `FairShare.Core.Comm.QueueLength()`); got != lua.LNumber(1) {
		t.Fatalf("queue length = %v, want 1", got)
	}
	// Encounter ends: retry immediately.
	h.Set("SendResult", lua.LNumber(0))
	if err := h.FireEvent("ENCOUNTER_END"); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 1 {
		t.Fatalf("after ENCOUNTER_END: sent %d, want 1", n)
	}

	// Without the event, the retry timer takes over.
	h.ClearSent()
	h.Set("SendResult", lua.LNumber(11))
	if err := h.Do(`FairShare.Core.Comm.Send("B", {})`); err != nil {
		t.Fatal(err)
	}
	h.Set("SendResult", lua.LNumber(0))
	if err := h.AdvanceTime(1); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 0 {
		t.Fatalf("retried too early: %d", n)
	}
	if err := h.AdvanceTime(2); err != nil {
		t.Fatal(err)
	}
	if n := len(h.SentMessages()); n != 1 {
		t.Fatalf("after retry window: sent %d, want 1", n)
	}
}

// TestPingPong runs two players. Alice pings; both receive it; Bob's pong (and Alice's
// own) come back; Alice's result list shows both with versions.
func TestPingPong(t *testing.T) {
	alice := mustNew(t, Options{PlayerName: "Alice", Leader: true})
	bob := mustNew(t, Options{PlayerName: "Bob"})
	for _, h := range []*Harness{alice, bob} {
		if err := h.Login(); err != nil {
			t.Fatal(err)
		}
	}
	if err := alice.Do(`FairShare.Core.Ping.Send()`); err != nil {
		t.Fatal(err)
	}
	pings := alice.SentMessages()
	if len(pings) != 1 || !strings.HasPrefix(pings[0].Msg, "=PING^") {
		t.Fatalf("unexpected ping traffic: %+v", pings)
	}
	alice.ClearSent()
	for _, h := range []*Harness{alice, bob} {
		if err := h.Deliver(pings[0], "Alice"); err != nil {
			t.Fatal(err)
		}
	}
	if err := alice.AdvanceTime(0.05); err != nil {
		t.Fatal(err)
	}
	// Both replied with PONG on RAID.
	for name, h := range map[string]*Harness{"Alice": alice, "Bob": bob} {
		s := h.SentMessages()
		if len(s) != 1 || !strings.HasPrefix(s[0].Msg, "=PONG^") {
			t.Fatalf("%s pong traffic: %+v", name, s)
		}
	}
	if err := alice.Deliver(alice.SentMessages()[0], "Alice"); err != nil {
		t.Fatal(err)
	}
	if err := alice.Deliver(bob.SentMessages()[0], "Bob"); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, alice, `#FairShare.Core.Ping.results`); got != lua.LNumber(2) {
		t.Fatalf("results = %v, want 2", got)
	}
	summary := mustEval(t, alice, `(function()
		local out = {}
		for _, r in ipairs(FairShare.Core.Ping.results) do out[#out+1] = r.name .. ":" .. r.version end
		return table.concat(out, ",")
	end)()`).String()
	if summary != "Alice:test,Bob:test" {
		t.Fatalf("summary = %q", summary)
	}
	// Bob never sent a ping, so a stray PONG to him is ignored.
	if err := bob.Deliver(alice.SentMessages()[0], "Alice"); err != nil {
		t.Fatal(err)
	}
	if got := mustEval(t, bob, `#FairShare.Core.Ping.results`); got != lua.LNumber(0) {
		t.Fatalf("bob results = %v, want 0", got)
	}
}

// TestSoloWhispersSelf: with no group, messages go to the player by whisper.
func TestSoloWhispersSelf(t *testing.T) {
	h := mustNew(t, Options{PlayerName: "Solo"})
	h.Set("InRaid", lua.LFalse)
	h.Set("InGroup", lua.LFalse)
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	if err := h.Do(`FairShare.Core.Ping.Send()`); err != nil {
		t.Fatal(err)
	}
	s := h.SentMessages()
	if len(s) != 1 || s[0].Channel != "WHISPER" || s[0].Target != "Solo" {
		t.Fatalf("solo send = %+v", s)
	}
}
