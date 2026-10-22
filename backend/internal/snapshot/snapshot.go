// Package snapshot builds and parses the canonical snapshot text (docs/protocol.md §4).
// The text is the thing that is checksummed, so it must be byte-identical to what the
// addon reconstructs; keep this file boring.
package snapshot

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Row is one character's standing.
type Row struct {
	Character string // bare in-game name (docs/protocol.md §1)
	Owner     string // Discord display name
	Balance   int64
	Credit    int64
}

// RosterEntry is one confirmed or standby signup carried into the raid (§4).
type RosterEntry struct {
	Name   string // character name
	Role   string // tank | healer | melee | ranged
	Status string // confirmed | standby
}

// Snapshot is the per-raid standings issued to the leader.
type Snapshot struct {
	RaidID     string
	Issued     int64
	OffspecPct int64
	Starts     int64            // scheduled start (unix); the addon uses the snapshot only near it. 0 = not set
	Party      bool             // a 5-player party counts as the raid group (testing); raid groups only otherwise
	Floors     map[string]int64 // itemEquipLoc -> floor; DEFAULT required
	Banned     []string
	Roster     []RosterEntry // optional; empty when no roster was confirmed
	Rows       []Row
}

// Text renders the canonical text. Lines joined with \n, no trailing newline.
func (s Snapshot) Text() string {
	var b strings.Builder
	b.WriteString("FAIRSHARE SNAPSHOT 1\n")
	fmt.Fprintf(&b, "raid=%s\n", clean(s.RaidID))
	fmt.Fprintf(&b, "issued=%d\n", s.Issued)
	fmt.Fprintf(&b, "offspec=%d", s.OffspecPct)
	if s.Starts > 0 {
		fmt.Fprintf(&b, "\nstarts=%d", s.Starts)
	}
	if s.Party {
		b.WriteString("\nparty=1")
	}

	keys := make([]string, 0, len(s.Floors))
	for k := range s.Floors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&b, "\nfloor:%s=%d", clean(k), s.Floors[k])
	}

	banned := append([]string(nil), s.Banned...)
	sort.Strings(banned)
	for _, n := range banned {
		fmt.Fprintf(&b, "\nban:%s", clean(n))
	}

	roster := append([]RosterEntry(nil), s.Roster...)
	sort.Slice(roster, func(i, j int) bool { return roster[i].Name < roster[j].Name })
	for _, r := range roster {
		fmt.Fprintf(&b, "\nroster:%s\t%s\t%s", clean(r.Name), clean(r.Role), clean(r.Status))
	}

	rows := append([]Row(nil), s.Rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Character < rows[j].Character })
	for _, r := range rows {
		fmt.Fprintf(&b, "\nrow:%s\t%s\t%d\t%d", clean(r.Character), clean(r.Owner), r.Balance, r.Credit)
	}
	return b.String()
}

// clean strips the characters that would break the line/tab structure.
func clean(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", "", ",", " ").Replace(s)
}

// Checksum is the SHA-256 hex of the exact text.
func Checksum(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// Short is the first 8 hex chars of a checksum.
func Short(checksum string) string {
	if len(checksum) < 8 {
		return checksum
	}
	return checksum[:8]
}

// Paste is the string a leader pastes into the addon.
func Paste(text string) string {
	return base64.StdEncoding.EncodeToString([]byte(text))
}

// DataLua renders the file the sync client writes into the addon folder.
func DataLua(text, checksum string, writtenAt int64) string {
	return fmt.Sprintf("FairShareData = {\n    version = 1,\n    snapshot = %s,\n    checksum = %q,\n    writtenAt = %d,\n}\n",
		luaString(text), checksum, writtenAt)
}

// luaString quotes s as a Lua double-quoted string literal.
func luaString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				fmt.Fprintf(&b, `\%03d`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Parse reads canonical text back. It accepts only well-formed input.
func Parse(text string) (Snapshot, error) {
	s := Snapshot{Floors: map[string]int64{}}
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || lines[0] != "FAIRSHARE SNAPSHOT 1" {
		return s, fmt.Errorf("not a FairShare snapshot")
	}
	for i, line := range lines[1:] {
		switch {
		case strings.HasPrefix(line, "raid="):
			s.RaidID = line[5:]
		case strings.HasPrefix(line, "issued="):
			n, err := strconv.ParseInt(line[7:], 10, 64)
			if err != nil {
				return s, fmt.Errorf("line %d: bad issued", i+2)
			}
			s.Issued = n
		case strings.HasPrefix(line, "offspec="):
			n, err := strconv.ParseInt(line[8:], 10, 64)
			if err != nil {
				return s, fmt.Errorf("line %d: bad offspec", i+2)
			}
			s.OffspecPct = n
		case strings.HasPrefix(line, "starts="):
			n, err := strconv.ParseInt(line[7:], 10, 64)
			if err != nil {
				return s, fmt.Errorf("line %d: bad starts", i+2)
			}
			s.Starts = n
		case line == "party=1":
			s.Party = true
		case strings.HasPrefix(line, "floor:"):
			k, v, ok := strings.Cut(line[6:], "=")
			n, err := strconv.ParseInt(v, 10, 64)
			if !ok || err != nil {
				return s, fmt.Errorf("line %d: bad floor", i+2)
			}
			s.Floors[k] = n
		case strings.HasPrefix(line, "ban:"):
			s.Banned = append(s.Banned, line[4:])
		case strings.HasPrefix(line, "roster:"):
			f := strings.Split(line[7:], "\t")
			if len(f) != 3 {
				return s, fmt.Errorf("line %d: bad roster line", i+2)
			}
			s.Roster = append(s.Roster, RosterEntry{Name: f[0], Role: f[1], Status: f[2]})
		case strings.HasPrefix(line, "row:"):
			f := strings.Split(line[4:], "\t")
			if len(f) != 4 {
				return s, fmt.Errorf("line %d: bad row", i+2)
			}
			bal, err1 := strconv.ParseInt(f[2], 10, 64)
			cr, err2 := strconv.ParseInt(f[3], 10, 64)
			if err1 != nil || err2 != nil {
				return s, fmt.Errorf("line %d: bad row numbers", i+2)
			}
			s.Rows = append(s.Rows, Row{Character: f[0], Owner: f[1], Balance: bal, Credit: cr})
		case line == "":
			continue
		default:
			return s, fmt.Errorf("line %d: unknown line %q", i+2, line)
		}
	}
	if _, ok := s.Floors["DEFAULT"]; !ok {
		return s, fmt.Errorf("snapshot has no DEFAULT floor")
	}
	return s, nil
}

// FloorsCSV is the floor table as a SNAP line carries it: KEY=value pairs sorted by
// key byte order, comma-joined (docs/protocol.md §2).
func (s Snapshot) FloorsCSV() string {
	keys := make([]string, 0, len(s.Floors))
	for k := range s.Floors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, s.Floors[k]))
	}
	return strings.Join(parts, ",")
}

// Floor returns the main-spec floor for an itemEquipLoc key, falling back to DEFAULT.
func (s Snapshot) Floor(key string) int64 {
	if v, ok := s.Floors[key]; ok {
		return v
	}
	return s.Floors["DEFAULT"]
}
