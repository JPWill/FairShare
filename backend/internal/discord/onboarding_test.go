package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/content"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/store"
)

func TestScreeningBodyCarriesTheRules(t *testing.T) {
	p := content.Params{FloorDefault: 10, Floor2H: 20, PoolPct: 10, Site: content.DefaultSite}
	body := screeningBody(p)
	fields := body["form_fields"].([]map[string]any)
	if len(fields) != 1 || fields[0]["field_type"] != "TERMS" || fields[0]["required"] != true {
		t.Fatalf("form: %+v", fields)
	}
	values := fields[0]["values"].([]string)
	if len(values) != len(p.Rules())+1 {
		t.Fatalf("%d values", len(values))
	}
	for _, v := range values {
		if strings.Contains(v, "**") {
			t.Errorf("markdown left in a screening rule: %s", v)
		}
	}
	if !strings.HasPrefix(values[0], "1. One person, one account.") {
		t.Errorf("first rule: %s", values[0])
	}
}

func TestOnboardingBodyMeetsDiscordMinimums(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := &Bot{guildID: "g", Store: st, People: &people.Service{S: st}}
	for n, key := range []string{"welcome", "rules", "faq", "getstarted", "announcements", "loot", "general", "raidchat", "help", "suggestions", "offtopic"} {
		_ = st.SetConfig("channel."+key, string(rune('a'+n)))
	}
	ob, excluded := b.onboardingBody(map[string]string{"horde": "h", "alliance": "a"}, nil)
	if len(excluded) != 0 {
		t.Errorf("nil visibility check excluded %v", excluded)
	}
	if len(ob.DefaultChannelIDs) < 7 {
		t.Errorf("only %d default channels; Discord needs seven", len(ob.DefaultChannelIDs))
	}
	if ob.Enabled == nil || !*ob.Enabled || ob.Prompts == nil || len(*ob.Prompts) != 1 {
		t.Fatalf("onboarding: %+v", ob)
	}
	p := (*ob.Prompts)[0]
	if p.Required || !p.InOnboarding || len(p.Options) != 2 || p.Options[0].RoleIDs[0] != "h" || p.Options[1].RoleIDs[0] != "a" {
		t.Errorf("faction prompt: %+v", p)
	}
	// Without the cosmetic roles the options carry no role, never an empty id.
	noRoles, _ := b.onboardingBody(map[string]string{}, nil)
	for _, o := range (*noRoles.Prompts)[0].Options {
		if len(o.RoleIDs) != 0 {
			t.Errorf("option %s has roles with none configured", o.Title)
		}
	}
}

func TestOnboardingLeavesOutHiddenChannels(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	b := &Bot{guildID: "g", Store: st}
	_ = st.SetConfig("channel.welcome", "w")
	_ = st.SetConfig("channel.general", "gen")
	ob, excluded := b.onboardingBody(nil, func(id string) bool { return id != "gen" })
	if len(ob.DefaultChannelIDs) != 1 || ob.DefaultChannelIDs[0] != "w" || len(excluded) != 1 || excluded[0] != "#general" {
		t.Errorf("defaults %v excluded %v", ob.DefaultChannelIDs, excluded)
	}
}

func TestEveryoneCanView(t *testing.T) {
	const g = "g"
	view := int64(discordgo.PermissionViewChannel)
	cat := &discordgo.Channel{ID: "cat", PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: g, Deny: view}}}
	parents := map[string]*discordgo.Channel{"cat": cat}
	cases := []struct {
		name string
		ch   *discordgo.Channel
		base int64
		want bool
	}{
		{"own allow beats category deny", &discordgo.Channel{ParentID: "cat", PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: g, Allow: view}}}, 0, true},
		{"own deny", &discordgo.Channel{PermissionOverwrites: []*discordgo.PermissionOverwrite{{ID: g, Deny: view}}}, view, false},
		{"inherits category deny", &discordgo.Channel{ParentID: "cat"}, view, false},
		{"falls back to base", &discordgo.Channel{}, view, true},
		{"base denies", &discordgo.Channel{}, 0, false},
		{"unknown channel", nil, view, false},
	}
	for _, c := range cases {
		if got := everyoneCanView(c.ch, parents, g, c.base); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}
