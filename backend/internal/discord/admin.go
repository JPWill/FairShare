package discord

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/JPWill/FairShare/backend/internal/store"
	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/snapshot"
)

// adminPanel is what /fairshare-admin opens for officers.
func (b *Bot) adminPanel(i *discordgo.InteractionCreate) {
	if !b.isOfficer(i) {
		b.ephemeral(i, "Officers only.")
		return
	}
	e, comps := b.adminView()
	b.ephemeralEmbed(i, e, comps...)
}

// adminView renders the officer panel from the current settings.
func (b *Bot) adminView() (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	unsettled, _ := b.Raids.Unsettled()
	events, _ := b.Raids.OpenEvents()
	e := &discordgo.MessageEmbed{
		Title:       "FairShare officer panel",
		Color:       0xF9C74F,
		Description: fmt.Sprintf("**Open events** %d   **Unsettled raids** %d   **Dev posting** %s   **Sandbox** %s", len(events), len(unsettled), b.Store.ConfigString("devPosting"), onOff(b.Store.IsSandbox())),
	}
	if b.Store.IsSandbox() {
		e.Title = "FairShare officer panel  [SANDBOX]"
		e.Color = 0x57C785
	}
	if b.channel("log") == "" {
		e.Footer = &discordgo.MessageEmbedFooter{Text: "Run Setup server first to create roles and channels."}
	}
	return e, []discordgo.MessageComponent{
		row(button("Setup server", "fs:setup", discordgo.PrimaryButton), button("Create raid event", "fs:event-new", discordgo.PrimaryButton), button("Edit event", "fs:event-edit", discordgo.SecondaryButton), button("Cancel event", "fs:event-cancel", discordgo.SecondaryButton)),
		row(button("Open raid", "fs:open", discordgo.SuccessButton), button("Settle / override", "fs:settle", discordgo.SecondaryButton), button("Post ledger", "fs:ledger-post", discordgo.SecondaryButton)),
		row(button("Ban", "fs:ban", discordgo.DangerButton), button("Unban", "fs:unban", discordgo.SecondaryButton), button("Announce", "fs:announce", discordgo.SecondaryButton), button("Website", "fs:website", discordgo.SecondaryButton), button("Raid leaders", "fs:leaders", discordgo.SecondaryButton)),
		row(button("Split", "fs:economy", discordgo.SecondaryButton), button("Rules", "fs:rules", discordgo.SecondaryButton), button("Floors", "fs:floors", discordgo.SecondaryButton), button("Settlement", "fs:settlement", discordgo.SecondaryButton), button("Toggle dev posting", "fs:devposting", discordgo.SecondaryButton)),
		row(button(partyLabel(b.Store.ConfigString("allowParty")), "fs:allowparty", discordgo.SecondaryButton), button(sandboxLabel(b.Store.IsSandbox()), "fs:sandbox", discordgo.SecondaryButton), button("Reset sandbox", "fs:sandbox-reset", discordgo.DangerButton), button("Timezone: "+b.Store.ConfigString("timezone"), "fs:timezone", discordgo.SecondaryButton),
			discordgo.Button{Label: "Dev: test snapshot", CustomID: "fs:dev-snapshot", Style: discordgo.SecondaryButton, Disabled: !b.Store.IsSandbox()}),
	}
}

// devSnapshot opens a sandbox test raid with rows for the officer's characters and the
// addon's virtual raiders, and hands back the paste string right here, so the whole
// addon -> export -> upload loop can be rehearsed without an event or a second uploader.
func (b *Bot) devSnapshot(i *discordgo.InteractionCreate) {
	if !b.Store.IsSandbox() {
		b.ephemeral(i, "Enter the sandbox first; test snapshots never touch live data.")
		return
	}
	u := userOf(i)
	_ = b.People.Ensure(u.ID, displayName(i))
	raid, err := b.Raids.OpenTestRaid(u.ID)
	if err != nil {
		b.ephemeral(i, "Could not open a test raid: "+err.Error())
		return
	}
	paste := snapshot.Paste(raid.SnapshotText)
	short := snapshot.Short(raid.SnapshotChecksum)
	b.devPost(fmt.Sprintf("Test raid **%d** opened by <@%s>, checksum **%s**. Paste string attached.", raid.ID, u.ID, short), nil)
	if ch := b.channel("dev"); ch != "" {
		_, _ = b.send(ch, &discordgo.MessageSend{
			Files: []*discordgo.File{{Name: fmt.Sprintf("fairshare-test-snapshot-%d.txt", raid.ID), ContentType: "text/plain", Reader: bytes.NewBufferString(paste)}},
		})
	}
	msg := fmt.Sprintf("Test raid **%d** opened, checksum **%s**. In the addon: Leader → Import, paste this, then Dev → Add 5 raiders, Broadcast, run auctions, Export, and `/fairshare-upload` the result. One upload settles it in the sandbox.\n", raid.ID, short)
	if len(msg)+len(paste)+8 <= 1990 {
		msg += "```\n" + paste + "\n```"
	} else {
		msg += "The string is too long for a message; download it from the file posted in #fairshare-dev."
	}
	b.ephemeral(i, msg)
}

func onOff(on bool) string {
	if on {
		return "ON"
	}
	return "off"
}

// partyLabel names the 5-player switch by its current state.
func partyLabel(v string) string {
	if strings.EqualFold(v, "true") {
		return "5-player groups: on"
	}
	return "5-player groups: off"
}

func sandboxLabel(on bool) string {
	if on {
		return "Leave sandbox (go live)"
	}
	return "Enter sandbox"
}

// toggleSandbox switches between the live database and the sandbox copy. Every post
// while sandboxed goes to #fairshare-dev; the live ledger and channels are untouched.
func (b *Bot) toggleSandbox(i *discordgo.InteractionCreate) {
	on := !b.Store.IsSandbox()
	if err := b.Store.SetSandbox(on); err != nil {
		b.ephemeral(i, "Could not switch: "+err.Error())
		return
	}
	if on {
		b.devPost(fmt.Sprintf("Sandbox **on** by <@%s>: all posts come here, data goes to `%s`.", userOf(i).ID, b.Store.SandboxPath()), nil)
		b.adminDone(i, "Sandbox is ON. Raids, signups, uploads and ledger changes now use the sandbox database and post only to #fairshare-dev. Server changes (bans, roles, channels, pins) are reported there instead of made.")
	} else {
		b.devPost(fmt.Sprintf("Sandbox **off** by <@%s>: back to the live database.", userOf(i).ID), nil)
		b.adminDone(i, "Sandbox is off. Back to the live database and the public channels.")
	}
	b.logAction(userOf(i).ID, "sandbox", onOff(on))
}

func (b *Bot) resetSandbox(i *discordgo.InteractionCreate) {
	if !b.Store.IsSandbox() {
		b.ephemeral(i, "Enter the sandbox first; reset only clears sandbox data.")
		return
	}
	if err := b.Store.ResetSandbox(); err != nil {
		b.ephemeral(i, "Could not reset: "+err.Error())
		return
	}
	b.devPost(fmt.Sprintf("Sandbox **reset** by <@%s>: fresh database.", userOf(i).ID), nil)
	b.adminDone(i, "Sandbox reset: empty ledger, no raids, no registrations. Roles and channel ids kept.")
}

// ---------------------------------------------------------------- setup server

// adopt picks the existing object for a key: the stored id if it still exists (a rename
// must not make the bot grab a different role or channel), else the first name match.
// It warns when several objects share the name, because the bot can only use one.
func adopt(kind, key, name string, stored string, existing map[string]string, byName []string) (id string, warn string) {
	if stored != "" {
		if _, ok := existing[stored]; ok {
			return stored, ""
		}
	}
	if len(byName) == 0 {
		return "", ""
	}
	if len(byName) > 1 {
		warn = fmt.Sprintf("%d %ss are named %s; using the first (%s). Delete or rename the others", len(byName), kind, name, byName[0])
	}
	return byName[0], warn
}

// ---------------------------------------------------------------- events

func (b *Bot) pickEvent(i *discordgo.InteractionCreate, action, prompt string) {
	events, _ := b.Raids.OpenEvents()
	var opts []discordgo.SelectMenuOption
	for _, e := range events {
		opts = append(opts, discordgo.SelectMenuOption{Label: truncate(safeText(e.Title), 90), Value: fmt.Sprint(e.ID), Description: truncate(safeText(e.Instance), 90)})
	}
	if action == "open-pick" {
		opts = append(opts, discordgo.SelectMenuOption{Label: "Ad-hoc raid (no event)", Value: "adhoc"})
	}
	if len(opts) == 0 {
		b.ephemeral(i, "No open events.")
		return
	}
	b.ephemeral(i, prompt, row(discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "fs:" + action, Placeholder: "Event", Options: opts}))
}

func (b *Bot) cancelEventPicked(i *discordgo.InteractionCreate) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	id, _ := strconv.ParseInt(vals[0], 10, 64)
	b.confirmCancelEvent(i, id)
}

// ---------------------------------------------------------------- open raid

func (b *Bot) openRaidPicked(i *discordgo.InteractionCreate) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	if vals[0] == "adhoc" {
		b.modal(i, "fsm:open-adhoc", "Ad-hoc raid", text("instance", "Instance", "Molten Core", true, 60))
		return
	}
	id, _ := strconv.ParseInt(vals[0], 10, 64)
	b.openRaid(i, id, "", true)
}

func (b *Bot) submitOpenAdhoc(i *discordgo.InteractionCreate) {
	b.openRaid(i, 0, modalValues(i)["instance"], false)
}

func (b *Bot) openRaid(i *discordgo.InteractionCreate, eventID int64, instance string, isUpdate bool) {
	reply := b.ephemeral
	if isUpdate {
		reply = func(i *discordgo.InteractionCreate, s string, c ...discordgo.MessageComponent) { b.update(i, s, c...) }
	}
	u := userOf(i)
	_ = b.People.Ensure(u.ID, displayName(i))
	raid, err := b.Raids.Open(eventID, instance, u.ID)
	if err != nil {
		if errors.Is(err, raids.ErrUnsettled) {
			reply(i, "Cannot open: "+err.Error()+". Settle or void it first (Settle / override).")
			return
		}
		reply(i, "Could not open the raid: "+err.Error())
		return
	}
	short := snapshot.Short(raid.SnapshotChecksum)
	ch := b.channel("officers")
	if ch == "" {
		ch = i.ChannelID
	}
	content := fmt.Sprintf("**Raid %d opened** (%s) by <@%s>. Snapshot checksum **%s**.\nLeader: run the sync client, or paste the attached file into the addon's Leader panel. The addon shows the same 8 characters.",
		raid.ID, safeText(raid.Instance), u.ID, short)
	_, err = b.send(ch, &discordgo.MessageSend{
		Content: content,
		Files:   []*discordgo.File{{Name: fmt.Sprintf("fairshare-snapshot-%d.txt", raid.ID), ContentType: "text/plain", Reader: bytes.NewBufferString(snapshot.Paste(raid.SnapshotText))}},
	})
	if err != nil {
		reply(i, fmt.Sprintf("Raid %d opened (checksum %s) but the snapshot could not be posted: %v", raid.ID, short, err))
		return
	}
	b.logAction(u.ID, "raid_open", fmt.Sprintf("raid %d %s checksum %s", raid.ID, safeText(raid.Instance), short))
	reply(i, fmt.Sprintf("Raid %d opened. Snapshot posted in <#%s>, checksum **%s**.", raid.ID, ch, short))
	if eventID != 0 {
		if ev, err := b.Raids.Event(eventID); err == nil {
			b.refreshEvent(ev.ID)
			b.spawnNext(ev)
		}
	}
}

// ---------------------------------------------------------------- settle / override

func (b *Bot) pickRaid(i *discordgo.InteractionCreate) {
	rs, _ := b.Raids.Unsettled()
	if len(rs) == 0 {
		b.ephemeral(i, "No unsettled raids.")
		return
	}
	var opts []discordgo.SelectMenuOption
	for _, r := range rs {
		opts = append(opts, discordgo.SelectMenuOption{Label: fmt.Sprintf("Raid %d: %s", r.ID, truncate(safeText(r.Instance), 60)), Value: fmt.Sprint(r.ID), Description: truncate(safeText(r.Status+" "+r.SettleNote), 90)})
	}
	b.ephemeral(i, "Which raid?", row(discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "fs:settle-pick", Placeholder: "Raid", Options: opts}))
}

func (b *Bot) settlePicked(i *discordgo.InteractionCreate) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	b.modal(i, "fsm:settle:"+vals[0], "Settle or void raid "+vals[0],
		text("action", "Action: settle or void", "settle", true, 6),
		paragraph("reason", "Reason (logged)", "Verified both transcripts by hand", true, 400))
}

func (b *Bot) submitSettle(i *discordgo.InteractionCreate, args []string) {
	if len(args) == 0 {
		return
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	v := modalValues(i)
	action := strings.ToLower(v["action"])
	out, err := b.Raids.Override(id, userOf(i).ID, action, v["reason"])
	if err != nil {
		b.ephemeral(i, "Override failed: "+err.Error())
		return
	}
	b.logAction(userOf(i).ID, "raid_override", fmt.Sprintf("raid %d %s: %s", id, action, safeText(v["reason"])))
	b.ephemeral(i, out.Message)
	if out.Status == "settled" {
		b.announceSettle(id, out)
	}
}

// ---------------------------------------------------------------- bans
//
// Ban → pick member → reason (modal) → confirmation card showing who, why and what will
// be recorded → Confirm. The guards run at the pick and again at the confirm, because
// roles can change in between and the confirm is what acts.

type banDraft struct {
	Target string
	Reason string
}

// banGuard says why this officer may not ban this target: "" when they may.
func (b *Bot) banGuard(i *discordgo.InteractionCreate, target string) string {
	if target == userOf(i).ID {
		return "You cannot ban yourself."
	}
	why, err := b.memberPrivilege(target)
	if err != nil {
		// Not a member any more (left the server): the ban can still be recorded so
		// their identities stay blocked, and the Discord ban will simply apply on return.
		return ""
	}
	if why != "" {
		return "Cannot ban that member: " + why + ". Remove their role first if you really mean it."
	}
	return ""
}

func (b *Bot) banPicked(i *discordgo.InteractionCreate) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	if why := b.banGuard(i, vals[0]); why != "" {
		b.updateClear(i, why)
		return
	}
	b.modal(i, "fsm:ban:"+vals[0], "Ban reason", paragraph("reason", "Reason", "Traded a won item for gold", true, 400))
}

// submitBan takes the reason and asks for confirmation; nothing is recorded yet.
func (b *Bot) submitBan(i *discordgo.InteractionCreate, args []string) {
	if len(args) == 0 {
		return
	}
	target := args[0]
	reason := modalValues(i)["reason"]
	if why := b.banGuard(i, target); why != "" {
		b.ephemeral(i, why)
		return
	}
	setPending("ban", userOf(i).ID, &banDraft{Target: target, Reason: reason})
	var ids []string
	if p, err := b.People.Get(target); err == nil {
		if p.BattlenetTag != "" {
			ids = append(ids, "Battle.net tag")
		}
	}
	if chars, _ := b.People.Characters(target); len(chars) > 0 {
		names := make([]string, len(chars))
		for k, c := range chars {
			names[k] = c.Name
		}
		ids = append(ids, "characters "+strings.Join(names, ", "))
	}
	what := "their Discord account"
	if len(ids) > 0 {
		what += ", " + strings.Join(ids, ", ")
	}
	b.ephemeral(i, fmt.Sprintf("Ban <@%s>?\nReason: %s\nThis bans them from the server, revokes their sync token and addon key, and blocks %s from ever registering again. It is logged in <#%s>.",
		target, safeText(reason), what, b.channel("log")),
		row(button("Confirm ban", "fs:ban-confirm:"+target, discordgo.DangerButton), button("Cancel", "fs:ban-cancel", discordgo.SecondaryButton)))
}

// confirmBan records the ban (source of truth), revokes access, then asks Discord for the
// server ban. A Discord refusal does not undo the record; it is reported to the officer
// and the log channel so someone fixes the bot's role position and bans by hand.
func (b *Bot) confirmBan(i *discordgo.InteractionCreate, args []string) {
	u := userOf(i)
	d, ok := getPending[*banDraft]("ban", u.ID)
	if !ok || len(args) == 0 || d.Target != args[0] {
		b.updateClear(i, "That ban request expired. Start again from the officer panel.")
		return
	}
	delPending("ban", u.ID)
	if why := b.banGuard(i, d.Target); why != "" {
		b.updateClear(i, why)
		return
	}
	ban, err := b.Bans.Issue(d.Target, d.Reason, u.ID)
	if err != nil {
		b.updateClear(i, "Ban failed: "+err.Error())
		return
	}
	if err := b.banMember(d.Target, d.Reason); err != nil {
		b.logAction(u.ID, "ban", fmt.Sprintf("<@%s>: %s (%d identities recorded) **but Discord refused the server ban: %v**", d.Target, safeText(d.Reason), len(ban.Identities), err))
		b.updateClear(i, fmt.Sprintf("Recorded ban %d (%d identities, tokens revoked) but Discord refused the server ban: %v. Check the bot's role position, then ban them from the server by hand.", ban.ID, len(ban.Identities), err))
		return
	}
	b.logAction(u.ID, "ban", fmt.Sprintf("<@%s>: %s (%d identities recorded)", d.Target, safeText(d.Reason), len(ban.Identities)))
	b.updateClear(i, fmt.Sprintf("Banned <@%s>. %d linked identities will be refused if they ever try to register again.", d.Target, len(ban.Identities)))
}

func (b *Bot) pickBan(i *discordgo.InteractionCreate) {
	bl, _ := b.Bans.Active()
	if len(bl) == 0 {
		b.ephemeral(i, "No active bans.")
		return
	}
	var opts []discordgo.SelectMenuOption
	for _, x := range bl {
		p, _ := b.People.Get(x.PersonID)
		label := p.DisplayName
		if label == "" {
			label = x.PersonID
		}
		opts = append(opts, discordgo.SelectMenuOption{Label: truncate(safeText(label), 90), Value: fmt.Sprint(x.ID), Description: truncate(safeText(x.Reason), 90)})
	}
	b.ephemeral(i, "Lift which ban?", row(discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "fs:unban-pick", Placeholder: "Ban", Options: opts}))
}

func (b *Bot) submitUnban(i *discordgo.InteractionCreate, args []string) {
	if len(args) == 0 {
		return
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	reason := modalValues(i)["reason"]
	var person string
	_ = b.Store.DB.QueryRow(`SELECT person_id FROM ban WHERE id = ?`, id).Scan(&person)
	if err := b.Bans.Lift(id, userOf(i).ID, reason); err != nil {
		b.ephemeral(i, "Unban failed: "+err.Error())
		return
	}
	note := ""
	if person != "" {
		if err := b.unbanMember(person); err != nil {
			note = fmt.Sprintf(" Discord would not lift the server ban (%v); do it by hand.", err)
		}
	}
	b.logAction(userOf(i).ID, "unban", fmt.Sprintf("<@%s>: %s%s", person, safeText(reason), note))
	b.ephemeral(i, "Ban lifted."+note)
}

// ---------------------------------------------------------------- announcements

func (b *Bot) submitAnnounce(i *discordgo.InteractionCreate) {
	v := modalValues(i)
	chName := strings.TrimPrefix(strings.TrimSpace(v["channel"]), "#")
	chID := ""
	if id := b.channel(chName); id != "" {
		chID = id
	} else if chans, err := b.S.GuildChannels(b.guildID); err == nil {
		for _, c := range chans {
			if c.Type == discordgo.ChannelTypeGuildText && strings.EqualFold(c.Name, chName) {
				chID = c.ID
			}
		}
	}
	if chID == "" {
		b.ephemeral(i, "No channel named #"+safeText(chName)+".")
		return
	}
	if v["when"] != "" {
		at, err := parseTime(v["when"])
		if err != nil {
			b.ephemeral(i, err.Error())
			return
		}
		if _, err := b.Store.DB.Exec(`INSERT INTO scheduled_announcement (channel_id, body, post_at, created_by) VALUES (?, ?, ?, ?)`, chID, v["body"], at, userOf(i).ID); err != nil {
			b.ephemeral(i, "Could not schedule: "+err.Error())
			return
		}
		b.logAction(userOf(i).ID, "announce_scheduled", fmt.Sprintf("<#%s> at %s", chID, fmtTime(at)))
		b.ephemeral(i, fmt.Sprintf("Scheduled for %s in <#%s>.", fmtTime(at), chID))
		return
	}
	// Announcements are officer-written and pinging is their point, so mentions stay on.
	if _, err := b.send(chID, &discordgo.MessageSend{Content: v["body"], AllowedMentions: announceMentions}); err != nil {
		b.ephemeral(i, "Could not post: "+err.Error())
		return
	}
	b.logAction(userOf(i).ID, "announce", fmt.Sprintf("<#%s>", chID))
	b.adminDone(i, "Posted.")
}

// ---------------------------------------------------------------- config

// floorKeys maps the Floors modal fields to floor_price slots. DEFAULT applies to every
// item whose equip location has no row of its own.
var floorKeys = map[string]string{"floorDefault": "DEFAULT", "floor2H": "INVTYPE_2HWEAPON"}

// floorValue is the stored floor for a slot, or fallback when the row is missing.
func (b *Bot) floorValue(slot, fallback string) string {
	var v int64
	if err := b.Store.DB.QueryRow(`SELECT value FROM floor_price WHERE slot = ?`, slot).Scan(&v); err != nil {
		return fallback
	}
	return fmt.Sprint(v)
}

func (b *Bot) submitConfig(i *discordgo.InteractionCreate, keys []string) {
	v := modalValues(i)
	var changed []string
	for _, k := range keys {
		n, err := strconv.ParseInt(v[k], 10, 64)
		if err != nil || n < 0 {
			b.ephemeral(i, k+" must be a non-negative whole number.")
			return
		}
		if slot, ok := floorKeys[k]; ok {
			_, _ = b.Store.DB.Exec(`INSERT INTO floor_price (slot, value) VALUES (?, ?) ON CONFLICT(slot) DO UPDATE SET value = excluded.value`, slot, n)
		} else {
			_ = b.Store.SetConfig(k, fmt.Sprint(n))
		}
		changed = append(changed, fmt.Sprintf("%s=%d", k, n))
	}
	b.logAction(userOf(i).ID, "config", strings.Join(changed, " "))
	b.adminDone(i, "Saved: "+strings.Join(changed, ", "))
}

// submitWebsite stores the links the public website shows. Only a Discord invite is
// accepted, so the site can never be made to point anywhere else.
func (b *Bot) submitWebsite(i *discordgo.InteractionCreate) {
	v := modalValues(i)
	invite := strings.TrimSpace(v["discordInvite"])
	site := strings.TrimRight(strings.TrimSpace(v["websiteURL"]), "/")
	if site != "" && (!strings.HasPrefix(site, "https://") || strings.ContainsAny(site, " <>\"'`")) {
		b.ephemeral(i, "The website must be an https:// address, for example https://fairsharewow.com.")
		return
	}
	if site != "" {
		_ = b.Store.SetConfig("websiteURL", site)
	}
	if !store.ValidDiscordInvite(invite) {
		b.ephemeral(i, "That is not a Discord invite link. Use https://discord.gg/<code> or https://discord.com/invite/<code>, or leave it empty to hide the link.")
		return
	}
	_ = b.Store.SetConfig("discordInvite", invite)
	b.logAction(userOf(i).ID, "config", "discordInvite="+invite)
	if invite == "" {
		b.adminDone(i, "Discord link removed from the website.")
		return
	}
	b.adminDone(i, "The website now links to "+invite+" in its header, footer and How it works page.")
}

// ---------------------------------------------------------------- ledger post (dev)

func (b *Bot) postLedger(i *discordgo.InteractionCreate) {
	table := b.standingsTable(50)
	if b.channel("dev") == "" {
		b.ephemeral(i, "No #fairshare-dev channel yet. Run Setup server.")
		return
	}
	b.devPost("**Current standings** (requested by <@"+userOf(i).ID+">)\n"+table, nil)
	b.ephemeral(i, "Posted to <#"+b.channel("dev")+">.")
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n-3] + "..."
	}
	return s
}
