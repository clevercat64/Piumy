//go:build windows

package main

import (
	"testing"
	"time"
)

// A second launch asking the first for its dashboard (T186,
// ct-2026-11-11-095349) — the real named event, one made-up data directory per
// test so nothing here can reach a running Piumy.

// One request is ONE call, and the listener keeps listening: the next launch,
// minutes later, is heard too.
func TestShowRequestReachesTheListener(t *testing.T) {
	dir := t.TempDir()
	got := make(chan struct{}, 8)
	listenShowRequests(dir, func() { got <- struct{}{} })
	for i := 1; i <= 2; i++ {
		requestShowDashboard(dir)
		select {
		case <-got:
		case <-time.After(5 * time.Second):
			t.Fatalf("request %d: the listener never heard it", i)
		}
		select {
		case <-got:
			t.Fatalf("request %d: the listener was called twice for one request", i)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Two accounts run side by side: a launch of one must not wake the other.
func TestShowRequestStaysWithinItsDataDir(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	gotA, gotB := make(chan struct{}, 1), make(chan struct{}, 1)
	listenShowRequests(dirA, func() { gotA <- struct{}{} })
	listenShowRequests(dirB, func() { gotB <- struct{}{} })
	requestShowDashboard(dirB)
	select {
	case <-gotB:
	case <-time.After(5 * time.Second):
		t.Fatal("B's listener never heard B's request")
	}
	select {
	case <-gotA:
		t.Fatal("A's listener woke for B's request")
	case <-time.After(200 * time.Millisecond):
	}
}

// A launch that finds nobody listening (the seconds Piumy takes to start)
// leaves nothing behind: the instance that starts listening afterwards must
// not wake up to a request from the past.
func TestShowRequestWithoutListenerLeavesNothingBehind(t *testing.T) {
	dir := t.TempDir()
	requestShowDashboard(dir)
	got := make(chan struct{}, 1)
	listenShowRequests(dir, func() { got <- struct{}{} })
	select {
	case <-got:
		t.Fatal("a request made before anyone listened was delivered later")
	case <-time.After(200 * time.Millisecond):
	}
}

// TestDataDirHashSameForDifferentCaseSpellings is S1's core guarantee
// (ct-2026-09-20-1100): two spellings of the SAME Windows directory (which
// is case-insensitive) must collide on one mutex name, or the lock would
// stop doing its job for the exact "same folder, called differently" case
// the contract calls out.
func TestDataDirHashSameForDifferentCaseSpellings(t *testing.T) {
	a := dataDirHash(`C:\Users\boss\AppData\Local\Piumy`)
	b := dataDirHash(`c:\users\BOSS\appdata\local\PIUMY`)
	if a != b {
		t.Errorf("dataDirHash differs by case: %q vs %q, want equal", a, b)
	}
}

// TestDataDirHashDifferentForDifferentDirs is the flip side: two distinct
// accounts (two distinct data directories) must NOT collide, or two live
// Piumy instances could never run at once.
func TestDataDirHashDifferentForDifferentDirs(t *testing.T) {
	a := dataDirHash(`C:\Users\boss\AppData\Local\Piumy\accounts\trabajo`)
	b := dataDirHash(`C:\Users\boss\AppData\Local\Piumy\accounts\personal`)
	if a == b {
		t.Errorf("dataDirHash(trabajo) == dataDirHash(personal) = %q, want different accounts to get different mutexes", a)
	}
}

// TestDataDirHashCollapsesTrailingSeparator: a trailing "\" is the same
// directory as without it — must still hash the same.
func TestDataDirHashCollapsesTrailingSeparator(t *testing.T) {
	a := dataDirHash(`C:\Users\boss\AppData\Local\Piumy`)
	b := dataDirHash(`C:\Users\boss\AppData\Local\Piumy\`)
	if a != b {
		t.Errorf("dataDirHash differs by trailing separator: %q vs %q, want equal", a, b)
	}
}
