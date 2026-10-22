package discord

import (
	"encoding/base64"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/JPWill/FairShare/backend/internal/people"
)

// Class and role icons in embeds, Raid-Helper style. Discord embeds cannot carry
// images inline, so the icons are application-owned emoji: uploaded once under the
// bot's application and usable in every server it is in. The PNGs are not part of the
// repo (they are game art); drop them in the emoji directory and the bot uploads any
// that are missing at startup. Without a file, a plain Unicode fallback is used.

const emojiPrefix = "fs_"

var emojiNames = append(append([]string{}, lowerAll(people.Classes)...), "tank", "healer", "melee", "ranged", "absent")

var emojiFallback = map[string]string{
	"warrior": "⚔️", "paladin": "🔨", "hunter": "🏹", "rogue": "🗡️", "priest": "✨",
	"shaman": "⚡", "mage": "🔮", "warlock": "💀", "druid": "🐻",
	"tank": "🛡️", "healer": "💚", "melee": "⚔️", "ranged": "🎯", "absent": "🚫",
}

func lowerAll(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}

// emojiDir is where the icon PNGs live: FAIRSHARE_EMOJI_DIR, else backend/assets/emoji
// relative to the working directory (the repo root when run with `go run`).
func emojiDir() string {
	if d := os.Getenv("FAIRSHARE_EMOJI_DIR"); d != "" {
		return d
	}
	for _, c := range []string{"backend/assets/emoji", "assets/emoji"} {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			return c
		}
	}
	return "backend/assets/emoji"
}

// syncEmoji lists the application's emoji, uploads any class/role icon that has a PNG
// but no emoji yet, and fills b.emoji with the "<:name:id>" strings embeds use.
func (b *Bot) syncEmoji() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.emoji = map[string]string{}
	have := map[string]string{}
	if list, err := b.S.ApplicationEmojis(b.Cfg.DiscordAppID); err == nil {
		for _, e := range list {
			have[e.Name] = e.ID
		}
	} else {
		log.Printf("emoji: cannot list application emoji: %v (using text fallbacks)", err)
		return
	}
	dir := emojiDir()
	uploaded := 0
	for _, name := range emojiNames {
		full := emojiPrefix + name
		if id, ok := have[full]; ok {
			b.emoji[name] = "<:" + full + ":" + id + ">"
			continue
		}
		path := filepath.Join(dir, name+".png")
		data, err := os.ReadFile(path)
		if err != nil {
			continue // no file: fallback text
		}
		e, err := b.S.ApplicationEmojiCreate(b.Cfg.DiscordAppID, &discordgo.EmojiParams{
			Name:  full,
			Image: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data),
		})
		if err != nil {
			log.Printf("emoji: upload %s: %v", path, err)
			continue
		}
		b.emoji[name] = "<:" + full + ":" + e.ID + ">"
		uploaded++
	}
	if uploaded > 0 {
		log.Printf("emoji: uploaded %d icon(s) from %s", uploaded, dir)
	}
	missing := 0
	for _, name := range emojiNames {
		if b.emoji[name] == "" {
			missing++
		}
	}
	if missing > 0 {
		log.Printf("emoji: %d icon(s) have no PNG in %s; using text fallbacks (regenerate with tools/icons/make_icons.py)", missing, dir)
	}
}

// icon returns the emoji string for a class or role key (any case), or a text fallback.
func (b *Bot) icon(key string) string {
	k := strings.ToLower(strings.TrimSpace(key))
	b.mu.Lock()
	e := b.emoji[k]
	b.mu.Unlock()
	if e != "" {
		return e
	}
	if f, ok := emojiFallback[k]; ok {
		return f
	}
	return "•"
}
