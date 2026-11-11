package main

// Decides WHAT the tray must show for a given status (T190,
// ct-2026-11-11-101756) — whether the alert mark goes on the icon and what
// the tooltip says. Its own file with NO build tag, same reasoning as
// trayicon_recolor.go: this is a pure decision, so its test runs on every
// platform even though only tray_windows.go uses it.
//
// Split out of the tray loop on purpose: systray cannot be driven from a
// test (it owns the process's UI thread), so leaving this logic inline
// would have made "does the icon change when WhatsApp drops?" unverifiable
// except by hand, on Windows, with a real outage.

import (
	"log"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/state"
)

// trayAppearance is what the tray should look like right now.
type trayAppearance struct {
	// Alert is true while WhatsApp is down on a PAIRED install: the icon
	// carries the alert mark on top of the account tint.
	Alert bool
	// Tooltip is the full hover text: the product + account label, plus the
	// reason when there is an outage to explain.
	Tooltip string
}

// appearanceFor decides the tray's look from the status. title is the
// hover text the tray would show with nothing wrong, already built by the
// caller (trayTitle, product + account label).
//
// title is a PARAMETER and not built here on purpose: trayTitle lives in
// tray_windows.go, behind //go:build windows, and this file has no build
// tag so its test runs everywhere. Calling it from here compiled fine on
// Windows and would have broken the Linux and macOS builds — the very
// platforms this project is headed for — without anything on this machine
// noticing. Keeping the dependency out also leaves this function pure.
//
// Waiting for the first QR is NOT an outage — same criterion wanotice
// applies, for the same reason: a fresh install is disconnected by design,
// and marking the tray red there would alarm the owner about a WhatsApp
// they have not linked yet.
func appearanceFor(s state.Status, title string, lang i18n.Lang) trayAppearance {
	if s.WAConnected || state.NotPaired(s) {
		return trayAppearance{Alert: false, Tooltip: title}
	}
	return trayAppearance{Alert: true, Tooltip: title + " — " + downReasonText(s.WADownReason, lang)}
}

// trayIconFor paints the icon the tray should be showing: the account's
// tint always, plus the alert mark when WhatsApp is down. base is the
// embedded brand icon, never modified on disk.
//
// On any failure it returns base — the tray keeps the plain brand icon
// rather than none, same fallback RecolorTrayIcon already chose. Both
// painters decide the account tint through config.ColorForAccount, the one
// function that owns that answer (S3), so the tray and the dashboard
// cannot disagree about an account's color.
func trayIconFor(base []byte, account string, alert bool) []byte {
	hue := config.ColorForAccount(account).HueDelta
	var (
		icon []byte
		err  error
	)
	if alert {
		icon, err = AlertTrayIcon(base, hue)
	} else {
		icon, err = RecolorTrayIcon(base, hue)
	}
	if err != nil {
		log.Printf("tray: pintando el ícono (cuenta %q, alerta=%v): %v — se usa el ícono normal", account, alert, err)
		return base
	}
	return icon
}

// downReasonText turns the closed code into the short phrase the owner
// reads. Deliberately the SAME catalog keys the dashboard uses for the
// same codes: one source of truth for "what do we call this outage", so
// the tray and the dashboard can never disagree about the motive. An
// unknown or empty code falls back to the generic "disconnected" instead
// of inventing a reason nobody measured.
func downReasonText(r state.WADownReason, lang i18n.Lang) string {
	switch r {
	case state.WADownLoggedOut:
		return i18n.T(lang, "status.disconnected_logged_out")
	case state.WADownTempBan:
		return i18n.T(lang, "status.disconnected_temp_ban")
	case state.WADownReplaced:
		return i18n.T(lang, "status.disconnected_replaced")
	case state.WADownOutdated:
		return i18n.T(lang, "status.disconnected_outdated")
	case state.WADownNoNetwork:
		return i18n.T(lang, "status.disconnected_no_network")
	default:
		return i18n.T(lang, "status.disconnected")
	}
}
