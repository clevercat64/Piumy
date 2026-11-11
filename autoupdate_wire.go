package main

import (
	"context"
	"fmt"
	"os"
	"time"

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

// updateQuietWindowDefault is how far back updateIdle counts pending
// messages when no setting overrides it.
//
// Sized against the longest a message can legitimately sit unanswered while
// still being on its way to an agent: capipush's MaxDispatchDebounce, the
// hard ceiling on burst debouncing, is 5 minutes. Ten minutes covers that
// ceiling plus the dispatch and the sweep that follow it, with margin. It is
// a floor on caution, not a tuning knob — SettingUpdateQuietWindow overrides
// it without a rebuild.
const updateQuietWindowDefault = 10 * time.Minute

// updateIdle is the self-updater's "is this a quiet moment?" (T176,
// ct-2026-11-10-2103): nothing waiting for an agent to answer, nothing waiting
// to go out, no agent in the middle of a turn. Restarting Piumy then loses no
// message and cuts no turn. Not being able to tell is not a quiet moment.
//
// T206 (ct-2026-11-11-1912) bounds the first check to a window. It used to
// count EVERY pending message, which meant one message nobody was ever going
// to answer blocked every future update permanently — measured on a real
// install: 41 old pending, the oldest 9 days, a verified build downloaded and
// never installed. A pending message survives the restart; the turn in
// progress is what does not, and the other two checks below are precisely
// that turn (something on its way out, someone mid-dispatch).
//
// The same mistake had a second shape, measured right after the first: the
// outbox check counted dead letters, so three sends that had given up 95
// days earlier held an update just as permanently, even with zero pending
// messages. "Waiting to go out" means a send that will still be attempted,
// which is what DueOutbox answers and PendingOutbox does not.
func updateIdle(s *store.Store, gate *mcpserver.Gate) func() string {
	return func() string {
		// One clock for both questions: a tick that read the time twice could
		// judge the pending messages and the outbox against different moments.
		now := time.Now()
		window := s.SettingDuration(store.SettingUpdateQuietWindow, updateQuietWindowDefault)
		if n, err := s.CountPendingDedicatedSince(now.Add(-window).Unix()); err != nil || n > 0 {
			return autoupdate.BlockedBusy
		}
		// DueOutbox, not PendingOutbox: only sends that will still be
		// attempted. PendingOutbox is the "everything in the outbox" view and
		// deliberately includes dead letters, which a test of its own pins
		// down — counting those held an update forever on an install with three
		// sends from 95 days earlier that had already given up after four
		// retries. DueOutbox also skips anything still inside its backoff: a
		// send whose next attempt is not due yet is not a turn in progress.
		if queued, err := s.DueOutbox(1, now.Unix()); err != nil || len(queued) > 0 {
			return autoupdate.BlockedBusy
		}
		if gate.AnyInFlight() {
			return autoupdate.BlockedBusy
		}
		return ""
	}
}
