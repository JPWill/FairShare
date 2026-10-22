package discord

import (
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// commands are the three slash entry points. Discord does not allow a bare command to
// also carry subcommands, so the admin and upload entry points are separate commands.
// None of them work in DMs (a DM has no member roles to check), and the admin panel is
// hidden from everyone without Manage Server; the bot still checks isOfficer itself.
func commands() []*discordgo.ApplicationCommand {
	noDM := false
	manageGuild := int64(discordgo.PermissionManageGuild)
	return []*discordgo.ApplicationCommand{
		{Name: "fairshare", Description: "Open your FairShare panel", DMPermission: &noDM},
		{Name: "fairshare-admin", Description: "Open the FairShare officer panel", DMPermission: &noDM, DefaultMemberPermissions: &manageGuild},
		{
			Name:         "fairshare-upload",
			Description:  "Upload a raid transcript from the addon: paste the string, or attach it as a file",
			DMPermission: &noDM,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionAttachment, Name: "file", Description: "Optional: the exported transcript saved as a .txt (for very long transcripts)", Required: false},
			},
		},
	}
}

// onInteraction routes every interaction by kind and custom id prefix. Anything from a
// server other than the home guild is refused; a DM may only run the register flow.
func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("interaction panic: %v", r)
		}
	}()
	if b.foreignGuild(i) {
		b.ephemeral(i, "This bot only serves its home server.")
		return
	}
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		if i.GuildID == "" {
			b.ephemeral(i, "Use this command in the server.")
			return
		}
		switch i.ApplicationCommandData().Name {
		case "fairshare":
			b.personalPanel(i)
		case "fairshare-admin":
			b.adminPanel(i)
		case "fairshare-upload":
			b.handleUpload(i)
		}
	case discordgo.InteractionMessageComponent:
		id := i.MessageComponentData().CustomID
		parts := strings.Split(id, ":")
		if len(parts) < 2 || parts[0] != "fs" {
			return
		}
		if i.GuildID == "" && !dmActions[parts[1]] {
			b.ephemeral(i, "Use this in the server.")
			return
		}
		b.component(i, parts[1], parts[2:])
	case discordgo.InteractionModalSubmit:
		id := i.ModalSubmitData().CustomID
		parts := strings.Split(id, ":")
		if len(parts) < 2 || parts[0] != "fsm" {
			return
		}
		if i.GuildID == "" && !dmActions[parts[1]] {
			b.ephemeral(i, "Use this in the server.")
			return
		}
		b.modalSubmit(i, parts[1], parts[2:])
	}
}

func (b *Bot) component(i *discordgo.InteractionCreate, action string, args []string) {
	// Member actions
	switch action {
	case "register":
		b.startRegister(i)
		return
	case "reg-class":
		b.pickRegClass(i)
		return
	case "reg-roles":
		b.pickRegRoles(i)
		return
	case "howitworks":
		b.howItWorks(i)
		return
	case "accept":
		b.acceptRules(i)
		return
	case "su-char":
		b.pickSignupChar(i, args)
		return
	case "su-role":
		b.pickSignupRole(i, args)
		return
	case "battlenet":
		b.modal(i, "fsm:battlenet", "Link your Battle.net tag",
			text("tag", "Battle tag", "Name#1234", true, 40))
		return
	case "balance":
		b.showBalance(i)
		return
	case "ledger":
		b.showLedger(i)
		return
	case "standings":
		b.showStandings(i)
		return
	case "token":
		b.issueToken(i)
		return
	case "addonkey":
		b.issueAddonKey(i)
		return
	case "finish":
		b.finishRaid(i)
		return
	case "signup":
		b.handleSignup(i, args)
		return
	}
	// Raid leader actions (leaders.go), then officer actions.
	if leaderActions[action] {
		if !b.isLeader(i) {
			b.ephemeral(i, "Raid leaders and officers only.")
			return
		}
	} else if !b.isOfficer(i) {
		b.ephemeral(i, "Officers only.")
		return
	}
	switch action {
	case "roster":
		b.startRoster(i, args)
	case "roster-pick":
		b.pickRoster(i, args)
	case "roster-page":
		b.rosterTurnPage(i, args)
	case "roster-confirm":
		b.confirmRoster(i, args)
	case "setup":
		b.setupServer(i)
	case "event-new":
		b.startEvent(i)
	case "ev-d", "ev-pg", "ev-rep", "ev-tp", "ev-back", "ev-t":
		b.eventGridAction(i, action, args)
	case "timezone":
		b.modal(i, "fsm:timezone", "Raid timezone",
			text("timezone", "IANA timezone name", b.Store.ConfigString("timezone"), true, 40))
	case "website":
		b.modal(i, "fsm:website", "Website links",
			text("discordInvite", "Discord invite link (empty hides it)", b.Store.ConfigString("discordInvite"), false, 100),
			text("websiteURL", "Website address", b.Store.ConfigString("websiteURL"), false, 100))
	case "event-cancel":
		b.pickEvent(i, "event-cancel-pick", "Cancel which event?")
	case "event-cancel-pick":
		b.cancelEventPicked(i)
	case "event-edit":
		b.pickEvent(i, "event-edit-pick", "Edit which event?")
	case "event-edit-pick":
		b.editEventPicked(i)
	case "event-edit-pick-keep":
		b.editEventPanel(i, eventIDArg(args), true)
	case "ev-edit-time":
		b.startEditTime(i, eventIDArg(args))
	case "ev-edit-details":
		b.editEventDetails(i, eventIDArg(args))
	case "ev-edit-cancel":
		b.confirmCancelEvent(i, eventIDArg(args))
	case "ev-edit-cancel-yes":
		b.cancelEvent(i, eventIDArg(args))
	case "open":
		b.pickEvent(i, "open-pick", "Open which raid? (or ad-hoc)")
	case "open-pick":
		b.openRaidPicked(i)
	case "settle":
		b.pickRaid(i)
	case "settle-pick":
		b.settlePicked(i)
	case "ban":
		b.ephemeral(i, "Pick the member to ban.", row(discordgo.SelectMenu{MenuType: discordgo.UserSelectMenu, CustomID: "fs:ban-pick", Placeholder: "Member"}))
	case "ban-pick":
		b.banPicked(i)
	case "ban-confirm":
		b.confirmBan(i, args)
	case "ban-cancel":
		delPending("ban", userOf(i).ID)
		b.updateClear(i, "Ban cancelled; nothing was recorded.")
	case "unban":
		b.pickBan(i)
	case "unban-pick":
		vals := i.MessageComponentData().Values
		if len(vals) == 0 {
			return
		}
		b.modal(i, "fsm:unban:"+vals[0], "Lift ban", text("reason", "Reason", "Appeal accepted", true, 200))
	case "announce":
		b.modal(i, "fsm:announce", "Announcement",
			text("channel", "Channel", "announcements", true, 40),
			paragraph("body", "Message", "", true, 1800),
			text("when", "Post at (UTC, optional) YYYY-MM-DD HH:MM", "", false, 20))
	case "economy":
		b.modal(i, "fsm:economy", "Split settings",
			text("poolPct", "Performance pool %", b.Store.ConfigString("poolPct"), true, 3),
			text("newcomerCredit", "Newcomer credit", b.Store.ConfigString("newcomerCredit"), true, 6),
			text("creditPerRaid", "Credit per raid attended", b.Store.ConfigString("creditPerRaid"), true, 6),
			text("creditCap", "Credit cap", b.Store.ConfigString("creditCap"), true, 6),
			text("norevealPenalty", "Non-reveal penalty", b.Store.ConfigString("norevealPenalty"), true, 6))
	case "rules":
		b.modal(i, "fsm:rules", "Rules settings",
			text("offspecPct", "Off-spec floor discount %", b.Store.ConfigString("offspecPct"), true, 3),
			text("minAccountAgeDays", "Min Discord account age (days)", b.Store.ConfigString("minAccountAgeDays"), true, 4),
			text("sharePerKill", "Boss-kill points per kill", b.Store.ConfigString("sharePerKill"), true, 4),
			text("fullRunBonus", "Boss-kill bonus for a full run", b.Store.ConfigString("fullRunBonus"), true, 4))
	case "floors":
		b.modal(i, "fsm:floors", "Floor prices (shares)",
			text("floorDefault", "Every item", b.floorValue("DEFAULT", "10"), true, 6),
			text("floor2H", "Two-handed weapons", b.floorValue("INVTYPE_2HWEAPON", "20"), true, 6))
	case "settlement":
		b.modal(i, "fsm:settlement", "Settlement settings",
			text("requiredUploads", "Uploads needed to settle (1 = leader only)", b.Store.ConfigString("requiredUploads"), true, 2),
			text("freePasses", "Free passes for a missed reveal", b.Store.ConfigString("freePasses"), true, 2))
	case "ledger-post":
		b.postLedger(i)
	case "devposting":
		cur := strings.ToLower(b.Store.ConfigString("devPosting")) != "false"
		if cur {
			_ = b.Store.SetConfig("devPosting", "false")
		} else {
			_ = b.Store.SetConfig("devPosting", "true")
		}
		b.logAction(userOf(i).ID, "devposting", map[bool]string{true: "off", false: "on"}[cur])
		b.adminDone(i, "Dev posting is now "+map[bool]string{true: "off", false: "on"}[cur]+".")
	case "allowparty":
		on := !strings.EqualFold(b.Store.ConfigString("allowParty"), "true")
		_ = b.Store.SetConfig("allowParty", map[bool]string{true: "true", false: "false"}[on])
		b.logAction(userOf(i).ID, "allowparty", onOff(on))
		if on {
			b.adminDone(i, "5-player groups are now **on**. Raids opened from now on count a 5-player party as the raid group, so FairShare can be tested in dungeons. Raids already open keep what they were opened with. Turn this off before real raids start.")
		} else {
			b.adminDone(i, "5-player groups are now **off**. Raids opened from now on run only in raid groups; a 5-player party is ignored.")
		}
	case "sandbox":
		b.toggleSandbox(i)
	case "sandbox-reset":
		b.resetSandbox(i)
	case "dev-snapshot":
		b.devSnapshot(i)
	case "leaders":
		b.pickLeader(i)
	case "leader-pick":
		b.toggleLeader(i)
	default:
		b.ephemeral(i, "Unknown action.")
	}
}

func (b *Bot) modalSubmit(i *discordgo.InteractionCreate, action string, args []string) {
	switch action {
	case "register":
		b.submitRegisterName(i)
		return
	case "battlenet":
		b.submitBattlenet(i)
		return
	case "upload":
		// Any member may upload a witness transcript; raids.Upload decides whether this
		// person may vouch for this raid.
		b.submitUploadText(i)
		return
	}
	if leaderModals[action] {
		if !b.isLeader(i) {
			b.ephemeral(i, "Raid leaders and officers only.")
			return
		}
	} else if !b.isOfficer(i) {
		b.ephemeral(i, "Officers only.")
		return
	}
	switch action {
	case "event-new":
		b.submitEvent(i)
	case "event-edit":
		b.submitEventEdit(i, args)
	case "open-adhoc":
		b.submitOpenAdhoc(i)
	case "settle":
		b.submitSettle(i, args)
	case "ban":
		b.submitBan(i, args)
	case "unban":
		b.submitUnban(i, args)
	case "announce":
		b.submitAnnounce(i)
	case "timezone":
		b.submitTimezone(i)
	case "website":
		b.submitWebsite(i)
	case "economy":
		b.submitConfig(i, []string{"poolPct", "newcomerCredit", "creditPerRaid", "creditCap", "norevealPenalty"})
	case "rules":
		b.submitConfig(i, []string{"offspecPct", "minAccountAgeDays", "sharePerKill", "fullRunBonus"})
	case "floors":
		b.submitConfig(i, []string{"floorDefault", "floor2H"})
	case "settlement":
		b.submitConfig(i, []string{"requiredUploads", "freePasses"})
	}
}
