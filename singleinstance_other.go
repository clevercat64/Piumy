//go:build !windows

package main

// acquireSingleInstance is a no-op off Windows, same reasoning as
// acquireAppMutex (appmutex_other.go) — T59's launch surfaces (tray,
// installer) are Windows-only for now. dataDir kept in the signature (S1,
// ct-2026-09-20-1100) only so both build tags match — unused here.
func acquireSingleInstance(dataDir string) bool { return true }

// listenShowRequests and requestShowDashboard (T186, ct-2026-11-11-095349) are
// no-ops off Windows for the same reason: with no second-launch refusal there
// is nobody to ask, and showDashboard doesn't find windows here yet either.
func listenShowRequests(dataDir string, onRequest func()) {}
func requestShowDashboard(dataDir string)                 {}
