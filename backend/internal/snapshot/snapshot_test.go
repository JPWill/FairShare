package snapshot

import (
	"encoding/base64"
	"strings"
	"testing"
)

func golden() Snapshot {
	return Snapshot{
		RaidID:     "1",
		Issued:     1700000000,
		OffspecPct: 0,
		Floors:     map[string]int64{"INVTYPE_HEAD": 20, "DEFAULT": 10},
		Rows: []Row{
			{Character: "Bob", Owner: "Bob", Balance: -50, Credit: 200},
			{Character: "Alice", Owner: "Alice", Balance: 0, Credit: 100},
		},
	}
}

const goldenText = "FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1700000000\noffspec=0\nfloor:DEFAULT=10\nfloor:INVTYPE_HEAD=20\nrow:Alice\tAlice\t0\t100\nrow:Bob\tBob\t-50\t200"

// TestGolden pins the canonical text and logs its checksum so the Lua side can be
// cross-checked against the same input.
func TestGolden(t *testing.T) {
	text := golden().Text()
	if text != goldenText {
		t.Fatalf("canonical text mismatch:\n%q\nwant\n%q", text, goldenText)
	}
	cs := Checksum(text)
	t.Logf("golden snapshot checksum: %s (short %s)", cs, Short(cs))
	if len(cs) != 64 {
		t.Fatal("checksum length")
	}
	if strings.HasSuffix(text, "\n") {
		t.Fatal("trailing newline")
	}
}

func TestSortingAndCleaning(t *testing.T) {
	s := golden()
	s.Banned = []string{"Zed", "Amy"}
	s.Roster = []RosterEntry{{Name: "Bob", Role: "healer", Status: "standby"}, {Name: "Alice", Role: "tank", Status: "confirmed"}}
	s.Rows = append(s.Rows, Row{Character: "Cara", Owner: "Ca\tra\nX", Balance: 1, Credit: 2})
	text := s.Text()
	want := goldenText[:len("FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1700000000\noffspec=0\nfloor:DEFAULT=10\nfloor:INVTYPE_HEAD=20")] +
		"\nban:Amy\nban:Zed" +
		"\nroster:Alice\ttank\tconfirmed\nroster:Bob\thealer\tstandby" +
		"\nrow:Alice\tAlice\t0\t100\nrow:Bob\tBob\t-50\t200\nrow:Cara\tCa ra X\t1\t2"
	if text != want {
		t.Fatalf("got\n%q\nwant\n%q", text, want)
	}
}

func TestParseRoundTrip(t *testing.T) {
	s := golden()
	s.Banned = []string{"Zed"}
	s.Roster = []RosterEntry{{Name: "Alice", Role: "tank", Status: "confirmed"}}
	text := s.Text()
	p, err := Parse(text)
	if err == nil && (len(p.Roster) != 1 || p.Roster[0].Role != "tank") {
		t.Fatalf("roster not parsed: %+v", p.Roster)
	}
	if err != nil {
		t.Fatal(err)
	}
	if p.Text() != text {
		t.Fatalf("roundtrip differs:\n%q\n%q", p.Text(), text)
	}
	if p.Floor("INVTYPE_HEAD") != 20 || p.Floor("INVTYPE_FEET") != 10 {
		t.Fatal("floor lookup")
	}
	if _, err := Parse("nope"); err == nil {
		t.Fatal("accepted garbage")
	}
	if _, err := Parse("FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1\noffspec=0"); err == nil {
		t.Fatal("accepted snapshot without DEFAULT floor")
	}
}

func TestPasteAndDataLua(t *testing.T) {
	text := golden().Text()
	dec, err := base64.StdEncoding.DecodeString(Paste(text))
	if err != nil || string(dec) != text {
		t.Fatal("paste roundtrip")
	}
	lua := DataLua("a\"b\\c\nd\te", "abc", 5)
	want := "FairShareData = {\n    version = 1,\n    snapshot = \"a\\\"b\\\\c\\nd\\te\",\n    checksum = \"abc\",\n    writtenAt = 5,\n}\n"
	if lua != want {
		t.Fatalf("DataLua:\n%s\nwant\n%s", lua, want)
	}
}

// The scheduled start and the 5-player switch are optional lines right after offspec;
// a snapshot without them renders exactly as before, so old checksums stay valid.
func TestStartsAndParty(t *testing.T) {
	s := golden()
	plain := s.Text()
	s.Starts, s.Party = 1792550000, true
	text := s.Text()
	if !strings.Contains(text, "offspec=0\nstarts=1792550000\nparty=1\nfloor:") {
		t.Fatalf("lines not where expected:\n%s", text)
	}
	p, err := Parse(text)
	if err != nil || p.Starts != 1792550000 || !p.Party || p.Text() != text {
		t.Fatalf("round trip: %+v, %v", p, err)
	}
	s.Starts, s.Party = 0, false
	if s.Text() != plain {
		t.Fatal("unset fields changed the text")
	}
	if _, err := Parse(strings.Replace(text, "starts=1792550000", "starts=soon", 1)); err == nil {
		t.Fatal("accepted a bad starts line")
	}
}
