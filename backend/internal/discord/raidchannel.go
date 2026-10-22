package discord

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/raids"
)

// Every raid series gets its own channel under the Signups category, named from its
// schedule and instance: #tuesday-7pm-mc, #thursday-9pm-bwl. The signup embed, the
// pinned roster, the reminder and the raid-night chat all live there; #announcements
// gets a one-line pointer when a raid is created, moved or cancelled. Repeating events keep the channel
// across occurrences; a reschedule renames it; cancelling the last open event in a
// channel deletes it.

// instanceAbbrev maps an instance name to the short form raiders say.
var instanceAbbrev = map[string]string{
	"molten core": "mc", "blackwing lair": "bwl", "onyxia's lair": "ony", "onyxia": "ony",
	"zul'gurub": "zg", "ruins of ahn'qiraj": "aq20", "temple of ahn'qiraj": "aq40", "naxxramas": "naxx",
	"upper blackrock spire": "ubrs", "lower blackrock spire": "lbrs", "blackrock depths": "brd",
	"stratholme": "strat", "scholomance": "scholo", "dire maul": "dm",
	"karazhan": "kara", "gruul's lair": "gruul", "magtheridon's lair": "mag", "serpentshrine cavern": "ssc",
	"tempest keep": "tk", "the eye": "tk", "hyjal summit": "hyjal", "mount hyjal": "hyjal", "black temple": "bt",
	"zul'aman": "za", "sunwell plateau": "swp",
}

// abbrev shortens an instance name: the known short form, else the initials of a
// multi-word name, else the first four letters.
func abbrev(instance string) string {
	s := strings.ToLower(strings.TrimSpace(instance))
	if a, ok := instanceAbbrev[s]; ok {
		return a
	}
	words := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == '-' || r == '_' })
	if len(words) >= 2 {
		var b strings.Builder
		for _, w := range words {
			if w == "the" || w == "of" {
				continue
			}
			b.WriteByte(w[0])
		}
		if b.Len() >= 2 {
			return b.String()
		}
	}
	if len(s) > 4 {
		s = s[:4]
	}
	return s
}

// timeSlug: 19:00 -> 7pm, 19:30 -> 730pm, 00:00 -> 12am.
func timeSlug(t time.Time) string {
	h, m := t.Hour(), t.Minute()
	ap := "am"
	if h >= 12 {
		ap = "pm"
	}
	h12 := h % 12
	if h12 == 0 {
		h12 = 12
	}
	if m == 0 {
		return fmt.Sprintf("%d%s", h12, ap)
	}
	return fmt.Sprintf("%d%02d%s", h12, m, ap)
}

// slug keeps what Discord allows in a channel name: lowercase letters, digits and dashes.
func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 90 {
		out = out[:90]
	}
	return out
}

// raidChannelName is "<weekday>-<time>-<instance>" in the guild's timezone.
func raidChannelName(ev raids.Event, loc *time.Location) string {
	t := time.Unix(ev.StartsAt, 0).In(loc)
	return slug(strings.ToLower(t.Weekday().String()) + "-" + timeSlug(t) + "-" + abbrev(ev.Instance))
}

// ensureRaidChannel creates the event's channel if it has none and remembers it. It
// returns the channel id, or "" when none could be made (sandbox, or no Setup server).
func (b *Bot) ensureRaidChannel(ev raids.Event) string {
	if ev.RaidChannelID != "" {
		return ev.RaidChannelID
	}
	name := raidChannelName(ev, b.location())
	id, err := b.createChannelIn(name, b.Store.ConfigString("category.signups"), b.raidChannelOverwrites(b.roleIDs()))
	if err != nil {
		log.Printf("raid channel #%s: %v", name, err)
		return ""
	}
	if id == "" {
		return ""
	}
	if err := b.Raids.SetRaidChannel(ev.ID, id); err != nil {
		log.Printf("remember raid channel: %v", err)
	}
	b.post("log", fmt.Sprintf("Created <#%s> for **%s**.", id, safeText(ev.Title)))
	return id
}

// syncRaidChannelName renames the channel after a reschedule or an instance change.
func (b *Bot) syncRaidChannelName(ev raids.Event) {
	if ev.RaidChannelID == "" {
		return
	}
	want := raidChannelName(ev, b.location())
	ch, err := b.S.Channel(ev.RaidChannelID)
	if err != nil || ch.Name == want {
		return
	}
	if err := b.renameChannel(ev.RaidChannelID, want); err != nil {
		log.Printf("rename raid channel: %v", err)
		return
	}
	b.post("log", fmt.Sprintf("Renamed #%s to <#%s> for **%s**.", ch.Name, ev.RaidChannelID, safeText(ev.Title)))
}

// releaseRaidChannel deletes the channel once no open event uses it.
func (b *Bot) releaseRaidChannel(ev raids.Event) {
	if ev.RaidChannelID == "" {
		return
	}
	n, err := b.Raids.OpenEventsInChannel(ev.RaidChannelID)
	if err != nil || n > 0 {
		return
	}
	if err := b.deleteChannel(ev.RaidChannelID); err != nil {
		log.Printf("delete raid channel: %v", err)
		return
	}
	b.post("log", fmt.Sprintf("Deleted the channel for **%s**: no open raid uses it.", safeText(ev.Title)))
}

// eventChannel is where an event's posts go: its own channel, else #announcements.
func (b *Bot) eventChannel(ev raids.Event) string {
	if ev.RaidChannelID != "" && !b.Store.IsSandbox() {
		return ev.RaidChannelID
	}
	return b.channel("announcements")
}

// tellRaid posts a line about an event where its people are: its channel, else #announcements.
func (b *Bot) tellRaid(ev raids.Event, content string) {
	ch := b.eventChannel(ev)
	if ch == "" {
		return
	}
	if b.Store.IsSandbox() {
		content = "[sandbox raid] " + content
	}
	if _, err := b.send(ch, &discordgo.MessageSend{Content: content}); err != nil {
		log.Printf("tell raid %d: %v", ev.ID, err)
	}
}
