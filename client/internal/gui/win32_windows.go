//go:build windows

package gui

// The Win32 calls the window needs, through the standard library's syscall package so the
// client keeps zero dependencies. Every struct matches its Windows SDK layout on both
// 32- and 64-bit builds (pointer-sized fields are uintptr or pointers).

import (
	"syscall"
	"unsafe"
)

var (
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	ole32    = syscall.NewLazyDLL("ole32.dll")
	comctl32 = syscall.NewLazyDLL("comctl32.dll")

	procRegisterClassExW         = user32.NewProc("RegisterClassExW")
	procCreateWindowExW          = user32.NewProc("CreateWindowExW")
	procDefWindowProcW           = user32.NewProc("DefWindowProcW")
	procDestroyWindow            = user32.NewProc("DestroyWindow")
	procShowWindow               = user32.NewProc("ShowWindow")
	procUpdateWindow             = user32.NewProc("UpdateWindow")
	procGetMessageW              = user32.NewProc("GetMessageW")
	procPeekMessageW             = user32.NewProc("PeekMessageW")
	procTranslateMessage         = user32.NewProc("TranslateMessage")
	procDispatchMessageW         = user32.NewProc("DispatchMessageW")
	procIsDialogMessageW         = user32.NewProc("IsDialogMessageW")
	procPostQuitMessage          = user32.NewProc("PostQuitMessage")
	procPostMessageW             = user32.NewProc("PostMessageW")
	procSendMessageW             = user32.NewProc("SendMessageW")
	procSetWindowTextW           = user32.NewProc("SetWindowTextW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procEnableWindow             = user32.NewProc("EnableWindow")
	procSetFocus                 = user32.NewProc("SetFocus")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procFindWindowW              = user32.NewProc("FindWindowW")
	procMessageBoxW              = user32.NewProc("MessageBoxW")
	procSetTimer                 = user32.NewProc("SetTimer")
	procKillTimer                = user32.NewProc("KillTimer")
	procLoadCursorW              = user32.NewProc("LoadCursorW")
	procLoadIconW                = user32.NewProc("LoadIconW")
	procGetDC                    = user32.NewProc("GetDC")
	procReleaseDC                = user32.NewProc("ReleaseDC")
	procAdjustWindowRectEx       = user32.NewProc("AdjustWindowRectEx")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procGetSysColor              = user32.NewProc("GetSysColor")
	procGetSysColorBrush         = user32.NewProc("GetSysColorBrush")
	procSetProcessDPIAware       = user32.NewProc("SetProcessDPIAware")
	procSetProcessDpiAwarenessCt = user32.NewProc("SetProcessDpiAwarenessContext")
	procPrintWindow              = user32.NewProc("PrintWindow")
	procCreateIconFromResourceEx = user32.NewProc("CreateIconFromResourceEx")

	procCreateFontW            = gdi32.NewProc("CreateFontW")
	procGetDeviceCaps          = gdi32.NewProc("GetDeviceCaps")
	procSetTextColor           = gdi32.NewProc("SetTextColor")
	procSetBkColor             = gdi32.NewProc("SetBkColor")
	procCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	procCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	procSelectObject           = gdi32.NewProc("SelectObject")
	procDeleteObject           = gdi32.NewProc("DeleteObject")
	procDeleteDC               = gdi32.NewProc("DeleteDC")
	procGetDIBits              = gdi32.NewProc("GetDIBits")

	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procCreateActCtxW         = kernel32.NewProc("CreateActCtxW")
	procActivateActCtx        = kernel32.NewProc("ActivateActCtx")
	procCreateMutexW          = kernel32.NewProc("CreateMutexW")
	procGetConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	procFreeConsole           = kernel32.NewProc("FreeConsole")

	procSHBrowseForFolderW   = shell32.NewProc("SHBrowseForFolderW")
	procSHGetPathFromIDListW = shell32.NewProc("SHGetPathFromIDListW")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procOleInitialize        = ole32.NewProc("OleInitialize")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procInitCommonControlsEx = comctl32.NewProc("InitCommonControlsEx")
)

const (
	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsMinimizeBox  = 0x00020000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsVScroll      = 0x00200000
	wsClipChildren = 0x02000000
	wsExClientEdge = 0x00000200

	bsPushButton    = 0x0
	bsDefPushButton = 0x1
	esAutoHScroll   = 0x0080
	esAutoVScroll   = 0x0040
	esMultiline     = 0x0004
	esReadOnly      = 0x0800
	esPassword      = 0x0020
	ssLeft          = 0x0000
	ssNoPrefix      = 0x0080
	cbsDropDownList = 0x0003

	wmCreate         = 0x0001
	wmDestroy        = 0x0002
	wmClose          = 0x0010
	wmSetFont        = 0x0030
	wmSetIcon        = 0x0080
	wmCommand        = 0x0111
	wmTimer          = 0x0113
	wmCtlColorStatic = 0x0138
	wmCtlColorEdit   = 0x0133
	wmApp            = 0x8000

	emSetSel       = 0x00B1
	emScrollCaret  = 0x00B7
	emReplaceSel   = 0x00C2
	emSetCueBanner = 0x1501
	cbAddString    = 0x0143
	cbGetCurSel    = 0x0147
	cbGetLBText    = 0x0148
	cbGetLBTextLen = 0x0149
	cbResetContent = 0x014B
	cbSetCurSel    = 0x014E

	bnClicked   = 0
	enKillFocus = 0x0200

	idOK     = 1
	idCancel = 2
	idYes    = 6

	mbOK              = 0x00000000
	mbYesNo           = 0x00000004
	mbIconInformation = 0x00000040
	mbIconWarning     = 0x00000030
	mbIconQuestion    = 0x00000020

	swHide    = 0
	swShow    = 5
	swRestore = 9

	colorWindow   = 5
	colorGrayText = 17

	cwUseDefault = 0x80000000
	idcArrow     = 32512
	idiApp       = 32512
	logPixelsY   = 90
	smCxScreen   = 0
	smCyScreen   = 1
	smCxIcon     = 11
	smCxSmIcon   = 49
	pmRemove     = 1

	errorAlreadyExists = 183
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   uintptr
	Icon       uintptr
	Cursor     uintptr
	Background uintptr
	MenuName   *uint16
	ClassName  *uint16
	IconSm     uintptr
}

type point struct{ X, Y int32 }

type msgT struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type rect struct{ Left, Top, Right, Bottom int32 }

type actCtx struct {
	Size                  uint32
	Flags                 uint32
	Source                *uint16
	ProcessorArchitecture uint16
	LangID                uint16
	AssemblyDirectory     *uint16
	ResourceName          *uint16
	ApplicationName       *uint16
	Module                uintptr
}

type initCommonControlsEx struct {
	Size uint32
	ICC  uint32
}

type browseInfo struct {
	Owner       uintptr
	Root        uintptr
	DisplayName *uint16
	Title       *uint16
	Flags       uint32
	Fn          uintptr
	LParam      uintptr
	Image       int32
}

func call(p *syscall.LazyProc, args ...uintptr) uintptr {
	r, _, _ := p.Call(args...)
	return r
}

// wstr converts to a NUL-terminated UTF-16 pointer; embedded NULs are dropped.
func wstr(s string) *uint16 {
	b := make([]rune, 0, len(s))
	for _, r := range s {
		if r != 0 {
			b = append(b, r)
		}
	}
	p, _ := syscall.UTF16PtrFromString(string(b))
	return p
}

func ptr(s string) uintptr { return uintptr(unsafe.Pointer(wstr(s))) }

func sendMessage(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	return call(procSendMessageW, hwnd, uintptr(msg), wp, lp)
}

func setText(hwnd uintptr, s string) {
	call(procSetWindowTextW, hwnd, ptr(s))
}

func getText(hwnd uintptr) string {
	n := call(procGetWindowTextLengthW, hwnd)
	buf := make([]uint16, n+1)
	call(procGetWindowTextW, hwnd, uintptr(unsafe.Pointer(&buf[0])), n+1)
	return syscall.UTF16ToString(buf)
}

func enable(hwnd uintptr, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	call(procEnableWindow, hwnd, v)
}

func messageBox(owner uintptr, text, title string, flags uintptr) uintptr {
	return call(procMessageBoxW, owner, ptr(text), ptr(title), flags)
}

func rgb(r, g, b byte) uintptr { return uintptr(r) | uintptr(g)<<8 | uintptr(b)<<16 }

func loword(v uintptr) uint16 { return uint16(v & 0xFFFF) }
func hiword(v uintptr) uint16 { return uint16((v >> 16) & 0xFFFF) }
