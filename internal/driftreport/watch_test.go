package driftreport

import (
	"context"
	"testing"
	"time"

	"piumy-gateway/internal/state"
)

// guard is a deadlock guard for POSITIVE waits (the watcher stopping, a goroutine
// returning). It is not a measurement: a short one only makes a loaded machine
// fail a correct test.
const guard = 30 * time.Second

// A paired install whose WhatsApp is down: OwnJID is what a previous
// connection left in status.json (state.NewManager seeds it on every boot).
func downStatus() state.Status {
	return state.Status{OwnJID: "55500000001@s.whatsapp.net", Mood: "error", WAConnected: false}
}

func newTestWatch(window time.Duration) (*waWatch, *[]Event) {
	var got []Event
	w := &waWatch{
		report: func(e Event) { got = append(got, e) },
		window: func() time.Duration { return window },
	}
	return w, &got
}

func TestWatchReportsOnlyAfterTheWindow(t *testing.T) {
	w, got := newTestWatch(30 * time.Minute)
	w.observe(t0, downStatus())
	w.observe(t0.Add(29*time.Minute), downStatus())
	if len(*got) != 0 {
		t.Fatalf("reported inside the window: %v", *got)
	}
	w.observe(t0.Add(30*time.Minute), downStatus())
	if len(*got) != 1 {
		t.Fatalf("reports at the window = %d, want 1", len(*got))
	}
	e := (*got)[0]
	if e.Kind != KindWADisconnected || e.Cause != CauseWADown || e.Count != 1 || e.Duration != 30*time.Minute {
		t.Errorf("event = %+v, want wa_disconnected/wa_down, count 1, 30m", e)
	}
}

// Back online resets the clock: a flapping line is not "down for 30 minutes".
func TestWatchRecoveryRestartsTheClock(t *testing.T) {
	w, got := newTestWatch(30 * time.Minute)
	w.observe(t0, downStatus())
	w.observe(t0.Add(20*time.Minute), downStatus())
	up := downStatus()
	up.WAConnected = true
	w.observe(t0.Add(21*time.Minute), up)
	w.observe(t0.Add(22*time.Minute), downStatus())
	w.observe(t0.Add(50*time.Minute), downStatus()) // 28 min since the second drop
	if len(*got) != 0 {
		t.Fatalf("reported %v although each outage was under 30 min", *got)
	}
	w.observe(t0.Add(52*time.Minute), downStatus())
	if len(*got) != 1 {
		t.Fatalf("second outage past 30 min: reports = %d, want 1", len(*got))
	}
	if (*got)[0].Count != 2 {
		t.Errorf("count = %d, want 2 (the second outage since start)", (*got)[0].Count)
	}
}

// An install that never paired is "not connected" by design; that is not drift.
func TestWatchIgnoresAnInstallThatNeverPaired(t *testing.T) {
	for name, s := range map[string]state.Status{
		"no own number yet": {OwnJID: "", Mood: "idle"},
		"QR on screen":      {OwnJID: "55500000001@s.whatsapp.net", ShowQR: true},
		"mood qr":           {OwnJID: "55500000001@s.whatsapp.net", Mood: "qr"},
	} {
		w, got := newTestWatch(20 * time.Minute)
		w.observe(t0, s)
		w.observe(t0.Add(3*time.Hour), s)
		if len(*got) != 0 {
			t.Errorf("%s: reported %v, want nothing", name, *got)
		}
	}
}

func TestWatchCauseFollowsWhatTheStateAlreadyShows(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*state.Status)
		want   Cause
	}{
		"plain drop":          {func(*state.Status) {}, CauseWADown},
		"stream replaced":     {func(s *state.Status) { s.Mood = "alert" }, CauseWAReplaced},
		"kill switch tripped": {func(s *state.Status) { s.Muted = true }, CauseWAHalted},
	} {
		s := downStatus()
		tc.mutate(&s)
		if got := waCause(s); got != tc.want {
			t.Errorf("%s: cause = %s, want %s", name, got, tc.want)
		}
	}
}

func TestWatchWindowIsRandomWithinTwentyToFortyMinutes(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 2000; i++ {
		d := randomWAWindow()
		if d < 20*time.Minute || d > 40*time.Minute {
			t.Fatalf("window = %s, want within 20m–40m", d)
		}
		seen[d] = true
	}
	if len(seen) < 100 {
		t.Errorf("only %d distinct windows in 2000 draws — a fixed cadence", len(seen))
	}
}

// While reports are off the watcher does not even look at the state; when the
// owner switches them on mid-outage the clock starts THEN, so a report never
// claims a duration nobody measured.
func TestWatchDoesNotObserveWhileOffAndStartsClockWhenSwitchedOn(t *testing.T) {
	w, got := newTestWatch(30 * time.Minute)
	untouchable := panicSource{}

	w.tick(t0, false, untouchable) // off: the source must not be read
	w.tick(t0.Add(2*time.Hour), false, untouchable)
	if len(*got) != 0 || !w.downSince.IsZero() {
		t.Fatalf("observed while off: reports=%v downSince=%v", *got, w.downSince)
	}

	on := t0.Add(3 * time.Hour)
	w.tick(on, true, stubSource{downStatus()}) // switched on, already down
	if len(*got) != 0 {
		t.Fatalf("reported at the instant it was switched on: %v", *got)
	}
	w.tick(on.Add(30*time.Minute), true, stubSource{downStatus()})
	if len(*got) != 1 || (*got)[0].Duration != 30*time.Minute {
		t.Fatalf("reports = %v, want one of exactly 30m (measured since switch-on, not since the outage began)", *got)
	}

	w.tick(on.Add(31*time.Minute), false, untouchable) // switched off again: forget the outage
	if !w.downSince.IsZero() {
		t.Error("switched off but the outage clock kept running")
	}
}

func TestWatchWhatsAppReturnsAtOnceWhenNothingCouldEverBeSent(t *testing.T) {
	for name, r := range map[string]*Reporter{
		"no reporter":       nil,
		"incomplete wiring": New(Config{Enabled: always}), // no URL, data dir or KV
	} {
		done := make(chan struct{})
		go func() {
			WatchWhatsApp(context.Background(), panicSource{}, r)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(guard):
			t.Errorf("%s: the watcher kept running", name)
		}
	}
}

// A switched-off Reporter keeps the watcher alive (the owner can switch it on
// while Piumy runs), and it still stops with its context.
func TestWatchWhatsAppKeepsRunningWhileOffAndStopsWithItsContext(t *testing.T) {
	old := watchEvery
	watchEvery = 5 * time.Millisecond
	t.Cleanup(func() { watchEvery = old })

	c := newCapture(t)
	r := New(Config{Enabled: never, URL: c.srv.URL, DataDir: t.TempDir(), KV: newMemKV()})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		WatchWhatsApp(ctx, panicSource{}, r) // off: ticks must not read the source
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("the watcher exited while merely switched off")
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(guard):
		t.Fatal("the watcher ignored its context")
	}
}

type stubSource struct{ s state.Status }

func (s stubSource) Snapshot() state.Status { return s.s }

// panicSource fails the test if the watcher reads it: used where reading the
// state would be the bug.
type panicSource struct{}

func (panicSource) Snapshot() state.Status { panic("watcher read the state when it must not") }
