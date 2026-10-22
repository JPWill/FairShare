//go:build windows

package gui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	stdsync "sync"
	"syscall"
	"unsafe"

	"github.com/JPWill/FairShare/client/internal/app"
	"github.com/JPWill/FairShare/client/internal/config"
)

const (
	idURL     = 201
	idToken   = 202
	idFolder  = 203
	idBrowse  = 204
	idFlavor  = 205
	wmChecked = wmApp + 1
)

// flavorNames gives the game-version folders the names players know them by.
var flavorNames = map[string]string{
	"_retail_":       "Retail (Midnight)",
	"_ptr_":          "Retail PTR",
	"_beta_":         "Retail beta",
	"_classic_beta_": "Forever beta",
	"_classic_":      "Classic",
	"_classic_era_":  "Classic Era",
	"_classic_ptr_":  "Classic PTR",
}

type settingsWin struct {
	hwnd                         uintptr
	url, token, folder, flavor   uintptr
	status, save, cancel, browse uintptr
	flavors                      []string
	statusErr                    bool

	mu     stdsync.Mutex
	result *checkResult
}

type checkResult struct {
	cfg  config.Config
	name string
	err  error
}

func (u *ui) openSettings() {
	if u.settings != nil {
		call(procSetForegroundWindow, u.settings.hwnd)
		return
	}
	hwnd, err := u.topLevel(settingsClass, "FairShare sync settings", 520, 290, u.main)
	if err != nil {
		messageBox(u.main, err.Error(), appTitle, mbOK|mbIconWarning)
		return
	}
	s := &settingsWin{hwnd: hwnd}
	u.settings = s
	const lx, lw, fx = 16, 116, 140

	u.label(hwnd, "Backend URL", lx, 19, lw, 20, true)
	s.url = u.child(hwnd, "EDIT", "", wsTabStop|esAutoHScroll, fx, 16, 364, 24, idURL)
	sendMessage(s.url, emSetCueBanner, 1, ptr("https://fairsharewow.com")) // shown even while focused

	u.label(hwnd, "Sync token", lx, 53, lw, 20, true)
	s.token = u.child(hwnd, "EDIT", "", wsTabStop|esAutoHScroll|esPassword, fx, 50, 364, 24, idToken)
	u.label(hwnd, "From Discord: /fairshare, then Sync client token.", fx, 77, 364, 18, true)

	u.label(hwnd, "WoW folder", lx, 105, lw, 20, true)
	s.folder = u.child(hwnd, "EDIT", "", wsTabStop|esAutoHScroll, fx, 102, 268, 24, idFolder)
	s.browse = u.child(hwnd, "BUTTON", "Browse...", wsTabStop|bsPushButton, 414, 101, 90, 26, idBrowse)

	u.label(hwnd, "Game version", lx, 139, lw, 20, true)
	s.flavor = u.child(hwnd, "COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDownList, fx, 136, 364, 200, idFlavor)
	u.label(hwnd, "The version you raid in, with the addon installed.", fx, 163, 364, 18, true)

	s.status = u.label(hwnd, "", lx, 194, 488, 36, false)
	s.save = u.child(hwnd, "BUTTON", "Save", wsTabStop|bsDefPushButton, 310, 246, 94, 28, idOK)
	s.cancel = u.child(hwnd, "BUTTON", "Cancel", wsTabStop|bsPushButton, 410, 246, 94, 28, idCancel)

	cfg := u.ctrl.Config()
	setText(s.url, cfg.BackendURL)
	setText(s.token, cfg.Token)
	dir := cfg.WoWDir
	if dir == "" {
		dir = config.GuessWoWDir()
	}
	setText(s.folder, dir)
	u.fillFlavors(cfg.Flavor)
	if !cfg.Complete() {
		u.setStatus("Paste the backend URL and your sync token, check the game folder, then Save.", false)
	}

	enable(u.main, false)
	call(procShowWindow, hwnd, swShow)
	if cfg.BackendURL == "" {
		call(procSetFocus, s.url)
	} else {
		call(procSetFocus, s.token)
	}
}

// fillFlavors lists the game versions found in the folder box, keeping want selected.
func (u *ui) fillFlavors(want string) {
	s := u.settings
	sendMessage(s.flavor, cbResetContent, 0, 0)
	dir := strings.TrimSpace(getText(s.folder))
	s.flavors = nil
	if dir != "" {
		s.flavors = config.DetectFlavors(dir)
	}
	sel := -1
	for i, f := range s.flavors {
		name := flavorNames[f]
		if name == "" {
			name = f
		} else {
			name += " (" + f + ")"
		}
		if st, err := os.Stat(filepath.Join(dir, f, "Interface", "AddOns", "FairShare")); err != nil || !st.IsDir() {
			name += ", addon not installed"
		} else if sel < 0 && want == "" {
			sel = i // first folder with the addon
		}
		sendMessage(s.flavor, cbAddString, 0, ptr(name))
		if f == want {
			sel = i
		}
	}
	if sel < 0 && len(s.flavors) > 0 {
		sel = 0
	}
	if sel >= 0 {
		sendMessage(s.flavor, cbSetCurSel, uintptr(sel), 0)
	}
	if dir != "" && len(s.flavors) == 0 {
		u.setStatus("No game versions found in that folder. Pick the World of Warcraft folder that holds _retail_ or _classic_beta_.", true)
	}
}

func (u *ui) selectedFlavor() string {
	s := u.settings
	i := int(int32(sendMessage(s.flavor, cbGetCurSel, 0, 0)))
	if i < 0 || i >= len(s.flavors) {
		return ""
	}
	return s.flavors[i]
}

func (u *ui) setStatus(text string, isErr bool) {
	s := u.settings
	s.statusErr = isErr
	setText(s.status, text)
}

func (u *ui) settingsProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	s := u.settings
	if s == nil || s.hwnd != hwnd {
		return call(procDefWindowProcW, hwnd, uintptr(msg), wp, lp)
	}
	switch msg {
	case wmCommand:
		id, code := int(loword(wp)), hiword(wp)
		switch {
		case id == idOK && code == bnClicked:
			u.saveSettings()
			return 0
		case id == idCancel && code == bnClicked:
			u.closeSettings()
			return 0
		case id == idBrowse && code == bnClicked:
			u.browseFolder()
			return 0
		case id == idFolder && code == enKillFocus:
			u.fillFlavors(u.selectedFlavor())
			return 0
		}
	case wmChecked:
		u.onChecked()
		return 0
	case wmCtlColorStatic:
		return u.colorStatic(wp, lp, lp == s.status && s.statusErr)
	case wmClose:
		u.closeSettings()
		return 0
	}
	return call(procDefWindowProcW, hwnd, uintptr(msg), wp, lp)
}

func (u *ui) closeSettings() {
	s := u.settings
	if s == nil {
		return
	}
	u.settings = nil
	enable(u.main, true)
	call(procDestroyWindow, s.hwnd)
	call(procSetForegroundWindow, u.main)
	u.refresh()
}

// saveSettings checks the new settings on a background goroutine (it talks to the
// backend) and applies them when the check comes back.
func (u *ui) saveSettings() {
	s := u.settings
	cfg := u.ctrl.Config()
	cfg.BackendURL = strings.TrimRight(strings.TrimSpace(getText(s.url)), "/")
	cfg.Token = strings.TrimSpace(getText(s.token))
	cfg.WoWDir = strings.TrimSpace(getText(s.folder))
	cfg.Flavor = u.selectedFlavor()
	enable(s.save, false)
	u.setStatus("Checking the backend and your token...", false)
	go func(hwnd uintptr) {
		name, err := app.Validate(cfg)
		s.mu.Lock()
		s.result = &checkResult{cfg: cfg, name: name, err: err}
		s.mu.Unlock()
		call(procPostMessageW, hwnd, wmChecked, 0, 0)
	}(s.hwnd)
}

func (u *ui) onChecked() {
	s := u.settings
	s.mu.Lock()
	r := s.result
	s.result = nil
	s.mu.Unlock()
	if r == nil {
		return
	}
	if r.err != nil {
		u.setStatus(sentence(r.err.Error()), true)
		enable(s.save, true)
		return
	}
	u.ctrl.Apply(r.cfg)
	u.closeSettings()
}

// browseFolder shows the Windows folder picker, starting at the folder already typed.
func (u *ui) browseFolder() {
	s := u.settings
	start := wstr(strings.TrimSpace(getText(s.folder)))
	display := make([]uint16, 260)
	bi := browseInfo{
		Owner:       s.hwnd,
		DisplayName: &display[0],
		Title:       wstr("Pick your World of Warcraft folder (the one that holds _retail_ or _classic_beta_)"),
		Flags:       0x1 | 0x40 | 0x200, // file-system folders only, new dialog style, no New Folder button
		Fn:          browseCB,
		LParam:      uintptr(unsafe.Pointer(start)),
	}
	pidl := call(procSHBrowseForFolderW, uintptr(unsafe.Pointer(&bi)))
	runtime.KeepAlive(start)
	if pidl == 0 {
		return
	}
	defer call(procCoTaskMemFree, pidl)
	buf := make([]uint16, 1024)
	if call(procSHGetPathFromIDListW, pidl, uintptr(unsafe.Pointer(&buf[0]))) == 0 {
		return
	}
	setText(s.folder, syscall.UTF16ToString(buf))
	u.setStatus("", false)
	u.fillFlavors("")
}
