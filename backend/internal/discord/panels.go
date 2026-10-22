package discord

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

// Panels are rendered from the current settings every time, and every finished action
// answers with its result line followed by the panel again, so the labels that show
// state (sandbox, 5-player groups, timezone) are already refreshed and the next action
// is one click away instead of a slash command away.

// adminDone replies to a finished officer action with the result and a fresh panel.
func (b *Bot) adminDone(i *discordgo.InteractionCreate, text string) {
	e, comps := b.adminView()
	b.respond(i, discordgo.InteractionResponseChannelMessageWithSource,
		&discordgo.InteractionResponseData{Content: text, Embeds: []*discordgo.MessageEmbed{e}, Components: comps, Flags: discordgo.MessageFlagsEphemeral}, "admin done")
}

// adminUpdate is adminDone for flows that live on a component message (a select menu):
// the message becomes the result plus the panel.
func (b *Bot) adminUpdate(i *discordgo.InteractionCreate, text string) {
	e, comps := b.adminView()
	b.respond(i, discordgo.InteractionResponseUpdateMessage,
		&discordgo.InteractionResponseData{Content: text, Embeds: []*discordgo.MessageEmbed{e}, Components: comps}, "admin update")
}

// adminFollowup is adminDone after defer_.
func (b *Bot) adminFollowup(i *discordgo.InteractionCreate, text string) {
	e, comps := b.adminView()
	if _, err := b.S.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: text, Embeds: []*discordgo.MessageEmbed{e}, Components: comps, Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: noMentions,
	}); err != nil {
		log.Printf("admin followup: %v", err)
	}
}

// personalDone replies to a finished member action with the result and their panel.
func (b *Bot) personalDone(i *discordgo.InteractionCreate, text string) {
	if i.GuildID == "" {
		b.ephemeral(i, text) // a DM: most panel buttons only work in the server
		return
	}
	e, comps := b.personalView(i)
	b.respond(i, discordgo.InteractionResponseChannelMessageWithSource,
		&discordgo.InteractionResponseData{Content: text, Embeds: []*discordgo.MessageEmbed{e}, Components: comps, Flags: discordgo.MessageFlagsEphemeral}, "personal done")
}

// personalUpdate is personalDone on a component message.
func (b *Bot) personalUpdate(i *discordgo.InteractionCreate, text string) {
	if i.GuildID == "" {
		b.updateClear(i, text)
		return
	}
	e, comps := b.personalView(i)
	b.respond(i, discordgo.InteractionResponseUpdateMessage,
		&discordgo.InteractionResponseData{Content: text, Embeds: []*discordgo.MessageEmbed{e}, Components: comps}, "personal update")
}

// personalPanel is what /fairshare opens.
func (b *Bot) personalPanel(i *discordgo.InteractionCreate) {
	e, comps := b.personalView(i)
	b.ephemeralEmbed(i, e, comps...)
}
