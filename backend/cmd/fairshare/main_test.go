package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JPWill/FairShare/backend/internal/config"
)

func TestDemoAlongside(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "demo.db")
	cfg := config.Config{HTTPAddr: "127.0.0.1:8080", DemoHTTPAddr: "127.0.0.1:8081", DemoDBPath: db}

	if r := demoAlongside(cfg); !strings.Contains(r, "no demo database") {
		t.Fatalf("missing file: %q", r)
	}
	if err := os.WriteFile(db, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := demoAlongside(cfg); r != "" {
		t.Fatalf("file present: %q", r)
	}
	cfg.DemoHTTPAddr = "off"
	if r := demoAlongside(cfg); !strings.Contains(r, "off") {
		t.Fatalf("off: %q", r)
	}
	cfg.DemoHTTPAddr = cfg.HTTPAddr
	if r := demoAlongside(cfg); !strings.Contains(r, "own address") {
		t.Fatalf("same address: %q", r)
	}
	cfg.DemoHTTPAddr, cfg.DemoDBPath = "127.0.0.1:8081", ""
	if r := demoAlongside(cfg); !strings.Contains(r, "empty") {
		t.Fatalf("empty path: %q", r)
	}
}
