package capipush

import (
	"testing"

	"piumy-gateway/internal/store"
)

// T208 (ct-2026-11-11-2206-t208): the owner's pause, seen from the dispatch
// pipeline. Both tests use timestamps instead of the clock — the pause is a
// floor on message ts (store.notPausedSQL), so "during" and "after" are just
// numbers on either side of paused_until and nothing here can flake.

// TestSweepSkipsAPausedChatAndNeverReturnsItsBacklog walks a pause through
// real sweeps: a paused chat is not dispatched while another chat is (the
// control: the sweep works, the pause is what differs); what arrived during
// the pause never comes back, even after the chat is free; a message after
// the pause goes out alone.
func TestSweepSkipsAPausedChatAndNeverReturnsItsBacklog(t *testing.T) {
	st, _, gate, inj, pusher := newTestPusher(t, "")
	if err := st.SetSettingDuration(store.SettingCapipushDispatchDebounce, 0); err != nil {
		t.Fatal(err)
	}
	const paused, other = "555000000001@s.whatsapp.net", "555000000002@s.whatsapp.net"
	for _, chat := range []string{paused, other} {
		if err := st.TouchChat(chat, "Contacto", 1); err != nil {
			t.Fatal(err)
		}
		dedicate(t, st, chat)
		if err := st.AddMessage(store.Message{ChatJID: chat, ID: "during", Text: "hola", TS: 500}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetPausedUntil(paused, 1000); err != nil {
		t.Fatal(err)
	}

	// finish closes the in-flight dispatch the way the agent does: marks the
	// one message it answered, then releases the terminal.
	finish := func(chat, msgID string) {
		t.Helper()
		active, ok := gate.Active("port-fallback")
		if !ok || active.ChatJID != chat {
			t.Fatalf("setup: Active = %+v (ok=%v), want a dispatch for %s", active, ok, chat)
		}
		if err := st.MarkHandled(chat, msgID); err != nil {
			t.Fatal(err)
		}
		gate.Consume("port-fallback", active.Nonce)
	}

	pusher.sweepOnce()
	if inj.count() != 1 {
		t.Fatalf("first sweep dispatched %d times, want 1 (only the unpaused chat)", inj.count())
	}
	finish(other, "during")

	pusher.sweepOnce()
	if inj.count() != 1 {
		t.Fatalf("sweep with only the paused chat pending dispatched again (count %d) — the pause did not hold", inj.count())
	}

	if err := st.AddMessage(store.Message{ChatJID: paused, ID: "after", Text: "ya libre", TS: 2000}); err != nil {
		t.Fatal(err)
	}
	pusher.sweepOnce()
	if inj.count() != 2 {
		t.Fatalf("sweep after the pause dispatched %d times in total, want 2", inj.count())
	}
	if active, _ := gate.Active("port-fallback"); active.BurstMaxTS != 2000 {
		t.Fatalf("burst newest ts = %d, want 2000", active.BurstMaxTS)
	}
	finish(paused, "after")

	pusher.sweepOnce()
	if inj.count() != 2 {
		t.Errorf("a further sweep dispatched again (count %d) — the message from DURING the pause came back once the chat was free", inj.count())
	}
}

// TestDispatchHonorsAPauseThatLandedAfterTheSweepSnapshot: sweepOnce reads the
// queue once and dispatches from that snapshot (the T196 shape). A pause set
// in between must hold the stale burst back — dispatch re-reads it through
// stillPending. The same snapshot with the pause lifted goes out, so the zero
// above is the pause's doing and not a dispatch that never works.
func TestDispatchHonorsAPauseThatLandedAfterTheSweepSnapshot(t *testing.T) {
	st, _, _, inj, pusher := newTestPusher(t, "")
	const chat = "555000000001@s.whatsapp.net"
	if err := st.TouchChat(chat, "Contacto", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 500}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.PendingDedicated(10)
	if err != nil || len(snapshot) != 1 {
		t.Fatalf("setup: PendingDedicated = %+v (err %v), want m1", snapshot, err)
	}

	if err := st.SetPausedUntil(chat, store.PausedForever); err != nil {
		t.Fatal(err)
	}
	if err := pusher.dispatch(chat, "", snapshot); err != nil {
		t.Fatal(err)
	}
	if inj.count() != 0 {
		t.Fatalf("dispatch of a stale burst into a paused chat injected %d times, want 0", inj.count())
	}

	if err := st.SetPausedUntil(chat, 0); err != nil {
		t.Fatal(err)
	}
	if err := pusher.dispatch(chat, "", snapshot); err != nil {
		t.Fatal(err)
	}
	if inj.count() != 1 {
		t.Fatalf("control: the same burst with the pause lifted injected %d times, want 1", inj.count())
	}
}
