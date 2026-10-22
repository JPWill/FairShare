package discord

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/content"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// Server layout. Setup server builds it and repairs it on every run.
//
// Roles, lowest first. Each is granted by the bot at a milestone:
//   Member      accepted the rules in #welcome; sees the server
//   Raider      registered a character; can sign up for raids
//   Veteran     attended a settled raid
//   Raid Leader may create, edit and open raids (officers grant it)
//   Officer     runs the server
//
// Channels, by category. Everyone can read Start Here, Community and Raids (Discord's rules
// screening stops a newcomer from posting until they accept); Signups, Voice and Staff
// need Member, which the bot grants on acceptance.
//   Start Here   #welcome (accept the rules), #rules, #faq, #get-started
//   Community    #general, #raid-chat, #help, #suggestions, #off-topic
//   Signups      one text channel per raid series (#tuesday-7pm-mc), nothing else
//   Raids        #announcements, #loot
//   Voice        The Barrens (general), Raid 1-5, Dungeon 1-5
//   Staff        #officers, #fairshare-log, #fairshare-dev

type roleSpec struct{ key, name string }

var roleSpecs = []roleSpec{
	{"member", "Member"},
	{"raider", "Raider"},
	{"veteran", "Veteran"},
	{"leader", "Raid Leader"},
	{"officer", ""},          // name from config (FAIRSHARE_OFFICER_ROLE_NAME)
	{"horde", "Horde"},       // cosmetic, from the onboarding question
	{"alliance", "Alliance"}, // cosmetic, from the onboarding question
}

type access int

const (
	accessWelcome  access = iota // @everyone reads, nobody but officers and the bot writes
	accessReadOnly               // members read, officers and leaders write
	accessChat                   // members read and write
	accessOfficers               // officers only
	accessVoice                  // members connect and speak; leaders and officers mute and move
	accessMembers                // hidden until Member: the Signups category and its raid channels
)

type channelSpec struct {
	key, name, category string
	access              access
}

var categorySpecs = []roleSpec{{"start", "Start Here"}, {"community", "Community"}, {"signups", "Signups"}, {"raids", "Raids"}, {"voice", "Voice"}, {"staff", "Staff"}}

var channelSpecs = []channelSpec{
	{"welcome", "welcome", "start", accessWelcome},
	{"rules", "rules", "start", accessReadOnly},
	{"faq", "faq", "start", accessReadOnly},
	{"getstarted", "get-started", "start", accessReadOnly},
	{"general", "general", "community", accessChat},
	{"raidchat", "raid-chat", "community", accessChat},
	{"help", "help", "community", accessChat},
	{"suggestions", "suggestions", "community", accessChat},
	{"offtopic", "off-topic", "community", accessChat},
	{"announcements", "announcements", "raids", accessReadOnly},
	{"loot", "loot", "raids", accessReadOnly},
	{"officers", "officers", "staff", accessOfficers},
	{"log", "fairshare-log", "staff", accessOfficers},
	{"dev", "fairshare-dev", "staff", accessOfficers},
}

// voiceSpecs are the voice channels under Voice: The Barrens for everyone, five for
// raids, five for dungeon groups.
var voiceSpecs = []roleSpec{
	{"barrens", "The Barrens"},
	{"raid1", "Raid 1"}, {"raid2", "Raid 2"}, {"raid3", "Raid 3"}, {"raid4", "Raid 4"}, {"raid5", "Raid 5"},
	{"dungeon1", "Dungeon 1"}, {"dungeon2", "Dungeon 2"}, {"dungeon3", "Dungeon 3"}, {"dungeon4", "Dungeon 4"}, {"dungeon5", "Dungeon 5"},
}

const voicePerms = discordgo.PermissionViewChannel | discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak | discordgo.PermissionVoiceUseVAD | discordgo.PermissionVoiceStreamVideo
const voiceModPerms = discordgo.PermissionVoiceMuteMembers | discordgo.PermissionVoiceDeafenMembers | discordgo.PermissionVoiceMoveMembers

const botPerms = discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionReadMessageHistory |
	discordgo.PermissionEmbedLinks | discordgo.PermissionAttachFiles | discordgo.PermissionManageMessages | discordgo.PermissionManageChannels |
	discordgo.PermissionCreateInstantInvite

const readPerms = discordgo.PermissionViewChannel | discordgo.PermissionReadMessageHistory
const chatPerms = readPerms | discordgo.PermissionSendMessages | discordgo.PermissionSendMessagesInThreads | discordgo.PermissionAddReactions | discordgo.PermissionAttachFiles | discordgo.PermissionEmbedLinks

// overwrites builds the permission set for one access level. The bot always has an
// explicit allow so it can never lock itself out of a channel it must post in.
func (b *Bot) overwrites(a access, roles map[string]string) []*discordgo.PermissionOverwrite {
	everyone := b.guildID // the @everyone role id equals the guild id
	botID := ""
	if b.S != nil && b.S.State != nil && b.S.State.User != nil {
		botID = b.S.State.User.ID
	}
	role := func(key string, allow, deny int64) *discordgo.PermissionOverwrite {
		return &discordgo.PermissionOverwrite{ID: roles[key], Type: discordgo.PermissionOverwriteTypeRole, Allow: allow, Deny: deny}
	}
	var ov []*discordgo.PermissionOverwrite
	switch a {
	case accessWelcome:
		ov = []*discordgo.PermissionOverwrite{
			{ID: everyone, Type: discordgo.PermissionOverwriteTypeRole, Allow: readPerms, Deny: discordgo.PermissionSendMessages | discordgo.PermissionAddReactions},
			role("officer", discordgo.PermissionSendMessages, 0),
		}
	case accessReadOnly:
		ov = []*discordgo.PermissionOverwrite{
			{ID: everyone, Type: discordgo.PermissionOverwriteTypeRole, Allow: readPerms, Deny: discordgo.PermissionSendMessages},
			role("member", readPerms|discordgo.PermissionAddReactions, discordgo.PermissionSendMessages),
			role("leader", discordgo.PermissionSendMessages, 0),
			role("officer", discordgo.PermissionSendMessages|discordgo.PermissionManageMessages, 0),
		}
	case accessChat:
		ov = []*discordgo.PermissionOverwrite{
			{ID: everyone, Type: discordgo.PermissionOverwriteTypeRole, Allow: chatPerms},
			role("member", chatPerms, 0),
			role("officer", discordgo.PermissionManageMessages, 0),
		}
	case accessOfficers:
		ov = []*discordgo.PermissionOverwrite{
			{ID: everyone, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			role("officer", chatPerms|discordgo.PermissionManageMessages, 0),
		}
	case accessMembers:
		ov = []*discordgo.PermissionOverwrite{
			{ID: everyone, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel},
			role("member", chatPerms, 0),
			role("officer", discordgo.PermissionManageMessages, 0),
		}
	case accessVoice:
		ov = []*discordgo.PermissionOverwrite{
			{ID: everyone, Type: discordgo.PermissionOverwriteTypeRole, Deny: discordgo.PermissionViewChannel | discordgo.PermissionVoiceConnect},
			role("member", voicePerms, 0),
			role("leader", voicePerms|voiceModPerms, 0),
			role("officer", voicePerms|voiceModPerms, 0),
		}
	}
	if botID != "" {
		ov = append(ov, &discordgo.PermissionOverwrite{ID: botID, Type: discordgo.PermissionOverwriteTypeMember, Allow: botPerms})
	}
	// Drop overwrites for roles that do not exist yet (sandbox, or a failed create), then
	// anything the bot itself may not grant (perms.go).
	out := ov[:0]
	for _, o := range ov {
		if o.ID != "" {
			out = append(out, o)
		}
	}
	out, _ = maskOverwrites(out, b.permMask)
	return out
}

// raidChannelOverwrites: members chat, leaders and officers moderate.
func (b *Bot) raidChannelOverwrites(roles map[string]string) []*discordgo.PermissionOverwrite {
	ov := b.overwrites(accessMembers, roles)
	if id := roles["leader"]; id != "" {
		ov = append(ov, &discordgo.PermissionOverwrite{ID: id, Type: discordgo.PermissionOverwriteTypeRole, Allow: chatPerms | discordgo.PermissionManageMessages | discordgo.PermissionMentionEveryone})
	}
	return ov
}

// roleIDs is the stored role id per key.
func (b *Bot) roleIDs() map[string]string {
	m := map[string]string{}
	for _, r := range roleSpecs {
		m[r.key] = b.Store.ConfigString("role." + r.key)
	}
	return m
}

// setupServer is idempotent: it creates what is missing, repairs permissions, posts or
// refreshes the pinned cards, and records every id.
func (b *Bot) setupServer(i *discordgo.InteractionCreate) {
	b.defer_(i)
	var report []string
	var hidden []string

	// Roles.
	existing, err := b.S.GuildRoles(b.guildID)
	if err != nil {
		b.followup(i, "Could not list roles: "+err.Error())
		return
	}
	roleNames := map[string]string{}
	for _, r := range existing {
		roleNames[r.ID] = r.Name
	}
	roles := map[string]string{}
	for _, want := range roleSpecs {
		name := want.name
		if want.key == "officer" {
			name = b.Cfg.OfficerRoleName
		}
		var byName []string
		for _, r := range existing {
			if strings.EqualFold(r.Name, name) {
				byName = append(byName, r.ID)
			}
		}
		id, warn := adopt("role", want.key, name, b.Store.ConfigString("role."+want.key), roleNames, byName)
		if warn != "" {
			report = append(report, warn)
		}
		if id == "" {
			id, err = b.createRole(name)
			if err != nil {
				b.followup(i, "Could not create role "+name+": "+err.Error())
				return
			}
			report = append(report, "created role "+name)
		}
		roles[want.key] = id
		if id != "" {
			_ = b.Store.SetConfig("role."+want.key, id)
		}
	}

	// What the bot may grant. Overwrites are masked to this; the leftovers go in the report.
	b.permMask = b.botPermissions(existing)
	var wanted int64
	for _, a := range []access{accessWelcome, accessReadOnly, accessChat, accessOfficers, accessVoice} {
		saved := b.permMask
		b.permMask = -1
		for _, o := range b.overwrites(a, roles) {
			wanted |= o.Allow | o.Deny
		}
		b.permMask = saved
	}
	var lacking int64
	if b.permMask > 0 {
		lacking = wanted &^ b.permMask
	}

	// Categories and channels.
	chans, err := b.S.GuildChannels(b.guildID)
	if err != nil {
		b.followup(i, "Could not list channels: "+err.Error())
		return
	}
	catNames, chanNames := map[string]string{}, map[string]string{}
	for _, c := range chans {
		switch c.Type {
		case discordgo.ChannelTypeGuildCategory:
			catNames[c.ID] = c.Name
		case discordgo.ChannelTypeGuildText:
			chanNames[c.ID] = c.Name
		}
	}
	cats := map[string]string{}
	for _, want := range categorySpecs {
		var byName []string
		for _, c := range chans {
			if c.Type == discordgo.ChannelTypeGuildCategory && strings.EqualFold(c.Name, want.name) {
				byName = append(byName, c.ID)
			}
		}
		id, warn := adopt("category", want.key, want.name, b.Store.ConfigString("category."+want.key), catNames, byName)
		if warn != "" {
			report = append(report, warn)
		}
		a := accessReadOnly
		switch want.key {
		case "community":
			a = accessChat
		case "signups":
			a = accessMembers
		case "staff":
			a = accessOfficers
		case "voice":
			a = accessVoice
		}
		if id != "" {
			// Existing category: re-apply its overwrites so a re-run repairs what an earlier
			// layout set (channels inherit from it unless they override).
			for _, o := range b.overwrites(a, roles) {
				if err := b.setPermission(id, o); err != nil {
					report = append(report, "could not set permissions on category "+want.name+": "+err.Error())
					break
				}
			}
		}
		if id == "" {
			id, err = b.createCategory(want.name, b.overwrites(a, roles))
			if err != nil {
				report = append(report, "could not create category "+want.name+": "+err.Error())
				id = ""
			} else {
				report = append(report, "created category "+want.name)
			}
		}
		cats[want.key] = id
		if id != "" {
			_ = b.Store.SetConfig("category."+want.key, id)
		}
	}
	for _, spec := range channelSpecs {
		ov := b.overwrites(spec.access, roles)
		var byName []string
		for _, c := range chans {
			if c.Type == discordgo.ChannelTypeGuildText && c.Name == spec.name {
				byName = append(byName, c.ID)
			}
		}
		id, warn := adopt("channel", spec.key, "#"+spec.name, b.Store.ConfigString("channel."+spec.key), chanNames, byName)
		if warn != "" {
			report = append(report, warn)
		}
		if id == "" {
			id, err = b.createChannelIn(spec.name, cats[spec.category], ov)
			if err != nil {
				report = append(report, "could not create #"+spec.name+": "+err.Error())
				id = ""
			} else {
				report = append(report, "created #"+spec.name)
			}
		} else {
			// Existing channel: re-apply the overwrites so a re-run repairs permissions,
			// and move it under its category.
			for _, o := range ov {
				if err := b.setPermission(id, o); err != nil {
					if strings.Contains(err.Error(), "50001") {
						hidden = append(hidden, "#"+spec.name) // the bot cannot see it, so cannot edit it
					} else {
						report = append(report, "could not set permissions on #"+spec.name+": "+err.Error())
					}
					break
				}
			}
			if parent := cats[spec.category]; parent != "" {
				_ = b.moveChannel(id, parent)
			}
		}
		if id != "" {
			_ = b.Store.SetConfig("channel."+spec.key, id)
		}
	}

	// Voice channels.
	voiceNames := map[string]string{}
	for _, c := range chans {
		if c.Type == discordgo.ChannelTypeGuildVoice {
			voiceNames[c.ID] = c.Name
		}
	}
	for _, spec := range voiceSpecs {
		var byName []string
		for _, c := range chans {
			if c.Type == discordgo.ChannelTypeGuildVoice && strings.EqualFold(c.Name, spec.name) {
				byName = append(byName, c.ID)
			}
		}
		id, warn := adopt("voice channel", spec.key, spec.name, b.Store.ConfigString("voice."+spec.key), voiceNames, byName)
		if warn != "" {
			report = append(report, warn)
		}
		if id == "" {
			id, err = b.createVoiceChannel(spec.name, cats["voice"], b.overwrites(accessVoice, roles))
			if err != nil {
				report = append(report, "could not create voice channel "+spec.name+": "+err.Error())
				id = ""
			} else {
				report = append(report, "created voice channel "+spec.name)
			}
		} else {
			for _, o := range b.overwrites(accessVoice, roles) {
				if err := b.setPermission(id, o); err != nil {
					break
				}
			}
			if parent := cats["voice"]; parent != "" {
				_ = b.moveChannel(id, parent)
			}
		}
		if id != "" {
			_ = b.Store.SetConfig("voice."+spec.key, id)
		}
	}

	// Pinned cards. Each is posted once and edited on later runs so wording changes land.
	if !b.Store.IsSandbox() {
		for _, c := range b.cards() {
			if note := b.ensureCard(c); note != "" {
				report = append(report, note)
			}
		}
		if note := b.ensureInvite(); note != "" {
			report = append(report, note)
		}
		if note := b.setupScreening(); note != "" {
			report = append(report, note)
		}
		if note := b.setupOnboarding(roles, chans, existing); note != "" {
			report = append(report, note)
		}
	} else {
		report = append(report, "pinned cards and the invite are skipped in the sandbox")
	}

	if len(report) == 0 {
		report = append(report, "everything already existed; ids refreshed")
	}
	b.logAction(userOf(i).ID, "setup", strings.Join(report, ", "))
	msg := "Setup complete: " + strings.Join(report, ", ") + ".\nMake sure the FairShare bot role sits above Member, Raider, Veteran, Raid Leader and " + b.Cfg.OfficerRoleName + " in Server Settings → Roles. Newcomers can read the public channels but Discord's rules screening stops them posting until they accept; Signups, Voice and Staff open once they have."
	if b.Store.IsSandbox() {
		msg = "Sandbox is on, so nothing was created or changed on the server; what would have happened is listed in #fairshare-dev. " + msg
	}
	if lacking != 0 {
		msg += "\n\n**Action needed:** my role is missing " + permList(lacking) + ", so I left those out of the channel permissions (Discord refuses any permission I do not hold myself). " +
			"Server Settings → Roles → the FairShare bot role → turn them on, then press Setup server again and I will fill them in."
	}
	if len(hidden) > 0 {
		msg += "\n\n**Action needed:** I cannot see " + strings.Join(hidden, ", ") + ", so I cannot fix their permissions (a channel-level deny on @everyone hides a channel from me whatever my role order). " +
			"Either delete those channels and press Setup server again (I will recreate them correctly), or open each channel → Edit → Permissions → add the FairShare bot with View Channel, then press Setup server again."
	}
	b.adminFollowup(i, msg)
}

// ---------------------------------------------------------------- pinned cards

// card is one persistent, pinned message the bot owns.
type card struct {
	key        string // guild_config "post.<key>" holds the message id
	channelKey string
	embeds     []*discordgo.MessageEmbed
	components []discordgo.MessageComponent
}

// params gathers the live settings the texts quote.
func (b *Bot) params() content.Params {
	floor := func(slot string, fallback int64) int64 {
		var v int64
		if err := b.Store.DB.QueryRow(`SELECT value FROM floor_price WHERE slot = ?`, slot).Scan(&v); err != nil {
			return fallback
		}
		return v
	}
	return content.Params{
		FloorDefault: floor("DEFAULT", 10), Floor2H: floor("INVTYPE_2HWEAPON", 20),
		Newcomer: b.Store.ConfigInt("newcomerCredit"), PerRaid: b.Store.ConfigInt("creditPerRaid"), Cap: b.Store.ConfigInt("creditCap"),
		PoolPct: b.Store.ConfigInt("poolPct"), FreePasses: b.Store.ConfigInt("freePasses"), Penalty: b.Store.ConfigInt("norevealPenalty"),
		MinAge: b.Store.ConfigInt("minAccountAgeDays"), OffspecPct: b.Store.ConfigInt("offspecPct"),
		Site: b.site(), Discord: b.Store.ConfigString("discordInvite"),
	}
}

// site is the community website without a trailing slash.
func (b *Bot) site() string {
	s := strings.TrimRight(b.Store.ConfigString("websiteURL"), "/")
	if !strings.HasPrefix(s, "https://") {
		return content.DefaultSite
	}
	return s
}

const (
	colorBronze = 0xC89C5C
	colorGreen  = 0x57C785
	colorGold   = 0xF9C74F
)

// cards are every pinned message, in posting order.
func (b *Bot) cards() []card {
	p := b.params()
	var out []card

	// #welcome: the gate.
	out = append(out, card{key: "welcome", channelKey: "welcome",
		embeds: []*discordgo.MessageEmbed{{
			Title: "Welcome to FairShare", Color: colorBronze,
			Description: "Cross-guild PUG raids on WoW: Forever. Loot is auctioned for **shares** that follow you across every character, every price is public, and nobody can see your bid.\n\n" +
				"Press **Accept the rules** to open the rest of the server. Then register a character in #get-started.\n\n" + p.Terms(),
			Footer: &discordgo.MessageEmbedFooter{Text: "Rules: #rules  ·  Questions: #faq  ·  " + p.Site},
		}},
		components: []discordgo.MessageComponent{row(
			button("Accept the rules", "fs:accept", discordgo.SuccessButton),
			button("How it works", "fs:howitworks", discordgo.SecondaryButton),
		)},
	})

	// #rules.
	var rules []string
	for n, r := range p.Rules() {
		rules = append(rules, fmt.Sprintf("%d. %s", n+1, r))
	}
	out = append(out, card{key: "rules", channelKey: "rules",
		embeds: []*discordgo.MessageEmbed{{
			Title: "FairShare rules", Color: colorBronze,
			Description: content.RulesIntro + "\n\n" + strings.Join(rules, "\n\n") + "\n\n" + content.RulesOutro,
			Footer:      &discordgo.MessageEmbedFooter{Text: "Full detail: " + p.Site + "/faq"},
		}},
	})

	// #faq: six questions per message so every answer fits an embed field.
	faq := p.FAQ()
	for n := 0; n < len(faq); n += 6 {
		end := n + 6
		if end > len(faq) {
			end = len(faq)
		}
		e := &discordgo.MessageEmbed{Color: colorBronze}
		if n == 0 {
			e.Title = "Frequently asked questions"
			e.Description = "The same answers, with live numbers, at " + p.Site + "/faq"
		}
		for _, qa := range faq[n:end] {
			e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: qa.Q, Value: qa.A})
		}
		out = append(out, card{key: fmt.Sprintf("faq%d", n/6), channelKey: "faq", embeds: []*discordgo.MessageEmbed{e}})
	}

	// #get-started: the Register button.
	out = append(out, card{key: "getstarted", channelKey: "getstarted",
		embeds: []*discordgo.MessageEmbed{{Title: "Get started", Color: colorGreen, Description: p.GetStarted()}},
		components: []discordgo.MessageComponent{row(
			button("Register a character", "fs:register", discordgo.PrimaryButton),
			button("How it works", "fs:howitworks", discordgo.SecondaryButton),
		)},
	})
	return out
}

// ensureCard posts a card once, then edits it in place on later runs. Returns a note
// for the setup report, or "" when nothing changed.
func (b *Bot) ensureCard(c card) string {
	ch := b.Store.ConfigString("channel." + c.channelKey)
	if ch == "" {
		return ""
	}
	comps := c.components
	if comps == nil {
		comps = []discordgo.MessageComponent{}
	}
	if id := b.Store.ConfigString("post." + c.key); id != "" {
		if _, err := b.S.ChannelMessage(ch, id); err == nil {
			_, err := b.edit(&discordgo.MessageEdit{Channel: ch, ID: id, Embeds: &c.embeds, Components: &comps})
			if err != nil {
				return "could not refresh the " + c.key + " card: " + err.Error()
			}
			return ""
		}
	}
	msg, err := b.send(ch, &discordgo.MessageSend{Embeds: c.embeds, Components: comps})
	if err != nil {
		return "could not post the " + c.key + " card: " + err.Error()
	}
	if err := b.pinMessage(ch, msg.ID); err != nil {
		log.Printf("pin %s: %v", c.key, err)
	}
	_ = b.Store.SetConfig("post."+c.key, msg.ID)
	return "posted the " + c.key + " card"
}

// ensureInvite creates a permanent invite to #welcome and stores it as the website's
// Discord link, unless an officer already set one.
func (b *Bot) ensureInvite() string {
	if b.Store.ConfigString("discordInvite") != "" {
		return ""
	}
	ch := b.Store.ConfigString("channel.welcome")
	if ch == "" {
		return ""
	}
	code, err := b.createInvite(ch)
	if err != nil {
		return "could not create an invite: " + err.Error()
	}
	if code == "" {
		return ""
	}
	url := "https://discord.gg/" + code
	if !store.ValidDiscordInvite(url) {
		return ""
	}
	_ = b.Store.SetConfig("discordInvite", url)
	return "created the invite " + url + " and put it on the website"
}
