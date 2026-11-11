package capipush

import (
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/store"
)

// blockingInjector parks Inject until released — stands for ANY slow neighbour
// in the same sweep (an unreachable antenna, a busy DB connection).
type blockingInjector struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingInjector) Inject(_, _, _ string) error {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return nil
}

// TestDupDispatchStaleSnapshotRedispatchesHandledBurst: sweepOnce reads the
// pending set ONCE (dueChats) and dispatches key by key from that snapshot.
// If, between the snapshot and the InFlight check of chat G's key, the agent
// finishes G's dispatch the way silent_act does (mark handled, then Consume),
// the terminal looks free and G's STALE burst is injected again although its
// message is already handled=1.
func TestDupDispatchStaleSnapshotRedispatchesHandledBurst(t *testing.T) {
	const (
		group   = "555000000999@g.us"
		speaker = "555000000001@s.whatsapp.net"
		other   = "555000000777@s.whatsapp.net"
	)
	for attempt := 1; attempt <= 40; attempt++ {
		st, _, gate, inj, pusher := newTestPusher(t, "")
		if err := st.SetSettingDuration(store.SettingCapipushDispatchDebounce, 0); err != nil {
			t.Fatal(err)
		}
		blocker := &blockingInjector{entered: make(chan struct{}), release: make(chan struct{})}
		pusher.RegisterInjector("term-x", blocker)

		if err := st.TouchChat(group, "Grupo", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.SetStatus(group, "new"); err != nil {
			t.Fatal(err)
		}
		dedicate(t, st, group)
		old := time.Now().Add(-time.Minute).Unix()
		if err := st.AddMessage(store.Message{ChatJID: group, ID: "m1", Sender: speaker, Text: "hola", TS: old}); err != nil {
			t.Fatal(err)
		}
		// sweep 1: only G pending -> dispatch d1 to the principal terminal
		pusher.sweepOnce()
		if inj.count() != 1 {
			t.Fatalf("setup: first dispatch count = %d, want 1", inj.count())
		}
		// a second chat, routed to a terminal whose Inject is slow
		if err := st.TouchChat(other, "X", 1); err != nil {
			t.Fatal(err)
		}
		dedicate(t, st, other)
		if err := st.SetStatus(other, store.AgentExclusiveStatus("term-x")); err != nil {
			t.Fatal(err)
		}
		if err := st.AddMessage(store.Message{ChatJID: other, ID: "x1", Text: "hola", TS: old - 60}); err != nil {
			t.Fatal(err)
		}
		// backoff already elapsed for G (real run: 60-75 s after d1)
		pusher.lastDispatchAt[dispatchAnchor{group, "m1"}] = time.Now().Add(-2 * time.Hour)

		done := make(chan struct{})
		go func() { pusher.sweepOnce(); close(done) }()
		select {
		case <-blocker.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("sweep never reached the slow injector")
		}
		if pusher.logState["inFlight:port-fallback"] {
			// G's key was already evaluated (postponed) before the slow key:
			// no stale window this attempt. Map order is random; try again.
			close(blocker.release)
			<-done
			continue
		}
		// G's key is still ahead in this sweep, holding the snapshot with m1.
		// The agent closes d1 exactly like silent_act: mark handled, then Consume.
		active, ok := gate.Active("port-fallback")
		if !ok {
			t.Fatal("setup: no active dispatch")
		}
		if err := st.MarkHandledBeforeForSender(active.ChatJID, active.Sender, active.BurstMaxTS); err != nil {
			t.Fatal(err)
		}
		gate.Consume("port-fallback", active.Nonce)
		pending, _ := st.PendingDedicated(10)
		for _, m := range pending {
			if m.ChatJID == group {
				t.Fatalf("setup: m1 still pending after the mark: %+v", m)
			}
		}
		close(blocker.release)
		<-done

		if got := inj.count(); got != 1 {
			t.Fatalf("attempt %d: principal terminal got %d dispatches, want 1 — m1 was handled=1 before the 2nd dispatch (stale-snapshot redispatch)", attempt, got)
		}
		return
	}
	t.Skip("map order never put the slow key first in 40 attempts")
}

// TestStillPendingKeepsUnhandledInOrder: the re-read keeps the burst's
// messages the agent has not closed, in the burst's own ts order.
func TestStillPendingKeepsUnhandledInOrder(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"
	st, _, _, _, pusher := newTestPusher(t, "")
	var burst []store.Message
	for i, id := range []string{"m1", "m2", "m3"} {
		m := store.Message{ChatJID: chat, ID: id, Text: id, TS: int64(100 + i)}
		if err := st.AddMessage(m); err != nil {
			t.Fatal(err)
		}
		burst = append(burst, m)
	}
	if err := st.MarkHandled(chat, "m2"); err != nil {
		t.Fatal(err)
	}

	got, err := pusher.stillPending(chat, burst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "m1" || got[1].ID != "m3" {
		t.Fatalf("stillPending = %+v, want [m1 m3]", got)
	}
}
