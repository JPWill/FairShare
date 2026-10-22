package discord

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/content"
)

// Discord's own onboarding, for Community servers: a rules screen every newcomer must
// accept before they can post anywhere (membership screening), and the "customize your
// experience" questions. Setup server configures both from the same texts as the pinned
// cards, so the rules a member accepts in Discord's dialog are the rules in #rules.
//
// Screening cannot collect typed answers, so character names and Battle.net tags stay in
// the bot's Register flow. What it gives us is enforcement: a member is `pending` until
// they accept, Discord blocks them from posting, reacting or joining voice until then,
// and the bot sees the flag clear and records the acceptance exactly as the Accept
// button does.

// screeningBody is the membership-screening form. The endpoint is the one Discord's own
// client uses; it is not in the public API reference, so a failure is reported, never fatal.
func screeningBody(p content.Params) map[string]any {
	values := make([]string, 0, len(p.Rules())+1)
	for n, r := range p.Rules() {
		values = append(values, fmt.Sprintf("%d. %s", n+1, stripBold(r)))
	}
	values = append(values, stripBold(content.RulesOutro))
	return map[string]any{
		"enabled":     true,
		"description": "FairShare raids: loot is auctioned for shares that follow you across every character, every price is public, and nobody can see your bid. Accept the rules to join in.",
		"form_fields": []map[string]any{{
			"field_type": "TERMS",
			"label":      "Read and agree to the community rules",
			"values":     values,
			"required":   true,
		}},
	}
}

func stripBold(s string) string { return strings.ReplaceAll(s, "**", "") }

// onboardingBody is the onboarding configuration: the public channels as defaults, and
// one optional question for the cosmetic faction role. Discord requires at least seven
// default channels that @everyone can view, five of them writable, which the Community
// channels provide.
// visible says whether @everyone can view a channel; a default channel that is not is
// refused by Discord (DEFAULT_CHANNEL_REQUIRES_EVERYONE_ACCESS), so it is left out and named.
func (b *Bot) onboardingBody(roles map[string]string, visible func(channelID string) bool) (*discordgo.GuildOnboarding, []string) {
	var defaults, excluded []string
	for _, key := range []string{"welcome", "rules", "faq", "getstarted", "announcements", "loot", "general", "raidchat", "help", "suggestions", "offtopic"} {
		id := b.Store.ConfigString("channel." + key)
		if id == "" {
			continue
		}
		if visible != nil && !visible(id) {
			excluded = append(excluded, "#"+key)
			continue
		}
		defaults = append(defaults, id)
	}
	option := func(id, title, desc, emoji, role string) discordgo.GuildOnboardingPromptOption {
		o := discordgo.GuildOnboardingPromptOption{ID: id, Title: title, Description: desc, EmojiName: emoji, ChannelIDs: []string{}, RoleIDs: []string{}}
		if role != "" {
			o.RoleIDs = []string{role}
		}
		return o
	}
	enabled := true
	mode := discordgo.GuildOnboardingModeDefault
	prompts := []discordgo.GuildOnboardingPrompt{{
		ID:           "0",
		Type:         discordgo.GuildOnboardingPromptTypeMultipleChoice,
		Title:        "Which faction do you play? (You can pick both)",
		SingleSelect: false,
		Required:     false,
		InOnboarding: true,
		Options: []discordgo.GuildOnboardingPromptOption{
			option("0", "Horde", "Gives the Horde role. Purely cosmetic.", "🔴", roles["horde"]),
			option("1", "Alliance", "Gives the Alliance role. Purely cosmetic.", "🔵", roles["alliance"]),
		},
	}}
	return &discordgo.GuildOnboarding{Prompts: &prompts, DefaultChannelIDs: defaults, Enabled: &enabled, Mode: &mode}, excluded
}

// everyoneCanView computes whether @everyone may view a channel: its own overwrite, else
// its category's, else the @everyone role's base permission.
func everyoneCanView(ch *discordgo.Channel, parents map[string]*discordgo.Channel, guildID string, basePerms int64) bool {
	if ch == nil {
		return false // not in the guild's channel list: never a default
	}
	decide := func(c *discordgo.Channel) (bool, bool) {
		if c == nil {
			return false, false
		}
		for _, o := range c.PermissionOverwrites {
			if o.ID == guildID {
				if o.Deny&discordgo.PermissionViewChannel != 0 {
					return false, true
				}
				if o.Allow&discordgo.PermissionViewChannel != 0 {
					return true, true
				}
			}
		}
		return false, false
	}
	if v, ok := decide(ch); ok {
		return v
	}
	if ch != nil && ch.ParentID != "" {
		if v, ok := decide(parents[ch.ParentID]); ok {
			return v
		}
	}
	return basePerms&discordgo.PermissionViewChannel != 0
}

// setupScreening pushes the rules into Discord's membership screening. Returns a report line.
func (b *Bot) setupScreening() string {
	if b.sandboxed("set the rules screening form") {
		return ""
	}
	body := screeningBody(b.params())
	if _, err := b.S.RequestWithBucketID("PATCH", discordgo.EndpointGuild(b.guildID)+"/member-verification", body, discordgo.EndpointGuild(b.guildID)+"/member-verification"); err != nil {
		return "could not set the rules screening (needs Community enabled in Server Settings → Community): " + err.Error()
	}
	return "rules screening set from the community rules"
}

// setupOnboarding pushes the onboarding questions and default channels. Returns a report line.
func (b *Bot) setupOnboarding(roles map[string]string, chans []*discordgo.Channel, guildRoles []*discordgo.Role) string {
	if b.sandboxed("configure onboarding") {
		return ""
	}
	// Re-read the channels: the loop above may just have changed their overwrites.
	if fresh, err := b.S.GuildChannels(b.guildID); err == nil {
		chans = fresh
	}
	byID := map[string]*discordgo.Channel{}
	for _, c := range chans {
		byID[c.ID] = c
	}
	var base int64
	for _, r := range guildRoles {
		if r.ID == b.guildID {
			base = r.Permissions
		}
	}
	body, excluded := b.onboardingBody(roles, func(id string) bool { return everyoneCanView(byID[id], byID, b.guildID, base) })
	note := ""
	if len(excluded) > 0 {
		note = " (" + strings.Join(excluded, ", ") + " left out of the defaults: @everyone cannot view them; check the channel and its category permissions, then press Setup again)"
	}
	if len(body.DefaultChannelIDs) < 7 {
		return "onboarding not configured: Discord needs at least seven default channels @everyone can view and only " + fmt.Sprint(len(body.DefaultChannelIDs)) + " qualify" + note
	}
	if _, err := b.S.GuildOnboardingEdit(b.guildID, body); err != nil {
		return "could not configure onboarding (needs Community enabled in Server Settings → Community): " + err.Error() + note
	}
	return "onboarding configured: default channels and the faction question" + note
}

// pendingJoins remembers members who joined while screening was on, so the update that
// clears their pending flag is recognised even when the member cache has no before-state.
var pendingJoins sync.Map

// onGuildMemberUpdate records acceptance of Discord's rules screen and grants Member,
// the same as the Accept button in #welcome.
func (b *Bot) onGuildMemberUpdate(s *discordgo.Session, m *discordgo.GuildMemberUpdate) {
	if m == nil || m.Member == nil || m.GuildID != b.guildID || m.User == nil || m.User.Bot || m.Pending {
		return
	}
	wasPending := m.BeforeUpdate != nil && m.BeforeUpdate.Pending
	if _, ok := pendingJoins.LoadAndDelete(m.User.ID); ok {
		wasPending = true
	}
	if !wasPending || b.People.Accepted(m.User.ID) {
		return
	}
	name := m.User.GlobalName
	if m.Nick != "" {
		name = m.Nick
	}
	if name == "" {
		name = m.User.Username
	}
	if err := b.People.AcceptTerms(m.User.ID, name); err != nil {
		log.Printf("record screening acceptance for %s: %v", m.User.ID, err)
		return
	}
	b.grantMember(m.User.ID)
	b.post("log", fmt.Sprintf("<@%s> accepted the rules (Discord screening)", m.User.ID))
	if chars, _ := b.People.Characters(m.User.ID); len(chars) == 0 {
		b.nudgeRegister(m.User.ID)
	}
}

// ---------------------------------------------------------------- first character

// nudges remembers the #get-started message posted for a member whose DMs are closed,
// so it can be removed once they register.
var nudges sync.Map

// nudgeRegister makes registering the first character the next thing a newcomer sees
// after accepting the rules: a DM with the Register button, or, when DMs are closed, a
// mention in #get-started with the same button. Onboarding itself cannot ask for typed
// answers, so this is the hand-off from Discord's flow to the bot's.
func (b *Bot) nudgeRegister(userID string) {
	e := &discordgo.MessageEmbed{
		Title: "Set up your first character", Color: colorGreen,
		Description: "You are in. One more step before you can sign up for raids: register the character you will raid on. Name, class and roles, three clicks.\n\n" +
			"Then install the FairShare addon (search *FairShare* in the CurseForge app) and pick a raid under Signups. Linking your Battle.net tag is optional and raises your standing with leaders.",
	}
	comps := []discordgo.MessageComponent{row(
		button("Register a character", "fs:register", discordgo.PrimaryButton),
		button("How it works", "fs:howitworks", discordgo.SecondaryButton),
	)}
	if ch, err := b.S.UserChannelCreate(userID); err == nil {
		if _, err := b.send(ch.ID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{e}, Components: comps}); err == nil {
			return
		}
	}
	gs := b.channel("getstarted")
	if gs == "" {
		return
	}
	msg, err := b.send(gs, &discordgo.MessageSend{
		Content:         fmt.Sprintf("<@%s>, welcome in. Set up your first character here:", userID),
		Embeds:          []*discordgo.MessageEmbed{e},
		Components:      comps,
		AllowedMentions: mentionUsers(userID),
	})
	if err != nil {
		log.Printf("register nudge for %s: %v", userID, err)
		return
	}
	nudges.Store(userID, gs+"/"+msg.ID)
}

// clearNudge removes the #get-started nudge once the member has registered.
func (b *Bot) clearNudge(userID string) {
	v, ok := nudges.LoadAndDelete(userID)
	if !ok {
		return
	}
	ch, id, _ := strings.Cut(v.(string), "/")
	if err := b.S.ChannelMessageDelete(ch, id); err != nil {
		log.Printf("clear register nudge for %s: %v", userID, err)
	}
}
