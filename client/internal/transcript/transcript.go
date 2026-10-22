// Package transcript rebuilds the canonical transcript text (docs/protocol.md §5)
// from the addon's SavedVariables (§6).
package transcript

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/JPWill/FairShare/client/internal/luatable"
)

// Transcript is one raid's transcript as recorded by one client.
type Transcript struct {
	RaidID   string
	Snapshot string // checksum of the snapshot the raid ran on
	Player   string // Name-Realm of the recording client
	Version  string // addon version
	Lines    []string
}

// Text renders the canonical transcript text: header lines then the recorded lines,
// joined by \n with no trailing newline.
func (t *Transcript) Text() string {
	var b strings.Builder
	b.WriteString("FAIRSHARE TRANSCRIPT 1\n")
	fmt.Fprintf(&b, "raid=%s\n", t.RaidID)
	fmt.Fprintf(&b, "snapshot=%s\n", t.Snapshot)
	fmt.Fprintf(&b, "player=%s\n", t.Player)
	fmt.Fprintf(&b, "version=%s", t.Version)
	for _, l := range t.Lines {
		b.WriteByte('\n')
		b.WriteString(l)
	}
	return b.String()
}

// Hash identifies this transcript's content for upload de-duplication.
func (t *Transcript) Hash() string {
	sum := sha256.Sum256([]byte(t.Text()))
	return hex.EncodeToString(sum[:])
}

// FromSavedVariables extracts every raid's transcript from a parsed SavedVariables
// file. Raids with no lines are skipped. The snapshot checksum and addon version
// come from the transcript entry itself when the addon recorded them there, and
// otherwise from FairShareDB.snapshot when it belongs to the same raid.
func FromSavedVariables(globals map[string]luatable.Value) ([]*Transcript, error) {
	db, ok := globals["FairShareDB"].(*luatable.Table)
	if !ok {
		return nil, fmt.Errorf("FairShareDB not found in SavedVariables")
	}
	snap := db.GetTable("snapshot")
	all := db.GetTable("transcript")
	var out []*Transcript
	for _, raidID := range all.StringKeys() {
		entry := all.GetTable(raidID)
		if entry == nil {
			continue
		}
		lines := entry.GetTable("lines").Strings()
		if len(lines) == 0 {
			continue
		}
		t := &Transcript{
			RaidID:   raidID,
			Snapshot: entry.GetString("snapshot"),
			Player:   entry.GetString("player"),
			Version:  entry.GetString("version"),
			Lines:    lines,
		}
		if t.Snapshot == "" && snap != nil && snap.GetString("raidID") == raidID {
			t.Snapshot = snap.GetString("checksum")
		}
		out = append(out, t)
	}
	return out, nil
}
