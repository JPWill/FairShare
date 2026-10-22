package discord

import (
	"sort"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Discord refuses a channel create or an overwrite edit (50013, Missing Permissions)
// when an overwrite grants or denies a permission the bot's own role does not hold.
// Setup server therefore learns the bot's permissions first and masks every overwrite
// to them, reporting what it had to leave out, so a thin invite degrades to a working
// server plus a to-do line instead of a half-built one.

// botPermissions is the union of the bot's role permissions in the guild. It returns -1
// (every bit) for Administrator or when the member cannot be read, which means "do not
// mask": a wrong guess would silently strip permissions the bot does have.
func (b *Bot) botPermissions(roles []*discordgo.Role) int64 {
	if b.S == nil || b.S.State == nil || b.S.State.User == nil {
		return -1
	}
	botID := b.S.State.User.ID
	m, err := b.S.State.Member(b.guildID, botID)
	if err != nil || m == nil {
		m, err = b.S.GuildMember(b.guildID, botID)
		if err != nil || m == nil {
			return -1
		}
	}
	byID := map[string]*discordgo.Role{}
	for _, r := range roles {
		byID[r.ID] = r
	}
	var p int64
	for _, rid := range append([]string{b.guildID}, m.Roles...) {
		if r := byID[rid]; r != nil {
			p |= r.Permissions
		}
	}
	if p&discordgo.PermissionAdministrator != 0 {
		return -1
	}
	return p
}

// maskOverwrites keeps only the bits in mask and returns the bits it removed. A mask
// of 0 or -1 means unknown or everything: nothing is removed.
func maskOverwrites(ov []*discordgo.PermissionOverwrite, mask int64) ([]*discordgo.PermissionOverwrite, int64) {
	if mask == 0 || mask == -1 {
		return ov, 0
	}
	var dropped int64
	out := make([]*discordgo.PermissionOverwrite, 0, len(ov))
	for _, o := range ov {
		dropped |= (o.Allow | o.Deny) &^ mask
		c := *o
		c.Allow &= mask
		c.Deny &= mask
		if c.Allow == 0 && c.Deny == 0 {
			continue
		}
		out = append(out, &c)
	}
	return out, dropped
}

var permNames = map[int64]string{
	discordgo.PermissionViewChannel:           "View Channels",
	discordgo.PermissionManageChannels:        "Manage Channels",
	discordgo.PermissionManageRoles:           "Manage Roles",
	discordgo.PermissionCreateInstantInvite:   "Create Invite",
	discordgo.PermissionSendMessages:          "Send Messages",
	discordgo.PermissionSendMessagesInThreads: "Send Messages in Threads",
	discordgo.PermissionManageMessages:        "Manage Messages",
	discordgo.PermissionEmbedLinks:            "Embed Links",
	discordgo.PermissionAttachFiles:           "Attach Files",
	discordgo.PermissionReadMessageHistory:    "Read Message History",
	discordgo.PermissionMentionEveryone:       "Mention Everyone",
	discordgo.PermissionAddReactions:          "Add Reactions",
	discordgo.PermissionVoiceConnect:          "Connect",
	discordgo.PermissionVoiceSpeak:            "Speak",
	discordgo.PermissionVoiceStreamVideo:      "Video",
	discordgo.PermissionVoiceUseVAD:           "Use Voice Activity",
	discordgo.PermissionVoiceMuteMembers:      "Mute Members",
	discordgo.PermissionVoiceDeafenMembers:    "Deafen Members",
	discordgo.PermissionVoiceMoveMembers:      "Move Members",
}

// permList names the bits in p as Discord's settings page shows them.
func permList(p int64) string {
	var names []string
	for bit, name := range permNames {
		if p&bit != 0 {
			names = append(names, name)
			p &^= bit
		}
	}
	sort.Strings(names)
	if p != 0 {
		names = append(names, "other")
	}
	return strings.Join(names, ", ")
}
