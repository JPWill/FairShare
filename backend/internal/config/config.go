// Package config loads backend settings from the environment, with an optional .env
// file (plain KEY=VALUE lines) read from the working directory or from the directory
// the executable lives in. Parent directories are not searched: a stray .env higher up
// the tree must never be able to point a deployed binary at another token or database.
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config is everything the backend needs from the environment.
type Config struct {
	DiscordToken   string
	DiscordAppID   string
	DiscordGuildID string
	// DiscordClientSecret is the application's OAuth2 client secret, used only for
	// Battle.net verification through Discord Connections. Empty disables that feature.
	DiscordClientSecret string
	DBPath              string
	HTTPAddr            string
	OfficerRoleName     string
	// TrustedProxy is true when FAIRSHARE_TRUSTED_PROXY=1: the API then takes the
	// client address from X-Forwarded-For (set by a reverse proxy in front of it).
	// Off by default so a direct caller cannot spoof its address past the rate limit.
	TrustedProxy bool
	// Demo is true when FAIRSHARE_DEMO=1 (or the -demo flag): serve only the demo website,
	// over the database built by fairshare-demo, with a banner on every page. Never the
	// bot, never the sync API.
	Demo bool
	// DemoDBPath and DemoHTTPAddr are the demo site's own database and address. The live
	// process also serves the demo on DemoHTTPAddr while that database exists, so the bot
	// and the demo run side by side; DemoHTTPAddr "off" disables that.
	DemoDBPath   string
	DemoHTTPAddr string
	// DiscordInvite is the invite the standalone demo site links to (FAIRSHARE_DISCORD_INVITE);
	// alongside the bot the demo borrows the live server's instead.
	DiscordInvite string
}

// Load reads .env (if found) into the process environment without overriding values
// already set, then builds the Config.
func Load() Config {
	loadDotEnv()
	return Config{
		DiscordToken:        os.Getenv("FAIRSHARE_DISCORD_TOKEN"),
		DiscordAppID:        os.Getenv("FAIRSHARE_DISCORD_APP_ID"),
		DiscordGuildID:      os.Getenv("FAIRSHARE_DISCORD_GUILD_ID"),
		DiscordClientSecret: os.Getenv("FAIRSHARE_DISCORD_CLIENT_SECRET"),
		DBPath:              getenv("FAIRSHARE_DB_PATH", "./data/fairshare.db"),
		// Loopback by default: the API carries bearer tokens and must only be exposed
		// through a TLS-terminating reverse proxy (see backend/README.md).
		HTTPAddr:        getenv("FAIRSHARE_HTTP_ADDR", "127.0.0.1:8080"),
		OfficerRoleName: getenv("FAIRSHARE_OFFICER_ROLE_NAME", "Officer"),
		TrustedProxy:    os.Getenv("FAIRSHARE_TRUSTED_PROXY") == "1",
		Demo:            os.Getenv("FAIRSHARE_DEMO") == "1",
		DemoDBPath:      getenv("FAIRSHARE_DEMO_DB_PATH", "./data/demo.db"),
		DemoHTTPAddr:    getenv("FAIRSHARE_DEMO_HTTP_ADDR", "127.0.0.1:8081"),
		DiscordInvite:   os.Getenv("FAIRSHARE_DISCORD_INVITE"),
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadDotEnv reads the first .env found in the working directory, then next to the
// executable. Nothing else is searched.
func loadDotEnv() {
	var dirs []string
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Dir(exe))
	}
	for _, dir := range dirs {
		if f, err := os.Open(filepath.Join(dir, ".env")); err == nil {
			parseEnv(f)
			f.Close()
			return
		}
	}
}

func parseEnv(f *os.File) {
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}
