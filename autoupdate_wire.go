package main

import (
	"context"
	"fmt"
	"os"

	"piumy-gateway/internal/autoupdate"
	"piumy-gateway/internal/mcpserver"
	"piumy-gateway/internal/store"
)

// sessionSnapshotter copies the WhatsApp session DB on demand —
// whatsmeow.Adapter.SessionSnapshot satisfies it. An interface, not the
// Adapter, so this wiring is testable AND so the compiler refuses anything
// that is not the WhatsApp session: sessionbackup.Backuper has no such
// method, and pointing this at it is exactly the bug T193's first attempt
// had (it would have copied piumy's own store.db while telling the owner
// their WhatsApp session was safe).
type sessionSnapshotter interface {
	SessionSnapshot(ctx context.Context) (string, error)
}

// sessionBackupBeforeUpdate is the copy the self-updater takes right before
// launching the installer (T193 parte B, ct-2026-11-11-1039). If it comes
// back with an error the install does not happen: a postponed update costs
// one more look, a lost session costs re-scanning the QR.
//
// It does not trust "I wrote it": the file has to BE on disk with something
// in it. A backup nobody looks at is the kind that turns up empty the day
// it is needed.
func sessionBackupBeforeUpdate(ctx context.Context, gw sessionSnapshotter) func() error {
	return func() error {
		path, err := gw.SessionSnapshot(ctx)
		if err != nil {
			return err
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("la copia de la sesión no quedó escrita: %w", err)
		}
		if info.Size() == 0 {
			return fmt.Errorf("la copia de la sesión quedó vacía: %s", path)
		}
		return nil
	}
}

// updateIdle is the self-updater's "is this a quiet moment?" (T176,
// ct-2026-11-10-2103): nothing waiting for an agent to answer, nothing waiting
// to go out, no agent in the middle of a turn. Restarting Piumy then loses no
// message and cuts no turn. Not being able to tell is not a quiet moment.
func updateIdle(s *store.Store, gate *mcpserver.Gate) func() string {
	return func() string {
		if n, err := s.CountPendingDedicated(); err != nil || n > 0 {
			return autoupdate.BlockedBusy
		}
		if queued, err := s.PendingOutbox(1); err != nil || len(queued) > 0 {
			return autoupdate.BlockedBusy
		}
		if gate.AnyInFlight() {
			return autoupdate.BlockedBusy
		}
		return ""
	}
}
