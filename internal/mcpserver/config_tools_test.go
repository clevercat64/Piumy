package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/store"
)

// configCall runs a tool and unwraps its JSON-RPC envelope: the text the tool
// returned, and whether it was an error.
func configCall(t *testing.T, ctx context.Context, srv *server.MCPServer, tool string, args map[string]any) (text string, isErr bool) {
	t.Helper()
	var env struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(callTool(t, ctx, srv, tool, args)), &env); err != nil || len(env.Result.Content) == 0 {
		t.Fatalf("%s: unreadable response (err=%v)", tool, err)
	}
	return env.Result.Content[0].Text, env.Result.IsError
}

func configEntries(t *testing.T, ctx context.Context, srv *server.MCPServer, args map[string]any) map[string]configEntry {
	t.Helper()
	text, isErr := configCall(t, ctx, srv, "get_config", args)
	if isErr {
		t.Fatalf("get_config failed: %s", text)
	}
	var out struct {
		Keys []configEntry `json:"keys"`
	}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("get_config: not JSON: %v\n%s", err, text)
	}
	byKey := map[string]configEntry{}
	for _, e := range out.Keys {
		byKey[e.Key] = e
	}
	return byKey
}

func TestGetConfigListsEveryKeyWithItsDescription(t *testing.T) {
	_, srv, ctx := serverWithGate(t, NewGate())
	got := configEntries(t, ctx, srv, nil)
	if len(got) != len(configKeys()) {
		t.Errorf("get_config returned %d keys, want %d", len(got), len(configKeys()))
	}
	for name, e := range got {
		if e.Description == "" || e.Type == "" {
			t.Errorf("%s: description %q / type %q must not be empty", name, e.Description, e.Type)
		}
	}
	if e, ok := got["config_level_default_new"]; !ok || len(e.Enum) != 4 || !e.Writable {
		t.Errorf("config_level_default_new = %+v, want writable with its 4 levels", e)
	}
	if e := got["delay_dispatch_min"]; e.Writable || e.Note != antiBanNote {
		t.Errorf("delay_dispatch_min = %+v, want read-only with the anti-ban note", e)
	}
}

func TestGetConfigPrefixFilters(t *testing.T) {
	_, srv, ctx := serverWithGate(t, NewGate())
	got := configEntries(t, ctx, srv, map[string]any{"prefix": "agent_default"})
	if len(got) != 4 {
		t.Errorf("prefix agent_default matched %v, want the 4 agent defaults", got)
	}
}

func TestSetConfigRoundTrip(t *testing.T) {
	for _, c := range []struct{ key, value, want string }{
		{"config_level_default_new", "auto", "auto"},
		{"config_level_default_contact", "confirm", "confirm"},
		{"rules_default_contact", "hablar de usted", "hablar de usted"},
		{"rules_default_new_number", "preguntar el nombre", "preguntar el nombre"},
		{"rules_type_group", "no opinar en grupos", "no opinar en grupos"},
		{"identity", "una tienda de barrio", "una tienda de barrio"},
		{"drift_reports", "true", "true"},
		{"dispatch_debounce_sec", "30", "30"},
		{"max_dispatch_debounce_sec", "90", "90"},
		{"language", "es", "es"},
		{"dashboard_recovery_email", "dueno@example.com", "dueno@example.com"},
	} {
		t.Run(c.key, func(t *testing.T) {
			_, srv, ctx := serverWithGate(t, NewGate())
			text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": c.key, "value": c.value})
			if isErr || !strings.Contains(text, `"applied"`) {
				t.Fatalf("set_config(%s=%s) = %s (error=%v)", c.key, c.value, text, isErr)
			}
			if got := configEntries(t, ctx, srv, map[string]any{"prefix": c.key})[c.key]; got.Value != c.want || got.Source != "kv" {
				t.Errorf("get_config after set: %+v, want value %q from kv", got, c.want)
			}
		})
	}
}

func TestSetConfigWritesTheSameSettingTheDashboardReads(t *testing.T) {
	st, srv, ctx := serverWithGate(t, NewGate())
	configCall(t, ctx, srv, "set_config", map[string]any{"key": "config_level_default_new", "value": "ignored"})
	if got, _ := st.KVGet(store.SettingConfigLevelDefaultNew); got != "ignored" {
		t.Errorf("kv %s = %q, want ignored", store.SettingConfigLevelDefaultNew, got)
	}
	configCall(t, ctx, srv, "set_config", map[string]any{"key": "dispatch_debounce_sec", "value": "45"})
	if got := st.SettingDuration(store.SettingCapipushDispatchDebounce, 0); got.Seconds() != 45 {
		t.Errorf("debounce = %v, want 45s", got)
	}
}

func TestSetConfigReportsOldAndNew(t *testing.T) {
	_, srv, ctx := serverWithGate(t, NewGate())
	configCall(t, ctx, srv, "set_config", map[string]any{"key": "identity", "value": "uno"})
	text, _ := configCall(t, ctx, srv, "set_config", map[string]any{"key": "identity", "value": "dos"})
	var out map[string]string
	if err := json.Unmarshal([]byte(text), &out); err != nil || out["old"] != "uno" || out["new"] != "dos" || out["applied"] != "live" {
		t.Errorf("set_config response = %s (err=%v), want old=uno new=dos applied=live", text, err)
	}
}

func TestSetConfigRejectsBadValues(t *testing.T) {
	for _, c := range []struct{ key, value, wantInError string }{
		{"config_level_default_new", "boss", "must be one of"},
		{"drift_reports", "maybe", "true or false"},
		{"dispatch_debounce_sec", "-1", "whole number"},
		{"dispatch_debounce_sec", "abc", "whole number"},
		{"language", "xx", "unsupported language"},
		{"dashboard_recovery_email", "no-es-un-correo", "not an email"},
	} {
		t.Run(c.key+"="+c.value, func(t *testing.T) {
			_, srv, ctx := serverWithGate(t, NewGate())
			text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": c.key, "value": c.value})
			if !isErr || !strings.Contains(text, c.wantInError) {
				t.Errorf("set_config(%s=%s) = %s (error=%v), want an error containing %q", c.key, c.value, text, isErr, c.wantInError)
			}
			if got := configEntries(t, ctx, srv, map[string]any{"prefix": c.key})[c.key]; got.Source != "default" {
				t.Errorf("a rejected value was stored anyway: %+v", got)
			}
		})
	}
}

func TestSetConfigDebounceCeilingStaysAboveFloor(t *testing.T) {
	_, srv, ctx := serverWithGate(t, NewGate())
	configCall(t, ctx, srv, "set_config", map[string]any{"key": "max_dispatch_debounce_sec", "value": "10"})
	text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "dispatch_debounce_sec", "value": "20"})
	if !isErr || !strings.Contains(text, "ceiling") {
		t.Errorf("floor above the stored ceiling = %s (error=%v), want a ceiling error", text, isErr)
	}
	text, isErr = configCall(t, ctx, srv, "set_config", map[string]any{"key": "max_dispatch_debounce_sec", "value": "5"})
	if isErr {
		t.Fatalf("lowering the ceiling with no floor stored must work, got %s", text)
	}
	text, isErr = configCall(t, ctx, srv, "set_config", map[string]any{"key": "dispatch_debounce_sec", "value": "6"})
	if !isErr {
		t.Errorf("floor 6s above ceiling 5s was accepted: %s", text)
	}
}

func TestSetConfigAgentDefault(t *testing.T) {
	t.Run("unknown agent is refused", func(t *testing.T) {
		_, srv, ctx := serverWithGate(t, NewGate())
		text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "agent_default_group", "value": "nadie"})
		if !isErr || !strings.Contains(text, "unknown agent_id") {
			t.Errorf("unknown agent = %s (error=%v)", text, isErr)
		}
	})
	t.Run("a registered agent is accepted and \"\" clears it", func(t *testing.T) {
		st, srv, ctx := serverWithGate(t, NewGate())
		if err := st.UpsertAgent(store.Agent{AgentID: "agente-a", Role: "secondary"}); err != nil {
			t.Fatal(err)
		}
		if text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "agent_default_group", "value": "agente-a"}); isErr {
			t.Fatalf("registered agent refused: %s", text)
		}
		if got, _ := st.KVGet(store.SettingAgentDefaultGroup); got != "agente-a" {
			t.Errorf("stored %q, want agente-a", got)
		}
		configCall(t, ctx, srv, "set_config", map[string]any{"key": "agent_default_group", "value": ""})
		if got, _ := st.KVGet(store.SettingAgentDefaultGroup); got != "" {
			t.Errorf("\"\" did not clear it: %q", got)
		}
	})
	t.Run("the principal needs no agents row", func(t *testing.T) {
		_, srv, ctx := serverWithGateAndPrincipal(t, NewGate())
		if text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "agent_default_boss", "value": principalTerm}); isErr {
			t.Errorf("the principal as default agent was refused: %s", text)
		}
	})
}

func TestSetConfigAntiBanKeysAreReadOnly(t *testing.T) {
	st, srv, ctx := serverWithGate(t, NewGate())
	for _, key := range []string{"delay_dispatch_min", "delay_chunk_max", "chunk_max_len"} {
		text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": key, "value": "1"})
		if !isErr || !strings.Contains(text, antiBanNote) {
			t.Errorf("set_config(%s) = %s (error=%v), want the %q refusal", key, text, isErr, antiBanNote)
		}
	}
	if got, _ := st.KVGet(store.SettingDispatchDelayMin); got != "" {
		t.Errorf("a refused anti-ban write still landed: %q", got)
	}
}

func TestSetConfigUnknownKeyListsTheValidOnes(t *testing.T) {
	_, srv, ctx := serverWithGate(t, NewGate())
	text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "no_existe", "value": "x"})
	if !isErr || !strings.Contains(text, "valid keys") || !strings.Contains(text, "config_level_default_new") {
		t.Errorf("unknown key = %s (error=%v), want the list of valid keys", text, isErr)
	}
	text, isErr = configCall(t, ctx, srv, "set_config", map[string]any{"key": "identity"})
	if !isErr || !strings.Contains(text, "value is required") {
		t.Errorf("missing value = %s (error=%v)", text, isErr)
	}
}

// A secret must never travel through get_config: nothing in the table may be
// named like one. Cheap guard against adding a key that leaks.
func TestConfigKeysNeverExposeSecrets(t *testing.T) {
	for _, k := range configKeys() {
		for _, bad := range []string{"pin", "password", "secret", "token", "_key"} {
			if strings.Contains(k.name, bad) {
				t.Errorf("config key %q looks like a secret — get_config would print its value", k.name)
			}
		}
	}
}

func TestConfigToolsLiveOnTheManagerProfileOnly(t *testing.T) {
	set := newTestSet(t)
	for _, tool := range []string{"get_config", "set_config"} {
		if _, ok := set.Manager.ListTools()[tool]; !ok {
			t.Errorf("manager lacks %s", tool)
		}
		if _, ok := set.Operator.ListTools()[tool]; ok {
			t.Errorf("operator has %s, it must not", tool)
		}
	}
}
