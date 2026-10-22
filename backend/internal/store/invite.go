package store

import "strings"

// ValidDiscordInvite accepts an empty value or a Discord invite link and nothing else, so
// the only outbound link the public website ever renders is one to Discord. The bot
// checks it when an officer sets it and the website checks it again when rendering, in
// case the guild_config row was edited some other way.
func ValidDiscordInvite(s string) bool {
	if s == "" {
		return true
	}
	var code string
	for _, prefix := range []string{"https://discord.gg/", "https://discord.com/invite/"} {
		if strings.HasPrefix(s, prefix) {
			code = strings.TrimPrefix(s, prefix)
		}
	}
	if code == "" || len(code) > 64 {
		return false
	}
	for _, r := range code {
		alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !alnum && r != '-' {
			return false
		}
	}
	return true
}
