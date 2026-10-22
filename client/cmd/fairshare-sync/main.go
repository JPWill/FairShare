// fairshare-sync keeps the FairShare addon in step with the backend: it writes the
// current raid snapshot into the addon folder and uploads transcripts after a raid.
// With no flags on Windows it opens a window; -cli, -once, -finish and -setup run in the
// console as before. See client/README.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/JPWill/FairShare/client/internal/api"
	"github.com/JPWill/FairShare/client/internal/app"
	"github.com/JPWill/FairShare/client/internal/config"
	"github.com/JPWill/FairShare/client/internal/gui"
	"github.com/JPWill/FairShare/client/internal/sync"
)

// version is stamped by the release workflow (-ldflags "-X main.version=client-vX.Y.Z").
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	defaultCfg, _ := config.DefaultPath()
	var (
		cfgPath  = flag.String("config", defaultCfg, "path to config.json")
		backend  = flag.String("backend", "", "backend URL (overrides config)")
		token    = flag.String("token", "", "API token (overrides config)")
		wowDir   = flag.String("wow", "", "World of Warcraft install folder (overrides config)")
		flavor   = flag.String("flavor", "", "game version folder such as _classic_beta_ (overrides config)")
		once     = flag.Bool("once", false, "pull the snapshot and upload transcripts once as witness statements, then exit")
		finish   = flag.Bool("finish", false, "settle the raid you lead from its last live update (after the final /reload), then exit")
		interval = flag.Duration("interval", 2*time.Second, "how often to check SavedVariables in watch mode")
		snapEach = flag.Duration("snapshot-interval", time.Minute, "how often to re-check the backend for a new snapshot")
		setup    = flag.Bool("setup", false, "re-run interactive setup even if config exists")
		cli      = flag.Bool("cli", false, "run in this console without the window (watch mode)")
	)
	flag.Parse()
	logger := log.New(os.Stdout, "", log.Ltime)

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *backend != "" {
		cfg.BackendURL = *backend
	}
	if *token != "" {
		cfg.Token = *token
	}
	if *wowDir != "" {
		cfg.WoWDir = *wowDir
	}
	if *flavor != "" {
		cfg.Flavor = *flavor
	}
	if runtime.GOOS == "windows" && !*cli && !*once && !*finish && !*setup {
		return runWindow(*cfg, *cfgPath)
	}
	if *setup || !cfg.Complete() {
		fmt.Println("FairShare sync setup (saved to " + *cfgPath + ")")
		if *setup {
			// Let the user change values: blank them so Interactive asks, showing nothing stored.
			cfg = &config.Config{}
		}
		if err := cfg.Interactive(os.Stdin, os.Stdout); err != nil {
			return err
		}
		if err := config.CheckBackendURL(cfg.BackendURL); err != nil {
			return err
		}
		if err := cfg.Save(*cfgPath); err != nil {
			return fmt.Errorf("could not save config: %w", err)
		}
		fmt.Println("saved.")
	}

	if st, err := os.Stat(cfg.AddonDir()); err != nil || !st.IsDir() {
		return fmt.Errorf("FairShare addon folder not found at %s; install the addon first", cfg.AddonDir())
	}

	client := api.New(cfg.BackendURL, cfg.Token)
	me, err := client.Me()
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			return fmt.Errorf("%w; run with -setup to enter a new one", err)
		}
		return err
	}
	logger.Printf("fairshare-sync %s, signed in as %s", version, me.DisplayName)

	uploads, err := config.LoadUploads()
	if err != nil {
		return err
	}
	s := sync.New(cfg, client, uploads, logger)

	if *finish {
		return s.Finish()
	}
	if *once {
		return s.Once()
	}

	stop := make(chan struct{})
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		close(stop)
	}()
	s.Watch(*interval, *snapEach, stop)
	logger.Print("stopped")
	return nil
}

// runWindow opens the window. Settings, the upload memory and the activity log live in
// the same folder as config.json, so the window and the console share them.
func runWindow(cfg config.Config, cfgPath string) error {
	ctrl, err := app.New(cfg, app.Options{ConfigPath: cfgPath, DataDir: filepath.Dir(cfgPath), Version: version})
	if err != nil {
		return err
	}
	return gui.Run(ctrl)
}
