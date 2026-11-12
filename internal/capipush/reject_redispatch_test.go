package capipush

import (
	"testing"
	"time"

	"piumy-gateway/internal/store"
)

// TestRejectDraftRedispatchSkipsBackoffEvenWithNewerPendingDraft is T205
// (origin: Bernstein's T191 e2e — TestOperatorToolsCoverage kept timing out
// waiting for reject_draft's own redispatch). Mirrors the e2e test's exact
// shape: FOUR drafts answering one message, resolved out of order (discard
// #1, discard #2, reject #3), with #4 left PENDING throughout — exactly
// what broke the FIRST attempt at this fix, which only ever looked at the
// chat's single "most recent" draft (always #4, never #3's rejection).
// MostRecentRejectionAt looks at every rejected draft for the chat, not
// just the newest one, so #4 staying unresolved must not matter here.
func TestRejectDraftRedispatchSkipsBackoffEvenWithNewerPendingDraft(t *testing.T) {
	st, _, gate, inj, pusher := newTestPusher(t, "")
	pusher.cfg.MaxRedispatch = 7
	chat := "55500000041@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	// First dispatch — attempt 0 -> 1, same as any first contact.
	pusher.sweepOnce()
	consumeCurrent(gate, "port-fallback")
	if got := inj.count(); got != 1 {
		t.Fatalf("setup: Inject calls after first sweep = %d, want 1", got)
	}
	if err := st.MarkHandled(chat, "m1"); err != nil {
		t.Fatal(err)
	}

	// Four drafts answering the same message — same shape as the e2e
	// test's TestOperatorToolsCoverage. burstMaxTS=1 (m1's own TS, plain
	// AddDraft always stores 0) — MarkPendingBefore below filters on
	// `ts <= burstMaxTS`, and without this "m1" (TS=1) would never match.
	for _, text := range []string{"uno", "dos", "tres", "cuatro"} {
		if err := st.AddDraftWithConfirmer(chat, text, "m", "", "", 1, 2); err != nil {
			t.Fatalf("AddDraftWithConfirmer(%q): %v", text, err)
		}
	}
	drafts, err := st.PendingDrafts(10)
	if err != nil || len(drafts) != 4 {
		t.Fatalf("PendingDrafts = %+v, err=%v", drafts, err)
	}
	ids := map[string]int64{}
	for _, d := range drafts {
		ids[d.Text] = d.ID
	}
	if ok, err := st.DiscardDraft(ids["uno"]); err != nil || !ok {
		t.Fatalf("DiscardDraft uno: ok=%v err=%v", ok, err)
	}
	if ok, err := st.DiscardDraft(ids["dos"]); err != nil || !ok {
		t.Fatalf("DiscardDraft dos: ok=%v err=%v", ok, err)
	}
	// "cuatro" stays pending, untouched — the detail that broke the first
	// attempt: it is the chat's most recent draft by created_ts AND by id.
	chatJID, burstMaxTS, _, ok, err := st.RejectDraft(ids["tres"], "no corresponde")
	if err != nil || !ok {
		t.Fatalf("RejectDraft: ok=%v err=%v", ok, err)
	}
	if err := st.MarkPendingBefore(chatJID, burstMaxTS); err != nil {
		t.Fatal(err)
	}

	// Immediate re-sweep, no real elapsed time — without the fix this is
	// TestBackoffHoldsBackImmediateRedispatch's scenario: blocked for
	// >=60s. With it, the rejection (not "cuatro") resets the budget.
	pusher.sweepOnce()
	if got := inj.count(); got != 2 {
		t.Fatalf("Inject calls after reject_draft's redispatch = %d, want 2 (the rejection resets the backoff despite 'cuatro' staying pending)", got)
	}
	consumeCurrent(gate, "port-fallback")

	// The agent never answers this round (no mark_handled, no new draft,
	// "cuatro" still untouched) — the SAME still-outstanding rejection
	// must NOT reset the budget again. Advance lastDispatchAt past the
	// rejection's own second by hand (same technique
	// TestRedispatchCapStopsRunawayFlood uses, just forward instead of
	// back) — real wall-clock time would otherwise tie the rejection and
	// this reset dispatch in the SAME second, which the fix deliberately
	// resolves toward dispatching (see capipush.go's own comment); this
	// assertion is about the round AFTER that one, where the tie must not
	// recur.
	anchor := dispatchAnchor{chat, "m1"}
	if lastAt, ok := pusher.lastDispatchAt[anchor]; ok {
		pusher.lastDispatchAt[anchor] = lastAt.Add(2 * time.Second)
	}
	pusher.sweepOnce()
	if got := inj.count(); got != 2 {
		t.Errorf("Inject calls after a second immediate re-sweep (agent never answered the reset dispatch) = %d, want still 2 (backoff must hold back a repeat of the SAME unanswered rejection)", got)
	}
}
