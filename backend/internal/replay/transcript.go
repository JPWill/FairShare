// Package replay re-runs a raid from an addon transcript: it verifies every commit and
// reveal, recomputes every winner and price with the rules in docs/protocol.md §3,
// compares transcripts from different uploaders, and produces settlement deltas.
package replay

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// Line is one transcript entry.
type Line struct {
	T      int64
	Dir    string // in | loot | note
	Sender string
	Body   string // decoded message body, fields still escaped
}

// Transcript is one client's record of a raid.
type Transcript struct {
	RaidID           string
	SnapshotChecksum string
	Player           string
	Version          string
	Lines            []Line
}

// Message is a decoded addon message.
type Message struct {
	Type   string
	Fields []string
}

// ParseTranscript reads canonical transcript text (§5). It also accepts the base64
// paste string form.
func ParseTranscript(text string) (*Transcript, error) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "FAIRSHARE TRANSCRIPT") {
		dec, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(text), ""))
		if err != nil {
			return nil, fmt.Errorf("not a FairShare transcript (and not base64)")
		}
		text = strings.TrimSpace(string(dec))
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if lines[0] != "FAIRSHARE TRANSCRIPT 1" {
		return nil, fmt.Errorf("unsupported transcript header %q", lines[0])
	}
	tr := &Transcript{}
	for i, line := range lines[1:] {
		if line == "" {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && !strings.Contains(k, "\t") {
			switch k {
			case "raid":
				tr.RaidID = v
				continue
			case "snapshot":
				tr.SnapshotChecksum = v
				continue
			case "player":
				tr.Player = v
				continue
			case "version":
				tr.Version = v
				continue
			}
		}
		f := strings.SplitN(line, "\t", 4)
		if len(f) != 4 {
			return nil, fmt.Errorf("line %d: expected 4 tab-separated fields", i+2)
		}
		t, err := strconv.ParseInt(f[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: bad time", i+2)
		}
		tr.Lines = append(tr.Lines, Line{T: t, Dir: f[1], Sender: f[2], Body: f[3]})
	}
	if tr.RaidID == "" {
		return nil, fmt.Errorf("transcript has no raid id")
	}
	return tr, nil
}

// Decode splits a message body into type and unescaped fields (§2).
func Decode(body string) Message {
	parts := strings.Split(body, "^")
	m := Message{Type: parts[0]}
	for _, p := range parts[1:] {
		m.Fields = append(m.Fields, unescape(p))
	}
	return m
}

// Encode is the inverse of Decode, used by tests to build transcripts.
func Encode(typ string, fields ...string) string {
	out := []string{typ}
	for _, f := range fields {
		out = append(out, escape(f))
	}
	return strings.Join(out, "^")
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "^", "~1")
}

func unescape(s string) string {
	if !strings.Contains(s, "~") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '~' && i+1 < len(s) {
			switch s[i+1] {
			case '0':
				b.WriteByte('~')
				i++
				continue
			case '1':
				b.WriteByte('^')
				i++
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// field returns fields[i] or "".
func (m Message) field(i int) string {
	if i < len(m.Fields) {
		return m.Fields[i]
	}
	return ""
}

// protocolTypes are the message types that matter for replay and comparison.
var protocolTypes = map[string]bool{
	"SNAP": true, "ROWS": true, "OPEN": true, "COMMIT": true, "CLOSE": true, "REVEAL": true, "RESULT": true, "KILL": true, "TRANSFER": true, "VOID": true, "LEAD": true, "ACK": true, "ABANDON": true,
}

// Text renders the transcript back to canonical text (§5).
func (tr *Transcript) Text() string {
	var b strings.Builder
	b.WriteString("FAIRSHARE TRANSCRIPT 1\nraid=" + tr.RaidID + "\nsnapshot=" + tr.SnapshotChecksum + "\nplayer=" + tr.Player + "\nversion=" + tr.Version)
	for _, l := range tr.Lines {
		fmt.Fprintf(&b, "\n%d\t%s\t%s\t%s", l.T, l.Dir, l.Sender, l.Body)
	}
	return b.String()
}
