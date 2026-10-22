package addontest

import (
	"fmt"
	"strings"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

// These tests use a real (numeric) raid id, so none of the developer exemptions apply:
// they check that FairShare tracks only the scheduled raid the bot opened, in a group of
// the right kind, inside the right kind of instance.

// leaderIn logs a group leader in with the given group shape, Developer mode off.
func leaderIn(t *testing.T, inRaid, inGroup bool) *Harness {
	t.Helper()
	h := mustNew(t, Options{PlayerName: "Leader", Leader: true})
	h.Set("InRaid", lua.LBool(inRaid))
	h.Set("InGroup", lua.LBool(inGroup))
	if err := h.Login(); err != nil {
		t.Fatal(err)
	}
	return h
}

// importRaid imports a snapshot for raid id starting startOffset seconds from now.
func importRaid(t *testing.T, h *Harness, id string, startOffset int, party bool) {
	t.Helper()
	code := fmt.Sprintf(`
		local S, B = FairShare.Lib.Serialize, FairShare.Lib.Base64
		local text = S.BuildSnapshot({ raidID = %q, issued = GetServerTime(), starts = GetServerTime() + (%d), party = %v,
			offspecPct = 0, floors = { DEFAULT = 10 }, banned = {},
			rows = { ["Leader"] = { owner = "L", balance = 50, credit = 100 }, ["Other"] = { owner = "O", balance = 20, credit = 100 } } })
		assert(FairShare.Snapshot.Import(B.encode(text), "paste"))
	`, id, startOffset, party)
	if err := h.Do(code); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
}

func sentType(h *Harness, msgType string) int {
	n := 0
	for _, m := range h.SentMessages() {
		if strings.HasPrefix(strings.TrimPrefix(m.Msg, "="), msgType+"^") {
			n++
		}
	}
	return n
}

func TestPartyIsIgnoredUnlessTheBotAllowsIt(t *testing.T) {
	h := leaderIn(t, false, true) // a 5-player party
	importRaid(t, h, "12", 0, false)
	if n := sentType(h, "SNAP"); n != 0 {
		t.Fatalf("snapshot sent to a party %d times", n)
	}
	if mustEval(t, h, `FairShare.Raid.context == nil`) != lua.LTrue {
		t.Fatal("a party started the raid")
	}
	if why := mustEval(t, h, `FairShare.Raid.BroadcastBlocker()`).String(); !strings.Contains(why, "raid groups") {
		t.Fatalf("blocker = %q", why)
	}
	if ok := mustEval(t, h, `(FairShare.Raid.Broadcast())`); ok != lua.LFalse {
		t.Fatal("Rebroadcast sent to a party")
	}

	// A snapshot issued while the bot's "5-player groups" switch was on counts the party.
	importRaid(t, h, "13", 0, true)
	if n := sentType(h, "SNAP"); n != 1 {
		t.Fatalf("party-allowed snapshot sent %d times", n)
	}
	if got := mustEval(t, h, `FairShare.Raid.context and FairShare.Raid.context.raidID`).String(); got != "13" {
		t.Fatalf("context = %s", got)
	}
}

func TestRaidGroupBroadcasts(t *testing.T) {
	h := leaderIn(t, true, true)
	importRaid(t, h, "12", 0, false)
	if n := sentType(h, "SNAP"); n != 1 {
		t.Fatalf("SNAP sent %d times in a raid group", n)
	}
}

func TestSoloLeaderSendsNothing(t *testing.T) {
	h := leaderIn(t, false, false)
	importRaid(t, h, "12", 0, false)
	if n := len(h.SentMessages()); n != 0 {
		t.Fatalf("solo leader sent %d messages", n)
	}
	if mustEval(t, h, `FairShare.Raid.context == nil`) != lua.LTrue {
		t.Fatal("solo login started the raid")
	}
}

func TestSnapshotOnlyNearItsStart(t *testing.T) {
	h := leaderIn(t, true, true)
	importRaid(t, h, "12", -13*3600, false) // last night's raid
	if n := sentType(h, "SNAP"); n != 0 {
		t.Fatal("stale snapshot sent")
	}
	if why := mustEval(t, h, `FairShare.Raid.BroadcastBlocker()`).String(); !strings.Contains(why, "12 hours") {
		t.Fatalf("blocker = %q", why)
	}

	h = leaderIn(t, true, true)
	importRaid(t, h, "14", 4*3600, false) // tonight, four hours from now
	if n := sentType(h, "SNAP"); n != 0 {
		t.Fatal("snapshot sent four hours early")
	}
	if why := mustEval(t, h, `FairShare.Raid.BroadcastBlocker()`).String(); !strings.Contains(why, "starts at") {
		t.Fatalf("blocker = %q", why)
	}
	// An hour later the window is open; the next roster change sends it.
	if err := h.AdvanceTime(3600 + 60); err != nil {
		t.Fatal(err)
	}
	if err := h.FireEvent("GROUP_ROSTER_UPDATE"); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
	if n := sentType(h, "SNAP"); n != 1 {
		t.Fatalf("SNAP sent %d times once the window opened", n)
	}
}

func setInstance(t *testing.T, h *Harness, kind string) {
	t.Helper()
	code := `function IsInInstance() return false, "none" end`
	if kind != "" {
		code = fmt.Sprintf(`function IsInInstance() return true, %q end`, kind)
	}
	if err := h.Do(code); err != nil {
		t.Fatal(err)
	}
}

func kill(t *testing.T, h *Harness) {
	t.Helper()
	if err := h.FireEvent("ENCOUNTER_END", lua.LNumber(1001), lua.LString("Some Boss"), lua.LNumber(1), lua.LNumber(5), lua.LNumber(1)); err != nil {
		t.Fatal(err)
	}
	pump(t, h, 1)
}

func TestKillsCountOnlyInTheRaidsInstance(t *testing.T) {
	h := leaderIn(t, true, true)
	importRaid(t, h, "12", 0, false)
	setInstance(t, h, "party")
	kill(t, h)
	setInstance(t, h, "")
	kill(t, h)
	if n := sentType(h, "KILL"); n != 0 {
		t.Fatalf("dungeon or open-world kill recorded %d times", n)
	}
	setInstance(t, h, "raid")
	kill(t, h)
	if n := sentType(h, "KILL"); n != 1 {
		t.Fatalf("raid kill sent %d times", n)
	}

	// With the 5-player switch, a dungeon boss is the test raid's boss.
	h = leaderIn(t, false, true)
	importRaid(t, h, "13", 0, true)
	setInstance(t, h, "party")
	kill(t, h)
	if n := sentType(h, "KILL"); n != 1 {
		t.Fatalf("party-allowed dungeon kill sent %d times", n)
	}
}

func TestLeavingTheGroupEndsTheRaid(t *testing.T) {
	h := leaderIn(t, true, true)
	importRaid(t, h, "12", 0, false)
	if mustEval(t, h, `FairShare.Raid.context ~= nil`) != lua.LTrue {
		t.Fatal("no raid to end")
	}
	h.Set("InRaid", lua.LFalse)
	h.Set("InGroup", lua.LFalse)
	if err := h.FireEvent("GROUP_ROSTER_UPDATE"); err != nil {
		t.Fatal(err)
	}
	if mustEval(t, h, `FairShare.Raid.context == nil`) != lua.LTrue {
		t.Fatal("raid still active after leaving the group")
	}
	before := mustEval(t, h, `#FairShare.Transcript.Lines("12")`).String()
	setInstance(t, h, "party")
	if err := h.FireEvent("CHAT_MSG_LOOT", lua.LString("You receive loot: |cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r.")); err != nil {
		t.Fatal(err)
	}
	if after := mustEval(t, h, `#FairShare.Transcript.Lines("12")`).String(); after != before {
		t.Fatalf("loot recorded after leaving: %s -> %s lines", before, after)
	}

	// The same for a party that is not allowed, when the leader turns a raid back into one.
	h = leaderIn(t, true, true)
	importRaid(t, h, "14", 0, false)
	h.Set("InRaid", lua.LFalse)
	if err := h.FireEvent("GROUP_ROSTER_UPDATE"); err != nil {
		t.Fatal(err)
	}
	if mustEval(t, h, `FairShare.Raid.context == nil`) != lua.LTrue {
		t.Fatal("raid still active in a party it does not allow")
	}
}

func TestLootLinesOnlyInsideAnInstance(t *testing.T) {
	h := leaderIn(t, true, true)
	importRaid(t, h, "12", 0, false)
	lines := func() string { return mustEval(t, h, `#FairShare.Transcript.Lines("12")`).String() }
	loot := lua.LString("Other receives loot: |cffa335ee|Hitem:19019::::::::60:::::::::|h[Thunderfury]|h|r.")
	before := lines()
	setInstance(t, h, "")
	if err := h.FireEvent("CHAT_MSG_LOOT", loot); err != nil {
		t.Fatal(err)
	}
	if lines() != before {
		t.Fatal("open-world loot recorded")
	}
	setInstance(t, h, "raid")
	if err := h.FireEvent("CHAT_MSG_LOOT", loot); err != nil {
		t.Fatal(err)
	}
	if lines() == before {
		t.Fatal("raid loot not recorded")
	}
}

// The Lua builder writes the optional lines exactly where the Go builder does
// (backend/internal/snapshot TestStartsAndParty pins the same text), so checksums agree.
func TestSnapshotStartsAndPartyLines(t *testing.T) {
	h := mustNew(t)
	got := mustEval(t, h, `FairShare.Lib.Serialize.BuildSnapshot({ raidID = "1", issued = 5, offspecPct = 0, starts = 1792550000, party = true, floors = { DEFAULT = 10 }, banned = {}, rows = {} })`).String()
	want := "FAIRSHARE SNAPSHOT 1\nraid=1\nissued=5\noffspec=0\nstarts=1792550000\nparty=1\nfloor:DEFAULT=10"
	if got != want {
		t.Fatalf("Lua build:\n%q\nwant\n%q", got, want)
	}
	if mustEval(t, h, `(function() local s = FairShare.Lib.Serialize.ParseSnapshot(`+"[["+want+"]]"+`) return s.starts == 1792550000 and s.party == true end)()`) != lua.LTrue {
		t.Fatal("Lua parse lost starts or party")
	}
}
