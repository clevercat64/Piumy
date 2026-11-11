// Package wanotice decides WHEN the principal agent must be told that
// WhatsApp went down, and when it came back (T190, ct-2026-11-11-101756).
//
// Why it exists: WhatsApp can unlink an account at any moment, and Piumy
// used to trip its kill switch and tell nobody — not the agent on the
// antenna, not the tray. An unlink then stays invisible for as long as
// nobody opens the dashboard, with every message silently going nowhere.
//
// This package only decides. It does NOT render the text and it does NOT
// know about the antenna: it hands a Notice to a Sender the wiring
// provides. That split is deliberate — the phrasing needs i18n and the
// delivery needs capipush, and keeping both out of here is what lets the
// decision be tested with nothing but a clock and a fake state.
//
// It reads state.Status on a ticker instead of hooking whatsmeow's own
// events, for the reason driftreport's WatchWhatsApp already documents and
// that was re-measured for this contract: no single whatsmeow hook sees
// every way of being down — handleDisconnect never runs when Piumy boots
// with no network, because nothing ever disconnected.
package wanotice

import (
	"context"
	"log"
	"time"

	"piumy-gateway/internal/state"
)

// watchEvery is how often the watcher looks at the status. One cheap
// in-memory read a minute: the thresholds below are minutes long, so finer
// granularity would buy nothing. A const, not a var — the tests drive
// Observe with their own clock instead of speeding up a ticker.
const watchEvery = time.Minute

// DefaultThreshold is how long a non-terminal outage must last before the
// agent is told. Five minutes: below that is a network blip nobody wants a
// notice about, and much above it a real outage stays silent too long. The
// real value is read live from config on every look — this is only the
// fallback (approved default, Citrino 2026-11-11).
const DefaultThreshold = 5 * time.Minute

// Kind says which of the two notices this is.
type Kind string

const (
	// KindDown: WhatsApp is down and the agent is being told for the first
	// time in this incident.
	KindDown Kind = "down"
	// KindBack: WhatsApp reconnected after an incident the agent was told
	// about. Never sent for an outage that stayed silent.
	KindBack Kind = "back"
)

// Notice is everything the wiring needs to write the message. Deliberately
// no free text and no identity: Reason is state's closed code set, so
// nothing personal (a JID, a number, a name) can travel inside a notice.
type Notice struct {
	Kind   Kind
	Reason state.WADownReason
	// Since is when this incident started being observed.
	Since time.Time
	// Down is how long it had lasted when the notice went out.
	Down time.Duration
}

// Sender delivers one notice. An error means it did not arrive; the
// watcher logs it and does NOT retry — one attempt per incident, by
// contract (Citrino 2026-11-11: the dashboard and the tray show the outage
// anyway). Retrying every tick against a dead antenna would be the loop
// the contract explicitly rules out.
type Sender func(Notice) error

// StateSource is the slice of *state.Manager the watcher needs.
type StateSource interface{ Snapshot() state.Status }

// Watcher is the state machine. Build it with New and drive it with Watch,
// or call Observe directly from a test with your own clock.
type Watcher struct {
	src       StateSource
	send      Sender
	threshold func() time.Duration

	// downSince is zero while connected; set on the first look that sees
	// the outage, NOT when the outage really began (nothing observes the
	// gap between a crash and the next tick, so claiming otherwise would
	// report a duration nobody measured).
	downSince time.Time
	// notifiedFor is the reason the agent was already told about in THIS
	// incident, "" if it has not been told. Keyed by reason, not a bool, on
	// purpose: an outage that starts as no_network and turns into
	// logged_out is new information the owner needs, so it earns one more
	// notice. Same reason twice never does.
	notifiedFor state.WADownReason
	notified    bool
}

// New builds a Watcher. threshold is asked on every look, never captured
// once, so the owner changing it from the dashboard takes effect without a
// restart (same pattern as capipush's live settings). A nil threshold falls
// back to DefaultThreshold.
func New(src StateSource, send Sender, threshold func() time.Duration) *Watcher {
	if threshold == nil {
		threshold = func() time.Duration { return DefaultThreshold }
	}
	return &Watcher{src: src, send: send, threshold: threshold}
}

// Watch looks at the status every watchEvery until ctx is done. Returns at
// once if it could never do anything (no source or no sender).
func Watch(ctx context.Context, w *Watcher) {
	if w == nil || w.src == nil || w.send == nil {
		return
	}
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.Observe(now, w.src.Snapshot())
		}
	}
}

// Observe is one look at the status.
func (w *Watcher) Observe(now time.Time, s state.Status) {
	// An install still waiting for its first QR is not connected BY
	// DESIGN, so it is not an outage — notifying there would mean telling
	// the agent that a WhatsApp that never existed went down.
	if state.NotPaired(s) {
		w.reset()
		return
	}

	if s.WAConnected {
		if w.notified {
			w.emit(Notice{
				Kind:   KindBack,
				Reason: w.notifiedFor,
				Since:  w.downSince,
				Down:   now.Sub(w.downSince),
			})
		}
		w.reset()
		return
	}

	if w.downSince.IsZero() {
		w.downSince = now
	}
	down := now.Sub(w.downSince)

	// Already told the agent about this exact reason in this incident.
	if w.notified && w.notifiedFor == s.WADownReason {
		return
	}
	// A terminal reason cannot fix itself — the account needs the owner to
	// scan a QR again — so it goes out on the first look, with no
	// threshold. Everything else waits, because it usually comes back by
	// itself and a blip must stay silent.
	if !terminal(s.WADownReason) && down < w.threshold() {
		return
	}
	w.emit(Notice{Kind: KindDown, Reason: s.WADownReason, Since: w.downSince, Down: down})
	w.notified = true
	w.notifiedFor = s.WADownReason
}

// emit sends and logs a failure. The caller marks the incident notified
// either way: one attempt per incident is the contract.
func (w *Watcher) emit(n Notice) {
	if err := w.send(n); err != nil {
		log.Printf("wanotice: no se pudo avisar al agente principal (%s, motivo=%q): %v", n.Kind, n.Reason, err)
	}
}

func (w *Watcher) reset() {
	w.downSince = time.Time{}
	w.notifiedFor = ""
	w.notified = false
}

// terminal reports whether a reason means the account cannot recover on its
// own. Those are the two the owner has to act on, so they skip the wait.
func terminal(r state.WADownReason) bool {
	return r == state.WADownLoggedOut || r == state.WADownTempBan
}
