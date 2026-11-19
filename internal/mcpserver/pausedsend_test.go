package mcpserver

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/store"
)

// ── T208 (ct-2026-11-11-2206-t208) — a send to a chat the owner paused ───
//
// The dispatch queue already skips a paused chat (store.notPausedSQL), but a
// send the agent had in flight when the pause landed would still go out in the
// middle of the owner's own conversation. validateSend refuses it — except
// when the owner is the one ordering it.

const pausedRefusal = "paused the agent"

func sendHola(t *testing.T, ctx context.Context, srv *server.MCPServer, to string) string {
	t.Helper()
	_, policyVersion := decisionPolicy("")
	return callTool(t, ctx, srv, "send_message", map[string]any{
		"to": to, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
}

func pauseChat(t *testing.T, st *store.Store, chat string, until int64) {
	t.Helper()
	if err := st.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPausedUntil(chat, until); err != nil {
		t.Fatal(err)
	}
}

func outboxChats(t *testing.T, st *store.Store) []string {
	t.Helper()
	items, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	var to []string
	for _, it := range items {
		to = append(to, it.ToJID)
	}
	return to
}

// TestSendToPausedChatIsRefusedFromItsOwnDispatch: the reported shape — a live
// dispatch of the very chat that was paused after it went out. The refusal says
// the owner paused it and points at silent_act, which still closes the turn.
func TestSendToPausedChatIsRefusedFromItsOwnDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	const chat, termID, nonce = "555000000081@c.us", "term-paused-own", "nonce-paused-own"
	pauseChat(t, st, chat, store.PausedForever)
	seedAnyChatRules(t, st, "responder normalmente")
	if err := gate.RegisterDispatch(nonce, chat, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": nonce})
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": unlockToken(t, instr)})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	out := sendHola(t, termCtx, srv, chat)
	if !strings.Contains(out, pausedRefusal) || !strings.Contains(out, "silent_act") {
		t.Fatalf("send_message to the paused chat from its own dispatch = %s, want a refusal saying the owner paused it and pointing at silent_act", out)
	}
	if got := outboxChats(t, st); len(got) != 0 {
		t.Errorf("outbox after the refusal = %v, want empty", got)
	}
	if out := callTool(t, termCtx, srv, "silent_act", map[string]any{"reason": "el dueño pausó este chat"}); !strings.Contains(out, "silence recorded") {
		t.Errorf("silent_act after the refusal = %s, want the turn closed", out)
	}
}

// TestSendToPausedChatIsAllowedFromABossDispatch: the owner ordering "escríbele
// a X" over the boss chat must reach a chat they paused.
func TestSendToPausedChatIsAllowedFromABossDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	const bossChat, paused = "555000000082@c.us", "555000000083@c.us"
	if err := st.TouchChat(bossChat, "Dueño", 1); err != nil {
		t.Fatal(err)
	}
	pauseChat(t, st, paused, store.PausedForever)
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, bossChat)

	if out := sendHola(t, termCtx, srv, paused); !strings.Contains(out, "queued for sending") {
		t.Fatalf("send_message to a paused chat from a boss dispatch = %s, want queued for sending", out)
	}
	if got := outboxChats(t, st); len(got) != 1 || got[0] != paused {
		t.Errorf("outbox = %v, want one item to %s", got, paused)
	}
}

// TestConsumedBossDispatchDoesNotExemptThePause: a consumed dispatch stays in
// memory after its turn. If it still exempted the pause, the principal — whose
// last boss turn is almost always sitting there — would walk through every
// pause the owner ever set.
func TestConsumedBossDispatchDoesNotExemptThePause(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	const bossChat, other, paused = "555000000082@c.us", "555000000084@c.us", "555000000083@c.us"
	for _, c := range []string{bossChat, other} {
		if err := st.TouchChat(c, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	pauseChat(t, st, paused, store.PausedForever)
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, bossChat)

	if out := sendHola(t, termCtx, srv, other); !strings.Contains(out, "queued for sending") {
		t.Fatalf("setup: send_message to an unpaused chat from a boss dispatch = %s", out)
	}
	if active, ok := gate.Active("term-test"); !ok || !active.Done {
		t.Fatalf("setup: Active after the send = %+v, ok=%v, want the dispatch consumed (Done)", active, ok)
	}

	if out := sendHola(t, termCtx, srv, paused); !strings.Contains(out, pausedRefusal) {
		t.Errorf("send_message to a paused chat from a CONSUMED boss dispatch = %s, want the pause refusal", out)
	}
	if got := outboxChats(t, st); len(got) != 1 || got[0] != other {
		t.Errorf("outbox = %v, want only the first send to %s", got, other)
	}
}

// TestSendWithNoDispatchHonorsThePause: an agent initiating on its own (T64)
// gets the same refusal, with an end or without; a chat whose pause already
// ended, or never had one, takes the send.
func TestSendWithNoDispatchHonorsThePause(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := withTerminalID(ctx, "term-initiative")

	cases := []struct {
		chat        string
		pausedUntil int64
		refused     bool
	}{
		{"555000000085@c.us", store.PausedForever, true},
		{"555000000086@c.us", time.Now().Add(time.Hour).Unix(), true},
		{"555000000087@c.us", 100, false},
		{"555000000088@c.us", 0, false},
	}
	var wantQueued []string
	for _, c := range cases {
		pauseChat(t, st, c.chat, c.pausedUntil)
		out := sendHola(t, termCtx, srv, c.chat)
		if c.refused {
			if !strings.Contains(out, pausedRefusal) {
				t.Errorf("paused_until=%d: send_message = %s, want the pause refusal", c.pausedUntil, out)
			}
			continue
		}
		if !strings.Contains(out, "queued for sending") {
			t.Errorf("paused_until=%d: send_message = %s, want queued for sending", c.pausedUntil, out)
		}
		wantQueued = append(wantQueued, c.chat)
	}
	got := outboxChats(t, st)
	if strings.Join(got, ",") != strings.Join(wantQueued, ",") {
		t.Errorf("outbox = %v, want exactly the sends that went through: %v", got, wantQueued)
	}
}

func TestSendAtPauseDeadlineIsAllowed(t *testing.T) {
	st := openAgentDB(t)
	seedAnyChatRules(t, st, "responder normalmente")
	_, policyVersion := decisionPolicy("")

	synctest.Test(t, func(t *testing.T) {
		const chat = "555000000089@c.us"
		pauseChat(t, st, chat, time.Now().Unix())
		_, refusal := validateSend(t.Context(), Deps{Store: st}, ActiveDispatch{}, false, chat, "m", policyVersion)
		if refusal != "" {
			t.Fatalf("validateSend at the pause deadline refused: %s", refusal)
		}
	})
}
