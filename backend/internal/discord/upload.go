package discord

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/replay"
)

// handleUpload is /fairshare-upload file:<attachment>.
func (b *Bot) handleUpload(i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	var attID string
	for _, o := range data.Options {
		if o.Name == "file" {
			attID = o.Value.(string)
		}
	}
	var att *discordgo.MessageAttachment
	if data.Resolved != nil && attID != "" {
		att = data.Resolved.Attachments[attID]
	}
	if att == nil {
		// No file (Discord then sends no Resolved block at all): take the pasted string.
		b.modal(i, "fsm:upload", "Paste the transcript string",
			paragraph("transcript", "Transcript (from the addon's Export button)", "", true, 4000))
		return
	}
	if att.Size > 4*1024*1024 {
		b.ephemeral(i, "That file is too large for a transcript.")
		return
	}
	b.defer_(i)
	text, err := fetch(att.URL)
	if err != nil {
		b.followup(i, "Could not download the file: "+err.Error())
		return
	}
	b.ingestTranscript(i, text, b.followup)
}

// submitUploadText handles the pasted string from the modal.
func (b *Bot) submitUploadText(i *discordgo.InteractionCreate) {
	text := strings.TrimSpace(modalValues(i)["transcript"])
	// Discord cuts a modal field at 4000 characters; a raid's transcript string is usually
	// longer, so a full field means the end is missing rather than malformed.
	if len(text) >= 4000 {
		b.ephemeral(i, "That string was cut off at Discord's 4000-character limit for text boxes, so the end of the raid is missing. Save the whole string as a .txt file and attach it with `/fairshare-upload file:`, or let the sync client upload it.")
		return
	}
	b.defer_(i)
	b.ingestTranscript(i, text, b.followup)
}

// decodeTranscript accepts the addon's paste string (base64) or the canonical text.
func decodeTranscript(text string) (string, error) {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "FAIRSHARE TRANSCRIPT") {
		return t, nil
	}
	compact := strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, t)
	raw, err := base64.StdEncoding.DecodeString(compact)
	if err != nil {
		return "", fmt.Errorf("not a FairShare transcript string (base64 decode failed: %v)", err)
	}
	if !strings.HasPrefix(string(raw), "FAIRSHARE TRANSCRIPT") {
		return "", errors.New("decoded text is not a FairShare transcript")
	}
	return string(raw), nil
}

// ingestTranscript is the shared path for file and pasted uploads. The uploader must
// already be registered: an upload is a witness statement, and we do not create a person
// row for someone who has never registered just because they pressed Upload.
func (b *Bot) ingestTranscript(i *discordgo.InteractionCreate, text string, reply func(*discordgo.InteractionCreate, string)) {
	u := userOf(i)
	if _, err := b.People.Get(u.ID); err != nil {
		reply(i, "Register a character first; only registered raiders can upload transcripts.")
		return
	}
	text, err := decodeTranscript(text)
	if err != nil {
		reply(i, err.Error()+". If the string was longer than 4000 characters, save it as a .txt and attach it with /fairshare-upload file:, or use the sync client.")
		return
	}
	tr, err := replay.ParseTranscript(text)
	if err != nil {
		reply(i, "That is not a FairShare transcript: "+safeText(err.Error()))
		return
	}
	raidID, err := strconv.ParseInt(tr.RaidID, 10, 64)
	if err != nil {
		reply(i, "Transcript raid id is not a number.")
		return
	}
	out, err := b.Raids.Upload(raidID, u.ID, text)
	if err != nil {
		reply(i, "Upload rejected: "+safeText(err.Error()))
		return
	}
	reply(i, fmt.Sprintf("Raid %d: **%s**. %s", raidID, out.Status, safeText(out.Message)))
	b.logAction(u.ID, "transcript_upload", fmt.Sprintf("raid %d -> %s", raidID, out.Status))
	if out.Status == "contested" {
		b.post("officers", fmt.Sprintf("**Raid %d is contested.** A transcript from <@%s> disagrees with the one it was settled on:\n```\n%s\n```\nSettle / override → void reverses the shares; then settle again from the right transcript.", raidID, u.ID, truncate(safeText(strings.Join(out.Diffs, "\n")), 1500)))
	}
	b.devPostReplay(raidID, u.ID, out)
	if out.Status == "settled" {
		b.announceSettle(raidID, out)
	}
}

func fetch(url string) (string, error) {
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	return string(buf), err
}

// flagSummary counts a report's flags, split into disputing and informational when the
// Report can tell them apart.
func flagSummary(rep *replay.Report) string {
	if rep == nil {
		return ""
	}
	all := rep.AllFlags()
	if len(all) == 0 {
		return "No flags."
	}
	return fmt.Sprintf("%d flags: %d disputing, %d informational.", len(all), len(rep.Disputing()), len(rep.Informational()))
}

// devPostReplay posts the replay summary and ledger deltas to #fairshare-dev. The
// uploader is the one person it is allowed to ping.
func (b *Bot) devPostReplay(raidID int64, by string, out raids.Outcome) {
	if out.Report == nil {
		return
	}
	e := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Raid %d upload: %s", raidID, out.Status),
		Color:       statusColor(out.Status),
		Description: "```\n" + truncate(safeText(replay.Summary(out.Report)), 3500) + "```",
		Footer:      &discordgo.MessageEmbedFooter{Text: flagSummary(out.Report)},
	}
	if len(out.Diffs) > 0 {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Differences between uploads", Value: truncate(safeText(strings.Join(out.Diffs, "\n")), 1000)})
	}
	content := "Uploaded by " + mention(by) + "."
	if len(out.Deltas) > 0 {
		rows := make([][]string, 0, len(out.Deltas))
		for _, d := range out.Deltas {
			rows = append(rows, []string{d.AuctionID, safeText(d.Character), d.Reason, fmt.Sprint(d.Amount)})
		}
		content += "\n**Ledger deltas** (sum " + fmt.Sprint(replay.Sum(out.Deltas)) + ")\n" + codeTable([]string{"Auction", "Character", "Reason", "Delta"}, rows)
	}
	b.devPostMentioning(content, e, []string{by})
}

// announceSettle posts results to #loot and balances to #fairshare-dev.
func (b *Bot) announceSettle(raidID int64, out raids.Outcome) {
	if out.Report != nil {
		var lines []string
		for _, a := range out.Report.Auctions {
			if a.Kind != "loot" {
				continue
			}
			if a.Sale {
				lines = append(lines, fmt.Sprintf("%s → **%s** for %d", safeText(a.ItemLink), safeText(a.Winner), a.Price))
			} else {
				lines = append(lines, fmt.Sprintf("%s → no bids", safeText(a.ItemLink)))
			}
		}
		if len(lines) == 0 {
			lines = []string{"No loot auctions."}
		}
		b.post("loot", fmt.Sprintf("**Raid %d settled.** %d kills. %s\n%s\n%s/raids/%d", raidID, len(out.Report.Kills), flagSummary(out.Report), strings.Join(lines, "\n"), b.site(), raidID))
	}
	if len(out.Balances) > 0 {
		names := make([]string, 0, len(out.Balances))
		for n := range out.Balances {
			names = append(names, n)
		}
		sort.Strings(names)
		rows := make([][]string, 0, len(names))
		for _, n := range names {
			rows = append(rows, []string{safeText(n), fmt.Sprint(out.Balances[n])})
		}
		b.devPost(fmt.Sprintf("**Balances after raid %d**\n", raidID)+codeTable([]string{"Member", "Balance"}, rows), nil)
	}
}

func mention(id string) string { return "<@" + id + ">" }

func statusColor(status string) int {
	switch status {
	case "settled":
		return 0x43AA8B
	case "disputed":
		return 0xF94144
	default:
		return 0xF9C74F
	}
}

// OnAPIUpload mirrors what a slash-command upload posts, for uploads that arrive
// through the HTTP API (sync client).
func (b *Bot) OnAPIUpload(raidID int64, by string, out raids.Outcome) {
	b.logAction(by, "transcript_upload_api", fmt.Sprintf("raid %d -> %s", raidID, out.Status))
	b.devPostReplay(raidID, by, out)
	if out.Status == "settled" {
		b.announceSettle(raidID, out)
		b.closeLiveLedger(raidID)
	}
}

// OnAPIProgress keeps one message in #loot up to date with the raid as it happens:
// the first live update posts it, every later one edits it in place.
func (b *Bot) OnAPIProgress(raidID int64, by string, out raids.Outcome) {
	if out.Report == nil {
		return
	}
	ch := b.channel("loot")
	if ch == "" {
		return
	}
	embed := liveEmbed(raidID, out)
	raid, err := b.Raids.Get(raidID)
	if err == nil && raid.LiveMessageID != "" {
		_, err = b.edit(&discordgo.MessageEdit{Channel: ch, ID: raid.LiveMessageID, Embeds: &[]*discordgo.MessageEmbed{embed}})
		if err == nil {
			return
		}
		log.Printf("live ledger edit for raid %d: %v (posting a new one)", raidID, err)
	}
	msg, err := b.send(ch, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{embed}})
	if err != nil {
		log.Printf("live ledger post for raid %d: %v", raidID, err)
		return
	}
	_ = b.Raids.SetLiveMessage(raidID, msg.ID)
}

// closeLiveLedger marks the live message as settled once the raid is.
func (b *Bot) closeLiveLedger(raidID int64) {
	raid, err := b.Raids.Get(raidID)
	if err != nil || raid.LiveMessageID == "" {
		return
	}
	ch := b.channel("loot")
	if ch == "" {
		return
	}
	msg, err := b.S.ChannelMessage(ch, raid.LiveMessageID)
	if err != nil || len(msg.Embeds) == 0 {
		return
	}
	e := msg.Embeds[0]
	e.Title = fmt.Sprintf("Raid %d - settled", raidID)
	e.Color = statusColor("settled")
	e.Footer = &discordgo.MessageEmbedFooter{Text: "Settled. Balances above were provisional; see the settlement post."}
	_, _ = b.edit(&discordgo.MessageEdit{Channel: ch, ID: raid.LiveMessageID, Embeds: &[]*discordgo.MessageEmbed{e}})
}

// liveEmbed renders the live ledger: kills, every loot auction, provisional balances.
// Everything in it came over the addon's wire (item links, names, encounter names), so
// it all goes through safeText.
func liveEmbed(raidID int64, out raids.Outcome) *discordgo.MessageEmbed {
	rep := out.Report
	e := &discordgo.MessageEmbed{
		Title:       fmt.Sprintf("Raid %d - live", raidID),
		Color:       0x577590,
		Description: fmt.Sprintf("Updated <t:%d:R> from the leader's sync client. Nothing here is settled yet.", time.Now().Unix()),
	}
	kills := make([]string, 0, len(rep.Kills))
	for _, k := range rep.Kills {
		kills = append(kills, fmt.Sprintf("%s (%d present)", safeText(k.EncounterName), len(k.Roster)))
	}
	if len(kills) == 0 {
		kills = []string{"None yet."}
	}
	e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Kills", Value: truncate(strings.Join(kills, "\n"), 1024)})

	var loot []string
	for _, a := range rep.Auctions {
		if a.Kind != "loot" {
			continue
		}
		item := safeText(a.ItemLink)
		switch {
		case a.Voided:
			loot = append(loot, fmt.Sprintf("%s → voided (%s)", item, safeText(a.VoidReason)))
		case a.Abandoned:
			loot = append(loot, fmt.Sprintf("%s → abandoned", item))
		case a.Result == nil:
			loot = append(loot, fmt.Sprintf("%s → bidding", item))
		case a.Result.Winner == "":
			loot = append(loot, fmt.Sprintf("%s → no bids", item))
		case a.TransferredTo != "":
			loot = append(loot, fmt.Sprintf("%s → **%s** for %d (from %s)", item, safeText(a.TransferredTo), a.Result.Price, safeText(a.Result.Winner)))
		default:
			loot = append(loot, fmt.Sprintf("%s → **%s** for %d", item, safeText(a.Result.Winner), a.Result.Price))
		}
	}
	if len(loot) == 0 {
		loot = []string{"No loot auctions yet."}
	}
	e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Loot", Value: truncate(strings.Join(loot, "\n"), 1024)})

	if len(out.Deltas) > 0 {
		totals := map[string]int64{}
		for _, d := range out.Deltas {
			totals[d.Character] += d.Amount
		}
		names := make([]string, 0, len(totals))
		for n := range totals {
			names = append(names, n)
		}
		sort.Strings(names)
		rows := make([][]string, 0, len(names))
		for _, n := range names {
			rows = append(rows, []string{safeText(n), fmt.Sprint(totals[n])})
		}
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Tonight so far (provisional)", Value: truncate(codeTable([]string{"Raider", "Delta"}, rows), 1024)})
	}
	if rep.Flagged() {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Flags", Value: truncate(flagSummary(rep)+"\n"+safeText(strings.Join(rep.AllFlags(), "\n")), 1024)})
	}
	return e
}

// finishRaid is the personal-panel button: settle the raid you lead from its last live update.
func (b *Bot) finishRaid(i *discordgo.InteractionCreate) {
	u := userOf(i)
	raid, err := b.Raids.CurrentForLeader(u.ID)
	if err != nil {
		b.ephemeral(i, "You are not leading an open raid.")
		return
	}
	out, err := b.Raids.Finish(raid.ID, u.ID)
	if err != nil {
		b.ephemeral(i, "Could not finish raid "+fmt.Sprint(raid.ID)+": "+safeText(err.Error()))
		return
	}
	b.OnAPIUpload(raid.ID, u.ID, out)
	b.ephemeral(i, fmt.Sprintf("Raid %d: %s %s", raid.ID, out.Status, safeText(out.Message)))
}
