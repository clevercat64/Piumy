package capipush

import (
	"time"

	"piumy-gateway/internal/driftreport"
	"piumy-gateway/internal/store"
)

// The two drifts capipush is the one to notice (T174a, ct-2026-11-10-210158):
// dispatches held because nobody can take them, and a registered antenna that
// keeps failing. Everything about whether and how a report leaves — switch,
// window, cap, what the wire carries — is driftreport's; this file only
// measures how long the condition has lasted and hands over an Event.

// retention tracks, across sweeps, the dispatches held because the route or the
// agent has no antenna.
type retention struct {
	noRoute, noAntenna int       // dispatches held in the sweep in progress
	partial            bool      // that sweep skipped chats, so "nothing held" proves nothing
	since              time.Time // when holding began; zero = not holding
}

// SetDrift wires the drift reporter. Call once at startup before Run, like
// SetState; nil (or a disabled Reporter) keeps every hook inert.
func (p *Pusher) SetDrift(r *driftreport.Reporter) { p.drift = r }

// driftSustainedAfter is how long a condition must last before it counts as
// drift. Live-overridable, same layering as the other capipush knobs.
func (p *Pusher) driftSustainedAfter() time.Duration {
	return p.store.SettingDuration(store.SettingDriftSustainedAfter, p.cfg.DriftSustainedAfter)
}

// beginRetentionSweep resets the per-sweep counters. swamped means non-boss
// chats are skipped this sweep: exactly the situation a missing antenna
// causes (messages pile up until backpressure trips), so a sweep that holds
// nothing visible must not be read as "the antenna is back".
func (p *Pusher) beginRetentionSweep(swamped bool) {
	p.retention.noRoute, p.retention.noAntenna, p.retention.partial = 0, 0, swamped
}

// reportRetention runs once at the end of a sweep. Called every sweep while the
// condition lasts: driftreport's cap turns that into one report per window.
func (p *Pusher) reportRetention() {
	if !p.drift.Enabled() {
		return
	}
	r := &p.retention
	held := r.noRoute + r.noAntenna
	if held == 0 {
		if !r.partial {
			r.since = time.Time{}
		}
		return
	}
	if r.since.IsZero() {
		r.since = time.Now()
	}
	age := time.Since(r.since)
	if age < p.driftSustainedAfter() {
		return
	}
	cause := driftreport.CauseNoRoute
	if r.noAntenna > r.noRoute {
		cause = driftreport.CauseNoAntenna
	}
	p.drift.Report(driftreport.Event{Kind: driftreport.KindDispatchRetained, Count: held, Duration: age, Cause: cause})
}

// reportChannelDown reuses recordChannelDown's own bookkeeping (since/fails per
// terminal): the report carries the same count and duration the recovery log
// line will, once the outage is long enough to matter.
func (p *Pusher) reportChannelDown(terminalID string) {
	if !p.drift.Enabled() {
		return
	}
	since, down := p.channelDownSince[terminalID]
	if !down {
		return
	}
	age := time.Since(since)
	if age < p.driftSustainedAfter() {
		return
	}
	p.drift.Report(driftreport.Event{
		Kind: driftreport.KindAntennaFailing, Count: p.channelDownFails[terminalID],
		Duration: age, Cause: driftreport.CauseSendFailing,
	})
}
