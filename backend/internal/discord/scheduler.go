package discord

import (
	"log"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/store"
)

// scheduler posts due announcements once a minute and reminds signups an hour before
// a raid event starts.
func (b *Bot) scheduler() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-b.stopCh:
			return
		case <-t.C:
			b.postDueAnnouncements()
			b.remindEvents()
			b.rollRepeatingEvents()
		}
	}
}

func (b *Bot) postDueAnnouncements() {
	rows, err := b.Store.DB.Query(`SELECT id, channel_id, body FROM scheduled_announcement WHERE posted_at IS NULL AND post_at <= ?`, store.Now())
	if err != nil {
		return
	}
	type due struct {
		id      int64
		channel string
		body    string
	}
	var list []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.id, &d.channel, &d.body); err == nil {
			list = append(list, d)
		}
	}
	rows.Close()
	for _, d := range list {
		// Officer-written announcements keep their pings (see submitAnnounce).
		if _, err := b.send(d.channel, &discordgo.MessageSend{Content: d.body, AllowedMentions: announceMentions}); err != nil {
			log.Printf("scheduled announcement %d: %v", d.id, err)
			continue
		}
		_, _ = b.Store.DB.Exec(`UPDATE scheduled_announcement SET posted_at = ? WHERE id = ?`, store.Now(), d.id)
	}
}

// remindEvents pings confirmed signups 60 minutes before start, once. Only those
// signups may be pinged; the title is officer text but goes through safeText anyway.
func (b *Bot) remindEvents() {
	now := store.Now()
	rows, err := b.Store.DB.Query(`SELECT id FROM raid_event WHERE status = 'open' AND starts_at BETWEEN ? AND ? AND id NOT IN (SELECT CAST(value AS INTEGER) FROM guild_config WHERE key LIKE 'reminded.%')`, now, now+3600)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		ev, err := b.Raids.Event(id)
		if err != nil {
			continue
		}
		ups, _ := b.Raids.Signups(id)
		msg := "**" + safeText(ev.Title) + "** starts " + fmtTime(ev.StartsAt) + "."
		var users []string
		for _, s := range ups {
			if s.Status == "confirmed" {
				msg += " <@" + s.PersonID + ">"
				users = append(users, s.PersonID)
			}
		}
		ch := ev.ChannelID
		if ch == "" {
			ch = b.channel("announcements")
		}
		if ch != "" {
			_, _ = b.send(ch, &discordgo.MessageSend{Content: msg, AllowedMentions: mentionUsers(users...)})
		}
		_ = b.Store.SetConfig("reminded."+itoa(id), itoa(id))
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
