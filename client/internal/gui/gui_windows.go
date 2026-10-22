//go:build windows

// Package gui is the sync client's window: who you are signed in as, the raid you lead,
// what Data.lua holds, when the last live update went out, four buttons and the activity
// log. It drives an app.Controller and does no syncing itself. Plain Win32 through the
// standard library: no third-party code, and no local web server holding the token.
package gui

import (
	_ "embed"
	"fmt"
	"os"
	"runtime"
	"strings"
	stdsync "sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
	"unsafe"

	"github.com/JPWill/FairShare/client/internal/app"
)

const (
	mainClass     = "FairShareSyncWindow"
	settingsClass = "FairShareSyncSettings"
	mutexName     = `Local\FairShareSync.Window`
	appTitle      = "FairShare sync"

	idSync     = 101
	idFinish   = 102
	idWitness  = 103
	idSettings = 104
	idLog      = 105

	timerRefresh = 1
	maxLogChars  = 60000
)

// icon is the FairShare wheel from docs/brand, cut out with a transparent background.
//
//go:embed icon.png
var icon []byte

var (
	cur        *ui
	procs      stdsync.Once
	mainProcCB uintptr
	setProcCB  uintptr
	browseCB   uintptr
)

type ui struct {
	ctrl *app.Controller
	inst uintptr
	dpi  int

	font, mono uintptr

	main                                     uintptr
	vAccount, vRaid, vSnapshot, vLive, vNote uintptr
	bSync, bFinish, bWitness, bSettings      uintptr
	logEdit                                  uintptr

	muted    map[uintptr]bool
	noteErr  bool
	shown    map[uintptr]string
	seq      int
	settings *settingsWin
}

// Run shows the window and blocks until it is closed. A second copy of the program
// brings the first one's window forward instead of starting another sync loop.
func Run(ctrl *app.Controller) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	_, _, err := procCreateMutexW.Call(0, 1, ptr(mutexName))
	if err == syscall.Errno(errorAlreadyExists) {
		if h := call(procFindWindowW, ptr(mainClass), 0); h != 0 {
			call(procShowWindow, h, swRestore)
			call(procSetForegroundWindow, h)
		}
		return nil
	}
	hideOwnConsole()

	u, err := newUI(ctrl)
	if err != nil {
		return err
	}
	ctrl.Start()
	u.show()
	if cfg := ctrl.Config(); !cfg.Complete() {
		u.openSettings()
	}
	u.loop()
	return nil
}

// hideOwnConsole closes the console window Windows opens for a double-clicked console
// program. A console shared with a terminal the user started from is left alone.
func hideOwnConsole() {
	if call(procGetConsoleWindow) == 0 {
		return
	}
	var pids [2]uint32
	if n := call(procGetConsoleProcessList, uintptr(unsafe.Pointer(&pids[0])), 2); n == 1 {
		call(procFreeConsole)
	}
}

func newUI(ctrl *app.Controller) (*ui, error) {
	setupProcess()
	activateStyles()
	u := &ui{ctrl: ctrl, muted: map[uintptr]bool{}, shown: map[uintptr]string{}}
	cur = u
	u.inst = call(procGetModuleHandleW, 0)
	u.dpi = screenDPI()
	u.font = u.makeFont(9, 400, "Segoe UI")
	u.mono = u.makeFont(9, 400, "Consolas")
	if err := u.registerClasses(); err != nil {
		return nil, err
	}
	if err := u.createMain(); err != nil {
		return nil, err
	}
	return u, nil
}

var (
	setupOnce stdsync.Once
	styleCtx  = ^uintptr(0) // activation context for Common Controls 6, if created
)

// activateStyles turns the modern control look on for the calling thread. Windows keeps
// activation per thread, so it runs on whichever thread creates the windows.
func activateStyles() {
	if styleCtx == ^uintptr(0) {
		return
	}
	var cookie uintptr
	call(procActivateActCtx, styleCtx, uintptr(unsafe.Pointer(&cookie)))
}

// setupProcess runs once: DPI awareness, the modern control look, OLE for the folder picker.
func setupProcess() {
	setupOnce.Do(func() {
		if procSetProcessDpiAwarenessCt.Find() == nil {
			call(procSetProcessDpiAwarenessCt, ^uintptr(1)) // DPI_AWARENESS_CONTEXT_SYSTEM_AWARE
		} else {
			call(procSetProcessDPIAware)
		}
		enableVisualStyles()
		icc := initCommonControlsEx{ICC: 0x40FF} // standard and Win95 classes
		icc.Size = uint32(unsafe.Sizeof(icc))
		call(procInitCommonControlsEx, uintptr(unsafe.Pointer(&icc)))
		call(procOleInitialize, 0)
	})
}

// enableVisualStyles creates the Common Controls 6 activation context, so buttons and edit
// boxes look like a current Windows app rather than Windows 95. A program normally gets
// this from an embedded manifest; a temporary manifest file does the same at runtime.
func enableVisualStyles() {
	const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
</assembly>`
	f, err := os.CreateTemp("", "fairshare-sync-*.manifest")
	if err != nil {
		return
	}
	name := f.Name()
	f.WriteString(manifest)
	f.Close()
	defer os.Remove(name)
	ctx := actCtx{Source: wstr(name)}
	ctx.Size = uint32(unsafe.Sizeof(ctx))
	styleCtx = call(procCreateActCtxW, uintptr(unsafe.Pointer(&ctx)))
}

func screenDPI() int {
	dc := call(procGetDC, 0)
	defer call(procReleaseDC, 0, dc)
	if d := int(call(procGetDeviceCaps, dc, logPixelsY)); d > 0 {
		return d
	}
	return 96
}

func (u *ui) px(v int) uintptr { return uintptr(v * u.dpi / 96) }

func (u *ui) makeFont(points, weight int, face string) uintptr {
	h := -(points * u.dpi / 72)
	return call(procCreateFontW, uintptr(h), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, ptr(face))
}

func (u *ui) registerClasses() error {
	procs.Do(func() {
		mainProcCB = syscall.NewCallback(func(h, m, wp, lp uintptr) uintptr { return cur.mainProc(h, uint32(m), wp, lp) })
		setProcCB = syscall.NewCallback(func(h, m, wp, lp uintptr) uintptr { return cur.settingsProc(h, uint32(m), wp, lp) })
		browseCB = syscall.NewCallback(func(h, m, lp, data uintptr) uintptr {
			if m == 1 && data != 0 { // BFFM_INITIALIZED: start at the folder already typed
				sendMessage(h, 0x467, 1, data) // BFFM_SETSELECTIONW
			}
			return 0
		})
	})
	cursor := call(procLoadCursorW, 0, idcArrow)
	big, small := loadIcon(smCxIcon), loadIcon(smCxSmIcon)
	for _, c := range []struct {
		name string
		proc uintptr
	}{{mainClass, mainProcCB}, {settingsClass, setProcCB}} {
		wc := wndClassEx{WndProc: c.proc, Instance: u.inst, Icon: big, IconSm: small, Cursor: cursor, Background: colorWindow + 1, ClassName: wstr(c.name)}
		wc.Size = uint32(unsafe.Sizeof(wc))
		if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 && err != syscall.Errno(1410) { // 1410: already registered
			return fmt.Errorf("register window class %s: %v", c.name, err)
		}
	}
	return nil
}

// loadIcon turns the embedded PNG into an icon of the system size for metric (Windows
// reads PNG icon images directly), falling back to the stock application icon.
func loadIcon(metric uintptr) uintptr {
	size := call(procGetSystemMetrics, metric)
	h := call(procCreateIconFromResourceEx, uintptr(unsafe.Pointer(&icon[0])), uintptr(len(icon)), 1, 0x00030000, size, size, 0)
	if h == 0 {
		return call(procLoadIconW, 0, idiApp)
	}
	return h
}

// sentence capitalises the first letter, for error text shown on its own.
func sentence(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

// plural writes "1 line" or "3 lines".
func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// topLevel creates a centred window whose client area is w x h (96-DPI units).
func (u *ui) topLevel(class, title string, w, h int, owner uintptr) (uintptr, error) {
	style := uintptr(wsOverlapped | wsCaption | wsSysMenu | wsClipChildren)
	if owner == 0 {
		style |= wsMinimizeBox
	}
	r := rect{Right: int32(u.px(w)), Bottom: int32(u.px(h))}
	call(procAdjustWindowRectEx, uintptr(unsafe.Pointer(&r)), style, 0, 0)
	ww, wh := int(r.Right-r.Left), int(r.Bottom-r.Top)
	sx, sy := int(call(procGetSystemMetrics, smCxScreen)), int(call(procGetSystemMetrics, smCyScreen))
	x, y := (sx-ww)/2, (sy-wh)/2
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	hwnd, _, err := procCreateWindowExW.Call(0, ptr(class), ptr(title), style, uintptr(x), uintptr(y), uintptr(ww), uintptr(wh), owner, 0, u.inst, 0)
	if hwnd == 0 {
		return 0, fmt.Errorf("create window: %v", err)
	}
	return hwnd, nil
}

// child creates a control at (x, y) with size (w, h), all in 96-DPI units.
func (u *ui) child(parent uintptr, class, text string, style uintptr, x, y, w, h, id int) uintptr {
	ex := uintptr(0)
	if class == "EDIT" {
		ex = wsExClientEdge
	}
	hwnd := call(procCreateWindowExW, ex, ptr(class), ptr(text), wsChild|wsVisible|style,
		u.px(x), u.px(y), u.px(w), u.px(h), parent, uintptr(id), u.inst, 0)
	sendMessage(hwnd, wmSetFont, u.font, 1)
	return hwnd
}

func (u *ui) label(parent uintptr, text string, x, y, w, h int, muted bool) uintptr {
	hwnd := u.child(parent, "STATIC", text, ssLeft|ssNoPrefix, x, y, w, h, 0)
	if muted {
		u.muted[hwnd] = true
	}
	return hwnd
}

func (u *ui) createMain() error {
	hwnd, err := u.topLevel(mainClass, appTitle, 600, 470, 0)
	if err != nil {
		return err
	}
	u.main = hwnd
	const lx, lw, vx, vw = 16, 104, 124, 460
	row := func(y, h int, name string) uintptr {
		u.label(hwnd, name, lx, y, lw, 20, true)
		return u.label(hwnd, "", vx, y, vw, h, false)
	}
	// One line per fact; Status gets two so a long problem wraps instead of being cut.
	u.vAccount = row(16, 20, "Account")
	u.vRaid = row(40, 20, "Raid")
	u.vSnapshot = row(64, 20, "Snapshot")
	u.vLive = row(88, 20, "Live updates")
	u.vNote = row(112, 36, "Status")

	u.bSync = u.child(hwnd, "BUTTON", "Sync now", wsTabStop|bsPushButton, 16, 160, 110, 28, idSync)
	u.bFinish = u.child(hwnd, "BUTTON", "Finish raid", wsTabStop|bsPushButton, 134, 160, 110, 28, idFinish)
	u.bWitness = u.child(hwnd, "BUTTON", "Upload as witness", wsTabStop|bsPushButton, 252, 160, 150, 28, idWitness)
	u.bSettings = u.child(hwnd, "BUTTON", "Settings", wsTabStop|bsPushButton, 474, 160, 110, 28, idSettings)

	u.label(hwnd, "Activity", 16, 202, 200, 20, true)
	u.logEdit = u.child(hwnd, "EDIT", "", wsTabStop|wsVScroll|esMultiline|esReadOnly|esAutoVScroll, 16, 222, 568, 232, idLog)
	sendMessage(u.logEdit, wmSetFont, u.mono, 1)
	return nil
}

func (u *ui) show() {
	call(procShowWindow, u.main, swShow)
	call(procUpdateWindow, u.main)
	call(procSetTimer, u.main, timerRefresh, 400, 0)
	u.refresh()
}

func (u *ui) loop() {
	var m msgT
	for {
		r := call(procGetMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return
		}
		u.dispatch(&m)
	}
}

// pump handles waiting messages for d without blocking (tests).
func (u *ui) pump(d time.Duration) {
	var m msgT
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		for call(procPeekMessageW, uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove) != 0 {
			u.dispatch(&m)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (u *ui) dispatch(m *msgT) {
	if u.settings != nil && call(procIsDialogMessageW, u.settings.hwnd, uintptr(unsafe.Pointer(m))) != 0 {
		return
	}
	if call(procIsDialogMessageW, u.main, uintptr(unsafe.Pointer(m))) != 0 {
		return
	}
	call(procTranslateMessage, uintptr(unsafe.Pointer(m)))
	call(procDispatchMessageW, uintptr(unsafe.Pointer(m)))
}

func (u *ui) mainProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	switch msg {
	case wmCommand:
		if hiword(wp) == bnClicked {
			u.onButton(int(loword(wp)))
			return 0
		}
	case wmTimer:
		u.refresh()
		return 0
	case wmCtlColorStatic:
		return u.colorStatic(wp, lp, lp == u.vNote && u.noteErr)
	case wmClose:
		u.close()
		return 0
	case wmDestroy:
		call(procKillTimer, hwnd, timerRefresh)
		call(procPostQuitMessage, 0)
		return 0
	}
	return call(procDefWindowProcW, hwnd, uintptr(msg), wp, lp)
}

// colorStatic paints labels and the read-only log on the window background: muted grey
// for captions and hints, dark red for a problem, the normal text colour otherwise.
func (u *ui) colorStatic(hdc, ctl uintptr, isErr bool) uintptr {
	color := call(procGetSysColor, 8) // COLOR_WINDOWTEXT
	if u.muted[ctl] {
		color = call(procGetSysColor, colorGrayText)
	}
	if isErr {
		color = rgb(176, 0, 32)
	}
	call(procSetTextColor, hdc, color)
	call(procSetBkColor, hdc, call(procGetSysColor, colorWindow))
	return call(procGetSysColorBrush, colorWindow)
}

func (u *ui) onButton(id int) {
	st := u.ctrl.Status()
	queued := true
	switch id {
	case idSync:
		queued = u.ctrl.SyncNow()
	case idFinish:
		text := fmt.Sprintf("Settle raid %s now?\n\nDo this after the last boss, once you have typed /reload in game so the final auctions are saved. The raid settles from the newest live update and balances change in Discord.", st.RaidID)
		if messageBox(u.main, text, appTitle, mbYesNo|mbIconQuestion) == idYes {
			queued = u.ctrl.Finish()
		}
	case idWitness:
		text := "Upload every raid in your saved game data as a witness statement?\n\nUse this for a raid you attended but did not lead: your copy backs up the leader's. Raids already uploaded are skipped."
		if messageBox(u.main, text, appTitle, mbYesNo|mbIconQuestion) == idYes {
			queued = u.ctrl.UploadWitness()
		}
	case idSettings:
		u.openSettings()
	}
	if !queued {
		messageBox(u.main, "Still working on the last request. Try again in a moment.", appTitle, mbOK|mbIconInformation)
	}
	u.refresh()
}

func (u *ui) close() {
	if u.ctrl.Status().RaidID != "" {
		text := "Stop syncing?\n\nYou lead an open raid. While this window is closed, /reload in game sends nothing to Discord. Start FairShare sync again to pick up where it left off."
		if messageBox(u.main, text, appTitle, mbYesNo|mbIconWarning) != idYes {
			return
		}
	}
	u.destroy()
}

func (u *ui) destroy() {
	if u.settings != nil {
		call(procDestroyWindow, u.settings.hwnd)
		u.settings = nil
	}
	u.ctrl.Stop(3 * time.Second)
	call(procDestroyWindow, u.main)
}

// set changes a control's text only when it differs, so the window never flickers.
func (u *ui) set(hwnd uintptr, text string) {
	if u.shown[hwnd] == text {
		return
	}
	u.shown[hwnd] = text
	setText(hwnd, text)
}

func (u *ui) refresh() {
	st := u.ctrl.Status()
	switch {
	case !st.Configured:
		u.set(u.vAccount, "Not set up yet. Press Settings.")
	case st.Connected:
		u.set(u.vAccount, "Signed in as "+st.Account)
	default:
		u.set(u.vAccount, "Not connected")
	}
	switch {
	case !st.Connected:
		u.set(u.vRaid, "-")
	case st.RaidID != "":
		u.set(u.vRaid, "Raid "+st.RaidID+", led by you")
	default:
		u.set(u.vRaid, "No open raid led by you")
	}
	switch {
	case st.Snapshot == "":
		u.set(u.vSnapshot, "None yet")
	case st.SnapshotAt.IsZero():
		u.set(u.vSnapshot, "Checksum "+st.Snapshot+", already in the addon")
	default:
		u.set(u.vSnapshot, fmt.Sprintf("Checksum %s, written at %s. Type /reload in game to load it.", st.Snapshot, st.SnapshotAt.Format("15:04")))
	}
	if st.LastLive.IsZero() {
		u.set(u.vLive, "None sent yet. Every /reload in game sends one.")
	} else {
		u.set(u.vLive, fmt.Sprintf("Last sent at %s (%s). Every /reload in game sends the next.", st.LastLive.Format("15:04"), plural(st.LastLiveLines, "line")))
	}
	note, isErr := st.Note, false
	switch {
	case st.Busy != "":
		note = st.Busy + "..."
	case st.Err != "":
		note, isErr = sentence(st.Err), true
	}
	if isErr != u.noteErr {
		u.noteErr = isErr
		u.shown[u.vNote] = "" // repaint in the new colour
	}
	u.set(u.vNote, note)

	idle := st.Busy == ""
	enable(u.bSync, st.Configured && idle)
	enable(u.bFinish, st.Connected && st.RaidID != "" && idle)
	enable(u.bWitness, st.Connected && idle)

	if lines, seq := u.ctrl.Lines(u.seq); len(lines) > 0 {
		u.seq = seq
		u.appendLog(lines)
	}
}

func (u *ui) appendLog(lines []string) {
	text := strings.Join(lines, "\r\n") + "\r\n"
	n := int(call(procGetWindowTextLengthW, u.logEdit))
	if n+len(text) > maxLogChars {
		keep := getText(u.logEdit)
		keep = keep[len(keep)/2:]
		if i := strings.Index(keep, "\r\n"); i >= 0 {
			keep = keep[i+2:]
		}
		setText(u.logEdit, keep)
		n = int(call(procGetWindowTextLengthW, u.logEdit))
	}
	sendMessage(u.logEdit, emSetSel, uintptr(n), uintptr(n))
	sendMessage(u.logEdit, emReplaceSel, 0, ptr(text))
	sendMessage(u.logEdit, emScrollCaret, 0, 0)
}
