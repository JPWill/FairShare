// Package app runs the sync client for a window: one goroutine owns the Syncer and does
// every network and file operation in order, the window sends it requests (sync now,
// finish, upload as witness, new settings) and reads back a Status and the activity log.
// Nothing here is Windows-specific, so it is tested on every platform.
package app

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	stdsync "sync"
	"time"

	"github.com/JPWill/FairShare/client/internal/api"
	"github.com/JPWill/FairShare/client/internal/config"
	"github.com/JPWill/FairShare/client/internal/sync"
)

// Options configure a Controller.
type Options struct {
	ConfigPath    string        // config.json
	DataDir       string        // uploads.json and sync.log live here
	Version       string        // shown in the log header
	PollEvery     time.Duration // SavedVariables check; default 2 s
	SnapshotEvery time.Duration // snapshot check and reconnect; default 1 min
}

// Status is everything the window shows, copied out under the lock.
type Status struct {
	Configured    bool      // every setting is filled in
	Connected     bool      // the backend accepted the token
	Account       string    // Discord display name
	RaidID        string    // raid this account leads, "" when none
	Snapshot      string    // short checksum Data.lua holds
	SnapshotAt    time.Time // when this session wrote Data.lua; zero if it was already current
	LastLive      time.Time // last live update accepted
	LastLiveLines int
	Note          string // the latest outcome, in plain words
	Err           string // the latest problem; cleared by the next success
	Busy          string // what is running right now, "" when idle
}

// Controller owns the sync loop.
type Controller struct {
	opt Options

	mu     stdsync.Mutex
	cfg    config.Config
	st     Status
	lines  []string // newest last, at most maxLines
	seq    int      // sequence number of the newest line
	cmds   chan func()
	stop   chan struct{}
	done   chan struct{}
	logw   io.WriteCloser
	logger *log.Logger

	// Owned by the loop goroutine.
	syncer   *sync.Syncer
	uploads  *config.Uploads
	retryAt  time.Time // scans pause until then after a failure
	witness  int       // witness uploads accepted this session
	lastErr  string
	started  bool
	stopOnce stdsync.Once
}

const maxLines = 1000

// New prepares a Controller with the given settings. Start runs it.
func New(cfg config.Config, opt Options) (*Controller, error) {
	if opt.PollEvery == 0 {
		opt.PollEvery = 2 * time.Second
	}
	if opt.SnapshotEvery == 0 {
		opt.SnapshotEvery = time.Minute
	}
	if err := os.MkdirAll(opt.DataDir, 0o700); err != nil {
		return nil, err
	}
	c := &Controller{opt: opt, cfg: cfg, cmds: make(chan func(), 16), stop: make(chan struct{}), done: make(chan struct{})}
	c.logw = openLog(filepath.Join(opt.DataDir, "sync.log"))
	c.logger = log.New(lineWriter{c}, "", log.Ltime)
	up, err := config.LoadUploadsAt(filepath.Join(opt.DataDir, "uploads.json"))
	if err != nil {
		return nil, err
	}
	c.uploads = up
	c.st.Configured = cfg.Complete()
	return c, nil
}

// LogPath is where the activity log is kept between sessions.
func (c *Controller) LogPath() string { return filepath.Join(c.opt.DataDir, "sync.log") }

// Start runs the loop in the background and connects if the settings are complete.
func (c *Controller) Start() {
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		return
	}
	c.started = true
	c.mu.Unlock()
	c.logger.Printf("FairShare sync %s started; activity is also written to %s", c.opt.Version, c.LogPath())
	go c.loop()
}

// Stop ends the loop, waiting up to wait for a request in flight to finish.
func (c *Controller) Stop(wait time.Duration) {
	c.stopOnce.Do(func() {
		close(c.stop)
		c.mu.Lock()
		started := c.started
		c.mu.Unlock()
		if started {
			select {
			case <-c.done:
			case <-time.After(wait):
			}
		}
		c.logger.Print("FairShare sync stopped")
		c.logw.Close()
	})
}

// Status returns a copy of the current status.
func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

// Config returns a copy of the settings in use.
func (c *Controller) Config() config.Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// Lines returns every log line newer than after, and the sequence number of the newest.
func (c *Controller) Lines(after int) ([]string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.seq - after
	if n <= 0 {
		return nil, c.seq
	}
	if n > len(c.lines) {
		n = len(c.lines)
	}
	out := make([]string, n)
	copy(out, c.lines[len(c.lines)-n:])
	return out, c.seq
}

// SyncNow pulls the snapshot and reads SavedVariables straight away.
func (c *Controller) SyncNow() bool {
	return c.do(func() {
		if c.syncer == nil {
			c.connect()
			return
		}
		c.busy("Syncing")
		defer c.busy("")
		c.retryAt = time.Time{}
		c.pull()
		c.scan(true)
	})
}

// Finish settles the raid this account leads from its newest live update.
func (c *Controller) Finish() bool {
	return c.do(func() {
		if c.syncer == nil {
			c.fail(errors.New("not connected to the backend; check Settings"))
			return
		}
		c.busy("Finishing the raid")
		defer c.busy("")
		if err := c.syncer.Finish(); err != nil {
			c.fail(err)
			return
		}
		c.pull()
	})
}

// UploadWitness uploads every raid in SavedVariables as a witness statement, the way
// fairshare-sync -once does: for a raid you attended but did not lead.
func (c *Controller) UploadWitness() bool {
	return c.do(func() {
		if c.syncer == nil {
			c.fail(errors.New("not connected to the backend; check Settings"))
			return
		}
		c.busy("Uploading as a witness")
		defer c.busy("")
		c.syncer.Live = false
		defer func() { c.syncer.Live = true }()
		before := c.witness
		if err := c.syncer.ScanTranscripts(true); err != nil {
			c.fail(err)
			return
		}
		c.ok()
		if c.witness == before {
			c.note("Nothing new to upload: every raid in your SavedVariables was already sent.")
		}
	})
}

// Apply saves new settings and reconnects with them. Validate them first.
func (c *Controller) Apply(cfg config.Config) bool {
	return c.do(func() {
		if err := cfg.Save(c.opt.ConfigPath); err != nil {
			c.fail(fmt.Errorf("could not save settings: %w", err))
			return
		}
		c.mu.Lock()
		c.cfg = cfg
		c.st = Status{Configured: cfg.Complete()}
		c.mu.Unlock()
		c.syncer = nil
		c.logger.Printf("settings saved to %s", c.opt.ConfigPath)
		c.connect()
	})
}

// do queues a request for the loop. False when the queue is full (the loop is stuck on
// a slow request); the window says so instead of blocking.
func (c *Controller) do(f func()) bool {
	select {
	case c.cmds <- f:
		return true
	default:
		return false
	}
}

func (c *Controller) loop() {
	defer close(c.done)
	c.connect()
	poll := time.NewTicker(c.opt.PollEvery)
	snap := time.NewTicker(c.opt.SnapshotEvery)
	defer poll.Stop()
	defer snap.Stop()
	for {
		select {
		case <-c.stop:
			return
		case f := <-c.cmds:
			f()
		case <-poll.C:
			if c.syncer != nil && time.Now().After(c.retryAt) {
				c.scan(false)
			}
		case <-snap.C:
			if c.syncer == nil {
				c.connect()
			} else {
				c.pull()
			}
		}
	}
}

// connect checks the settings and the token, then does a first pull and scan.
func (c *Controller) connect() {
	cfg := c.Config()
	if !cfg.Complete() {
		c.mu.Lock()
		c.st.Configured, c.st.Connected = false, false
		c.mu.Unlock()
		c.note("Not set up yet. Press Settings and paste your sync token from Discord.")
		return
	}
	if st, err := os.Stat(cfg.AddonDir()); err != nil || !st.IsDir() {
		c.fail(fmt.Errorf("the FairShare addon is not installed at %s; install it or pick another game version in Settings", cfg.AddonDir()))
		return
	}
	client := api.New(cfg.BackendURL, cfg.Token)
	me, err := client.Me()
	if err != nil {
		if errors.Is(err, api.ErrUnauthorized) {
			c.fail(errors.New("the backend rejected your sync token; get a new one in Discord (/fairshare, Sync client token) and paste it in Settings"))
		} else {
			c.fail(fmt.Errorf("cannot reach the backend (%v); retrying every minute", err))
		}
		return
	}
	c.mu.Lock()
	c.st.Configured, c.st.Connected, c.st.Account = true, true, me.DisplayName
	c.mu.Unlock()
	c.logger.Printf("signed in as %s at %s", me.DisplayName, cfg.BackendURL)
	s := sync.New(&cfg, client, c.uploads, c.logger)
	s.Live = true
	s.OnEvent = c.onEvent
	c.syncer = s
	c.ok()
	c.note("Connected. Keep this window open while you raid: every /reload in game sends a live update to Discord.")
	c.pull()
	c.scan(true)
}

func (c *Controller) pull() {
	if _, err := c.syncer.PullSnapshot(); err != nil {
		c.fail(fmt.Errorf("snapshot: %w", err))
		return
	}
}

func (c *Controller) scan(force bool) {
	if err := c.syncer.ScanTranscripts(force); err != nil {
		c.fail(err)
		// The file is retried on every tick until it uploads; do not hammer a backend
		// that is down, or re-read a file the game is still writing, every two seconds.
		c.retryAt = time.Now().Add(30 * time.Second)
		return
	}
	c.retryAt = time.Time{}
}

func (c *Controller) onEvent(e sync.Event) {
	c.ok()
	switch e.Kind {
	case sync.EventNoRaid:
		c.mu.Lock()
		c.st.RaidID, c.st.Snapshot, c.st.SnapshotAt = "", "", time.Time{}
		c.mu.Unlock()
		c.note("No open raid led by you. When an officer opens one for you in Discord it appears here within a minute.")
	case sync.EventSnapshot:
		c.mu.Lock()
		c.st.RaidID, c.st.Snapshot = e.RaidID, shortSum(e.Checksum)
		if e.Written {
			c.st.SnapshotAt = time.Now()
		}
		c.mu.Unlock()
		if e.Written {
			c.note(fmt.Sprintf("New snapshot for raid %s written to the addon. Type /reload in game to load it.", e.RaidID))
		}
	case sync.EventLive:
		c.mu.Lock()
		c.st.LastLive, c.st.LastLiveLines = time.Now(), e.Lines
		c.mu.Unlock()
		c.note(fmt.Sprintf("Live update for raid %s sent to Discord (%s).", e.RaidID, lines(e.Lines)))
	case sync.EventWitness:
		c.witness++
		c.note(fmt.Sprintf("Raid %s uploaded as a witness: %s. %s", e.RaidID, e.Status, e.Message))
	case sync.EventFinished:
		c.note(fmt.Sprintf("Raid %s: %s. %s", e.RaidID, e.Status, e.Message))
	}
}

func (c *Controller) note(s string) {
	c.mu.Lock()
	c.st.Note = s
	c.mu.Unlock()
}

func (c *Controller) busy(s string) {
	c.mu.Lock()
	c.st.Busy = s
	c.mu.Unlock()
}

// fail records a problem once per distinct message, so a repeating error is one log line.
func (c *Controller) fail(err error) {
	msg := err.Error()
	c.mu.Lock()
	c.st.Err = msg
	repeat := c.lastErr == msg
	c.lastErr = msg
	c.mu.Unlock()
	if !repeat {
		c.logger.Print("problem: " + msg)
	}
}

func (c *Controller) ok() {
	c.mu.Lock()
	c.st.Err = ""
	c.lastErr = ""
	c.mu.Unlock()
}

func lines(n int) string {
	if n == 1 {
		return "1 line"
	}
	return fmt.Sprintf("%d lines", n)
}

func shortSum(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// lineWriter feeds the logger's output into the in-memory log and the log file.
type lineWriter struct{ c *Controller }

func (w lineWriter) Write(p []byte) (int, error) {
	text := strings.TrimRight(string(p), "\r\n")
	c := w.c
	c.mu.Lock()
	for _, line := range strings.Split(text, "\n") {
		c.lines = append(c.lines, line)
		c.seq++
	}
	if len(c.lines) > maxLines {
		c.lines = append([]string(nil), c.lines[len(c.lines)-maxLines:]...)
	}
	lw := c.logw
	c.mu.Unlock()
	if lw != nil {
		lw.Write([]byte(time.Now().Format("2006-01-02 ") + text + "\n"))
	}
	return len(p), nil
}

// openLog appends to sync.log, starting a fresh one (the old kept as sync.log.old) once it
// passes 1 MB. A log that cannot be opened is not fatal: the window still shows activity.
func openLog(path string) io.WriteCloser {
	if st, err := os.Stat(path); err == nil && st.Size() > 1<<20 {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nopCloser{}
	}
	return f
}

type nopCloser struct{}

func (nopCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopCloser) Close() error                { return nil }

// Validate checks settings before they are saved: the URL is safe for the token, the
// addon is installed in the chosen game version, and the backend accepts the token.
// Returns the account's Discord display name.
func Validate(cfg config.Config) (string, error) {
	if err := config.CheckBackendURL(cfg.BackendURL); err != nil {
		return "", err
	}
	if strings.TrimSpace(cfg.Token) == "" {
		return "", errors.New("paste your sync token (Discord: /fairshare, Sync client token)")
	}
	if cfg.WoWDir == "" || cfg.Flavor == "" {
		return "", errors.New("pick your World of Warcraft folder and game version")
	}
	if st, err := os.Stat(cfg.AddonDir()); err != nil || !st.IsDir() {
		return "", fmt.Errorf("the FairShare addon is not installed at %s", cfg.AddonDir())
	}
	me, err := api.New(cfg.BackendURL, cfg.Token).Me()
	if errors.Is(err, api.ErrUnauthorized) {
		return "", errors.New("the backend rejected this token; get a new one in Discord (/fairshare, Sync client token)")
	}
	if err != nil {
		return "", fmt.Errorf("cannot reach the backend: %v", err)
	}
	return me.DisplayName, nil
}
