// Package datafile writes the addon's Data.lua: the current snapshot as a Lua table
// literal, per docs/protocol.md §4.
package datafile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Render produces the exact Data.lua contents for a snapshot.
func Render(snapshotText, checksum string, writtenAt int64) string {
	return "FairShareData = {\n" +
		"    version = 1,\n" +
		"    snapshot = \"" + escape(snapshotText) + "\",\n" +
		"    checksum = \"" + checksum + "\",\n" +
		fmt.Sprintf("    writtenAt = %d,\n", writtenAt) +
		"}\n"
}

// escape turns raw text into the body of a double-quoted Lua string. Only backslash,
// double quote, newline and carriage return need escaping; tabs are legal as-is.
func escape(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// Checksum is the SHA-256 hex of the snapshot text, as the backend and addon compute it.
func Checksum(snapshotText string) string {
	sum := sha256.Sum256([]byte(snapshotText))
	return hex.EncodeToString(sum[:])
}

var checksumRe = regexp.MustCompile(`checksum = "([0-9a-f]{64})"`)

// ExistingChecksum reads the checksum recorded in an existing Data.lua, or "" if the
// file is missing or unreadable.
func ExistingChecksum(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	m := checksumRe.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

// Write stores Data.lua atomically: temp file in the same directory, then rename.
func Write(path, snapshotText, checksum string, writtenAt int64) error {
	dir := filepath.Dir(path)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("addon folder not found: %s (is FairShare installed there?)", dir)
	}
	tmp, err := os.CreateTemp(dir, "Data.lua.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.WriteString(Render(snapshotText, checksum, writtenAt)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	// Windows refuses to rename over an existing file in some cases; remove first.
	_ = os.Remove(path)
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
