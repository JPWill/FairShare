package transcript

import (
	"testing"

	"github.com/JPWill/FairShare/client/internal/luatable"
)

const saved = `
FairShareDB = {
	["snapshot"] = {
		["checksum"] = "cafe0000cafe0000cafe0000cafe0000cafe0000cafe0000cafe0000cafe0000",
		["raidID"] = "7",
	},
	["transcript"] = {
		["7"] = {
			["player"] = "Alice-Realm",
			["startedAt"] = 1700000100,
			["lines"] = {
				"1700000100\tin\tAlice-Realm\tSNAP^7^cafe^1700000000^0^DEFAULT=10^", -- [1]
				"1700000101\tin\tAlice-Realm\tOPEN^7-1^loot^[Thing]^10^1700000121", -- [2]
			},
		},
		["8"] = {
			["player"] = "Alice-Realm",
			["snapshot"] = "beef0000beef0000beef0000beef0000beef0000beef0000beef0000beef0000",
			["version"] = "0.2.0",
			["lines"] = {
				"1700009000\tnote\tAlice-Realm\tstarted", -- [1]
			},
		},
		["9"] = {
			["player"] = "Alice-Realm",
			["lines"] = {
			},
		},
	},
}
`

func TestFromSavedVariablesAndText(t *testing.T) {
	g, err := luatable.Parse(saved)
	if err != nil {
		t.Fatal(err)
	}
	list, err := FromSavedVariables(g)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("got %d transcripts, want 2 (empty raid 9 skipped)", len(list))
	}
	want7 := "FAIRSHARE TRANSCRIPT 1\n" +
		"raid=7\n" +
		"snapshot=cafe0000cafe0000cafe0000cafe0000cafe0000cafe0000cafe0000cafe0000\n" +
		"player=Alice-Realm\n" +
		"version=\n" +
		"1700000100\tin\tAlice-Realm\tSNAP^7^cafe^1700000000^0^DEFAULT=10^\n" +
		"1700000101\tin\tAlice-Realm\tOPEN^7-1^loot^[Thing]^10^1700000121"
	if got := list[0].Text(); got != want7 {
		t.Errorf("raid 7 text\n got: %q\nwant: %q", got, want7)
	}
	want8 := "FAIRSHARE TRANSCRIPT 1\n" +
		"raid=8\n" +
		"snapshot=beef0000beef0000beef0000beef0000beef0000beef0000beef0000beef0000\n" +
		"player=Alice-Realm\n" +
		"version=0.2.0\n" +
		"1700009000\tnote\tAlice-Realm\tstarted"
	if got := list[1].Text(); got != want8 {
		t.Errorf("raid 8 text\n got: %q\nwant: %q", got, want8)
	}
	if list[0].Hash() == list[1].Hash() || len(list[0].Hash()) != 64 {
		t.Error("hashes should differ and be 64 hex chars")
	}
}

func TestMissingDB(t *testing.T) {
	g, _ := luatable.Parse(`Other = {}`)
	if _, err := FromSavedVariables(g); err == nil {
		t.Fatal("expected error when FairShareDB is absent")
	}
}
