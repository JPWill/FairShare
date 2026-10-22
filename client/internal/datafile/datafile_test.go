package datafile

import (
	"os"
	"path/filepath"
	"testing"
)

const snapshot = "FAIRSHARE SNAPSHOT 1\nraid=1\nissued=1700000000\noffspec=0\nfloor:DEFAULT=10\nrow:Al\"ice-Realm\tAl\\ice\t0\t100"

const golden = "FairShareData = {\n" +
	"    version = 1,\n" +
	"    snapshot = \"FAIRSHARE SNAPSHOT 1\\nraid=1\\nissued=1700000000\\noffspec=0\\nfloor:DEFAULT=10\\nrow:Al\\\"ice-Realm\tAl\\\\ice\t0\t100\",\n" +
	"    checksum = \"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\",\n" +
	"    writtenAt = 1700000500,\n" +
	"}\n"

func TestRenderGolden(t *testing.T) {
	got := Render(snapshot, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", 1700000500)
	if got != golden {
		t.Fatalf("Data.lua mismatch\n got: %q\nwant: %q", got, golden)
	}
}

func TestWriteAndExistingChecksum(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Data.lua")
	sum := Checksum(snapshot)
	if len(sum) != 64 {
		t.Fatalf("checksum length %d", len(sum))
	}
	if err := Write(path, snapshot, sum, 1); err != nil {
		t.Fatal(err)
	}
	if got := ExistingChecksum(path); got != sum {
		t.Fatalf("existing checksum = %q, want %q", got, sum)
	}
	// Overwrite works (Windows rename-over-existing path).
	if err := Write(path, snapshot+"\nban:X-Y", "f"+sum[1:], 2); err != nil {
		t.Fatal(err)
	}
	if got := ExistingChecksum(path); got != "f"+sum[1:] {
		t.Fatalf("after overwrite = %q", got)
	}
	leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
	data, _ := os.ReadFile(path)
	if string(data) != Render(snapshot+"\nban:X-Y", "f"+sum[1:], 2) {
		t.Fatal("file content differs from Render")
	}
}

func TestWriteMissingFolder(t *testing.T) {
	err := Write(filepath.Join(t.TempDir(), "nope", "Data.lua"), snapshot, Checksum(snapshot), 1)
	if err == nil {
		t.Fatal("expected error for missing addon folder")
	}
}

func TestChecksumKnown(t *testing.T) {
	// sha256("abc")
	if got := Checksum("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("checksum(abc) = %s", got)
	}
}
