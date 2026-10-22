package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFlavorsAndPaths(t *testing.T) {
	wow := t.TempDir()
	for _, f := range []string{"_classic_beta_", "_retail_", "_custom_"} {
		if err := os.MkdirAll(filepath.Join(wow, f, "Interface", "AddOns", "FairShare"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(wow, "_nointerface_"), 0o755)
	os.MkdirAll(filepath.Join(wow, "_classic_beta_", "WTF", "Account", "ACCOUNT1", "SavedVariables"), 0o755)
	os.MkdirAll(filepath.Join(wow, "_classic_beta_", "WTF", "Account", "ACCOUNT2"), 0o755)
	os.MkdirAll(filepath.Join(wow, "_classic_beta_", "WTF", "Account", "SavedVariables"), 0o755)

	got := DetectFlavors(wow)
	want := []string{"_classic_beta_", "_retail_", "_custom_"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DetectFlavors = %v, want %v", got, want)
	}

	c := &Config{BackendURL: "http://x", Token: "t", WoWDir: wow, Flavor: "_classic_beta_"}
	if !c.Complete() {
		t.Fatal("config should be complete")
	}
	if c.DataLuaPath() != filepath.Join(wow, "_classic_beta_", "Interface", "AddOns", "FairShare", "Data.lua") {
		t.Fatalf("DataLuaPath = %s", c.DataLuaPath())
	}
	paths, err := c.SavedVariablesPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || !strings.HasSuffix(paths[0], filepath.Join("ACCOUNT1", "SavedVariables", "FairShare.lua")) {
		t.Fatalf("SavedVariablesPaths = %v", paths)
	}
}

func TestInteractiveAndSave(t *testing.T) {
	wow := t.TempDir()
	os.MkdirAll(filepath.Join(wow, "_classic_beta_", "Interface"), 0o755)
	os.MkdirAll(filepath.Join(wow, "_retail_", "Interface"), 0o755)
	c := &Config{}
	in := strings.NewReader("https://example.test\nTOKEN123\n" + wow + "\n1\n")
	var out strings.Builder
	if err := c.Interactive(in, &out); err != nil {
		t.Fatal(err)
	}
	if c.BackendURL != "https://example.test" || c.Token != "TOKEN123" || c.WoWDir != wow || c.Flavor != "_classic_beta_" {
		t.Fatalf("config = %+v", c)
	}
	if !strings.Contains(out.String(), "_retail_") {
		t.Fatal("flavor menu not shown")
	}
	path := filepath.Join(t.TempDir(), "config.json")
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil || *back != *c {
		t.Fatalf("round trip = %+v, %v", back, err)
	}
	if empty, err := Load(filepath.Join(t.TempDir(), "missing.json")); err != nil || empty.Complete() {
		t.Fatalf("missing config = %+v, %v", empty, err)
	}
}
