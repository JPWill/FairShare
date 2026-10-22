// Command release packages addon/FairShare into dist/FairShare-<version>.zip, excluding
// Data.lua (written per machine by the sync client) and anything that is not part of the
// shipped addon. Run from the repo root: go run ./tools/release
package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const addonDir = "addon/FairShare"

var skip = map[string]bool{
	"Data.lua":  true,
	"README.md": true, // folder notes, not needed in-game
	".pkgmeta":  true, // packager manifest for the release workflow
}

func main() {
	version, err := tocVersion(filepath.Join(addonDir, "FairShare.toc"))
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll("dist", 0o755); err != nil {
		fail(err)
	}
	out := filepath.Join("dist", "FairShare-"+version+".zip")
	f, err := os.Create(out)
	if err != nil {
		fail(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	n := 0
	err = filepath.WalkDir(addonDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if skip[d.Name()] {
			return nil
		}
		rel, _ := filepath.Rel(addonDir, path)
		w, err := zw.Create("FairShare/" + filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()
		if _, err := io.Copy(w, src); err != nil {
			return err
		}
		n++
		return nil
	})
	if err != nil {
		fail(err)
	}
	// The MIT licence rides along inside the addon folder so a zip downloaded on its own
	// still carries it. The addon has no third-party code, so no other notice is needed.
	lic, err := os.ReadFile("LICENSE")
	if err != nil {
		fail(fmt.Errorf("LICENSE must sit at the repo root: %w", err))
	}
	w, err := zw.Create("FairShare/LICENSE.txt")
	if err != nil {
		fail(err)
	}
	if _, err := w.Write(lic); err != nil {
		fail(err)
	}
	n++
	if err := zw.Close(); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s (%d files)\n", out, n)
}

func tocVersion(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "## Version:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "## Version:")), nil
		}
	}
	return "", fmt.Errorf("no ## Version in %s", path)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "release:", err)
	os.Exit(1)
}
