package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestSafeTextStripsFencesAndMassMentions(t *testing.T) {
	in := "```@everyone``` free loot @here `code`"
	got := safeText(in)
	for _, bad := range []string{"`", "@everyone", "@here"} {
		if contains(got, bad) {
			t.Fatalf("%q still contains %q", got, bad)
		}
	}
	if got != "'''everyone''' free loot here 'code'" {
		t.Fatalf("unexpected: %q", got)
	}
	// Plain item links and names pass through untouched.
	if s := "|cff0070dd|Hitem:1234::::::::60:::::|h[Sword]|h|r"; safeText(s) != s {
		t.Fatalf("item link altered: %q", safeText(s))
	}
}

func TestCommandsAreGuildOnlyAndAdminIsGated(t *testing.T) {
	for _, c := range commands() {
		if c.DMPermission == nil || *c.DMPermission {
			t.Fatalf("%s must not be usable in DMs", c.Name)
		}
		if c.Name == "fairshare-admin" {
			if c.DefaultMemberPermissions == nil || *c.DefaultMemberPermissions&discordgo.PermissionManageGuild == 0 {
				t.Fatalf("fairshare-admin must default to Manage Server")
			}
		} else if c.DefaultMemberPermissions != nil {
			t.Fatalf("%s should be open to every member", c.Name)
		}
	}
}

func TestForeignGuildAndDMRouting(t *testing.T) {
	b := &Bot{guildID: "home"}
	mk := func(guild string) *discordgo.InteractionCreate {
		return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: guild}}
	}
	if !b.foreignGuild(mk("elsewhere")) {
		t.Fatal("another server must be foreign")
	}
	if b.foreignGuild(mk("home")) || b.foreignGuild(mk("")) {
		t.Fatal("home server and DMs are not foreign")
	}
	for _, a := range []string{"register", "reg-class", "reg-roles", "howitworks"} {
		if !dmActions[a] {
			t.Fatalf("%s must work from the welcome DM", a)
		}
	}
	for _, a := range []string{"token", "addonkey", "signup", "ban", "setup", "roster", "finish", "upload"} {
		if dmActions[a] {
			t.Fatalf("%s must not be reachable from a DM", a)
		}
	}
}

func TestIsOfficerNeedsHomeGuildMember(t *testing.T) {
	b := &Bot{guildID: "home"}
	// No member block (DM) is never an officer, whatever else is set.
	if b.isOfficer(&discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: ""}}) {
		t.Fatal("DM counted as officer")
	}
	// A member of another guild with Administrator is still not our officer.
	i := &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{GuildID: "elsewhere",
		Member: &discordgo.Member{Permissions: discordgo.PermissionAdministrator, User: &discordgo.User{ID: "u"}}}}
	if b.isOfficer(i) {
		t.Fatal("foreign administrator counted as officer")
	}
}

func TestClampPage(t *testing.T) {
	cases := []struct{ page, pages, want int }{{-5, 3, 0}, {0, 3, 0}, {2, 3, 2}, {9, 3, 2}, {4, 0, 4}, {-1, 0, 0}}
	for _, c := range cases {
		if got := clampPage(c.page, c.pages); got != c.want {
			t.Fatalf("clampPage(%d,%d)=%d want %d", c.page, c.pages, got, c.want)
		}
	}
}

func TestAdoptPrefersStoredIDThenWarnsOnDuplicates(t *testing.T) {
	existing := map[string]string{"r1": "Officer", "r2": "officer", "r3": "Member"}
	if id, warn := adopt("role", "officer", "Officer", "r2", existing, []string{"r1", "r2"}); id != "r2" || warn != "" {
		t.Fatalf("stored id must win: %q %q", id, warn)
	}
	if id, warn := adopt("role", "officer", "Officer", "gone", existing, []string{"r1", "r2"}); id != "r1" || warn == "" {
		t.Fatalf("first name match with a warning: %q %q", id, warn)
	}
	if id, warn := adopt("role", "member", "Member", "", existing, []string{"r3"}); id != "r3" || warn != "" {
		t.Fatalf("single match: %q %q", id, warn)
	}
	if id, _ := adopt("role", "raider", "Raider", "", existing, nil); id != "" {
		t.Fatalf("nothing to adopt: %q", id)
	}
}

func TestNoMentionsIsExplicitEmptyParse(t *testing.T) {
	if noMentions.Parse == nil || len(noMentions.Parse) != 0 {
		t.Fatal("noMentions must send an empty parse list, not omit it")
	}
	m := mentionUsers("a", "b")
	if len(m.Users) != 2 || len(m.Parse) != 0 {
		t.Fatalf("mentionUsers: %+v", m)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && indexOf(s, sub) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
