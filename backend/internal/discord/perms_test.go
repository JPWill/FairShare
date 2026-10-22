package discord

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestMaskOverwrites(t *testing.T) {
	ov := []*discordgo.PermissionOverwrite{
		{ID: "m", Allow: discordgo.PermissionViewChannel | discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceMuteMembers},
		{ID: "e", Deny: discordgo.PermissionViewChannel},
		{ID: "l", Allow: discordgo.PermissionVoiceMoveMembers}, // nothing left once masked
	}
	mask := int64(discordgo.PermissionViewChannel | discordgo.PermissionVoiceConnect)
	out, dropped := maskOverwrites(ov, mask)
	if len(out) != 2 {
		t.Fatalf("kept %d overwrites, want 2", len(out))
	}
	if out[0].Allow != mask {
		t.Errorf("member allow = %d", out[0].Allow)
	}
	if dropped != discordgo.PermissionVoiceMuteMembers|discordgo.PermissionVoiceMoveMembers {
		t.Errorf("dropped = %d", dropped)
	}
	if s := permList(dropped); !strings.Contains(s, "Mute Members") || !strings.Contains(s, "Move Members") {
		t.Errorf("permList: %q", s)
	}
	// Unknown (0) and administrator (-1) masks change nothing.
	for _, m := range []int64{0, -1} {
		if o, d := maskOverwrites(ov, m); len(o) != 3 || d != 0 {
			t.Errorf("mask %d: %d overwrites, dropped %d", m, len(o), d)
		}
	}
	// The input is not modified.
	if ov[0].Allow&discordgo.PermissionVoiceMuteMembers == 0 {
		t.Error("maskOverwrites changed its input")
	}
}

func TestOverwritesApplyMask(t *testing.T) {
	b := &Bot{guildID: "g", permMask: discordgo.PermissionViewChannel | discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak}
	roles := map[string]string{"member": "m", "leader": "l", "officer": "o"}
	for _, o := range b.overwrites(accessVoice, roles) {
		if (o.Allow|o.Deny)&^b.permMask != 0 {
			t.Errorf("overwrite %s carries bits the bot lacks: %d", o.ID, o.Allow|o.Deny)
		}
	}
}
