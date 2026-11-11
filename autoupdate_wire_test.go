package main

import (
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/autoupdate"
	"piumy-gateway/internal/mcpserver"
	"piumy-gateway/internal/store"
)

// TestUpdateIdleIsOnlyAQuietMoment (T176): the self-updater restarts Piumy only
// when nothing waits for an agent, nothing waits to go out, and no agent is
// mid-turn. Each piece is closed on its own, with a control that is idle.
func TestUpdateIdleIsOnlyAQuietMoment(t *testing.T) {
	newMachine := func(t *testing.T) (*store.Store, *mcpserver.Gate, func() string) {
		t.Helper()
		s, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		gate := mcpserver.NewGate()
		return s, gate, updateIdle(s, gate)
	}
	const chat = "555000000001@s.whatsapp.net"

	t.Run("nothing at all is a quiet moment", func(t *testing.T) {
		_, _, idle := newMachine(t)
		if got := idle(); got != "" {
			t.Errorf("idle = %q on an empty machine, want a quiet moment", got)
		}
	})
	t.Run("a message waiting for an agent", func(t *testing.T) {
		s, _, idle := newMachine(t)
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
		s, _, idle := newMachine(t)
		if err := s.Enqueue(chat, "respuesta", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
		if got := idle(); got != autoupdate.BlockedBusy {
			t.Errorf("idle = %q with a reply in the outbox, want busy", got)
		}
	})
	t.Run("an agent in the middle of a turn", func(t *testing.T) {
		_, gate, idle := newMachine(t)
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
