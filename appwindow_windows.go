//go:build windows

package main

import (
	"log"
	"slices"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

// Finding the dashboard window an account already has open (T186,
// ct-2026-11-11-095349, boss: «que solo se abra 1 dashboard del mismo piumy»),
// so "Abrir tablero" brings it to the front instead of launching a second one.
//
// By title, on purpose. Reading the window's URL would mean asking Chromium's
// accessibility tree or its AppUserModelID (undocumented, and not measurable
// without a real window); a private --user-data-dir per account would drop the
// dashboard's login and language; remembering the HWND we launched dies with
// the process, and auto-update restarts Piumy with the old window still open.
// The title already carries the account (S3, ct-2026-09-20-1202), so it is
// the one thing that survives all three.

const (
	// dashboardWindowClass is the top-level window class of every Chromium
	// browser — msedge and chrome, the two openAppWindow launches. It also
	// keeps a File Explorer window on a folder called "Piumy Gateway" from
	// being raised in the dashboard's place.
	dashboardWindowClass = "Chrome_WidgetWin_1"

	swRestore = 9          // ShowWindow's SW_RESTORE
	asfwAny   = 0xFFFFFFFF // AllowSetForegroundWindow's ASFW_ANY, a DWORD -1

	// A cold browser start takes seconds to show its window. showDashboard
	// waits that long, holding its lock, so a second request in the meantime
	// finds the window instead of launching another.
	windowAppearTimeout = 8 * time.Second
	windowAppearEvery   = 200 * time.Millisecond
)

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procAllowSetForegroundWindow = user32.NewProc("AllowSetForegroundWindow")
)

// topWindow is what the finder needs to know about one top-level window.
type topWindow struct {
	hwnd      uintptr
	class     string
	title     string
	visible   bool
	minimized bool
}

// pickAppWindow is the dashboard window among wins: a visible Chromium window
// whose title is EXACTLY one of titles. Exact, not a prefix: the default
// account's "Piumy Gateway" must not match "Piumy Gateway — cuenta-2", nor
// cuenta-2 match cuenta-3 — each account owns its window. wins comes from
// EnumWindows, topmost first, so with two old windows open the one on top wins.
func pickAppWindow(wins []topWindow, titles ...string) (topWindow, bool) {
	for _, w := range wins {
		if w.class == dashboardWindowClass && w.visible && slices.Contains(titles, w.title) {
			return w, true
		}
	}
	return topWindow{}, false
}

// enumCallback is created once: syscall.NewCallback slots are never freed
// (about 2000 per process), so one per listTopWindows call would run out. It
// reports into enumCollect, which enumMu guards.
var (
	enumMu       sync.Mutex
	enumCollect  []topWindow
	enumCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		enumCollect = append(enumCollect, readTopWindow(hwnd))
		return 1 // TRUE: keep enumerating
	})
)

// listTopWindows is every top-level window of the desktop, topmost first.
func listTopWindows() []topWindow {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumCollect = nil
	procEnumWindows.Call(enumCallback, 0)
	return enumCollect
}

func readTopWindow(hwnd uintptr) topWindow {
	visible, _, _ := procIsWindowVisible.Call(hwnd)
	minimized, _, _ := procIsIconic.Call(hwnd)
	return topWindow{
		hwnd:      hwnd,
		class:     windowString(procGetClassNameW, hwnd),
		title:     windowString(procGetWindowTextW, hwnd),
		visible:   visible != 0,
		minimized: minimized != 0,
	}
}

// windowString runs one of the Get*W calls that fill a UTF-16 buffer. 512
// units: longer text is cut, and no dashboard title comes near that.
func windowString(proc *syscall.LazyProc, hwnd uintptr) string {
	var buf [512]uint16
	n, _, _ := proc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// raiseWindow brings w in front of the user. Restore only a MINIMIZED window:
// SW_RESTORE on a maximized one would shrink it. Windows lets a process take
// the foreground only after the user's last input went to it — a click on its
// own tray menu, or the grant allowAnyProcessToForeground hands over; without
// either it only flashes the taskbar button, and SetForegroundWindow says so
// by returning 0.
func raiseWindow(w topWindow) {
	if w.minimized {
		procShowWindow.Call(w.hwnd, swRestore)
	}
	if ok, _, _ := procSetForegroundWindow.Call(w.hwnd); ok == 0 {
		log.Printf("tray: Windows no dejó traer el tablero al frente — la barra de tareas lo marca")
	}
}

// allowAnyProcessToForeground lets the instance this launch is asking to show
// its dashboard (requestShowDashboard) take the foreground. This process was
// just started by the user, so Windows still lets it hand the right on.
func allowAnyProcessToForeground() {
	procAllowSetForegroundWindow.Call(asfwAny)
}

// waitUntil polls found every `every` until it says yes or timeout passes.
func waitUntil(timeout, every time.Duration, found func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !found() {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(every)
	}
	return true
}
