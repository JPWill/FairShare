package discord

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/raids"
)

// Event creation: two button grids on one ephemeral message. Step 1 is a grid of days
// labelled "Monday - 19 Oct" (16 per page, earlier/later paging, a Repeat cycle
// button); clicking a day shows step 2, a grid of half-hour start times (15 per page,
// earlier/later paging). Clicking a time opens the modal for the words (title,
// instance, caps, notes). Two clicks, no scrolling, nobody types a date. Rescheduling
// reuses the same grids and saves on the time click.

type eventDraft struct {
	Date     string // 2006-01-02 in the guild timezone
	Time     string // 15:04
	Repeat   int64  // 0, 7, 14
	DatePage int    // days offset of the first day shown
	TimePage int    // index of the first half-hour slot shown (0 = 00:00)
	EditID   int64  // non-zero while rescheduling an existing event
	Title    string // for the reschedule heading
	Current  int64  // current start time while rescheduling
}

const (
	daysPerPage  = 16
	slotsPerPage = 15
	defaultSlot  = 34 // 17:00
	dayFormat    = "Monday - 2 Jan"
)

// location is the guild's timezone (guild_config.timezone), UTC if it cannot load.
func (b *Bot) location() *time.Location {
	loc, err := time.LoadLocation(b.Store.ConfigString("timezone"))
	if err != nil {
		return time.UTC
	}
	return loc
}

func (b *Bot) startEvent(i *discordgo.InteractionCreate) {
	d := &eventDraft{TimePage: defaultSlot}
	setPending("event", userOf(i).ID, d)
	b.ephemeral(i, b.eventDraftText(d), b.eventDraftComponents(d)...)
}

func (b *Bot) eventDraftText(d *eventDraft) string {
	head := "**New raid event**"
	if d.EditID != 0 {
		head = fmt.Sprintf("**Reschedule: %s**  (now <t:%d:F>)", safeText(d.Title), d.Current)
	}
	rep := map[int64]string{0: "one-off", 7: "repeats weekly", 14: "repeats every two weeks"}[d.Repeat]
	step := "Pick a day."
	if d.Date != "" {
		if t, err := time.ParseInLocation("2006-01-02", d.Date, b.location()); err == nil {
			step = fmt.Sprintf("**%s**. Pick a start time.", t.Format("Monday 2 January"))
		}
	}
	return fmt.Sprintf("%s  ·  %s\n%s\nTimes are in %s; everyone sees them in their own timezone.", head, rep, step, b.location().String())
}

func (b *Bot) draftTime(d *eventDraft) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04", d.Date+" "+d.Time, b.location())
}

func slotLabel(slot int) string {
	return fmt.Sprintf("%02d:%02d", (slot/2)%24, (slot%2)*30)
}

func (b *Bot) eventDraftComponents(d *eventDraft) []discordgo.MessageComponent {
	if d.Date == "" {
		return b.dateGrid(d)
	}
	return b.timeGrid(d)
}

// dateGrid: 4 rows × 4 days ("Monday - 19 Oct"), then a control row.
func (b *Bot) dateGrid(d *eventDraft) []discordgo.MessageComponent {
	loc := b.location()
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	var rows []discordgo.MessageComponent
	for r := 0; r < 4; r++ {
		var btns []discordgo.MessageComponent
		for c := 0; c < 4; c++ {
			day := today.AddDate(0, 0, d.DatePage+r*4+c)
			label := day.Format(dayFormat)
			style := discordgo.SecondaryButton
			if day.Equal(today) {
				label = "Today, " + day.Format("Monday 2 Jan")
				style = discordgo.PrimaryButton
			}
			btns = append(btns, button(label, "fs:ev-d:"+day.Format("2006-01-02"), style))
		}
		rows = append(rows, row(btns...))
	}
	earlier := button("◀ Earlier", "fs:ev-pg:"+strconv.Itoa(d.DatePage-daysPerPage), discordgo.SecondaryButton)
	if d.DatePage <= 0 {
		earlier.Disabled = true
	}
	rep := map[int64]string{0: "Repeat: one-off", 7: "Repeat: weekly", 14: "Repeat: every 2 weeks"}[d.Repeat]
	rows = append(rows, row(earlier,
		button("Later ▶", "fs:ev-pg:"+strconv.Itoa(d.DatePage+daysPerPage), discordgo.SecondaryButton),
		button(rep, "fs:ev-rep", discordgo.SecondaryButton)))
	return rows
}

// timeGrid: 3 rows × 5 half-hour slots, then a control row.
func (b *Bot) timeGrid(d *eventDraft) []discordgo.MessageComponent {
	var rows []discordgo.MessageComponent
	for r := 0; r < 3; r++ {
		var btns []discordgo.MessageComponent
		for c := 0; c < 5; c++ {
			slot := d.TimePage + r*5 + c
			if slot >= 48 {
				btns = append(btns, discordgo.Button{Label: "—", CustomID: fmt.Sprintf("fs:ev-none:%d", slot), Style: discordgo.SecondaryButton, Disabled: true})
				continue
			}
			btns = append(btns, button(slotLabel(slot), "fs:ev-t:"+strconv.Itoa(slot), discordgo.SecondaryButton)) // slot index: a colon would split the custom id
		}
		rows = append(rows, row(btns...))
	}
	earlier := button("◀ Earlier", "fs:ev-tp:"+strconv.Itoa(d.TimePage-slotsPerPage), discordgo.SecondaryButton)
	if d.TimePage <= 0 {
		earlier.Disabled = true
	}
	later := button("Later ▶", "fs:ev-tp:"+strconv.Itoa(d.TimePage+slotsPerPage), discordgo.SecondaryButton)
	if d.TimePage+slotsPerPage >= 48 {
		later.Disabled = true
	}
	rows = append(rows, row(earlier, later, button("◀ Change day", "fs:ev-back", discordgo.SecondaryButton)))
	return rows
}

func (b *Bot) draftOf(i *discordgo.InteractionCreate) (*eventDraft, bool) {
	d, ok := getPending[*eventDraft]("event", userOf(i).ID)
	if !ok {
		b.updateClear(i, "That draft expired. Start again from the officer panel.")
	}
	return d, ok
}

// eventGridAction handles every button on the grids.
func (b *Bot) eventGridAction(i *discordgo.InteractionCreate, action string, args []string) {
	d, ok := b.draftOf(i)
	if !ok {
		return
	}
	arg := ""
	if len(args) > 0 {
		arg = args[0]
	}
	switch action {
	case "ev-d":
		d.Date = arg
	case "ev-pg":
		n, _ := strconv.Atoi(arg)
		if n < 0 {
			n = 0
		}
		d.DatePage = n
	case "ev-rep":
		d.Repeat = map[int64]int64{0: 7, 7: 14, 14: 0}[d.Repeat]
	case "ev-tp":
		n, _ := strconv.Atoi(arg)
		if n < 0 {
			n = 0
		}
		if n > 48-slotsPerPage {
			n = 48 - slotsPerPage
		}
		d.TimePage = n
	case "ev-back":
		d.Date = ""
	case "ev-t":
		slot, err := strconv.Atoi(arg)
		if err != nil || slot < 0 || slot >= 48 {
			b.update(i, "That time button is stale. Pick again.", b.eventDraftComponents(d)...)
			return
		}
		d.Time = slotLabel(slot)
		if d.EditID != 0 {
			b.saveEventTime(i, d)
		} else {
			b.modal(i, "fsm:event-new", "Raid event",
				text("title", "Title", "Tuesday Molten Core", true, 80),
				text("instance", "Instance", "Molten Core", true, 60),
				text("cap", "Raid size", "20", false, 3),
				text("standby", "Standby slots", "5", false, 3),
				paragraph("notes", "Notes (optional)", "", false, 500))
		}
		return
	}
	b.update(i, b.eventDraftText(d), b.eventDraftComponents(d)...)
}

func (b *Bot) submitEvent(i *discordgo.InteractionCreate) {
	u := userOf(i)
	d, ok := getPending[*eventDraft]("event", u.ID)
	if !ok {
		b.ephemeral(i, "That draft expired. Press Create raid event again.")
		return
	}
	start, err := b.draftTime(d)
	if err != nil {
		b.ephemeral(i, "Bad date or time: "+err.Error())
		return
	}
	v := modalValues(i)
	cap, _ := strconv.ParseInt(v["cap"], 10, 64)
	standby, _ := strconv.ParseInt(v["standby"], 10, 64)
	if v["standby"] == "" {
		standby = 5
	}
	ev, err := b.Raids.CreateEvent(v["title"], v["instance"], start.Unix(), u.ID, cap, standby, d.Repeat, 0)
	if err != nil {
		b.ephemeral(i, "Could not create event: "+err.Error())
		return
	}
	if v["notes"] != "" {
		_ = b.Raids.UpdateEventDetails(ev.ID, ev.Title, ev.Instance, ev.Cap, ev.StandbyCap, v["notes"], u.ID)
		ev, _ = b.Raids.Event(ev.ID)
	}
	delPending("event", u.ID)
	ev.RaidChannelID = b.ensureRaidChannel(ev)
	ch, err := b.postEvent(ev)
	if err != nil {
		b.ephemeral(i, fmt.Sprintf("Event created but the signup post failed: %v", err))
		return
	}
	b.logAction(u.ID, "event_create", fmt.Sprintf("%s (%s) at %s repeat %d", safeText(ev.Title), safeText(ev.Instance), fmtTime(ev.StartsAt), ev.RepeatDays))
	b.ephemeral(i, fmt.Sprintf("Event **%s** posted in <#%s>, starting <t:%d:F>.", safeText(ev.Title), ch, ev.StartsAt))
}

// postEvent sends the signup embed to the event's own channel (else #announcements) and
// remembers where it lives. #announcements gets a one-line pointer to the channel.
func (b *Bot) postEvent(ev raids.Event) (string, error) {
	ch := b.eventChannel(ev)
	if ch == "" {
		return "", fmt.Errorf("no raid channel and no #announcements; run Setup server")
	}
	msg, err := b.send(ch, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{b.signupEmbed(ev)}, Components: signupButtons(ev)})
	if err != nil {
		return "", err
	}
	if cal := b.channel("announcements"); cal != "" && cal != ch {
		b.post("announcements", fmt.Sprintf("**%s**  ·  %s  ·  <t:%d:F> (<t:%d:R>). Sign up in <#%s>.", safeText(ev.Title), safeText(ev.Instance), ev.StartsAt, ev.StartsAt, ch))
	}
	return ch, b.Raids.SetEventMessage(ev.ID, ch, msg.ID)
}

// spawnNext creates and posts the next occurrence of a repeating event, if due and
// not already there. Safe to call repeatedly.
func (b *Bot) spawnNext(ev raids.Event) {
	next, ok, err := b.Raids.NextOccurrence(ev)
	if err != nil {
		log.Printf("next occurrence of event %d: %v", ev.ID, err)
		return
	}
	if !ok {
		return
	}
	if _, err := b.postEvent(next); err != nil {
		log.Printf("post next occurrence of event %d: %v", ev.ID, err)
	}
	b.post("log", fmt.Sprintf("Repeating event **%s** scheduled again for <t:%d:F>.", safeText(next.Title), next.StartsAt))
}

// rollRepeatingEvents is called by the scheduler once a minute.
func (b *Bot) rollRepeatingEvents() {
	due, err := b.Raids.DueRepeats(time.Now().Unix())
	if err != nil {
		return
	}
	for _, ev := range due {
		b.spawnNext(ev)
	}
}

// ---------------------------------------------------------------- timezone

func (b *Bot) submitTimezone(i *discordgo.InteractionCreate) {
	tz := modalValues(i)["timezone"]
	if _, err := time.LoadLocation(tz); err != nil {
		b.ephemeral(i, "Unknown timezone. Use an IANA name like America/New_York, Europe/London or Australia/Sydney.")
		return
	}
	_ = b.Store.SetConfig("timezone", tz)
	b.logAction(userOf(i).ID, "config", "timezone="+tz)
	b.adminDone(i, "Timezone set to "+tz+". New events use it; existing ones keep their times.")
}

// ---------------------------------------------------------------- editing

// editEventPanel is the per-event officer view: what it is now, and what can change.
func (b *Bot) editEventPanel(i *discordgo.InteractionCreate, id int64, isUpdate bool) {
	ev, err := b.Raids.Event(id)
	if err != nil {
		b.update(i, "No such event.")
		return
	}
	rep := map[int64]string{0: "one-off", 7: "weekly", 14: "every two weeks"}[ev.RepeatDays]
	notes := ev.Notes
	if notes == "" {
		notes = "(none)"
	}
	text := fmt.Sprintf("**%s**  ·  %s\n<t:%d:F> (<t:%d:R>)  ·  %s\nSize %d, standby %d\nNotes: %s",
		safeText(ev.Title), safeText(ev.Instance), ev.StartsAt, ev.StartsAt, rep, ev.Cap, ev.StandbyCap, safeText(notes))
	sid := strconv.FormatInt(id, 10)
	comps := []discordgo.MessageComponent{row(
		button("Change time", "fs:ev-edit-time:"+sid, discordgo.PrimaryButton),
		button("Edit details", "fs:ev-edit-details:"+sid, discordgo.PrimaryButton),
		button("Cancel event", "fs:ev-edit-cancel:"+sid, discordgo.DangerButton),
	)}
	if isUpdate {
		b.update(i, text, comps...)
	} else {
		b.ephemeral(i, text, comps...)
	}
}

func (b *Bot) editEventPicked(i *discordgo.InteractionCreate) {
	vals := i.MessageComponentData().Values
	if len(vals) == 0 {
		return
	}
	id, _ := strconv.ParseInt(vals[0], 10, 64)
	b.editEventPanel(i, id, true)
}

func eventIDArg(args []string) int64 {
	if len(args) == 0 {
		return 0
	}
	id, _ := strconv.ParseInt(args[0], 10, 64)
	return id
}

// startEditTime reuses the grids; the time click saves instead of opening the modal.
func (b *Bot) startEditTime(i *discordgo.InteractionCreate, id int64) {
	ev, err := b.Raids.Event(id)
	if err != nil {
		b.update(i, "No such event.")
		return
	}
	t := time.Unix(ev.StartsAt, 0).In(b.location())
	slot := t.Hour()*2 + t.Minute()/30
	page := slot - slot%slotsPerPage
	if page > 48-slotsPerPage {
		page = 48 - slotsPerPage
	}
	d := &eventDraft{Repeat: ev.RepeatDays, TimePage: page, EditID: id, Title: ev.Title, Current: ev.StartsAt}
	setPending("event", userOf(i).ID, d)
	b.update(i, b.eventDraftText(d), b.eventDraftComponents(d)...)
}

func (b *Bot) saveEventTime(i *discordgo.InteractionCreate, d *eventDraft) {
	u := userOf(i)
	start, err := b.draftTime(d)
	if err != nil {
		b.update(i, "Bad date or time: "+err.Error())
		return
	}
	if err := b.Raids.RescheduleEvent(d.EditID, start.Unix(), d.Repeat, u.ID); err != nil {
		b.update(i, "Could not reschedule: "+err.Error())
		return
	}
	delPending("event", u.ID)
	b.refreshEvent(d.EditID)
	if ev, err := b.Raids.Event(d.EditID); err == nil {
		b.syncRaidChannelName(ev)
	}
	if ev, err := b.Raids.Event(d.EditID); err == nil {
		b.tellRaid(ev, fmt.Sprintf("**%s** moved to <t:%d:F> (<t:%d:R>).", safeText(d.Title), start.Unix(), start.Unix()))
	}
	b.logAction(u.ID, "event_reschedule", fmt.Sprintf("%s -> %s repeat %d", safeText(d.Title), fmtTime(start.Unix()), d.Repeat))
	b.editEventPanel(i, d.EditID, true)
}

func (b *Bot) editEventDetails(i *discordgo.InteractionCreate, id int64) {
	ev, err := b.Raids.Event(id)
	if err != nil {
		b.update(i, "No such event.")
		return
	}
	b.modal(i, "fsm:event-edit:"+strconv.FormatInt(id, 10), "Edit "+truncate(ev.Title, 30),
		text("title", "Title", ev.Title, true, 80),
		text("instance", "Instance", ev.Instance, true, 60),
		text("cap", "Raid size", strconv.FormatInt(ev.Cap, 10), false, 3),
		text("standby", "Standby slots", strconv.FormatInt(ev.StandbyCap, 10), false, 3),
		paragraph("notes", "Notes (shown on the signup post)", ev.Notes, false, 500))
}

func (b *Bot) submitEventEdit(i *discordgo.InteractionCreate, args []string) {
	id := eventIDArg(args)
	v := modalValues(i)
	cap, _ := strconv.ParseInt(v["cap"], 10, 64)
	standby, _ := strconv.ParseInt(v["standby"], 10, 64)
	if err := b.Raids.UpdateEventDetails(id, v["title"], v["instance"], cap, standby, v["notes"], userOf(i).ID); err != nil {
		b.ephemeral(i, "Could not save: "+err.Error())
		return
	}
	b.refreshEvent(id)
	if ev, err := b.Raids.Event(id); err == nil {
		b.syncRaidChannelName(ev)
	}
	b.logAction(userOf(i).ID, "event_edit", safeText(v["title"]))
	b.editEventPanel(i, id, false)
}

func (b *Bot) confirmCancelEvent(i *discordgo.InteractionCreate, id int64) {
	ev, err := b.Raids.Event(id)
	if err != nil {
		b.update(i, "No such event.")
		return
	}
	b.update(i, fmt.Sprintf("Cancel **%s** on <t:%d:F>? Everyone signed up will see it marked cancelled.", safeText(ev.Title), ev.StartsAt),
		row(button("Yes, cancel it", "fs:ev-edit-cancel-yes:"+strconv.FormatInt(id, 10), discordgo.DangerButton),
			button("Keep it", "fs:event-edit-pick-keep:"+strconv.FormatInt(id, 10), discordgo.SecondaryButton)))
}

// cancelEvent marks the event cancelled, rewrites its signup post, and tells the raid.
func (b *Bot) cancelEvent(i *discordgo.InteractionCreate, id int64) {
	ev, err := b.Raids.Event(id)
	if err != nil {
		b.update(i, "No such event.")
		return
	}
	if err := b.Raids.CancelEvent(id, userOf(i).ID); err != nil {
		b.update(i, "Could not cancel: "+err.Error())
		return
	}
	if ev.ChannelID != "" && ev.MessageID != "" {
		e := b.signupEmbed(ev)
		e.Title = "CANCELLED: " + safeText(ev.Title)
		e.Color = 0x8A8A8A
		_, _ = b.edit(&discordgo.MessageEdit{Channel: ev.ChannelID, ID: ev.MessageID, Embeds: &[]*discordgo.MessageEmbed{e}, Components: &[]discordgo.MessageComponent{}})
	}
	b.tellRaid(ev, fmt.Sprintf("**%s** on <t:%d:F> is cancelled.", safeText(ev.Title), ev.StartsAt))
	b.post("announcements", fmt.Sprintf("**%s** on <t:%d:F> is cancelled.", safeText(ev.Title), ev.StartsAt))
	b.releaseRaidChannel(ev)
	b.logAction(userOf(i).ID, "event_cancel", safeText(ev.Title))
	b.update(i, "Event cancelled.")
}
