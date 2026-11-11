// What is and is not gated in the MCP surface. T148 (ct-2026-09-07-1644, boss
// verbatim: "piumy estaba lleno de candados estupidos") emptied bossOnlyTools
// down to set_kill_switch alone, and T178 (ct-2026-11-10-2330-t178) took the
// per-handler refusals out of the config tools too. These tests pin that
// state: the one lock the boss asked to keep is still there, and nothing new
// has crept in beside it.
package mcpserver

import (
	"encoding/json"
	"testing"
)

func listTools(t *testing.T) []struct {
	Name        string `json:"name"`
	InputSchema struct {
		Properties map[string]any `json:"properties"`
	} `json:"inputSchema"`
} {
	t.Helper()
	_, srv, ctx, _ := newTestServer(t)
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	resp := srv.HandleMessage(ctx, req)
	out, _ := json.Marshal(resp)

	var parsed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]any `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("parse tools/list: %v\nraw: %s", err, out)
	}
	if len(parsed.Result.Tools) == 0 {
		t.Fatal("tools/list returned no tools — test setup broken")
	}
	return parsed.Result.Tools
}

// TestOnlyTheKillSwitchIsBossOnly: the anti-ban brake is the single entry. A
// second name here means a new lock was added without the boss's say-so.
func TestOnlyTheKillSwitchIsBossOnly(t *testing.T) {
	if !bossOnlyTools["set_kill_switch"] {
		t.Error("set_kill_switch left bossOnlyTools — the boss asked to keep this ONE lock (levelgate.go)")
	}
	if len(bossOnlyTools) != 1 {
		t.Errorf("bossOnlyTools = %v, want only set_kill_switch", bossOnlyTools)
	}
}

// TestConfigToolsAreRegisteredAndUngated: every DB-admin and group/profile
// tool exists and is outside bossOnlyTools — registered (a missing name would
// make the check vacuous) and open.
func TestConfigToolsAreRegisteredAndUngated(t *testing.T) {
	open := []string{
		"set_is_boss", "set_type_rules", "set_confirmation_mode", "set_config_level",
		"set_chat_rules", "set_is_approver",
		"approve_draft", "discard_draft", "reject_draft", "edit_draft",
		"create_group", "add_participant", "promote_group_admin", "set_group_icon",
		"set_group_description", "set_profile_status", "set_profile_photo",
	}
	found := map[string]bool{}
	for _, tool := range listTools(t) {
		found[tool.Name] = true
	}
	for _, name := range open {
		if !found[name] {
			t.Errorf("tool %q not registered", name)
		}
		if bossOnlyTools[name] {
			t.Errorf("tool %q is in bossOnlyTools — T148/T178 left only set_kill_switch there", name)
		}
	}
}

// TestAgentWritableToolsStillExist: set_chat_memory/set_chat_context remain
// agent-writable.
func TestAgentWritableToolsStillExist(t *testing.T) {
	foundSetMemory, foundSetContext := false, false
	for _, tool := range listTools(t) {
		switch tool.Name {
		case "set_chat_memory":
			foundSetMemory = true
		case "set_chat_context":
			foundSetContext = true
		}
	}
	if !foundSetMemory {
		t.Error("set_chat_memory tool not found")
	}
	if !foundSetContext {
		t.Error("set_chat_context tool not found")
	}
	if bossOnlyTools["set_chat_memory"] || bossOnlyTools["set_chat_context"] {
		t.Error("set_chat_memory/set_chat_context must stay agent-writable, not boss-only")
	}
}

// TestGetDraftsStillReadOnly: get_drafts is enumeration-gated (anti-leakage,
// levelgate.go), a different restriction from bossOnlyTools.
func TestGetDraftsStillReadOnly(t *testing.T) {
	found := false
	for _, tool := range listTools(t) {
		if tool.Name == "get_drafts" {
			found = true
		}
	}
	if !found {
		t.Error("get_drafts tool not found")
	}
	if bossOnlyTools["get_drafts"] {
		t.Error("get_drafts must not be boss-only — it's enumeration-gated (anti-leakage), a different restriction")
	}
}
