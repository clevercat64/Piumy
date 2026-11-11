package store

import (
	"fmt"
	"strings"
)

// Validation for settings that more than one surface writes (the dashboard's
// REST handlers and the MCP set_config tool). Kept next to the settings
// themselves so both surfaces enforce ONE rule, not two copies that drift.

// ValidConfigLevelDefault reports whether level is acceptable as a default
// config level (SettingConfigLevelDefaultNew/Contact). "boss" is deliberately
// excluded, unlike a per-chat level: a default is never the owner's identity
// (M5, ct-2026-07-22-1903).
func ValidConfigLevelDefault(level string) bool {
	switch level {
	case "auto", "confirm", "unattended", "ignored":
		return true
	}
	return false
}

// ValidRecoveryEmail accepts an empty address (clears it) or anything with an
// "@" and no whitespace — an obviously-broken address is what this rejects,
// real deliverability is not its job.
func ValidRecoveryEmail(email string) bool {
	return email == "" || (strings.Contains(email, "@") && !strings.ContainsAny(email, " \t\n"))
}

// SetAgentDefault stores the agent a chat of some origin is routed to when it
// has no agent of its own (SettingAgentDefault*). "" clears it. The principal
// is a valid target even though it has no `agents` row; any other id must
// exist.
func (s *Store) SetAgentDefault(setting, agentID, principalID string) error {
	if agentID != "" && agentID != principalID {
		if _, ok, err := s.GetAgent(agentID); err != nil {
			return err
		} else if !ok {
			return fmt.Errorf("unknown agent_id %q", agentID)
		}
	}
	return s.KVSet(setting, agentID)
}
