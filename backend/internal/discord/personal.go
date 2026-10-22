package discord

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/verify"
)

// personalView renders a member's panel: a short status plus every member action.
func (b *Bot) personalView(i *discordgo.InteractionCreate) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	u := userOf(i)
	e := &discordgo.MessageEmbed{Title: "FairShare", Color: 0xF9C74F}
	p, err := b.People.Get(u.ID)
	chars, _ := b.People.Characters(u.ID)
	switch {
	case err != nil || len(chars) == 0:
		e.Description = "You are not registered yet. Register a character to join raids and bid."
	default:
		names := make([]string, len(chars))
		for k, c := range chars {
			names[k] = c.Name
			if c.Class != "" {
				names[k] += " (" + c.Class + ")"
			}
		}
		e.Description = fmt.Sprintf("**Balance** %d   **Credit** %d   **Max bid** %d\n**Raids** %d   **Boss kills** %d\n**Characters** %s",
			p.Balance, b.People.Credit(p), p.Balance+b.People.Credit(p), p.RaidsAttended, p.LifetimeShares, strings.Join(names, ", "))
		e.Description += fmt.Sprintf("\n**Your page** %s/players/%s", b.site(), url.PathEscape(chars[0].Name))
		switch {
		case p.BattlenetVerifiedAt > 0:
			e.Footer = &discordgo.MessageEmbedFooter{Text: "Battle.net: verified through Discord."}
		case p.BattlenetTag != "":
			e.Footer = &discordgo.MessageEmbedFooter{Text: "Battle.net: typed, not verified. Verify Battle.net confirms it through Discord in two clicks."}
		default:
			e.Footer = &discordgo.MessageEmbedFooter{Text: "Battle.net: not linked. Optional; Verify Battle.net confirms it through Discord in two clicks."}
		}
	}
	comps := []discordgo.MessageComponent{
		b.registerRow(u.ID),
		row(button("Balance", "fs:balance", discordgo.SecondaryButton), button("Ledger", "fs:ledger", discordgo.SecondaryButton), button("Standings", "fs:standings", discordgo.SecondaryButton)),
		row(button("Sync client token", "fs:token", discordgo.SecondaryButton), button("Addon key", "fs:addonkey", discordgo.SecondaryButton), button("Finish raid", "fs:finish", discordgo.SuccessButton)),
	}
	if b.isLeader(i) {
		e.Description += "\n\n**Raid leader.** Create raid event, Edit event and Open raid below."
		comps = append(comps, leaderRow())
	}
	return e, comps
}

func (b *Bot) submitBattlenet(i *discordgo.InteractionCreate) {
	v := modalValues(i)
	u := userOf(i)
	if err := b.People.LinkBattlenet(u.ID, displayName(i), v["tag"]); err != nil {
		if errors.Is(err, people.ErrBanned) {
			b.ephemeral(i, "That Battle.net tag cannot be linked. Contact an officer.")
			b.logAction(u.ID, "battlenet_refused", "banned identity")
			return
		}
		b.ephemeral(i, "Could not link: "+err.Error())
		return
	}
	b.personalDone(i, "Battle.net tag linked.")
}

func (b *Bot) showBalance(i *discordgo.InteractionCreate) {
	p, err := b.People.Get(userOf(i).ID)
	if err != nil {
		b.ephemeral(i, "You are not registered yet.")
		return
	}
	b.ephemeral(i, fmt.Sprintf("You have **%d shares** and **%d** credit, so you can bid up to **%d shares**.", p.Balance, b.People.Credit(p), p.Balance+b.People.Credit(p)))
}

func (b *Bot) showLedger(i *discordgo.InteractionCreate) {
	lines, err := b.People.Ledger(userOf(i).ID, 15)
	if err != nil || len(lines) == 0 {
		b.ephemeral(i, "No ledger entries yet.")
		return
	}
	rows := make([][]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, []string{fmt.Sprint(l.RaidID), l.Reason, safeText(l.Character), l.AuctionID, fmt.Sprint(l.Delta), safeText(l.Note)})
	}
	b.ephemeral(i, "Your last ledger entries, in shares:\n"+codeTable([]string{"Raid", "Reason", "Character", "Auction", "Shares", "Note"}, rows))
}

func (b *Bot) showStandings(i *discordgo.InteractionCreate) {
	b.ephemeral(i, b.standingsTable(20))
}

func (b *Bot) standingsTable(limit int) string {
	ps, err := b.People.Standings(limit)
	if err != nil || len(ps) == 0 {
		return "No one is registered yet."
	}
	rows := make([][]string, 0, len(ps))
	for _, p := range ps {
		rows = append(rows, []string{safeText(p.DisplayName), fmt.Sprint(p.Balance), fmt.Sprint(b.People.Credit(p)), fmt.Sprint(p.RaidsAttended), fmt.Sprint(p.LifetimeShares)})
	}
	return codeTable([]string{"Member", "Shares", "Credit", "Raids", "Kills"}, rows)
}

func (b *Bot) issueToken(i *discordgo.InteractionCreate) {
	tok, err := b.People.IssueToken(userOf(i).ID)
	if err != nil {
		b.ephemeral(i, "Register a character first; tokens are only issued to registered raiders.")
		return
	}
	b.ephemeral(i, "Your sync client token (shown once; generating a new one replaces it):\n```\n"+tok+"\n```\n"+
		"Paste it into the sync client. It lets that program act as you on the FairShare API: fetch the snapshot of a raid you lead, send live updates and finish that raid, and upload transcripts of raids you attended. "+
		"It cannot read anyone else's data or change settings. Keep it private; if it leaks, press this button again and the old one stops working.")
}

// issueAddonKey hands the member a fresh 32-hex key for the addon's Settings → Addon key
// field. The addon signs bids with it so no other client can forge a commit in their
// name; issuing a new one invalidates the old.
func (b *Bot) issueAddonKey(i *discordgo.InteractionCreate) {
	u := userOf(i)
	if chars, _ := b.People.Characters(u.ID); len(chars) == 0 {
		b.ephemeral(i, "Register a character first; addon keys are only issued to registered raiders.")
		return
	}
	key, err := b.People.IssueAddonKey(u.ID)
	if err != nil {
		b.ephemeral(i, "Could not issue an addon key: "+err.Error())
		return
	}
	b.ephemeral(i, "Your addon key (shown once):\n```\n"+key+"\n```\nPaste this into FairShare → Settings → Addon key in game. It signs your bids so nobody can forge them. Issue a new one if you ever share it.")
}

// registerRow is the panel's first row: Register, then Verify Battle.net (a link to the
// website's Discord login, bound to this member for fifteen minutes) when the client
// secret is configured, else the typed fallback.
func (b *Bot) registerRow(userID string) discordgo.ActionsRow {
	reg := button("Register character", "fs:register", discordgo.PrimaryButton)
	if b.Cfg.DiscordClientSecret == "" {
		return row(reg, button("Link Battle.net", "fs:battlenet", discordgo.SecondaryButton))
	}
	state := verify.NewSigner(b.Cfg.DiscordClientSecret).State(userID, time.Now())
	return row(reg, discordgo.Button{Label: "Verify Battle.net", Style: discordgo.LinkButton, URL: b.site() + "/verify/battlenet?s=" + url.QueryEscape(state)},
		button("Type Battle.net tag", "fs:battlenet", discordgo.SecondaryButton))
}
