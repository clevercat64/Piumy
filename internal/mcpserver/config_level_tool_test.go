// set_config_level tests — the translation-layer MCP tool over store.
// SetConfigLevel. T178 (ct-2026-11-10-2330-t178) took every refusal out of it:
// any level, including auto and boss, is settable under any dispatch.
package mcpserver

import (
	"strings"
	"testing"
)

// TestSetConfigLevelAnyLevelUnderAnyDispatch: the levels that used to be
// refused (auto needed a boss dispatch, boss was blocked outright) now work
// under a danger dispatch — the boss's call, see admin_tools.go's header.
func TestSetConfigLevelAnyLevelUnderAnyDispatch(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000021@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	if err := gate.RegisterDispatch("nonce-cl-danger", chat, LevelDanger, "term-cl-danger", 0, ""); err != nil {
		t.Fatal(err)
	}
	termCtx := withTerminalID(ctx, "term-cl-danger")
	callTool(t, termCtx, srv, "get_instructions", map[string]any{"nonce": "nonce-cl-danger"})

	for _, level := range []string{"auto", "confirm", "unattended", "ignored", "boss"} {
		out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": level})
		if !strings.Contains(out, "config_level set to "+level) {
			t.Errorf("set_config_level(%s) under danger = %s, want success", level, out)
		}
	}
}

// TestSetConfigLevelBossSetsIsBoss: level=boss reaches store.SetConfigLevel,
// which marks the chat as the owner.
func TestSetConfigLevelBossSetsIsBoss(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000024@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, ctx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": "boss"})
	if !strings.Contains(out, "config_level set to boss") {
		t.Fatalf("set_config_level(boss) = %s, want success", out)
	}
	c, _, err := st.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if !c.IsBoss {
		t.Errorf("chat after set_config_level(boss) = %+v, want is_boss=true", c)
	}
}

// TestSetConfigLevelWorksForBoss covers the translation itself: a level
// persists via store.SetConfigLevel into the four underlying fields.
func TestSetConfigLevelWorksForBoss(t *testing.T) {
	gate := NewGate()
	st, srv, ctx := serverWithGate(t, gate)
	chat := "55500000074@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	termCtx := bossDispatchContext(t, gate, srv, ctx, chat)

	out := callTool(t, termCtx, srv, "set_config_level", map[string]any{"chat_id": chat, "level": "confirm"})
	if !strings.Contains(out, "config_level set to confirm") {
		t.Errorf("set_config_level(confirm) under boss = %s, want success", out)
	}
	c, _, err := st.GetChat(chat)
	if err != nil {
		t.Fatal(err)
	}
	if c.ConfirmationMode != "always" || !c.Active || c.IsBoss {
		t.Errorf("chat after set_config_level(confirm) = %+v, want active=true confirmation_mode=always is_boss=false", c)
	}
}

// TestSetConfigLevelRegistered is the structural check: the tool exists and
// has no gate of any kind.
func TestSetConfigLevelRegistered(t *testing.T) {
	found := false
	for _, tool := range listTools(t) {
		if tool.Name == "set_config_level" {
			found = true
		}
	}
	if !found {
		t.Error("set_config_level tool not registered")
	}
	if bossOnlyTools["set_config_level"] {
		t.Error("set_config_level must not be in bossOnlyTools (T178)")
	}
}
