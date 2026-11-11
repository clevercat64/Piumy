package capipush

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"piumy-gateway/internal/store"
)

const (
	pauseGroup  = "555001@g.us"
	pauseAlice  = "555000000001@s.whatsapp.net"
	pauseReopen = "2026-11-16T09:00:00-03:00"
	pauseText   = "Agotaste tu cuota. Espera hasta el lunes 16 de noviembre a las 09:00, o abona aquí: https://pago.example/555"
	pausePayURL = "https://pago.example/555"
)

// pausedGroupChat sets up one dedicated group chat, the same shape T108's
// tests use, with alice as the speaker.
func pausedGroupChat(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.TouchChat(pauseGroup, "Grupo", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetStatus(pauseGroup, "new"); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, pauseGroup)
}

func addPauseMessage(t *testing.T, st *store.Store, id string, ts int64) {
	t.Helper()
	if err := st.AddMessage(store.Message{ChatJID: pauseGroup, ID: id, Sender: pauseAlice, Text: "hola", TS: ts}); err != nil {
		t.Fatal(err)
	}
}

func pendingCount(t *testing.T, st *store.Store) int {
	t.Helper()
	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	return len(pending)
}

func outboxTexts(t *testing.T, st *store.Store) []string {
	t.Helper()
	rows, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, len(rows))
	for i, r := range rows {
		if r.ToJID != pauseGroup {
			t.Errorf("outbox row %d goes to %q, want the group %q that was paused", i, r.ToJID, pauseGroup)
		}
		texts[i] = r.Text
	}
	return texts
}

// TestPausedClientGetsOneNoticeAndTurnClosed is T175's core: CleverCoder says
// the client is paused → the group gets CleverCoder's text (via the outbox,
// i.e. the governor's pacing), the burst is closed so the sweep doesn't loop
// on it, the terminal isn't left "in flight", and a second message of the same
// pause — even after a restart — gets no second notice.
func TestPausedClientGetsOneNoticeAndTurnClosed(t *testing.T) {
	st, rt, gate, inj, pusher := newTestPusher(t, "")
	inj.setErr(&PausedError{Client: "555001", ReopenAt: pauseReopen, Text: pauseText})
	pausedGroupChat(t, st)
	addPauseMessage(t, st, "m1", 1)

	pusher.sweepOnce()

	if got := outboxTexts(t, st); len(got) != 1 || got[0] != pauseText {
		t.Fatalf("outbox = %q, want exactly CleverCoder's text", got)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Errorf("%d messages still pending, want the paused burst closed (else the sweep re-dispatches it forever)", n)
	}
	if gate.InFlight("port-fallback") {
		t.Error("terminal left in flight after a paused dispatch — nothing reached it, the dispatch must be cancelled")
	}

	// Same pause, new message, and a freshly built Pusher (a restart): the
	// marker lives in the store, not in memory.
	addPauseMessage(t, st, "m2", 2)
	restarted := New(st, rt, gate, inj, Config{PortFallback: "port-fallback", SwampedAt: 8})
	restarted.sweepOnce()

	if inj.count() != 2 {
		t.Fatalf("injector called %d times, want 2 — CleverCoder stays the authority on whether the client is still paused", inj.count())
	}
	if got := outboxTexts(t, st); len(got) != 1 {
		t.Errorf("outbox = %q after a second message in the same pause, want still exactly 1 notice", got)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Errorf("%d messages pending after the second paused message, want it closed too", n)
	}
}

func TestPausedNoticeAddsPayURLOnlyWhenTextLacksIt(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"text without the link gets it appended", "Cuota agotada, vuelve el lunes.", "Cuota agotada, vuelve el lunes.\n" + pausePayURL},
		{"text that already carries the link is sent as is", pauseText, pauseText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, _, _, inj, pusher := newTestPusher(t, "")
			inj.setErr(&PausedError{Client: "555001", ReopenAt: pauseReopen, Text: tc.text, PayURL: pausePayURL})
			pausedGroupChat(t, st)
			addPauseMessage(t, st, "m1", 1)

			pusher.sweepOnce()

			if got := outboxTexts(t, st); len(got) != 1 || got[0] != tc.want {
				t.Errorf("outbox = %q, want exactly %q", got, tc.want)
			}
		})
	}
}

// TestPausedWithoutTextInventsNothing is the old-CleverCoder case (its 200 has
// no "text"): nothing is sent to the group, the turn still closes, and the
// reason is logged once per pause, not once per message.
func TestPausedWithoutTextInventsNothing(t *testing.T) {
	st, _, _, inj, pusher := newTestPusher(t, "")
	inj.setErr(&PausedError{Client: "555001", ReopenAt: pauseReopen, PayURL: pausePayURL})
	pausedGroupChat(t, st)
	addPauseMessage(t, st, "m1", 1)
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	pusher.sweepOnce()
	addPauseMessage(t, st, "m2", 2)
	pusher.sweepOnce()

	if got := outboxTexts(t, st); len(got) != 0 {
		t.Errorf("outbox = %q, want nothing: Piumy never invents the notice text", got)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Errorf("%d messages pending, want the turn closed even with no text to send", n)
	}
	if n := strings.Count(buf.String(), "no mandó el texto del aviso"); n != 1 {
		t.Errorf("missing-text line logged %d times, want exactly once — log = %q", n, buf.String())
	}
}

// TestNewReopenAtIsANewPause: the marker is the pause's reopen_at, so a later
// pause (another week, another cap) gets its own notice.
func TestNewReopenAtIsANewPause(t *testing.T) {
	st, _, _, inj, pusher := newTestPusher(t, "")
	pausedGroupChat(t, st)
	inj.setErr(&PausedError{Client: "555001", ReopenAt: pauseReopen, Text: "primera pausa"})
	addPauseMessage(t, st, "m1", 1)
	pusher.sweepOnce()

	inj.setErr(&PausedError{Client: "555001", ReopenAt: "2026-11-23T09:00:00-03:00", Text: "segunda pausa"})
	addPauseMessage(t, st, "m2", 2)
	pusher.sweepOnce()

	got := outboxTexts(t, st)
	if len(got) != 2 || got[0] != "primera pausa" || got[1] != "segunda pausa" {
		t.Errorf("outbox = %q, want one notice per pause", got)
	}
}

// TestReopenedClientIsAnsweredAgainOnSamePauseDate is why a dispatch that
// reaches an agent clears the marker: "Reabrir ahora" and the client running
// out again the same week gives the SAME reopen_at — without the clear that
// second pause would be silent again.
func TestReopenedClientIsAnsweredAgainOnSamePauseDate(t *testing.T) {
	st, _, gate, inj, pusher := newTestPusher(t, "")
	pausedGroupChat(t, st)
	paused := &PausedError{Client: "555001", ReopenAt: pauseReopen, Text: pauseText}

	inj.setErr(paused)
	addPauseMessage(t, st, "m1", 1)
	pusher.sweepOnce()

	inj.setErr(nil) // the boss reopened the client: this dispatch reaches the agent
	addPauseMessage(t, st, "m2", 2)
	pusher.sweepOnce()
	if inj.count() != 2 || pendingCount(t, st) != 1 {
		t.Fatalf("setup: the dispatch after the reopening didn't reach the agent (calls=%d, pending=%d)", inj.count(), pendingCount(t, st))
	}
	consumeCurrent(gate, "port-fallback")
	if err := st.MarkHandledBefore(pauseGroup, 2); err != nil {
		t.Fatal(err)
	}

	inj.setErr(paused) // out of quota again, same week → same reopen_at
	addPauseMessage(t, st, "m3", 3)
	pusher.sweepOnce()

	if got := outboxTexts(t, st); len(got) != 2 {
		t.Errorf("outbox = %q, want a notice for each of the two pauses", got)
	}
}

// TestCleverInjectorPausedResponseBecomesPausedError reads the real wire
// shape (ExternalAgentApiService.cs: ok/paused/client/reopen_at, plus text and
// pay_url from T175) through the real injector.
func TestCleverInjectorPausedResponseBecomesPausedError(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want PausedError
	}{
		{
			"CleverCoder with text and pay_url",
			map[string]any{"ok": true, "paused": true, "client": "555001", "reopen_at": pauseReopen, "text": pauseText, "pay_url": pausePayURL},
			PausedError{Client: "555001", ReopenAt: pauseReopen, Text: pauseText, PayURL: pausePayURL},
		},
		{
			"CleverCoder older than T175: no text, no pay_url",
			map[string]any{"ok": true, "paused": true, "client": "555001", "reopen_at": pauseReopen},
			PausedError{Client: "555001", ReopenAt: pauseReopen},
		},
		{
			"paused wins over a false ok",
			map[string]any{"ok": false, "paused": true, "client": "555001"},
			PausedError{Client: "555001"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/handshake":
					_ = json.NewEncoder(w).Encode(map[string]string{"token": "tok0000000000000000000000000000"})
				case "/message":
					_ = json.NewEncoder(w).Encode(tc.body)
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
				}
			}))
			defer srv.Close()

			err := NewCleverInjector(srv.URL, "term-555", "pin==").Inject("", "555001, Grupo, caution", "payload")

			var paused *PausedError
			if !errors.As(err, &paused) {
				t.Fatalf("Inject error = %v, want a *PausedError", err)
			}
			if *paused != tc.want {
				t.Errorf("PausedError = %+v, want %+v", *paused, tc.want)
			}
		})
	}
}
