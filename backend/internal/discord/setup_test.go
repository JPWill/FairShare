package discord

import (
	"testing"
	"time"

	"github.com/JPWill/FairShare/backend/internal/raids"
)

func TestRaidChannelName(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	at := func(s string) int64 {
		tm, err := time.ParseInLocation("2006-01-02 15:04", s, ny)
		if err != nil {
			t.Fatal(err)
		}
		return tm.Unix()
	}
	cases := []struct {
		start, instance, want string
	}{
		{"2026-12-15 19:00", "Molten Core", "tuesday-7pm-mc"},
		{"2026-12-17 21:00", "Blackwing Lair", "thursday-9pm-bwl"},
		{"2026-12-20 19:30", "Onyxia's Lair", "sunday-730pm-ony"},
		{"2026-12-21 00:00", "Naxxramas", "monday-12am-naxx"},
		{"2026-12-21 12:00", "Temple of Ahn'Qiraj", "monday-12pm-aq40"},
		{"2026-12-22 18:00", "Some New Raid", "tuesday-6pm-snr"},
		{"2026-12-22 18:00", "Ulduar", "tuesday-6pm-uldu"},
		{"2026-12-22 18:00", "Halls of Lightning", "tuesday-6pm-hl"},
	}
	for _, c := range cases {
		got := raidChannelName(raids.Event{StartsAt: at(c.start), Instance: c.instance}, ny)
		if got != c.want {
			t.Errorf("%s %s: got %q want %q", c.start, c.instance, got, c.want)
		}
	}
	if s := slug("Hello, World!! -- 2026"); s != "hello-world-2026" {
		t.Errorf("slug: %q", s)
	}
}

func TestLeaderActionsAreNotOfficerOnly(t *testing.T) {
	// A leader may run raids but never touch bans, settlement, config or the server.
	for _, a := range []string{"ban", "ban-pick", "unban", "settle", "settle-pick", "setup", "economy", "rules", "floors", "settlement", "sandbox", "sandbox-reset", "website", "leaders", "leader-pick", "announce", "devposting", "allowparty"} {
		if leaderActions[a] || leaderModals[a] {
			t.Errorf("%s must stay officer-only", a)
		}
	}
	for _, a := range []string{"event-new", "ev-t", "open", "open-pick", "roster-confirm", "ev-edit-cancel-yes"} {
		if !leaderActions[a] {
			t.Errorf("%s should be a leader action", a)
		}
	}
}

func TestOverwritesSkipMissingRolesAndCoverEveryone(t *testing.T) {
	b := &Bot{guildID: "g"}
	roles := map[string]string{"member": "m", "officer": "", "leader": "l"}
	for _, a := range []access{accessWelcome, accessReadOnly, accessChat, accessOfficers, accessVoice} {
		ov := b.overwrites(a, roles)
		seenEveryone := false
		for _, o := range ov {
			if o.ID == "" {
				t.Errorf("access %d: overwrite with empty id", a)
			}
			if o.ID == "g" {
				seenEveryone = true
			}
		}
		if !seenEveryone {
			t.Errorf("access %d: no @everyone overwrite", a)
		}
	}
	if len(channelSpecs) != 14 || len(roleSpecs) != 7 || len(categorySpecs) != 6 || len(voiceSpecs) != 11 {
		t.Errorf("layout changed: %d channels, %d roles, %d categories", len(channelSpecs), len(roleSpecs), len(categorySpecs))
	}
	for _, c := range channelSpecs {
		found := false
		for _, cat := range categorySpecs {
			if cat.key == c.category {
				found = true
			}
		}
		if !found {
			t.Errorf("#%s has unknown category %q", c.name, c.category)
		}
	}
}
