package whatsmeow

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"

	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/state"
)

// TestFailureNodeReachesTheRightHandler (T200, ct-2026-11-11-1441)
// pins down the ONE link the rest of
// this file's kill-switch tests take on faith: they start from an already
// typed event (events.LoggedOut, events.TemporaryBan...), but WhatsApp does
// not send typed events — it sends a single <failure reason="N"> node, and
// whatsmeow decides which event that becomes. That decision lives in the
// library, not here, so nothing in this repo was asserting it.
//
// It matters because the sorting is NOT one-code-one-event: whatsmeow routes
// the codes it recognises to dedicated events and only funnels the rest into
// events.ConnectFailure (see its doc: "emitted when the WhatsApp server sends
// a <failure> node with an unknown reason"). 401 and 402 — an unlinked
// account and a temporary ban, the two codes that MUST stop the gateway from
// sending — are recognised, so they arrive as LoggedOut/TemporaryBan and trip
// the kill switch. If a library update ever moved them into the
// ConnectFailure bucket instead, Piumy would keep trying to send with a dead
// account (the anti-ban cost this guards against) and every existing test
// would still pass, because they inject the typed event by hand. This test
// fails instead.
//
// Measured, not assumed: these expectations come from feeding real nodes
// through the library's own handler at whatsmeow
// v0.0.0-20261110152934-c88388f983b4. 413/414 (auth token) and 500/503
// (server) are left out on purpose: they dispatch no failure event at all,
// so the owner is shown the generic "no network" instead of the real motive
// — a separate gap, tracked on its own (T204).
func TestFailureNodeReachesTheRightHandler(t *testing.T) {
	cases := []struct {
		name        string
		reason      string
		wantKill    bool
		wantMood    string
		wantDown    state.WADownReason
		wantDeleted bool // whatsmeow drops the session itself on a logout
	}{
		{"401 logged out", "401", true, "error", state.WADownLoggedOut, true},
		{"402 temporarily banned", "402", true, "error", state.WADownTempBan, false},
		{"403 primary device gone", "403", true, "error", state.WADownLoggedOut, true},
		{"406 logout, reason unknown", "406", true, "error", state.WADownLoggedOut, true},
		{"405 client outdated", "405", false, "error", state.WADownOutdated, false},
		{"409 user agent rejected", "409", false, "error", state.WADownOutdated, false},
		{"400 generic, no motive", "400", false, "error", "", false},
		{"415 not found, no motive", "415", false, "error", "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
			gov := governor.NewLimiter(10, time.Minute)
			a := &Adapter{inbound: make(chan gateway.Inbound, 4), state: sm, governor: gov}

			cli := newTestWmeowClient(t)
			handled := make(chan struct{}, 4)
			cli.AddEventHandler(func(evt any) {
				a.handleEvent(evt)
				handled <- struct{}{}
			})

			// The real node, exactly as the server sends it: attributes are
			// strings on the wire (binary.AttrUtility parses them), and code/
			// expire are the ones a 402 carries.
			cli.DangerousInternals().HandleConnectFailure(context.Background(), &waBinary.Node{
				Tag:   "failure",
				Attrs: waBinary.Attrs{"reason": c.reason, "code": "1", "expire": "600"},
			})

			// whatsmeow dispatches with `go cli.dispatchEvent(...)`, so wait
			// for the handler instead of asserting straight away. A timeout
			// here means the library stopped dispatching anything for this
			// code — itself the regression this test exists to catch.
			select {
			case <-handled:
			case <-time.After(2 * time.Second):
				t.Fatalf("la librería no despachó ningún evento para <failure reason=%q>", c.reason)
			}

			if got := gov.Killed(); got != c.wantKill {
				t.Errorf("governor.Killed() = %v, want %v", got, c.wantKill)
			}
			snap := sm.Snapshot()
			if snap.Muted != c.wantKill {
				t.Errorf("state.Muted = %v, want %v", snap.Muted, c.wantKill)
			}
			if snap.Mood != c.wantMood {
				t.Errorf("state.Mood = %q, want %q", snap.Mood, c.wantMood)
			}
			if snap.WADownReason != c.wantDown {
				t.Errorf("state.WADownReason = %q, want %q", snap.WADownReason, c.wantDown)
			}
			if snap.WAConnected {
				t.Error("state.WAConnected = true, want false")
			}

			// Store.Delete runs AFTER the event is dispatched (connectionevents.go),
			// so it can still be in flight when the handler returns — poll instead
			// of reading once, which would be flaky. This is the state resetDeletedDevice (T187) repairs
			// before pairing again, which is why "Conectar QR" works after a logout.
			if c.wantDeleted {
				deadline := time.Now().Add(2 * time.Second)
				for !cli.Store.Deleted && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
				}
				if !cli.Store.Deleted {
					t.Error("Store.Deleted = false, want true — whatsmeow must drop the session on a logout code")
				}
			} else {
				// Only a logout code deletes, so there is nothing to wait for
				// here; the short grace is what would catch a future version
				// that deleted the session on a ban or an outdated client.
				time.Sleep(200 * time.Millisecond)
				if cli.Store.Deleted {
					t.Error("Store.Deleted = true, want false — only a logout may drop the session")
				}
			}
		})
	}
}
