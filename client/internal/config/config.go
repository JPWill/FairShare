// Package config stores the sync client's settings and upload memory under the
// user's config directory (%APPDATA%\FairShare on Windows). The token lives here and
// nowhere else.
package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is config.json.
type Config struct {
	BackendURL string `json:"backendUrl"`
	Token      string `json:"token"`
	WoWDir     string `json:"wowDir"` // e.g. C:\Program Files (x86)\World of Warcraft
	Flavor     string `json:"flavor"` // e.g. _classic_beta_
}

// Dir returns the config directory, creating it if needed.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "FairShare")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// DefaultPath is where config.json lives.
func DefaultPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads config.json. A missing file yields an empty Config and no error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config %s is not valid JSON: %w", path, err)
	}
	return &c, nil
}

// Save writes config.json with owner-only permissions.
func (c *Config) Save(path string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// CheckBackendURL refuses a URL that would send the token in the clear: plain http is
// allowed only to this machine (a backend run locally for testing).
func CheckBackendURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("the backend URL must look like https://fairsharewow.com")
	}
	if u.Scheme == "http" {
		switch u.Hostname() {
		case "localhost", "127.0.0.1", "::1":
		default:
			return fmt.Errorf("use https:// for %s: plain http would send your token unencrypted", u.Hostname())
		}
	}
	return nil
}

// Complete reports whether every required field is set.
func (c *Config) Complete() bool {
	return c.BackendURL != "" && c.Token != "" && c.WoWDir != "" && c.Flavor != ""
}

// FlavorDir is the game-version folder, e.g. <wow>\_classic_beta_.
func (c *Config) FlavorDir() string { return filepath.Join(c.WoWDir, c.Flavor) }

// DataLuaPath is where the addon's Data.lua is written.
func (c *Config) DataLuaPath() string {
	return filepath.Join(c.FlavorDir(), "Interface", "AddOns", "FairShare", "Data.lua")
}

// AddonDir is the addon folder itself.
func (c *Config) AddonDir() string {
	return filepath.Join(c.FlavorDir(), "Interface", "AddOns", "FairShare")
}

// SavedVariablesPaths lists FairShare.lua under every account folder, existing or not.
func (c *Config) SavedVariablesPaths() ([]string, error) {
	accounts, err := c.AccountDirs()
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(accounts))
	for _, a := range accounts {
		paths = append(paths, filepath.Join(a, "SavedVariables", "FairShare.lua"))
	}
	return paths, nil
}

// AccountDirs lists <flavor>\WTF\Account\<name> directories (SavedVariables is a file
// there, not an account, so it is excluded).
func (c *Config) AccountDirs() ([]string, error) {
	root := filepath.Join(c.FlavorDir(), "WTF", "Account")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("no WTF\\Account folder under %s (log in to the game once so it exists)", c.FlavorDir())
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() && !strings.EqualFold(e.Name(), "SavedVariables") {
			dirs = append(dirs, filepath.Join(root, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

// knownFlavors in the order the game installer uses them.
var knownFlavors = []string{"_classic_beta_", "_classic_", "_classic_era_", "_classic_ptr_", "_retail_", "_ptr_", "_beta_"}

// DetectFlavors returns the game-version folders present under wowDir.
func DetectFlavors(wowDir string) []string {
	var found []string
	for _, f := range knownFlavors {
		if st, err := os.Stat(filepath.Join(wowDir, f)); err == nil && st.IsDir() {
			found = append(found, f)
		}
	}
	// Anything else shaped like _name_ with an Interface or WTF folder.
	entries, _ := os.ReadDir(wowDir)
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() || !strings.HasPrefix(n, "_") || !strings.HasSuffix(n, "_") {
			continue
		}
		dup := false
		for _, f := range found {
			if f == n {
				dup = true
			}
		}
		if dup {
			continue
		}
		if _, err := os.Stat(filepath.Join(wowDir, n, "Interface")); err == nil {
			found = append(found, n)
		}
	}
	return found
}

// defaultWoWDirs are the usual install locations on Windows.
var defaultWoWDirs = []string{
	`C:\Program Files (x86)\World of Warcraft`,
	`C:\Program Files\World of Warcraft`,
	`D:\World of Warcraft`,
	`D:\Games\World of Warcraft`,
}

// GuessWoWDir returns the first default install folder that exists, or "".
func GuessWoWDir() string {
	for _, d := range defaultWoWDirs {
		if len(DetectFlavors(d)) > 0 {
			return d
		}
	}
	return ""
}

// Interactive fills in missing fields by asking on the terminal.
func (c *Config) Interactive(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	ask := func(prompt, def string) (string, error) {
		if def != "" {
			fmt.Fprintf(out, "%s [%s]: ", prompt, def)
		} else {
			fmt.Fprintf(out, "%s: ", prompt)
		}
		line, err := r.ReadString('\n')
		if err != nil && line == "" {
			return "", fmt.Errorf("input closed")
		}
		line = strings.TrimSpace(line)
		if line == "" {
			return def, nil
		}
		return line, nil
	}

	var err error
	if c.BackendURL == "" {
		if c.BackendURL, err = ask("Backend URL (e.g. https://fairsharewow.com)", ""); err != nil {
			return err
		}
	}
	if c.Token == "" {
		if c.Token, err = ask("API token (from the bot's personal panel, 'Sync token')", ""); err != nil {
			return err
		}
	}
	if c.WoWDir == "" {
		if c.WoWDir, err = ask("World of Warcraft install folder", GuessWoWDir()); err != nil {
			return err
		}
	}
	if c.Flavor == "" {
		flavors := DetectFlavors(c.WoWDir)
		switch len(flavors) {
		case 0:
			return fmt.Errorf("no game version folders (like _classic_beta_) found under %s", c.WoWDir)
		case 1:
			c.Flavor = flavors[0]
			fmt.Fprintf(out, "Using game version folder %s\n", c.Flavor)
		default:
			fmt.Fprintln(out, "Game version folders found:")
			for i, f := range flavors {
				fmt.Fprintf(out, "  %d) %s\n", i+1, f)
			}
			choice, err := ask("Pick one", "1")
			if err != nil {
				return err
			}
			idx := 0
			fmt.Sscanf(choice, "%d", &idx)
			if idx < 1 || idx > len(flavors) {
				return fmt.Errorf("invalid choice %q", choice)
			}
			c.Flavor = flavors[idx-1]
		}
	}
	if !c.Complete() {
		return fmt.Errorf("setup incomplete")
	}
	return nil
}

// Uploads remembers which transcript content hashes were accepted, per raid.
type Uploads struct {
	path   string
	Hashes map[string][]string `json:"hashes"` // raidID -> content hashes
}

// LoadUploads reads uploads.json from the config directory.
func LoadUploads() (*Uploads, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LoadUploadsAt(filepath.Join(dir, "uploads.json"))
}

// LoadUploadsAt reads an upload memory file at an explicit path (tests, other profiles).
func LoadUploadsAt(path string) (*Uploads, error) {
	u := &Uploads{path: path, Hashes: map[string][]string{}}
	data, err := os.ReadFile(u.path)
	if errors.Is(err, os.ErrNotExist) {
		return u, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, u); err != nil {
		// A corrupt memory file only costs a re-upload; start fresh.
		u.Hashes = map[string][]string{}
	}
	if u.Hashes == nil {
		u.Hashes = map[string][]string{}
	}
	return u, nil
}

// Seen reports whether this content was already uploaded for the raid.
func (u *Uploads) Seen(raidID, hash string) bool {
	for _, h := range u.Hashes[raidID] {
		if h == hash {
			return true
		}
	}
	return false
}

// Remember records an upload and saves the file.
func (u *Uploads) Remember(raidID, hash string) error {
	if u.Seen(raidID, hash) {
		return nil
	}
	u.Hashes[raidID] = append(u.Hashes[raidID], hash)
	data, err := json.MarshalIndent(u, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(u.path, data, 0o600)
}
