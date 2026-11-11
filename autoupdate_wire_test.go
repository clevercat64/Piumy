package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/autoupdate"
	"piumy-gateway/internal/mcpserver"
	"piumy-gateway/internal/store"
)

// newIdleMachine builds what updateIdle runs against: a real store, a real
// gate, and the closure itself. Shared by T176's test and T206's below, so
// both judge the same machine.
func newIdleMachine(t *testing.T) (*store.Store, *mcpserver.Gate, func() string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	gate := mcpserver.NewGate()
	return s, gate, updateIdle(s, gate)
}

// pendingIn leaves one unanswered inbound message in chat, aged by age.
func pendingIn(t *testing.T, s *store.Store, chat, id string, age time.Duration) {
	t.Helper()
	if err := s.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	// TouchChat creates a GROUP with status 'ignored', and offStatusSQL
	// excludes that status from the pending counts — so a group left as
	// TouchChat made it contributes nothing, and a subtest that staged its
	// debt there would be measuring an empty database instead. A sabotage run
	// caught exactly that. 'new' is the neutral attended status; the measured
	// case overrides it with the assignment that made its 40 messages count.
	if store.IsGroupJID(chat) {
		if err := s.SetStatus(chat, "new"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMessage(store.Message{
		ChatJID: chat, ID: id, Text: "pendiente", TS: time.Now().Add(-age).Unix(),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestUpdateIdleIsOnlyAQuietMoment (T176): the self-updater restarts Piumy only
// when nothing waits for an agent, nothing waits to go out, and no agent is
// mid-turn. Each piece is closed on its own, with a control that is idle.
func TestUpdateIdleIsOnlyAQuietMoment(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"

	t.Run("nothing at all is a quiet moment", func(t *testing.T) {
		_, _, idle := newIdleMachine(t)
		if got := idle(); got != "" {
			t.Errorf("idle = %q on an empty machine, want a quiet moment", got)
		}
	})
	t.Run("a message waiting for an agent", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		if err := s.TouchChat(chat, "Contacto Uno", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.SetMode(chat, "dedicated"); err != nil {
			t.Fatal(err)
		}
		if err := s.SetActive(chat, true); err != nil {
			t.Fatal(err)
		}
		if err := s.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: time.Now().Unix()}); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with an unanswered message, want busy", got)
		}
		if err := s.MarkHandled(chat, "m1"); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != "" {
			t.Errorf("idle = %q once it was answered, want a quiet moment", got)
		}
	})
	t.Run("a reply waiting to go out", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		if err := s.Enqueue(chat, "respuesta", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a reply in the outbox, want busy", got)
		}
	})
	t.Run("an agent in the middle of a turn", func(t *testing.T) {
		_, gate, idle := newIdleMachine(t)
		if err := gate.RegisterDispatch("nonce-a", chat, mcpserver.LevelCaution, "term-a", 0, ""); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a dispatch in flight, want busy", got)
		}
		gate.CancelDispatch("nonce-a", "term-a")
		if got := idle(); got != "" {
			t.Errorf("idle = %q after the dispatch ended, want a quiet moment", got)
		}
	})
}

// fakeSnapshotter es la sesión de WhatsApp vista por el cableado: devuelve
// la ruta que se le diga, o el error que se le diga.
type fakeSnapshotter struct {
	path  string
	err   error
	calls int
}

func (f *fakeSnapshotter) SessionSnapshot(context.Context) (string, error) {
	f.calls++
	return f.path, f.err
}

// TestSessionBackupBeforeUpdateDoesNotTrustAWrittenCopy (T193 parte B): el
// freno del actualizador no se conforma con que la copia diga que se
// escribió — el archivo tiene que estar y tener algo adentro. Un respaldo
// que nadie mira es el que aparece vacío el día que hace falta.
func TestSessionBackupBeforeUpdateDoesNotTrustAWrittenCopy(t *testing.T) {
	dir := t.TempDir()

	good := filepath.Join(dir, "copia-buena")
	if err := os.WriteFile(good, []byte("contenido"), 0o600); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "copia-vacia")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		fake    *fakeSnapshotter
		wantErr bool
	}{
		{"la copia está y tiene contenido", &fakeSnapshotter{path: good}, false},
		{"la copia falló", &fakeSnapshotter{err: errors.New("disco lleno")}, true},
		{"dijo que escribió pero no está", &fakeSnapshotter{path: filepath.Join(dir, "no-existe")}, true},
		{"la copia quedó vacía", &fakeSnapshotter{path: empty}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := sessionBackupBeforeUpdate(context.Background(), tc.fake)()
			if tc.wantErr && err == nil {
				t.Error("debería frenar la actualización y no frenó")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("no debería frenar: %v", err)
			}
			if tc.fake.calls != 1 {
				t.Errorf("debería pedir la copia 1 vez, pidió %d", tc.fake.calls)
			}
		})
	}
}

// TestUpdateIdleIgnoresOldPendingNobodyWillAnswer is T206's regression
// (ct-2026-11-11-1912). Measured on a real install: 41 old pending messages,
// the oldest 9 days, sitting in groups whose assigned agent no longer exists
// or whose antenna is off. updateIdle counted every one of
// them, so a 0.13.1 that had already downloaded and verified stayed at
// blocked=busy and was never installed — not for a while, but permanently,
// because nothing was ever going to answer those messages.
//
// A pending message survives the restart, so restarting to update does not
// lose it. What a restart could actually cut is a conversation happening now:
// that is what the window measures, and what the outbox and gate checks
// cover. Each condition is closed on its own here, each with a control.
func TestUpdateIdleIgnoresOldPendingNobodyWillAnswer(t *testing.T) {
	// A group, like the measured case, and a 1:1 chat. Both with the 555
	// prefix the constitution reserves for an invented identifier.
	const group = "555000000002-555000001@g.us"
	const chat = "555000000001@s.whatsapp.net"

	t.Run("an install with 41 old pending and nothing in flight", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		for i := 0; i < 40; i++ {
			pendingIn(t, s, group, fmt.Sprintf("old-%d", i), 3*time.Hour)
		}
		pendingIn(t, s, chat, "ancient", 9*24*time.Hour)
		// A group is born 'ignored' (TouchChat) and offStatusSQL excludes that,
		// so 40 messages in an untouched group count as zero. In the measured
		// case the group was ASSIGNED to an agent whose terminal no longer
		// exists, which is an attended status — that assignment is what made
		// those 40 count, and leaving it out measured one 1:1 message, not 41.
		const deadAgent = "agent_exclusive:agente-que-ya-no-existe"
		if _, ok := store.AgentExclusiveID(deadAgent); !ok {
			t.Fatal("the agent_exclusive literal no longer parses — the fixture stopped describing the real case")
		}
		if err := s.SetStatus(group, deadAgent); err != nil {
			t.Fatal(err)
		}
		// Control: the debt has to really be in there. Without this,
		// the assertion below would also pass against an empty database and
		// prove nothing at all.
		if n, err := s.CountPendingDedicated(); err != nil || n != 41 {
			t.Fatalf("CountPendingDedicated = %d (err %v), want the 41 this case is about", n, err)
		}
		if got := idle(); got != "" {
			t.Errorf("idle = %q with nothing but old debt, want a quiet moment — this is the update that never installed", got)
		}
	})

	t.Run("a message that just arrived still blocks", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		pendingIn(t, s, chat, "fresh", time.Minute)
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a one-minute-old message, want busy", got)
		}
	})

	// A boss chat is where a shortcut would hurt most: capipush's backpressure
	// count (CountRecentPendingNonBoss) excludes is_boss on purpose, and
	// reusing it here would let an update restart Piumy in the middle of a
	// turn being written.
	t.Run("a recent message from the owner blocks too", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		pendingIn(t, s, chat, "del-boss", time.Minute)
		if err := s.SetIsBoss(chat, true); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a fresh message from the owner, want busy", got)
		}
	})

	// CLAUDE.md's "cero hardcode": the window is wiring, not a literal. Proven
	// by moving it and watching the verdict follow — a test that only used the
	// default could not tell a setting from a constant.
	t.Run("the window comes from the setting, not the code", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		pendingIn(t, s, chat, "five-min", 5*time.Minute)
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Fatalf("idle = %q five minutes in, want busy under the default window", got)
		}
		if err := s.SetSettingDuration(store.SettingUpdateQuietWindow, time.Minute); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != "" {
			t.Errorf("idle = %q after shrinking the window to a minute, want a quiet moment", got)
		}
		// And back the other way, so this measures the direction instead of
		// one lucky state.
		if err := s.SetSettingDuration(store.SettingUpdateQuietWindow, time.Hour); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with the window at an hour, want busy again", got)
		}
	})

	// The other two guards must not have been weakened: old debt stops
	// blocking, a live turn still does.
	t.Run("old debt plus something waiting to go out is still busy", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		pendingIn(t, s, group, "old", 3*time.Hour)
		if got := idle(); got != "" {
			t.Fatalf("idle = %q with only old debt, want a quiet moment before the outbox is loaded", got)
		}
		if err := s.Enqueue(chat, "respuesta", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a reply in the outbox, want busy", got)
		}
	})

	t.Run("old debt plus a dispatch in flight is still busy", func(t *testing.T) {
		s, gate, idle := newIdleMachine(t)
		pendingIn(t, s, group, "old", 3*time.Hour)
		if got := idle(); got != "" {
			t.Fatalf("idle = %q with only old debt, want a quiet moment before the gate is busy", got)
		}
		if err := gate.RegisterDispatch("nonce-t206", chat, mcpserver.LevelCaution, "term-t206", 0, ""); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a dispatch in flight, want busy", got)
		}
		gate.CancelDispatch("nonce-t206", "term-t206")
		if got := idle(); got != "" {
			t.Errorf("idle = %q once the dispatch ended, want a quiet moment", got)
		}
	})

	// The second shape of the same bug, measured on a real install right
	// after the first: zero pending messages and still busy, because the
	// outbox check counted three sends from 95 days earlier that had already
	// given up after four retries. A send nobody will ever attempt again is
	// not a turn in progress.
	t.Run("a dead letter never blocks", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		if err := s.Enqueue(chat, "nunca salio", time.Now().Add(-95*24*time.Hour).Unix()); err != nil {
			t.Fatal(err)
		}
		// Control: while it is still a live send, it MUST block — otherwise
		// this subtest would pass without the dead-lettering doing anything.
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Fatalf("idle = %q with a live send queued, want busy", got)
		}
		queued, err := s.PendingOutbox(10)
		if err != nil || len(queued) != 1 {
			t.Fatalf("setup: PendingOutbox = %+v (err %v), want the one send", queued, err)
		}
		if err := s.DeadLetterOutbox(queued[0].Seq, "destinatario no valido"); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != "" {
			t.Errorf("idle = %q with nothing but a dead letter, want a quiet moment", got)
		}
		// And it is still VISIBLE: hiding it from the agent would trade one
		// defect for another, and a test of PendingOutbox's own pins that down.
		if still, err := s.PendingOutbox(10); err != nil || len(still) != 1 || !still[0].DeadLetter {
			t.Errorf("PendingOutbox = %+v (err %v), want the dead letter still visible", still, err)
		}
	})

	// A send still inside its backoff is not in progress either: its next
	// attempt is in the future, and the restart does not lose it.
	t.Run("a send waiting out its backoff does not block", func(t *testing.T) {
		s, _, idle := newIdleMachine(t)
		if err := s.Enqueue(chat, "reintento lejano", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		queued, err := s.PendingOutbox(10)
		if err != nil || len(queued) != 1 {
			t.Fatalf("setup: PendingOutbox = %+v (err %v), want the one send", queued, err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Fatalf("idle = %q with a send due now, want busy", got)
		}
		far := time.Now().Add(2 * time.Hour).Unix()
		if err := s.SetOutboxRetry(queued[0].Seq, 1, far, "fallo transitorio"); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != "" {
			t.Errorf("idle = %q with the next attempt two hours away, want a quiet moment", got)
		}
	})
}
