package mcpserver

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/mcpguard"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// TestDupDispatchSilentActOnLaterBurstMarksIt (T196) rebuilds the production timeline
// through the REAL send_message / silent_act handlers: ce6a in flight, a new
// message M from the same speaker arrives, the agent answers under ce6a (M must
// stay pending: burst bound), then d23a (burst = M) is closed with silent_act
// through the agent's ANTENNA id (alias of the registered agent id).
// Rules out: sender mismatch, stale BurstMaxTS, in-flight-period exclusion.
func TestDupDispatchSilentActOnLaterBurstMarksIt(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	const (
		group   = "555000000999@g.us"
		speaker = "555000000001@s.whatsapp.net"
		agent   = "citrino"
		antenna = "capi-test-citrino-aaaa"
	)
	if err := st.UpsertAgent(store.Agent{AgentID: agent, AntennaTerminalID: antenna, Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	if err := st.SetConfirmationMode(group, "none"); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m0", Sender: speaker, Text: "primero", TS: 100}); err != nil {
		t.Fatal(err)
	}

	// ce6a: burst = m0
	if err := gate.RegisterDispatch("ce6a", group, LevelBoss, agent, 100, speaker); err != nil {
		t.Fatal(err)
	}
	// M arrives while ce6a is in flight
	if err := st.AddMessage(store.Message{ChatJID: group, ID: "m1", Sender: speaker, Text: "segundo", TS: 200}); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, antenna)
	_, policyVersion := decisionPolicy("")
	out := callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": group, "message": "respuesta", "model": "m", "policy_version": policyVersion,
	})
	if !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message under ce6a = %s", out)
	}
	pending, _ := st.PendingDedicated(10)
	if len(pending) != 1 || pending[0].ID != "m1" {
		t.Fatalf("after send_message under ce6a pending = %+v, want only m1 (by design: beyond ce6a's burst)", pending)
	}

	// d23a: burst = m1, same speaker string capipush reads from the same column
	if err := gate.RegisterDispatch("d23a", group, LevelBoss, agent, 200, speaker); err != nil {
		t.Fatal(err)
	}
	out = callTool(t, termCtx, srv, "silent_act", map[string]any{"reason": "ya contestado"})
	if !strings.Contains(out, "silence recorded") {
		t.Fatalf("silent_act under d23a = %s", out)
	}
	pending, _ = st.PendingDedicated(10)
	if len(pending) != 0 {
		t.Fatalf("after silent_act under d23a pending = %+v, want none", pending)
	}
}

// TestSendMessageMarksBurstBeforeReleasingTerminal: while send_message answers
// a dispatch, the terminal must never look free (gate.InFlight false) while
// the answered message is still pending. A capipush sweep landing in that gap
// sees a free terminal and a pending message, and re-injects a burst that was
// already answered. The observers check InFlight FIRST, then the store: with
// Consume after the mark, "free" can only be seen once the mark is done.
// Races a spinning observer against the handler, so one run of the broken
// order can miss it; many rounds with several observers make that negligible.
func TestSendMessageMarksBurstBeforeReleasingTerminal(t *testing.T) {
	gate := NewGate()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	// 150 sends in a row: the default emit cap (20/min) would throttle them.
	guard := mcpguard.New(mcpguard.Config{RatePerMin: 1 << 20, EmitRatePerMin: 1 << 20, BlockThreshold: 1 << 20})
	srv := New(ctx, Deps{
		Store: st, State: state.NewManager(filepath.Join(dir, "status.json"), 8),
		Router: router.NewManager(filepath.Join(dir, "router.json")), AgentIdle: time.Minute, Gate: gate, Guard: guard,
	})
	const (
		group   = "555000000999@g.us"
		speaker = "555000000001@s.whatsapp.net"
		agent   = "citrino"
		antenna = "capi-test-citrino-aaaa"
		rounds  = 150
	)
	if err := st.UpsertAgent(store.Agent{AgentID: agent, AntennaTerminalID: antenna, Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	if err := st.TouchChat(group, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	dedicateGroup(t, st, group)
	if err := st.SetConfirmationMode(group, "none"); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, antenna)
	_, policyVersion := decisionPolicy("")

	for i := 0; i < rounds; i++ {
		id, ts := fmt.Sprintf("m%d", i), int64(100+i)
		if err := st.AddMessage(store.Message{ChatJID: group, ID: id, Sender: speaker, Text: "hola", TS: ts}); err != nil {
			t.Fatal(err)
		}
		if err := gate.RegisterDispatch(fmt.Sprintf("n%d", i), group, LevelBoss, agent, ts, speaker); err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		var wg sync.WaitGroup
		var leaked sync.Once
		for o := 0; o < 4; o++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					if gate.InFlight(agent) {
						continue
					}
					pending, _ := st.PendingDedicated(10)
					for _, m := range pending {
						if m.ID == id {
							leaked.Do(func() { t.Errorf("round %d: terminal looked free while %s was still pending", i, id) })
						}
					}
					return
				}
			}()
		}
		out := callTool(t, termCtx, srv, "send_message", map[string]any{
			"to": group, "message": "respuesta", "model": "m", "policy_version": policyVersion,
		})
		close(stop)
		wg.Wait()
		if !strings.Contains(out, "queued for sending") {
			t.Fatalf("round %d: send_message = %s", i, out)
		}
		if t.Failed() {
			return
		}
	}
}
