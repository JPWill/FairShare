package discord

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/people"
)

// Short-lived per-user state for multi-step flows (register, event draft, roster pick).
// Keyed "kind:userID"; lost on restart, which only means the user starts the flow again.
var pending sync.Map

func setPending(kind, userID string, v any) { pending.Store(kind+":"+userID, v) }
func delPending(kind, userID string)        { pending.Delete(kind + ":" + userID) }
func getPending[T any](kind, userID string) (T, bool) {
	v, ok := pending.Load(kind + ":" + userID)
	if !ok {
		var zero T
		return zero, false
	}
	t, ok := v.(T)
	return t, ok
}

// updateClear edits the component message and removes its components.
func (b *Bot) updateClear(i *discordgo.InteractionCreate, content string) {
	b.respond(i, discordgo.InteractionResponseUpdateMessage,
		&discordgo.InteractionResponseData{Content: content, Components: []discordgo.MessageComponent{}}, "update")
}

// ---------------------------------------------------------------- registration flow
//
// Register button -> modal (character name, the only typed field) -> class select ->
// role multi-select -> stored. Works identically in a guild channel and in a DM.

type regState struct{ Name, Class string }

func (b *Bot) startRegister(i *discordgo.InteractionCreate) {
	if b.needsAcceptance(i) {
		return
	}
	b.modal(i, "fsm:register", "Register a character",
		text("character", "Character name, exactly as shown in game", "Thrall Doomhammer", true, 40))
}

func (b *Bot) submitRegisterName(i *discordgo.InteractionCreate) {
	u := userOf(i)
	name, err := people.NormalizeCharacter(modalValues(i)["character"])
	if err != nil {
		b.ephemeral(i, err.Error())
		return
	}
	setPending("reg", u.ID, &regState{Name: name})
	b.ephemeral(i, fmt.Sprintf("Registering **%s**. What class is it?", name), row(classSelect("fs:reg-class")))
}

func classSelect(customID string) discordgo.SelectMenu {
	opts := make([]discordgo.SelectMenuOption, 0, len(people.Classes))
	for _, c := range people.Classes {
		opts = append(opts, discordgo.SelectMenuOption{Label: c, Value: c})
	}
	return discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: customID, Placeholder: "Class", Options: opts}
}

func roleOptions(class string, selected map[string]bool) []discordgo.SelectMenuOption {
	var opts []discordgo.SelectMenuOption
	for _, r := range people.RolesFor(class) {
		opts = append(opts, discordgo.SelectMenuOption{Label: people.RoleLabel[r], Value: r, Default: selected[r]})
	}
	return opts
}

func (b *Bot) pickRegClass(i *discordgo.InteractionCreate) {
	u := userOf(i)
	st, ok := getPending[*regState]("reg", u.ID)
	vals := i.MessageComponentData().Values
	if !ok || len(vals) == 0 {
		b.updateClear(i, "That registration expired. Press Register again.")
		return
	}
	st.Class = people.NormalizeClass(vals[0])
	roles := people.RolesFor(st.Class)
	if len(roles) == 1 {
		b.finishRegister(i, st, roles)
		return
	}
	one, max := 1, len(roles)
	b.update(i, fmt.Sprintf("**%s** · %s. Which roles will you play on it? Pick every one you are willing to fill.", st.Name, st.Class),
		row(discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "fs:reg-roles", Placeholder: "Roles", MinValues: &one, MaxValues: max, Options: roleOptions(st.Class, nil)}))
}

func (b *Bot) pickRegRoles(i *discordgo.InteractionCreate) {
	u := userOf(i)
	st, ok := getPending[*regState]("reg", u.ID)
	if !ok {
		b.updateClear(i, "That registration expired. Press Register again.")
		return
	}
	b.finishRegister(i, st, i.MessageComponentData().Values)
}

func (b *Bot) finishRegister(i *discordgo.InteractionCreate, st *regState, roles []string) {
	u := userOf(i)
	name, err := b.People.Register(u.ID, displayName(i), people.DiscordAccountCreated(u.ID), st.Name, st.Class, roles...)
	if err != nil {
		switch {
		case errors.Is(err, people.ErrBanned):
			b.updateClear(i, "That character cannot be registered. Contact an officer.")
			b.logAction(u.ID, "register_refused", "banned identity: "+st.Name)
		case errors.Is(err, people.ErrAccountTooNew):
			b.updateClear(i, fmt.Sprintf("Your Discord account must be at least %d days old to register.", b.Store.ConfigInt("minAccountAgeDays")))
		case errors.Is(err, people.ErrCharacterTaken):
			b.updateClear(i, "That character is registered to someone else. If it is yours, contact an officer.")
		default:
			b.updateClear(i, "Could not register: "+err.Error())
		}
		return
	}
	delPending("reg", u.ID)
	b.grantRegistered(u.ID)
	b.clearNudge(u.ID)
	labels := make([]string, 0, len(roles))
	for _, r := range people.SplitRoles(people.NormalizeRoles(st.Class, roles)) {
		labels = append(labels, people.RoleLabel[r])
	}
	b.personalUpdate(i, fmt.Sprintf("Registered **%s** (%s · %s). You can sign up for raids now. Optional next step: Link Battle.net below.", name, st.Class, strings.Join(labels, ", ")))
	b.post("log", fmt.Sprintf("<@%s> registered %s (%s)", u.ID, name, st.Class))
}

// ---------------------------------------------------------------- onboarding

func (b *Bot) welcomeEmbed() *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title: "Welcome to FairShare",
		Color: 0xC89C5C,
		Description: "Every boss kill is worth shares and everyone present gets an equal cut.\n" +
			"Want an item? Bid your shares in a sealed auction; the winner pays the second-highest bid.\n" +
			"Your standing follows you across every character.\n\n" +
			"**First, accept the rules** (one button, here or in #welcome). Then register a character. Everything else is at " + b.site() + ".",
	}
}

func welcomeButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{row(
		button("Accept the rules", "fs:accept", discordgo.SuccessButton),
		button("Register a character", "fs:register", discordgo.PrimaryButton),
		button("How it works", "fs:howitworks", discordgo.SecondaryButton),
	)}
}

func (b *Bot) howItWorks(i *discordgo.InteractionCreate) {
	b.ephemeral(i, "**How FairShare works**\n"+
		"1. Register your character here (name, class, roles). Shares live on you, not the character.\n"+
		"2. Every raid has its own channel under Signups. Press Sign up there with a character and a role; the leader confirms the roster before the night. Voice is under Voice: Raid 1-5 and Dungeon 1-5.\n"+
		"3. In game, the FairShare addon runs sealed-bid auctions for loot. Bid up to your balance plus credit; the winner pays the second-highest bid and everyone present gets a share.\n"+
		"4. After the raid the leader uploads the transcript; the bot checks every auction and settles balances. `/fairshare` shows yours.\n"+
		"Won items must be equipped by the winner; trading them outside the system is a ban offense.\n"+
		"The full FAQ, the rules and the public ledger: "+b.site()+"/faq")
}

// onGuildMemberAdd DMs newcomers the welcome card. A closed DM is not an error worth
// anything more than a log line: the pinned Register message in #welcome covers them.
func (b *Bot) onGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd) {
	if m.GuildID != b.guildID || m.User == nil || m.User.Bot {
		return
	}
	if m.Member != nil && m.Member.Pending {
		// Discord's rules screen is up. The welcome waits until they accept, when
		// onGuildMemberUpdate hands them the Register button (nudgeRegister).
		pendingJoins.Store(m.User.ID, true)
		return
	}
	ch, err := s.UserChannelCreate(m.User.ID)
	if err != nil {
		log.Printf("welcome DM channel for %s: %v", m.User.ID, err)
		return
	}
	if _, err := b.send(ch.ID, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{b.welcomeEmbed()}, Components: welcomeButtons()}); err != nil {
		log.Printf("welcome DM to %s: %v (DMs closed?)", m.User.ID, err)
	}
}
