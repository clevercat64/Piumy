//go:build windows

package main

import (
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"piumy-gateway/internal/dashboard"
)

// chromeWindow is a visible dashboard-shaped window with the given title.
func chromeWindow(hwnd uintptr, title string) topWindow {
	return topWindow{hwnd: hwnd, class: dashboardWindowClass, title: title, visible: true}
}

// TestPickAppWindow is T186's core (ct-2026-11-11-095349): which window, among
// a synthetic desktop, is the one "Abrir tablero" must raise. No real window
// is opened — the list is made up.
func TestPickAppWindow(t *testing.T) {
	const (
		def    = "Piumy Gateway"
		acct2  = "Piumy Gateway — cuenta-2"
		acct3  = "Piumy Gateway — cuenta-3"
		named2 = "Piumy Gateway — Contacto Uno · ...0001"
	)
	minimized := chromeWindow(7, acct2)
	minimized.minimized = true
	hidden := chromeWindow(8, acct2)
	hidden.visible = false
	explorer := topWindow{hwnd: 9, class: "CabinetWClass", title: def, visible: true}

	cases := []struct {
		name   string
		wins   []topWindow
		titles []string
		want   uintptr // hwnd; 0 = none
	}{
		{"default account finds its window", []topWindow{chromeWindow(1, def)}, []string{def}, 1},
		{"named account finds its window by label", []topWindow{chromeWindow(1, named2)}, []string{named2, acct2}, 1},
		{"named account finds its window by id when the label is not painted yet", []topWindow{chromeWindow(1, acct2)}, []string{named2, acct2}, 1},
		{"another account's window is not mine", []topWindow{chromeWindow(1, acct3)}, []string{acct2}, 0},
		{"the default's window is not a named account's", []topWindow{chromeWindow(1, def)}, []string{acct2}, 0},
		{"a named account's window is not the default's", []topWindow{chromeWindow(1, acct2)}, []string{def}, 0},
		{"a longer title is not a match (exact, not prefix)", []topWindow{chromeWindow(1, acct2+" (2)")}, []string{acct2}, 0},
		{"a normal browser tab carries the browser's name: not an app window", []topWindow{chromeWindow(1, def+" - Microsoft Edge")}, []string{def}, 0},
		{"another window class with the title is not it", []topWindow{explorer}, []string{def}, 0},
		{"a hidden window is not it", []topWindow{hidden}, []string{acct2}, 0},
		{"a minimized window is found, flagged for restore", []topWindow{minimized}, []string{acct2}, 7},
		{"two matches: the topmost (first listed) wins", []topWindow{chromeWindow(4, acct2), chromeWindow(5, acct2)}, []string{acct2}, 4},
		{"the match among others", []topWindow{explorer, chromeWindow(2, acct3), chromeWindow(3, acct2), chromeWindow(6, def)}, []string{acct2}, 3},
		{"empty desktop", nil, []string{acct2}, 0},
	}
	for _, tc := range cases {
		got, ok := pickAppWindow(tc.wins, tc.titles...)
		if ok != (tc.want != 0) || got.hwnd != tc.want {
			t.Errorf("%s: pickAppWindow = (hwnd %d, %v), want hwnd %d", tc.name, got.hwnd, ok, tc.want)
		}
	}
	if got, _ := pickAppWindow([]topWindow{minimized}, acct2); !got.minimized {
		t.Error("the minimized flag must travel with the window it picked, or raiseWindow can't know to restore it")
	}
}

// TestListTopWindowsReadsAWindowOfOurOwn runs the real EnumWindows against a
// window this test creates and never shows (no WS_VISIBLE: nothing appears on
// anyone's screen) — a case whose answer is known: its exact title and class,
// not visible, not minimized.
func TestListTopWindowsReadsAWindowOfOurOwn(t *testing.T) {
	runtime.LockOSThread() // a window belongs to the thread that created it
	defer runtime.UnlockOSThread()

	title := fmt.Sprintf("piumy-t186-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	class, _ := syscall.UTF16PtrFromString("Static")
	text, _ := syscall.UTF16PtrFromString(title)
	user := syscall.NewLazyDLL("user32.dll")
	hwnd, _, err := user.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(text)), 0, 0, 0, 0, 0, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Fatalf("CreateWindowExW: %v", err)
	}
	defer user.NewProc("DestroyWindow").Call(hwnd)

	listTopWindows() // a second call must not carry the first one's windows over
	var found *topWindow
	listed := 0
	for _, w := range listTopWindows() {
		if w.title == title {
			found = &w
			listed++
		}
	}
	if found == nil {
		t.Fatalf("listTopWindows did not list the window just created (%q)", title)
	}
	if listed != 1 {
		t.Errorf("listTopWindows listed the window %d times, want 1", listed)
	}
	if found.hwnd != hwnd || found.class != "Static" || found.visible || found.minimized {
		t.Errorf("window read as %+v, want hwnd %d, class Static, hidden, not minimized", *found, hwnd)
	}
}

// TestListTopWindowsReadsTheFlagsTheOSReports: visible and minimized come from
// the OS, not from a default. The expected side is the OS asked directly about
// the very windows listTopWindows returned. A logged-in desktop always has some
// visible top-level window (the taskbar, at least) and usually a minimized one;
// a session with none has nothing to measure — the visible case skips, the
// minimized one just has nothing to compare. Skipping on "listTopWindows saw no
// visible window" instead would let a reader stuck on false pass as a skip.
func TestListTopWindowsReadsTheFlagsTheOSReports(t *testing.T) {
	var osVisible, readVisible, osMinimized, readMinimized int
	for _, w := range listTopWindows() {
		if v, _, _ := procIsWindowVisible.Call(w.hwnd); v != 0 {
			osVisible++
			if w.visible {
				readVisible++
			}
		}
		if m, _, _ := procIsIconic.Call(w.hwnd); m != 0 {
			osMinimized++
			if w.minimized {
				readMinimized++
			}
		}
	}
	if osVisible == 0 {
		t.Skip("the OS reports no visible top-level window in this session")
	}
	if readVisible == 0 {
		t.Errorf("the OS reports %d visible windows, listTopWindows read none as visible", osVisible)
	}
	if osMinimized > 0 && readMinimized == 0 {
		t.Errorf("the OS reports %d minimized windows, listTopWindows read none as minimized", osMinimized)
	}
}

// TestTrayTitleIsTheTitleAppJSPaints is the guard that keeps the finder's
// title (trayTitle) from drifting away from the one the page puts on its own
// window (app.js applyAccountIdentity). Three places build "Piumy Gateway —
// <account>" — the tray, restapi's served <title> (own test), and app.js — and
// the finder trusts the tray's. If someone changes one wording, "Abrir
// tablero" silently starts opening a second window; this test says so first.
// The single source (Go decides, JS paints) is the follow-up in
// docs/T186-UN-SOLO-TABLERO.md.
func TestTrayTitleIsTheTitleAppJSPaints(t *testing.T) {
	js, err := fs.ReadFile(dashboard.WebFS(), "app.js")
	if err != nil {
		t.Fatalf("read app.js: %v", err)
	}
	bare := regexp.MustCompile(`document\.title = "([^"]*)";`).FindAllSubmatch(js, -1)
	named := regexp.MustCompile(`document\.title = "([^"]*)" \+ account;`).FindAllSubmatch(js, -1)
	// Exactly one of each, or the instrument is lost, not the product.
	if len(bare) != 1 || len(named) != 1 {
		t.Fatalf("app.js: found %d no-account and %d named-account document.title assignments, want 1 and 1 — did applyAccountIdentity change? update this test with it", len(bare), len(named))
	}
	if got, want := trayTitle(""), string(bare[0][1]); got != want {
		t.Errorf("trayTitle(\"\") = %q, app.js paints %q", got, want)
	}
	if got, want := trayTitle("cuenta-2"), string(named[0][1])+"cuenta-2"; got != want {
		t.Errorf("trayTitle(\"cuenta-2\") = %q, app.js paints %q", got, want)
	}
}

func TestWaitUntil(t *testing.T) {
	calls := 0
	if !waitUntil(time.Minute, time.Millisecond, func() bool { calls++; return calls == 3 }) {
		t.Error("waitUntil gave up although found turned true on the 3rd poll")
	}
	if calls != 3 {
		t.Errorf("found polled %d times, want 3 — it must stop at the first yes", calls)
	}
	if waitUntil(20*time.Millisecond, time.Millisecond, func() bool { return false }) {
		t.Error("waitUntil says found although nothing ever was")
	}
}
