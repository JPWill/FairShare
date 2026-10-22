// Package sync does the two jobs of the client: pull the current snapshot into the
// addon's Data.lua, and push new transcripts from SavedVariables to the backend.
package sync

import (
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/JPWill/FairShare/client/internal/api"
	"github.com/JPWill/FairShare/client/internal/config"
	"github.com/JPWill/FairShare/client/internal/datafile"
	"github.com/JPWill/FairShare/client/internal/luatable"
	"github.com/JPWill/FairShare/client/internal/transcript"
)

// Syncer holds everything one run needs.
type Syncer struct {
	Cfg     *config.Config
	API     *api.Client
	Uploads *config.Uploads
	Log     *log.Logger

	// Live makes every scan send progress updates for the raid this account leads
	// (the backend refreshes its live ledger and settles nothing) instead of witness
	// uploads. Watch mode sets it; -once leaves it off.
	Live bool

	// OnEvent, when set, is told about every outcome a window would show: a snapshot
	// written or already current, no raid, a live update, a witness upload, a finish.
	// It is called on the goroutine running the Syncer.
	OnEvent func(Event)

	lastSnapshotErr string
	seen            map[string]fileStamp // SavedVariables path -> last observed stamp
	currentRaid     string               // raid id from the last successful snapshot pull
	skipped         map[string]bool      // raid ids already reported as "not yours" in live mode
}

// EventKind names what happened.
type EventKind int

const (
	EventNoRaid   EventKind = iota // the account leads no open raid
	EventSnapshot                  // Data.lua holds the current snapshot (Written: just now)
	EventLive                      // a live update reached the backend
	EventWitness                   // a witness upload was accepted
	EventFinished                  // the raid was settled (or its status changed) by Finish
)

// Event is one outcome, for a status display.
type Event struct {
	Kind     EventKind
	RaidID   string
	Checksum string // EventSnapshot
	Written  bool   // EventSnapshot: Data.lua was rewritten by this pull
	Lines    int    // EventLive, EventWitness
	Status   string // backend status for uploads and finish
	Message  string // backend message
}

func (s *Syncer) emit(e Event) {
	if s.OnEvent != nil {
		s.OnEvent(e)
	}
}

type fileStamp struct {
	mod  time.Time
	size int64
}

// New wires a Syncer.
func New(cfg *config.Config, client *api.Client, uploads *config.Uploads, logger *log.Logger) *Syncer {
	return &Syncer{Cfg: cfg, API: client, Uploads: uploads, Log: logger, seen: map[string]fileStamp{}, skipped: map[string]bool{}}
}

// CurrentRaid returns the id of the raid this account leads, asking the backend if the
// snapshot has not been pulled yet. Empty with api.ErrNoRaid when there is none.
func (s *Syncer) CurrentRaid() (string, error) {
	if s.currentRaid != "" {
		return s.currentRaid, nil
	}
	snap, err := s.API.CurrentSnapshot()
	if err != nil {
		return "", err
	}
	s.currentRaid = snap.RaidID
	return snap.RaidID, nil
}

// Finish settles the raid this account leads from the last live update the backend holds.
func (s *Syncer) Finish() error {
	id, err := s.CurrentRaid()
	if errors.Is(err, api.ErrNoRaid) {
		return errors.New("you are not leading an open raid; nothing to finish")
	}
	if err != nil {
		return err
	}
	// Send whatever the game has written since the last tick first, so the settlement
	// is from the newest file.
	if err := s.ScanTranscripts(true); err != nil {
		s.Log.Printf("transcripts: %v", err)
	}
	res, err := s.API.Finish(id)
	if err != nil {
		return fmt.Errorf("finish raid %s: %w", id, err)
	}
	s.Log.Printf("raid %s: %s. %s", id, res.Status, res.Message)
	s.emit(Event{Kind: EventFinished, RaidID: id, Status: res.Status, Message: res.Message})
	if res.Status == "settled" {
		s.currentRaid = ""
	}
	return nil
}

// PullSnapshot fetches the current snapshot and writes Data.lua if it changed.
// Returns true when a new file was written.
func (s *Syncer) PullSnapshot() (bool, error) {
	snap, err := s.API.CurrentSnapshot()
	if errors.Is(err, api.ErrNoRaid) {
		s.currentRaid = ""
		// A finished raid's snapshot must not linger in the addon folder: the addon would
		// keep loading it. Remove the file this client wrote.
		if path := s.Cfg.DataLuaPath(); datafile.ExistingChecksum(path) != "" {
			if err := os.Remove(path); err != nil {
				s.Log.Printf("could not remove the old Data.lua: %v", err)
			} else {
				s.Log.Print("removed Data.lua: you lead no open raid; /reload in game to drop the old snapshot")
			}
		}
		s.logOnce("snapshot", "no open raid led by you; nothing to write")
		s.emit(Event{Kind: EventNoRaid})
		return false, nil
	}
	if err != nil {
		return false, err
	}
	s.currentRaid = snap.RaidID
	if got := datafile.Checksum(snap.Text); got != snap.Checksum {
		return false, fmt.Errorf("snapshot checksum mismatch: backend says %s, text hashes to %s", snap.Checksum, got)
	}
	path := s.Cfg.DataLuaPath()
	if datafile.ExistingChecksum(path) == snap.Checksum {
		s.logOnce("snapshot", fmt.Sprintf("Data.lua already holds snapshot %s for raid %s", short(snap.Checksum), snap.RaidID))
		s.emit(Event{Kind: EventSnapshot, RaidID: snap.RaidID, Checksum: snap.Checksum})
		return false, nil
	}
	if err := datafile.Write(path, snap.Text, snap.Checksum, time.Now().Unix()); err != nil {
		return false, err
	}
	s.lastSnapshotErr = ""
	s.Log.Printf("wrote snapshot %s for raid %s to Data.lua; /reload in game to load it", short(snap.Checksum), snap.RaidID)
	s.emit(Event{Kind: EventSnapshot, RaidID: snap.RaidID, Checksum: snap.Checksum, Written: true})
	return true, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// short is the 8-character checksum people compare with the bot's post.
func short(checksum string) string {
	if len(checksum) > 8 {
		return checksum[:8]
	}
	return checksum
}

// logOnce prints a status line only when it differs from the last one for that key.
func (s *Syncer) logOnce(key, msg string) {
	if s.lastSnapshotErr == key+msg {
		return
	}
	s.lastSnapshotErr = key + msg
	s.Log.Print(msg)
}

// ScanTranscripts reads every account's SavedVariables and uploads anything new.
// With force=false only files whose mtime/size changed since the last scan are read.
func (s *Syncer) ScanTranscripts(force bool) error {
	paths, err := s.Cfg.SavedVariablesPaths()
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue // this account has never saved FairShare variables
		}
		stamp := fileStamp{mod: st.ModTime(), size: st.Size()}
		if !force {
			if prev, ok := s.seen[p]; ok && prev == stamp {
				continue
			}
		}
		if err := s.uploadFrom(p); err != nil {
			s.Log.Printf("%s: %v", p, err)
			if firstErr == nil {
				firstErr = err
			}
			continue // leave the stamp unset so the next tick retries
		}
		s.seen[p] = stamp
	}
	return firstErr
}

func (s *Syncer) uploadFrom(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	globals, err := luatable.Parse(string(data))
	if err != nil {
		return fmt.Errorf("could not parse (the game may still be writing it): %w", err)
	}
	list, err := transcript.FromSavedVariables(globals)
	if err != nil {
		return err
	}
	for _, t := range list {
		// Dev raids (ids starting with "dev") are the addon's sandbox; the backend has no
		// such raid and would reject them on every tick.
		if strings.HasPrefix(t.RaidID, "dev") {
			continue
		}
		if s.Live {
			if err := s.sendLive(t); err != nil {
				return err
			}
			continue
		}
		hash := t.Hash()
		if s.Uploads.Seen(t.RaidID, hash) {
			continue
		}
		res, err := s.API.UploadTranscript(t.RaidID, t.Text())
		if err != nil {
			return fmt.Errorf("upload raid %s: %w", t.RaidID, err)
		}
		s.Log.Printf("uploaded transcript for raid %s (%d line%s): %s %s", t.RaidID, len(t.Lines), plural(len(t.Lines)), res.Status, res.Message)
		s.emit(Event{Kind: EventWitness, RaidID: t.RaidID, Lines: len(t.Lines), Status: res.Status, Message: res.Message})
		if err := s.Uploads.Remember(t.RaidID, hash); err != nil {
			s.Log.Printf("could not record upload memory: %v", err)
		}
	}
	return nil
}

// sendLive posts one transcript as a progress update when it belongs to the raid this
// account leads. Other raids are skipped (once, with a note): the backend only takes live
// updates from the leader, and a witness upload of someone else's raid is a deliberate
// act (-once or the bot's upload command), never a side effect of watching.
// Live uploads are remembered under their own key, so a later Finish or -once never
// mistakes a live update for a witness upload already sent.
func (s *Syncer) sendLive(t *transcript.Transcript) error {
	current, err := s.CurrentRaid()
	if errors.Is(err, api.ErrNoRaid) {
		s.logOnce("live", "no open raid led by you; transcripts are not uploaded in watch mode")
		return nil
	}
	if err != nil {
		return err
	}
	if t.RaidID != current {
		if !s.skipped[t.RaidID] {
			s.skipped[t.RaidID] = true
			s.Log.Printf("raid %s is not the raid you lead (%s); upload it with -once or the bot's upload command if it still needs settling", t.RaidID, current)
		}
		return nil
	}
	hash := t.Hash()
	if s.Uploads.Seen("live:"+t.RaidID, hash) {
		return nil
	}
	res, err := s.API.UploadProgress(t.RaidID, t.Text())
	if err != nil {
		return fmt.Errorf("live update for raid %s: %w", t.RaidID, err)
	}
	s.Log.Printf("live update sent (%d line%s): %s", len(t.Lines), plural(len(t.Lines)), res.Message)
	s.emit(Event{Kind: EventLive, RaidID: t.RaidID, Lines: len(t.Lines), Status: res.Status, Message: res.Message})
	if err := s.Uploads.Remember("live:"+t.RaidID, hash); err != nil {
		s.Log.Printf("could not record upload memory: %v", err)
	}
	return nil
}

// Once pulls the snapshot and uploads transcripts a single time.
func (s *Syncer) Once() error {
	_, perr := s.PullSnapshot()
	if perr != nil {
		s.Log.Printf("snapshot: %v", perr)
	}
	terr := s.ScanTranscripts(true)
	if perr != nil {
		return perr
	}
	return terr
}

// Watch loops until stop is closed: transcripts every pollEvery, snapshot every snapshotEvery.
func (s *Syncer) Watch(pollEvery, snapshotEvery time.Duration, stop <-chan struct{}) {
	s.Live = true
	if _, err := s.PullSnapshot(); err != nil {
		s.Log.Printf("snapshot: %v", err)
	}
	if err := s.ScanTranscripts(true); err != nil {
		s.Log.Printf("transcripts: %v", err)
	}
	s.Log.Printf("watching %s every %s; press Ctrl+C to stop", s.Cfg.FlavorDir(), pollEvery)
	s.Log.Print("every /reload in game sends a live update to Discord; nothing settles until you finish")
	s.Log.Print("after the last boss: /reload in game, then run fairshare-sync -finish or press Finish raid in Discord")
	poll := time.NewTicker(pollEvery)
	snap := time.NewTicker(snapshotEvery)
	defer poll.Stop()
	defer snap.Stop()
	for {
		select {
		case <-stop:
			return
		case <-poll.C:
			_ = s.ScanTranscripts(false)
		case <-snap.C:
			if _, err := s.PullSnapshot(); err != nil {
				s.logOnce("snapshot-err", "snapshot: "+err.Error())
			}
		}
	}
}
