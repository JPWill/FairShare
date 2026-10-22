// Package discord is the FairShare bot. It is GUI-first: slash commands only open
// panels; everything else is buttons, select menus, and modals. It also administers
// the server (roles, channels, announcements, bans) so no other bot is needed.
//
// Running and configuring it is described in backend/README.md.
package discord

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/bans"
	"github.com/JPWill/FairShare/backend/internal/config"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/store"
)

// Bot holds the session and services.
type Bot struct {
	S       *discordgo.Session
	Cfg     config.Config
	Store   *store.Store
	People  *people.Service
	Bans    *bans.Service
	Raids   *raids.Service
	guildID string

	mu       sync.Mutex
	stopCh   chan struct{}
	stopOnce sync.Once
	emoji    map[string]string // class/role key -> "<:fs_name:id>" (see emoji.go)
	permMask int64             // the bot's own guild permissions, learnt by Setup server; 0 = unknown, -1 = all (perms.go)
}

// New creates the bot (does not connect).
func New(cfg config.Config, st *store.Store, p *people.Service, b *bans.Service, r *raids.Service) (*Bot, error) {
	s, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		return nil, err
	}
	s.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMembers | discordgo.IntentsGuildMessages
	bot := &Bot{S: s, Cfg: cfg, Store: st, People: p, Bans: b, Raids: r, guildID: cfg.DiscordGuildID, stopCh: make(chan struct{})}
	r.OnFirstAttendance = bot.grantRaider
	s.AddHandler(bot.onReady)
	s.AddHandler(bot.onInteraction)
	s.AddHandler(bot.onGuildMemberAdd)
	s.AddHandler(bot.onGuildMemberUpdate)
	return bot, nil
}

// Start connects, registers the guild commands, and starts the scheduler.
func (b *Bot) Start() error {
	if err := b.S.Open(); err != nil {
		return err
	}
	if _, err := b.S.ApplicationCommandBulkOverwrite(b.Cfg.DiscordAppID, b.guildID, commands()); err != nil {
		return fmt.Errorf("register commands: %w", err)
	}
	go b.scheduler()
	return nil
}

// Stop disconnects.
func (b *Bot) Stop() {
	b.stopOnce.Do(func() { close(b.stopCh) })
	_ = b.S.Close()
}

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Printf("connected as %s#%s", r.User.Username, r.User.Discriminator)
	go b.syncEmoji()
}

// ---------------------------------------------------------------- helpers

func userOf(i *discordgo.InteractionCreate) *discordgo.User {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User
	}
	return i.User
}

func displayName(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.Nick != "" {
		return i.Member.Nick
	}
	u := userOf(i)
	if u.GlobalName != "" {
		return u.GlobalName
	}
	return u.Username
}

// foreignGuild reports whether an interaction comes from a server other than the one
// the bot is bound to. DMs (no guild) are not foreign; dmAllowed decides what they may do.
func (b *Bot) foreignGuild(i *discordgo.InteractionCreate) bool {
	return i.GuildID != "" && i.GuildID != b.guildID
}

// dmActions are the component/modal actions a DM may trigger: the registration flow
// the welcome DM starts, and nothing else. Everything else needs the server, where the
// member's roles are known.
var dmActions = map[string]bool{"register": true, "reg-class": true, "reg-roles": true, "howitworks": true, "accept": true}

// isOfficer: guild owner, Administrator permission, or the officer role. Once Setup
// server has stored the officer role's id that id is the only role that counts; the
// match-by-name fallback exists for a server that has not run Setup yet, and would
// otherwise let anyone who can create a role called "Officer" into the panel.
func (b *Bot) isOfficer(i *discordgo.InteractionCreate) bool {
	if i.Member == nil || i.GuildID != b.guildID {
		return false
	}
	if i.Member.Permissions&discordgo.PermissionAdministrator != 0 {
		return true
	}
	if g, err := b.S.State.Guild(b.guildID); err == nil && g.OwnerID == userOf(i).ID {
		return true
	}
	officerRole := b.Store.ConfigString("role.officer")
	for _, rid := range i.Member.Roles {
		if rid == "" {
			continue
		}
		if officerRole != "" {
			if rid == officerRole {
				return true
			}
			continue
		}
		if role, err := b.S.State.Role(b.guildID, rid); err == nil && strings.EqualFold(role.Name, b.Cfg.OfficerRoleName) {
			return true
		}
	}
	return false
}

// memberPrivilege says why a member must not be banned through the bot: "" when they
// may be. Officers, administrators and the owner are protected so a compromised or
// careless officer account cannot lock the others out; the bot itself likewise.
func (b *Bot) memberPrivilege(userID string) (string, error) {
	if b.S.State != nil && b.S.State.User != nil && userID == b.S.State.User.ID {
		return "that is me", nil
	}
	if g, err := b.S.State.Guild(b.guildID); err == nil && g.OwnerID == userID {
		return "they own the server", nil
	}
	m, err := b.S.GuildMember(b.guildID, userID)
	if err != nil {
		return "", err
	}
	if m.User != nil && m.User.Bot {
		return "they are a bot", nil
	}
	officerRole := b.Store.ConfigString("role.officer")
	for _, rid := range m.Roles {
		if rid != "" && rid == officerRole {
			return "they are an officer", nil
		}
		role, err := b.S.State.Role(b.guildID, rid)
		if err != nil {
			continue
		}
		if role.Permissions&discordgo.PermissionAdministrator != 0 {
			return "they are an administrator", nil
		}
		if officerRole == "" && strings.EqualFold(role.Name, b.Cfg.OfficerRoleName) {
			return "they are an officer", nil
		}
	}
	return "", nil
}

// ---------------------------------------------------------------- mentions and text safety

// noMentions is the default for everything the bot sends: names still render, nobody is
// pinged. An empty Parse list (not nil) is what Discord expects for "allow none".
var noMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}}

// mentionUsers allows pings for exactly these users and nothing else.
func mentionUsers(ids ...string) *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{}, Users: ids}
}

// announceMentions is for officer announcements, the one place a ping is the point.
var announceMentions = &discordgo.MessageAllowedMentions{Parse: []discordgo.AllowedMentionType{discordgo.AllowedMentionTypeEveryone, discordgo.AllowedMentionTypeRoles, discordgo.AllowedMentionTypeUsers}}

// safeText makes user-controlled text safe to drop into an embed, a code fence or a
// post: backticks (which would close a fence) and mass-mention words are removed.
// Allowed-mentions already stop the ping; this stops the text from looking like one.
func safeText(s string) string {
	return strings.NewReplacer("`", "'", "@everyone", "everyone", "@here", "here").Replace(s)
}

// send posts a message with the no-mention default applied unless the caller chose.
func (b *Bot) send(channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	if msg.AllowedMentions == nil {
		msg.AllowedMentions = noMentions
	}
	return b.S.ChannelMessageSendComplex(channelID, msg)
}

// edit edits a message with the no-mention default applied unless the caller chose.
func (b *Bot) edit(m *discordgo.MessageEdit) (*discordgo.Message, error) {
	if m.AllowedMentions == nil {
		m.AllowedMentions = noMentions
	}
	return b.S.ChannelMessageEditComplex(m)
}

func (b *Bot) respond(i *discordgo.InteractionCreate, kind discordgo.InteractionResponseType, data *discordgo.InteractionResponseData, what string) {
	if data.AllowedMentions == nil {
		data.AllowedMentions = noMentions
	}
	if err := b.S.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{Type: kind, Data: data}); err != nil {
		log.Printf("%s: %v", what, err)
	}
}

func (b *Bot) ephemeral(i *discordgo.InteractionCreate, content string, components ...discordgo.MessageComponent) {
	b.respond(i, discordgo.InteractionResponseChannelMessageWithSource,
		&discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral, Components: components}, "respond")
}

func (b *Bot) ephemeralEmbed(i *discordgo.InteractionCreate, e *discordgo.MessageEmbed, components ...discordgo.MessageComponent) {
	b.respond(i, discordgo.InteractionResponseChannelMessageWithSource,
		&discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{e}, Flags: discordgo.MessageFlagsEphemeral, Components: components}, "respond")
}

// update edits the message the component lives on (keeps panels in place).
func (b *Bot) update(i *discordgo.InteractionCreate, content string, components ...discordgo.MessageComponent) {
	b.respond(i, discordgo.InteractionResponseUpdateMessage,
		&discordgo.InteractionResponseData{Content: content, Components: components}, "update")
}

func (b *Bot) modal(i *discordgo.InteractionCreate, customID, title string, inputs ...discordgo.TextInput) {
	rows := make([]discordgo.MessageComponent, 0, len(inputs))
	for _, in := range inputs {
		in := in
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{in}})
	}
	err := b.S.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{CustomID: customID, Title: title, Components: rows},
	})
	if err != nil {
		log.Printf("modal: %v", err)
	}
}

func (b *Bot) defer_(i *discordgo.InteractionCreate) {
	_ = b.S.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (b *Bot) followup(i *discordgo.InteractionCreate, content string) {
	if _, err := b.S.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: content, Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: noMentions}); err != nil {
		log.Printf("followup: %v", err)
	}
}

// modalValues maps text input custom ids to their values.
func modalValues(i *discordgo.InteractionCreate) map[string]string {
	out := map[string]string{}
	for _, row := range i.ModalSubmitData().Components {
		ar, ok := row.(*discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, c := range ar.Components {
			if ti, ok := c.(*discordgo.TextInput); ok {
				out[ti.CustomID] = strings.TrimSpace(ti.Value)
			}
		}
	}
	return out
}

func text(id, label, placeholder string, required bool, maxLen int) discordgo.TextInput {
	return discordgo.TextInput{CustomID: id, Label: label, Style: discordgo.TextInputShort, Placeholder: placeholder, Required: required, MaxLength: maxLen}
}

func paragraph(id, label, placeholder string, required bool, maxLen int) discordgo.TextInput {
	return discordgo.TextInput{CustomID: id, Label: label, Style: discordgo.TextInputParagraph, Placeholder: placeholder, Required: required, MaxLength: maxLen}
}

func button(label, customID string, style discordgo.ButtonStyle) discordgo.Button {
	return discordgo.Button{Label: label, CustomID: customID, Style: style}
}

func row(components ...discordgo.MessageComponent) discordgo.ActionsRow {
	return discordgo.ActionsRow{Components: components}
}

// ---------------------------------------------------------------- channels and logging

// channel returns the channel id for a key. In sandbox mode every post goes to
// #fairshare-dev so nothing lands in the public channels or the audit log.
func (b *Bot) channel(key string) string {
	if b.Store.IsSandbox() && key != "dev" {
		return b.Store.ConfigString("channel.dev")
	}
	return b.Store.ConfigString("channel." + key)
}

func (b *Bot) post(channelKey, content string) {
	id := b.channel(channelKey)
	if id == "" {
		return
	}
	if b.Store.IsSandbox() {
		content = "[sandbox " + channelKey + "] " + content
	}
	if _, err := b.send(id, &discordgo.MessageSend{Content: content}); err != nil {
		log.Printf("post to %s: %v", channelKey, err)
	}
}

// logAction writes to the audit log and #fairshare-log.
func (b *Bot) logAction(actor, action, detail string) {
	b.Store.Audit(actor, action, detail)
	b.post("log", fmt.Sprintf("**%s** by <@%s>: %s", action, actor, detail))
}

// devPost sends to #fairshare-dev when devPosting is on.
func (b *Bot) devPost(content string, embed *discordgo.MessageEmbed) {
	b.devPostMentioning(content, embed, nil)
}

// devPostMentioning is devPost with pings allowed for the given users only.
func (b *Bot) devPostMentioning(content string, embed *discordgo.MessageEmbed, users []string) {
	if strings.ToLower(b.Store.ConfigString("devPosting")) == "false" {
		return
	}
	id := b.channel("dev")
	if id == "" {
		return
	}
	msg := &discordgo.MessageSend{Content: content}
	if embed != nil {
		msg.Embeds = []*discordgo.MessageEmbed{embed}
	}
	if len(users) > 0 {
		msg.AllowedMentions = mentionUsers(users...)
	}
	if _, err := b.send(id, msg); err != nil {
		log.Printf("dev post: %v", err)
	}
}

// ---------------------------------------------------------------- guild mutations
//
// Every change to the server itself (bans, roles, channels, permissions, pins) goes
// through one of these so the sandbox gate lives in one place: while the sandbox is
// on, nothing about the real server changes; the helper reports what it would have
// done in #fairshare-dev and returns success, so the calling flow completes as it
// would live.

func (b *Bot) sandboxed(action string) bool {
	if !b.Store.IsSandbox() {
		return false
	}
	b.devPost("[sandbox] would "+action, nil)
	return true
}

func (b *Bot) banMember(userID, reason string) error {
	if b.sandboxed(fmt.Sprintf("ban <@%s> from the server: %s", userID, safeText(reason))) {
		return nil
	}
	return b.S.GuildBanCreateWithReason(b.guildID, userID, "FairShare: "+reason, 0)
}

func (b *Bot) unbanMember(userID string) error {
	if b.sandboxed(fmt.Sprintf("lift the server ban on <@%s>", userID)) {
		return nil
	}
	return b.S.GuildBanDelete(b.guildID, userID)
}

func (b *Bot) grantRole(userID, roleID string) error {
	if b.sandboxed(fmt.Sprintf("grant role <@&%s> to <@%s>", roleID, userID)) {
		return nil
	}
	return b.S.GuildMemberRoleAdd(b.guildID, userID, roleID)
}

func (b *Bot) removeRole(userID, roleID string) error {
	if b.sandboxed(fmt.Sprintf("remove role <@&%s> from <@%s>", roleID, userID)) {
		return nil
	}
	return b.S.GuildMemberRoleRemove(b.guildID, userID, roleID)
}

// createRole returns "" with no error in the sandbox: the caller must not store it.
func (b *Bot) createRole(name string) (string, error) {
	if b.sandboxed("create role " + safeText(name)) {
		return "", nil
	}
	r, err := b.S.GuildRoleCreate(b.guildID, &discordgo.RoleParams{Name: name})
	if err != nil {
		return "", err
	}
	return r.ID, nil
}

func (b *Bot) setPermission(channelID string, o *discordgo.PermissionOverwrite) error {
	if b.sandboxed(fmt.Sprintf("set a permission overwrite on <#%s> for %s", channelID, o.ID)) {
		return nil
	}
	return b.S.ChannelPermissionSet(channelID, o.ID, o.Type, o.Allow, o.Deny)
}

func (b *Bot) pinMessage(channelID, messageID string) error {
	if b.sandboxed(fmt.Sprintf("pin message %s in <#%s>", messageID, channelID)) {
		return nil
	}
	return b.S.ChannelMessagePin(channelID, messageID)
}

// createCategory returns "" with no error in the sandbox: the caller must not store it.
func (b *Bot) createCategory(name string, overwrites []*discordgo.PermissionOverwrite) (string, error) {
	if b.sandboxed("create category " + safeText(name)) {
		return "", nil
	}
	c, err := b.S.GuildChannelCreateComplex(b.guildID, discordgo.GuildChannelCreateData{Name: name, Type: discordgo.ChannelTypeGuildCategory, PermissionOverwrites: overwrites})
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// createChannelIn creates a text channel under a category (parent may be ""). Returns ""
// with no error in the sandbox.
func (b *Bot) createChannelIn(name, parent string, overwrites []*discordgo.PermissionOverwrite) (string, error) {
	if b.sandboxed("create channel #" + safeText(name)) {
		return "", nil
	}
	c, err := b.S.GuildChannelCreateComplex(b.guildID, discordgo.GuildChannelCreateData{Name: name, Type: discordgo.ChannelTypeGuildText, ParentID: parent, PermissionOverwrites: overwrites})
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

// createVoiceChannel creates a voice channel under a category. Returns "" with no error
// in the sandbox.
func (b *Bot) createVoiceChannel(name, parent string, overwrites []*discordgo.PermissionOverwrite) (string, error) {
	if b.sandboxed("create voice channel " + safeText(name)) {
		return "", nil
	}
	c, err := b.S.GuildChannelCreateComplex(b.guildID, discordgo.GuildChannelCreateData{Name: name, Type: discordgo.ChannelTypeGuildVoice, ParentID: parent, PermissionOverwrites: overwrites})
	if err != nil {
		return "", err
	}
	return c.ID, nil
}

func (b *Bot) moveChannel(channelID, parent string) error {
	if b.sandboxed(fmt.Sprintf("move <#%s> under category %s", channelID, parent)) {
		return nil
	}
	_, err := b.S.ChannelEditComplex(channelID, &discordgo.ChannelEdit{ParentID: parent})
	return err
}

func (b *Bot) renameChannel(channelID, name string) error {
	if b.sandboxed(fmt.Sprintf("rename <#%s> to #%s", channelID, safeText(name))) {
		return nil
	}
	_, err := b.S.ChannelEditComplex(channelID, &discordgo.ChannelEdit{Name: name})
	return err
}

func (b *Bot) deleteChannel(channelID string) error {
	if b.sandboxed(fmt.Sprintf("delete <#%s>", channelID)) {
		return nil
	}
	_, err := b.S.ChannelDelete(channelID)
	return err
}

// createInvite makes a permanent, unlimited invite to a channel and returns its code,
// or "" in the sandbox.
func (b *Bot) createInvite(channelID string) (string, error) {
	if b.sandboxed(fmt.Sprintf("create a permanent invite to <#%s>", channelID)) {
		return "", nil
	}
	inv, err := b.S.ChannelInviteCreate(channelID, discordgo.Invite{MaxAge: 0, MaxUses: 0, Temporary: false, Unique: true})
	if err != nil {
		return "", err
	}
	return inv.Code, nil
}

// grantRaider marks first attendance with the Veteran role (raids.OnFirstAttendance).
func (b *Bot) grantRaider(personIDs []string) {
	role := b.Store.ConfigString("role.veteran")
	if role == "" {
		return
	}
	for _, id := range personIDs {
		if err := b.grantRole(id, role); err != nil {
			log.Printf("grant raider to %s: %v", id, err)
		}
	}
}

func (b *Bot) grantMember(userID string) {
	role := b.Store.ConfigString("role.member")
	if role == "" {
		return
	}
	if err := b.grantRole(userID, role); err != nil {
		log.Printf("grant member to %s: %v", userID, err)
	}
}

// grantRegistered gives the Raider role on a first registered character.
func (b *Bot) grantRegistered(userID string) {
	b.grantMember(userID)
	role := b.Store.ConfigString("role.raider")
	if role == "" {
		return
	}
	if err := b.grantRole(userID, role); err != nil {
		log.Printf("grant raider to %s: %v", userID, err)
	}
}

func fmtTime(unix int64) string {
	if unix == 0 {
		return "-"
	}
	return fmt.Sprintf("<t:%d:f>", unix)
}

func parseTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02T15:04", "2006-01-02 15:04 MST"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix(), nil
		}
	}
	return 0, fmt.Errorf("time must look like 2026-12-09 19:30 (UTC)")
}
