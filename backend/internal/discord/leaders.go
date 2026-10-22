package discord

import (
	"fmt"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Raid Leader is the role between member and officer: it may create, edit, cancel and
// open raids and confirm rosters, from its own row on the personal panel. Officers
// grant and remove it from the officer panel.

// leaderActions are the component actions a raid leader may use; everything else past
// the member actions needs an officer.
var leaderActions = map[string]bool{
	"event-new": true, "ev-d": true, "ev-pg": true, "ev-rep": true, "ev-tp": true, "ev-back": true, "ev-t": true,
	"event-edit": true, "event-edit-pick": true, "event-edit-pick-keep": true,
	"ev-edit-time": true, "ev-edit-details": true, "ev-edit-cancel": true, "ev-edit-cancel-yes": true,
	"event-cancel": true, "event-cancel-pick": true,
	"open": true, "open-pick": true,
	"roster": true, "roster-pick": true, "roster-page": true, "roster-confirm": true,
}

// leaderModals are the modal submits that go with leaderActions.
var leaderModals = map[string]bool{"event-new": true, "event-edit": true, "open-adhoc": true}

// isLeader: an officer, or a home-guild member holding the Raid Leader role.
func (b *Bot) isLeader(i *discordgo.InteractionCreate) bool {
	if b.isOfficer(i) {
		return true
	}
	if i.Member == nil || i.GuildID != b.guildID {
		return false
	}
	role := b.Store.ConfigString("role.leader")
	if role == "" {
		return false
	}
	for _, rid := range i.Member.Roles {
		if rid == role {
			return true
		}
	}
	return false
}

// leaderRow is the extra row on the personal panel for raid leaders.
func leaderRow() discordgo.ActionsRow {
	return row(
		button("Create raid event", "fs:event-new", discordgo.PrimaryButton),
		button("Edit event", "fs:event-edit", discordgo.SecondaryButton),
		button("Open raid", "fs:open", discordgo.SuccessButton),
	)
}

// pickLeader asks which member to make or unmake a raid leader.
func (b *Bot) pickLeader(i *discordgo.InteractionCreate) {
	if b.Store.ConfigString("role.leader") == "" {
		b.ephemeral(i, "Run Setup server first; it creates the Raid Leader role.")
		return
	}
	b.ephemeral(i, "Pick a member. If they are a raid leader already, the role is removed; otherwise it is granted.",
		row(discordgo.SelectMenu{MenuType: discordgo.UserSelectMenu, CustomID: "fs:leader-pick", Placeholder: "Member"}))
}

func (b *Bot) toggleLeader(i *discordgo.InteractionCreate) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	target := vals[0]
	role := b.Store.ConfigString("role.leader")
	m, err := b.S.GuildMember(b.guildID, target)
	if err != nil {
		b.updateClear(i, "Could not read that member: "+err.Error())
		return
	}
	if m.User != nil && m.User.Bot {
		b.updateClear(i, "Bots cannot lead raids.")
		return
	}
	has := false
	for _, r := range m.Roles {
		if r == role {
			has = true
		}
	}
	actor := userOf(i).ID
	if has {
		if err := b.removeRole(target, role); err != nil {
			b.updateClear(i, "Could not remove the role: "+err.Error())
			return
		}
		b.logAction(actor, "leader_remove", fmt.Sprintf("<@%s>", target))
		b.adminUpdate(i, fmt.Sprintf("<@%s> is no longer a raid leader.", target))
		return
	}
	if chars, _ := b.People.Characters(target); len(chars) == 0 {
		b.updateClear(i, "They have not registered a character yet. Leaders must be registered so the bot can bind their raids to them.")
		return
	}
	if err := b.grantRole(target, role); err != nil {
		b.updateClear(i, "Could not grant the role: "+err.Error())
		return
	}
	b.logAction(actor, "leader_grant", fmt.Sprintf("<@%s>", target))
	b.post("announcements", fmt.Sprintf("<@%s> is now a raid leader. Raid leaders create and run raids; find theirs under Raids.", target))
	b.adminUpdate(i, fmt.Sprintf("<@%s> is now a raid leader. They get Create raid event, Edit event and Open raid on their `/fairshare` panel.", target))
}

// ---------------------------------------------------------------- accepting the rules

// acceptRules is the #welcome button: record it, grant Member, point at #get-started.
func (b *Bot) acceptRules(i *discordgo.InteractionCreate) {
	u := userOf(i)
	if err := b.People.AcceptTerms(u.ID, displayName(i)); err != nil {
		b.ephemeral(i, "Could not record that: "+err.Error())
		return
	}
	b.grantMember(u.ID)
	next := "Welcome in. Next: register a character in #get-started (three clicks), install the addon, and sign up for a raid under Raids."
	if ch := b.Store.ConfigString("channel.getstarted"); ch != "" {
		next = strings.Replace(next, "#get-started", "<#"+ch+">", 1)
	}
	b.ephemeral(i, next, row(button("Register a character", "fs:register", discordgo.PrimaryButton)))
	b.post("log", fmt.Sprintf("<@%s> accepted the rules", u.ID))
}

// needsAcceptance is the reply for someone who tries to register before accepting.
func (b *Bot) needsAcceptance(i *discordgo.InteractionCreate) bool {
	if b.People.Accepted(userOf(i).ID) {
		return false
	}
	where := "#welcome"
	if ch := b.Store.ConfigString("channel.welcome"); ch != "" {
		where = "<#" + ch + ">"
	}
	b.ephemeral(i, "Accept the rules first; it is one button in "+where+". Then press Register again.",
		row(button("Accept the rules", "fs:accept", discordgo.SuccessButton), button("How it works", "fs:howitworks", discordgo.SecondaryButton)))
	return true
}
