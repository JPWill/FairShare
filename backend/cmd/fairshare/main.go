// Command fairshare runs the FairShare backend: the Discord bot and the HTTP API in
// one process over one SQLite file. The same process can also serve the demo website
// (an invented guild, made by fairshare-demo) on a second address, so the real bot and
// the demo run side by side; -demo serves the demo alone.
package main

import (
	_ "time/tzdata" // embedded zone database so timezone names resolve on Windows

	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/JPWill/FairShare/backend/internal/api"
	"github.com/JPWill/FairShare/backend/internal/bans"
	"github.com/JPWill/FairShare/backend/internal/config"
	"github.com/JPWill/FairShare/backend/internal/discord"
	"github.com/JPWill/FairShare/backend/internal/people"
	"github.com/JPWill/FairShare/backend/internal/raids"
	"github.com/JPWill/FairShare/backend/internal/store"
	"github.com/JPWill/FairShare/backend/internal/verify"
	"github.com/JPWill/FairShare/backend/internal/web"
)

// demoMarker matches fairshare-demo's DemoMarker: the guild_config key set on a demo database.
const demoMarker = "demoDatabase"

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	demoOnly := flag.Bool("demo", false, "serve only the demo website from FAIRSHARE_DEMO_DB_PATH on FAIRSHARE_DEMO_HTTP_ADDR; the bot is not started")
	flag.Parse()
	cfg := config.Load()
	if *demoOnly {
		cfg.Demo = true
	}

	if cfg.Demo {
		// Demo mode never constructs the bot, so a token in .env is simply unused.
		if cfg.DiscordToken != "" {
			log.Printf("demo mode: the Discord token is ignored; nothing here reaches Discord")
		}
		st := openDemo(cfg)
		defer st.Close()
		serve(demoHandler(cfg, st, cfg.DiscordInvite), cfg.DemoHTTPAddr, "DEMO website (made-up data; no bot, no sync API)")
		return
	}

	if cfg.DiscordToken != "" && cfg.DiscordGuildID == "" {
		// The bot must be bound to its home server: without a guild id it would answer
		// interactions from any server that managed to add it.
		log.Fatalf("FAIRSHARE_DISCORD_GUILD_ID is empty: the bot only serves one server and must know which")
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database %s: %v", cfg.DBPath, err)
	}
	defer st.Close()
	log.Printf("database %s", cfg.DBPath)
	// A demo database (made by fairshare-demo) and the live one must never be mixed up:
	// the bot refuses invented raiders.
	if st.ConfigString(demoMarker) == "true" {
		log.Fatalf("%s is a demo database (made by fairshare-demo); serve it with -demo, never with the bot", cfg.DBPath)
	}
	if st.IsSandbox() {
		log.Printf("SANDBOX mode is on (from the live database): data goes to %s and every post goes to #fairshare-dev", st.SandboxPath())
	}

	p := &people.Service{S: st}
	b := &bans.Service{S: st, People: p}
	p.IsBannedIdentity = b.IsBannedIdentity
	r := &raids.Service{S: st, People: p, Bans: b}

	srv := &api.Server{People: p, Raids: r, TrustedProxy: cfg.TrustedProxy}

	var bot *discord.Bot
	if cfg.DiscordToken == "" {
		log.Printf("FAIRSHARE_DISCORD_TOKEN is empty: running the HTTP API only")
	} else {
		bot, err = discord.New(cfg, st, p, b, r)
		if err != nil {
			log.Fatalf("discord: %v", err)
		}
		if err := bot.Start(); err != nil {
			log.Fatalf("discord connect: %v", err)
		}
		srv.OnUpload = bot.OnAPIUpload
		srv.OnProgress = bot.OnAPIProgress
		defer bot.Stop()
	}

	// One listener: the sync client's API under /api/ and /healthz, the community website
	// everywhere else. Both read the same database.
	site := &web.Server{Store: st, People: p, TrustedProxy: cfg.TrustedProxy}
	if cfg.DiscordClientSecret != "" {
		// Battle.net verification: the callback must match the redirect registered in the
		// Developer Portal, which is the configured website address plus this path.
		site.Verify = &verify.Client{ClientID: cfg.DiscordAppID, ClientSecret: cfg.DiscordClientSecret, RedirectURI: strings.TrimRight(st.ConfigString("websiteURL"), "/") + "/oauth/discord/callback"}
		site.Signer = verify.NewSigner(cfg.DiscordClientSecret)
	} else {
		log.Printf("FAIRSHARE_DISCORD_CLIENT_SECRET is empty: Battle.net verification is off (members can still type a tag)")
	}
	apiHandler := srv.Handler()
	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)
	mux.Handle("/healthz", apiHandler)
	mux.Handle("/", site.Handler())

	// The demo site rides along on its own address while its database exists, so the
	// community can be shown around while the real ledger is still empty. It has its
	// own store and never touches the live one.
	var demoSrv *http.Server
	if reason := demoAlongside(cfg); reason != "" {
		log.Printf("demo website not served: %s", reason)
	} else {
		dst := openDemo(cfg)
		defer dst.Close()
		// The demo has no bot, so it borrows the live server's Discord invite for its links.
		demoSrv = listen(demoHandler(cfg, dst, st.ConfigString("discordInvite")), cfg.DemoHTTPAddr, "DEMO website alongside the bot (made-up data)")
	}

	httpSrv := listen(mux, cfg.HTTPAddr, "website and API (plain HTTP: put a TLS proxy in front before exposing it)")
	wait()
	log.Printf("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
	if demoSrv != nil {
		_ = demoSrv.Shutdown(ctx)
	}
}

// demoAlongside says why the live process will not also serve the demo: "" means it will.
func demoAlongside(cfg config.Config) string {
	if strings.EqualFold(cfg.DemoHTTPAddr, "off") {
		return "FAIRSHARE_DEMO_HTTP_ADDR=off"
	}
	if cfg.DemoDBPath == "" {
		return "FAIRSHARE_DEMO_DB_PATH is empty"
	}
	if _, err := os.Stat(cfg.DemoDBPath); err != nil {
		return "no demo database at " + cfg.DemoDBPath + " (make one with fairshare-demo, or set FAIRSHARE_DEMO_HTTP_ADDR=off to silence this)"
	}
	if cfg.DemoHTTPAddr == cfg.HTTPAddr {
		return "FAIRSHARE_DEMO_HTTP_ADDR equals FAIRSHARE_HTTP_ADDR; the demo needs its own address"
	}
	return ""
}

// openDemo opens the demo database and refuses anything fairshare-demo did not make.
func openDemo(cfg config.Config) *store.Store {
	st, err := store.Open(cfg.DemoDBPath)
	if err != nil {
		log.Fatalf("open demo database %s: %v", cfg.DemoDBPath, err)
	}
	if st.ConfigString(demoMarker) != "true" {
		log.Fatalf("%s was not made by fairshare-demo; point FAIRSHARE_DEMO_DB_PATH at the demo database", cfg.DemoDBPath)
	}
	log.Printf("demo database %s", cfg.DemoDBPath)
	return st
}

// demoHandler serves only the website (and /healthz) with the demo banner.
func demoHandler(cfg config.Config, st *store.Store, discord string) http.Handler {
	site := &web.Server{Store: st, People: &people.Service{S: st}, TrustedProxy: cfg.TrustedProxy, Demo: true, DiscordLink: discord}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	mux.Handle("/", site.Handler())
	return mux
}

// listen starts an HTTP server in the background.
func listen(h http.Handler, addr, what string) *http.Server {
	s := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		log.Printf("%s on http://%s", what, addr)
		if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http %s: %v", addr, err)
		}
	}()
	return s
}

// serve runs one server until interrupted.
func serve(h http.Handler, addr, what string) {
	s := listen(h, addr, what)
	wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.Shutdown(ctx)
}

func wait() {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
}
