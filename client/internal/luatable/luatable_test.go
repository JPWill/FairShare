package luatable

import "testing"

// A realistic SavedVariables file as the game writes it: tabs, bracketed keys,
// positional entries with trailing index comments, escapes, nested tables.
const fixture = `
FairShareDB = {
	["settings"] = {
		["theme"] = "auto",
		["minimap"] = {
			["hide"] = false,
			["angle"] = 220.5,
		},
		["sound"] = true,
	},
	["snapshot"] = {
		["text"] = "FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1700000000\nrow:Alice-Realm\tAlice\t0\t100",
		["checksum"] = "abc123",
		["issued"] = 1700000000,
		["raidID"] = "1",
	},
	["transcript"] = {
		["1"] = {
			["player"] = "Alice-Realm",
			["startedAt"] = 1700000100,
			["lines"] = {
				"1700000100\tin\tAlice-Realm\tSNAP^1^abc123^1700000000^0^DEFAULT=10^", -- [1]
				"1700000101\tin\tAlice-Realm\tOPEN^1-1^loot^|cff0070dd|Hitem:1234::::::::60:::::|h[Thing]|h|r^10^1700000121", -- [2]
				"1700000102\tnote\tAlice-Realm\tquote \" backslash \\ bell \7 end", -- [3]
			},
		},
	},
	["auctions"] = {
	},
	["negative"] = -42,
	["hex"] = 0x1F,
	bare = "bare key",
	[5] = "five",
}
FairShareOther = {
	true, false, nil, "x",
}
--[[ block
comment ]]
`

func TestParseFixture(t *testing.T) {
	g, err := Parse(fixture)
	if err != nil {
		t.Fatal(err)
	}
	db, ok := g["FairShareDB"].(*Table)
	if !ok {
		t.Fatal("FairShareDB missing")
	}
	if got := db.GetTable("settings").GetString("theme"); got != "auto" {
		t.Errorf("theme = %q", got)
	}
	if v := db.GetTable("settings").GetTable("minimap").Get("hide"); v != false {
		t.Errorf("hide = %v", v)
	}
	if n, ok := db.GetTable("settings").GetTable("minimap").GetNumber("angle"); !ok || n != 220.5 {
		t.Errorf("angle = %v %v", n, ok)
	}
	snap := db.GetTable("snapshot")
	if got := snap.GetString("text"); got != "FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1700000000\nrow:Alice-Realm\tAlice\t0\t100" {
		t.Errorf("snapshot text = %q", got)
	}
	if n, _ := snap.GetNumber("issued"); n != 1700000000 {
		t.Errorf("issued = %v", n)
	}
	tr := db.GetTable("transcript").GetTable("1")
	if tr == nil {
		t.Fatal("transcript[1] missing")
	}
	lines := tr.GetTable("lines").Strings()
	if len(lines) != 3 {
		t.Fatalf("lines = %d", len(lines))
	}
	if lines[1] != "1700000101\tin\tAlice-Realm\tOPEN^1-1^loot^|cff0070dd|Hitem:1234::::::::60:::::|h[Thing]|h|r^10^1700000121" {
		t.Errorf("line 2 = %q", lines[1])
	}
	if lines[2] != "1700000102\tnote\tAlice-Realm\tquote \" backslash \\ bell \a end" {
		t.Errorf("line 3 = %q", lines[2])
	}
	if db.GetTable("auctions").Len() != 0 {
		t.Error("auctions should be empty")
	}
	if n, _ := db.GetNumber("negative"); n != -42 {
		t.Errorf("negative = %v", n)
	}
	if n, _ := db.GetNumber("hex"); n != 31 {
		t.Errorf("hex = %v", n)
	}
	if got := db.GetString("bare"); got != "bare key" {
		t.Errorf("bare = %q", got)
	}
	if got := db.Index(5); got != "five" {
		t.Errorf("[5] = %v", got)
	}
	other := g["FairShareOther"].(*Table)
	if other.Index(1) != true || other.Index(2) != false || other.Index(3) != nil || other.Index(4) != "x" {
		t.Errorf("positional = %v %v %v %v", other.Index(1), other.Index(2), other.Index(3), other.Index(4))
	}
	if keys := db.GetTable("transcript").StringKeys(); len(keys) != 1 || keys[0] != "1" {
		t.Errorf("transcript keys = %v", keys)
	}
}

func TestParseErrors(t *testing.T) {
	bad := []string{
		`X = {`,
		`X = "unterminated`,
		`X = { ["a" = 1 }`,
		`= 1`,
	}
	for _, src := range bad {
		if _, err := Parse(src); err == nil {
			t.Errorf("expected error for %q", src)
		}
	}
	if g, err := Parse("   \n-- just a comment\n"); err != nil || len(g) != 0 {
		t.Errorf("empty file: %v %v", g, err)
	}
}
