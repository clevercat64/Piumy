package driftreport

import (
	"context"
	"math/rand"
	"time"

	"piumy-gateway/internal/state"
)

// How often the watcher looks at state.Status. The 20–40 min window below
// makes the precision irrelevant; one cheap read a minute is plenty. A var only
// so a test can make it tick fast.
var watchEvery = time.Minute

const (
	// WhatsApp counts as drifted after being down for a random 20–40 min,
	// drawn per outage (never a fixed number — see windowMin).
	waDownMin = 20 * time.Minute
	waDownMax = 40 * time.Minute
)

// StateSource is the slice of *state.Manager the watcher reads.
type StateSource interface{ Snapshot() state.Status }

// WatchWhatsApp reports when WhatsApp stays disconnected past a random
// 20–40 min window. It reads state.Status instead of hooking whatsmeow because
// no single whatsmeow hook sees every way of being down: scheduleReconnect
// misses the terminal drops (LoggedOut, StreamReplaced, ClientOutdated,
// ConnectFailure) and handleDisconnect misses a boot with no network.
// Blocks until ctx is done. Returns at once only when nothing could ever be
// sent (no Reporter, or incomplete wiring); a switched-off Reporter keeps it
// running, because the owner can switch reports on while Piumy runs.
func WatchWhatsApp(ctx context.Context, src StateSource, r *Reporter) {
	if r == nil || !r.usable {
		return
	}
	w := &waWatch{report: r.Report, window: randomWAWindow}
	t := time.NewTicker(watchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			w.tick(now, r.Enabled(), src)
		}
	}
}

// waWatch is the watcher's state machine, split from the ticker so a test can
// feed it a clock.
type waWatch struct {
	report func(Event)
	window func() time.Duration

	downSince time.Time     // zero while connected (or not drifted)
	limit     time.Duration // this outage's window
	outages   int           // outages seen since the process started
}

// tick is one look. While reports are off it does not observe at all: if the
// owner switches them on during an outage, the clock starts then — the report
// never claims a duration nobody measured.
func (w *waWatch) tick(now time.Time, enabled bool, src StateSource) {
	if !enabled {
		w.downSince = time.Time{}
		return
	}
	w.observe(now, src.Snapshot())
}

func (w *waWatch) observe(now time.Time, s state.Status) {
	if s.WAConnected || notPaired(s) {
		w.downSince = time.Time{}
		return
	}
	if w.downSince.IsZero() {
		w.downSince = now
		w.limit = w.window()
		w.outages++
	}
	if down := now.Sub(w.downSince); down >= w.limit {
		// Called every minute while it lasts: the Reporter's cap is what
		// keeps that to one report per window.
		w.report(Event{Kind: KindWADisconnected, Count: w.outages, Duration: down, Cause: waCause(s)})
	}
}

// notPaired is an install still waiting for its first QR scan: not connected
// by design, so not drift. OwnJID is seeded from status.json on every boot
// after a pairing, so a paired install that cannot reconnect still counts.
func notPaired(s state.Status) bool {
	return s.ShowQR || s.Mood == "qr" || s.OwnJID == ""
}

// waCause maps what state.Status already shows onto the closed Cause set.
func waCause(s state.Status) Cause {
	switch {
	case s.Muted:
		return CauseWAHalted // kill switch tripped (LoggedOut/TemporaryBan, or the owner's)
	case s.Mood == "alert":
		return CauseWAReplaced // StreamReplaced's own mood
	default:
		return CauseWADown
	}
}

func randomWAWindow() time.Duration {
	//nolint:gosec // no-crypto random a propósito: es cadencia, no seguridad
	return waDownMin + time.Duration(rand.Int63n(int64(waDownMax-waDownMin)+1))
}
