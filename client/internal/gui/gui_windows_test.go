//go:build windows

package gui

import (
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/JPWill/FairShare/client/internal/app"
	"github.com/JPWill/FairShare/client/internal/config"
	"github.com/JPWill/FairShare/client/internal/datafile"
)

const snapText = "FAIRSHARE SNAPSHOT 1\nraid=7\n"

func fakeBackend(t *testing.T) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"discordId": "1", "displayName": "Alice"})
	})
	mux.HandleFunc("/api/v1/snapshot/current", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"raidId": "7", "text": snapText, "checksum": datafile.Checksum(snapText), "issuedAt": 1})
	})
	mux.HandleFunc("/api/v1/transcripts", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "live", "message": "Live update recorded."})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func gameFolder(t *testing.T) string {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "_retail_", "Interface", "AddOns", "FairShare"), 0o755)
	os.MkdirAll(filepath.Join(dir, "_classic_beta_", "Interface", "AddOns"), 0o755)
	sv := filepath.Join(dir, "_retail_", "WTF", "Account", "ACC", "SavedVariables")
	os.MkdirAll(sv, 0o755)
	os.WriteFile(filepath.Join(sv, "FairShare.lua"), []byte("FairShareDB = {\n\t[\"transcript\"] = {\n\t\t[\"7\"] = {\n\t\t\t[\"player\"] = \"Alice\",\n\t\t\t[\"snapshot\"] = \"cafe\",\n\t\t\t[\"lines\"] = {\n\t\t\t\t\"1700000100\\tin\\tAlice\\tSNAP^7^cafe^1700000000^0^DEFAULT=10^\",\n\t\t\t},\n\t\t},\n\t},\n}\n"), 0o644)
	return dir
}

// TestWindowOpens creates the real window and the settings window against a fake backend,
// checks what they show, and closes them. Set FAIRSHARE_GUI_SHOTS to a folder to also save
// PNG screenshots of both windows for a visual check.
func TestWindowOpens(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	srv := fakeBackend(t)
	data := t.TempDir()
	cfg := config.Config{BackendURL: srv.URL, Token: "good", WoWDir: gameFolder(t), Flavor: "_retail_"}
	ctrl, err := app.New(cfg, app.Options{ConfigPath: filepath.Join(data, "config.json"), DataDir: data, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := newUI(ctrl)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Start()
	u.show()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && ctrl.Status().LastLiveLines == 0 {
		u.pump(50 * time.Millisecond)
	}
	u.pump(500 * time.Millisecond)
	if got := getText(u.vAccount); got != "Signed in as Alice" {
		t.Errorf("account = %q", got)
	}
	if got := getText(u.vRaid); got != "Raid 7, led by you" {
		t.Errorf("raid = %q", got)
	}
	if got := getText(u.vLive); !strings.HasPrefix(got, "Last sent at") {
		t.Errorf("live = %q", got)
	}
	if call(procIsWindowEnabled, u.bFinish) == 0 {
		t.Error("Finish raid is disabled while leading a raid")
	}
	if log := getText(u.logEdit); !strings.Contains(log, "signed in as Alice") {
		t.Errorf("log does not show the sign-in:\n%s", log)
	}
	shots := os.Getenv("FAIRSHARE_GUI_SHOTS")
	if shots != "" {
		capture(t, u.main, filepath.Join(shots, "main.png"))
	}

	u.openSettings()
	u.pump(400 * time.Millisecond)
	s := u.settings
	if s == nil {
		t.Fatal("settings did not open")
	}
	if got := u.selectedFlavor(); got != "_retail_" {
		t.Errorf("selected game version = %q", got)
	}
	if len(s.flavors) != 2 {
		t.Errorf("game versions found = %v", s.flavors)
	}
	if call(procIsWindowEnabled, u.main) != 0 {
		t.Error("main window still enabled under the settings window")
	}
	// A bad URL comes back as a red status line, and the settings stay open.
	setText(s.url, "http://fairshare.example.com")
	u.saveSettings()
	for i := 0; i < 50 && u.settings != nil && !u.settings.statusErr; i++ {
		u.pump(50 * time.Millisecond)
	}
	if u.settings == nil || !strings.Contains(getText(u.settings.status), "https://") {
		t.Fatalf("expected an https warning, got %q", getText(s.status))
	}
	if shots != "" {
		capture(t, s.hwnd, filepath.Join(shots, "settings.png"))
	}
	u.closeSettings()
	if call(procIsWindowEnabled, u.main) == 0 {
		t.Error("main window left disabled after settings closed")
	}
	u.destroy()
	u.pump(100 * time.Millisecond)
}

// TestFirstRun opens the window with no settings, the way a new leader first sees it.
func TestFirstRun(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	data := t.TempDir()
	ctrl, err := app.New(config.Config{}, app.Options{ConfigPath: filepath.Join(data, "config.json"), DataDir: data, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	u, err := newUI(ctrl)
	if err != nil {
		t.Fatal(err)
	}
	ctrl.Start()
	u.show()
	u.pump(500 * time.Millisecond)
	if got := getText(u.vAccount); got != "Not set up yet. Press Settings." {
		t.Errorf("account = %q", got)
	}
	for _, b := range []uintptr{u.bSync, u.bFinish, u.bWitness} {
		if call(procIsWindowEnabled, b) != 0 {
			t.Errorf("button %q enabled before setup", getText(b))
		}
	}
	if call(procIsWindowEnabled, u.bSettings) == 0 {
		t.Error("Settings disabled before setup")
	}
	u.openSettings()
	u.pump(300 * time.Millisecond)
	if shots := os.Getenv("FAIRSHARE_GUI_SHOTS"); shots != "" {
		capture(t, u.main, filepath.Join(shots, "first-run-main.png"))
		capture(t, u.settings.hwnd, filepath.Join(shots, "first-run-settings.png"))
	}
	u.destroy()
	u.pump(100 * time.Millisecond)
}

var (
	procIsWindowEnabled = user32.NewProc("IsWindowEnabled")
)

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

// capture renders a window into a PNG (PrintWindow, so it works even if covered).
func capture(t *testing.T, hwnd uintptr, path string) {
	t.Helper()
	var r rect
	call(procGetWindowRect, hwnd, uintptr(unsafe.Pointer(&r)))
	w, h := int(r.Right-r.Left), int(r.Bottom-r.Top)
	screen := call(procGetDC, 0)
	defer call(procReleaseDC, 0, screen)
	mem := call(procCreateCompatibleDC, screen)
	defer call(procDeleteDC, mem)
	bmp := call(procCreateCompatibleBitmap, screen, uintptr(w), uintptr(h))
	defer call(procDeleteObject, bmp)
	old := call(procSelectObject, mem, bmp)
	call(procPrintWindow, hwnd, mem, 2) // PW_RENDERFULLCONTENT
	call(procSelectObject, mem, old)
	bi := struct {
		Header bitmapInfoHeader
		Colors [1]uint32
	}{Header: bitmapInfoHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}}
	bi.Header.Size = uint32(unsafe.Sizeof(bi.Header))
	buf := make([]byte, w*h*4)
	call(procGetDIBits, mem, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&bi)), 0)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		img.Pix[i*4+0], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = buf[i*4+2], buf[i*4+1], buf[i*4+0], 255
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}
