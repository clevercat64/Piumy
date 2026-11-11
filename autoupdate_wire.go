package main

import (
	"piumy-gateway/internal/autoupdate"
	"piumy-gateway/internal/mcpserver"
	"piumy-gateway/internal/store"
)

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
