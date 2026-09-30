package mcpserver

import (
	"strings"
	"testing"
)

// ── T150 (ct-2026-09-07-1839) — a consumed dispatch must never block an
// ungated tool ─────────────────────────────────────────────────────────
//
// Reported live by Citrino de temascal: get_status (never gated — not in
// bossOnlyTools/enumerationTools/chatScopedArg) refused with "locked: this
// dispatch was already consumed", right when the owner asked whether the
// gateway was stuck. Root cause, already localized in the contract:
// levelGateMiddleware computes isGated up front but only CONSULTS it in the
// no-dispatch branch (!ok) — a CONSUMED dispatch (ok==true, Ready==false)
// skips straight into the boss-Ready gate below, blocking a tool that was
// never supposed to need one at all. The principle: a tool that needs no
// gate is INFORMATION, not permission — dispatch state must never block it.

// TestUngatedToolNeverBlockedByConsumedDispatch is the exact reported
// shape: a boss-level dispatch, consumed by a real send, then get_status on
// the SAME terminal.
func TestUngatedToolNeverBlockedByConsumedDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000090@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if active, ok := gate.Active("term-test"); !ok || active.Ready {
		t.Fatalf("setup: Active after consume = %+v, ok=%v, want bound with Ready=false", active, ok)
	}

	out := callTool(t, termCtx, srv, "get_status", map[string]any{})
	if strings.Contains(out, "locked") || strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status with a consumed boss dispatch bound = %s, want it to answer normally — it is never gated at all, dispatch state is not its business", out)
	}
}

// TestUngatedToolNeverBlockedByConsumedNonBossDispatch is the same check
// for a caution-level dispatch — the bug wasn't boss-specific (the !ok
// branch is the only place isGated used to matter, regardless of level).
func TestUngatedToolNeverBlockedByConsumedNonBossDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000094@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termID := "term-caution-consumed"
	if err := gate.RegisterDispatch("nonce-caution-consumed", chat, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	instr := callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-caution-consumed"})
	token := unlockToken(t, instr)
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	out := callTool(t, termCtx, srv, "get_status", map[string]any{})
	if strings.Contains(out, "locked") || strings.Contains(out, `"isError":true`) {
		t.Errorf("get_status with a consumed caution dispatch bound = %s, want it to answer normally", out)
	}
}

// TestGatedToolStillBlockedByConsumedBossDispatch is the DoD's own guard
// against overcorrecting: T150 must not become "consumed state never
// matters" — a tool that DOES need gating (bossOnlyTools here) must stay
// refused when the only dispatch on this terminal is a consumed one.
func TestGatedToolStillBlockedByConsumedBossDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000095@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})

	out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})
	if !strings.Contains(out, "boss-only") {
		t.Errorf("set_kill_switch (bossOnlyTools, gated) with a consumed boss dispatch = %s, want it to STILL refuse — T150 doesn't loosen actually-gated tools", out)
	}
}

// TestGatedToolStillDeniedWithNoDispatchAtAll: set_kill_switch is the one
// tool that stays refused with no dispatch at all (T170).
func TestGatedToolStillDeniedWithNoDispatchAtAll(t *testing.T) {
	gate := NewGate()
	_, srv, ctx := serverWithGate(t, gate)
	termCtx := withTerminalID(ctx, "term-no-dispatch-at-all")

	out := callTool(t, termCtx, srv, "set_kill_switch", map[string]any{"kill": true})
	if !strings.Contains(out, "boss-only") {
		t.Errorf("set_kill_switch with no dispatch at all = %s, want refused boss-only", out)
	}
}

// T170 DoD cases: what a dispatch that is not alive no longer blocks, and
// what a LIVE caution one still does.
func TestConsumedCautionDispatchCanEnumerate(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "555000000096@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	seedAnyChatRules(t, st, "responder normalmente")
	termID := "term-caution-done-enum"
	if err := gate.RegisterDispatch("nonce-cde", chat, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	token := unlockToken(t, callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cde"}))
	callTool(t, termCtx, srv, "unlock", map[string]any{"token": token})
	callTool(t, termCtx, srv, "skip", map[string]any{})

	if out := callTool(t, termCtx, srv, "list_chats", map[string]any{}); !strings.Contains(out, "refused") {
		t.Fatalf("setup: list_chats with a LIVE caution dispatch = %s, want refused (anti-leakage)", out)
	}
	_, policyVersion := decisionPolicy("")
	callTool(t, termCtx, srv, "send_message", map[string]any{
		"to": chat, "message": "hola", "model": "m", "policy_version": policyVersion,
	})
	if out := callTool(t, termCtx, srv, "list_chats", map[string]any{}); strings.Contains(out, "refused") {
		t.Errorf("list_chats with a consumed caution dispatch = %s, want it to answer", out)
	}
}

func TestLiveCautionDispatchStillDeniesOtherChat(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat, other := "555000000097@c.us", "555000000098@c.us"
	for _, c := range []string{chat, other} {
		if err := st.TouchChat(c, "C", 1); err != nil {
			t.Fatal(err)
		}
	}
	termID := "term-caution-live"
	if err := gate.RegisterDispatch("nonce-cl", chat, LevelCaution, termID, 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, termID)
	if out := callTool(t, termCtx, srv, "get_messages", map[string]any{"chat_id": other}); !strings.Contains(out, "anti-leakage") {
		t.Errorf("get_messages of ANOTHER chat with a live caution dispatch = %s, want refused (anti-leakage)", out)
	}
}
