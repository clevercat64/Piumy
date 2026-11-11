package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/config"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
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

// configServer builds a manager server over a real router.json in a temp dir;
// mutate may add Deps (the config dir, the principal) before it is built.
func configServer(t *testing.T, mutate func(*Deps)) (srv *server.MCPServer, ctx context.Context, routerPath string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	routerPath = filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Deps{Store: st, State: state.NewManager(filepath.Join(dir, "status.json"), 8), Router: router.NewManager(routerPath), AgentIdle: time.Minute}
	if mutate != nil {
		mutate(&d)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewSet(ctx, d).Manager, ctx, routerPath
}

func TestRouterDefaultMode(t *testing.T) {
	srv, ctx, path := configServer(t, nil)
	if text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "router.default_mode", "value": "auto"}); isErr {
		t.Fatalf("set router.default_mode: %s", text)
	}
	if got := router.Load(path).DefaultMode; got != "auto" {
		t.Errorf("router.json default_mode = %q, want auto (it must persist, not just apply)", got)
	}
	text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "router.default_mode", "value": "maybe"})
	if !isErr || !strings.Contains(text, "auto or dedicated") {
		t.Errorf("bad mode = %s (error=%v)", text, isErr)
	}
}

func TestRouterRoutes(t *testing.T) {
	srv, ctx, path := configServer(t, nil)
	jid := "555000001@s.whatsapp.net"
	set := func(match, body string) (string, bool) {
		return configCall(t, ctx, srv, "set_config", map[string]any{"key": routeKeyPrefix + match, "value": body})
	}

	if text, isErr := set("*", `{"mode":"auto"}`); isErr {
		t.Fatalf("catch-all route: %s", text)
	}
	if text, isErr := set(jid, `{"mode":"dedicated","vip":true,"terminal_id":"term-x"}`); isErr {
		t.Fatalf("route for a JID: %s", text)
	}
	rt := router.NewManager(path) // a fresh read: what got persisted, not what is in memory
	if d := rt.Resolve(jid); d.Mode != "dedicated" || d.TerminalID != "term-x" || !rt.IsVIP(jid) {
		t.Errorf("Resolve(%s) = %+v vip=%v, want the exact route to win over \"*\"", jid, d, rt.IsVIP(jid))
	}

	routes := configEntries(t, ctx, srv, map[string]any{"prefix": routeKeyPrefix})
	if len(routes) != 2 || routes[routeKeyPrefix+jid].Value == "" {
		t.Errorf("get_config lists %v, want the 2 routes just set", routes)
	}

	// Setting an existing match replaces it; it must not append a duplicate.
	set(jid, `{"mode":"auto"}`)
	if n := len(router.NewManager(path).Snapshot().Routes); n != 2 {
		t.Errorf("%d routes after replacing one, want 2", n)
	}

	// "" removes it.
	set(jid, "")
	if n := len(router.NewManager(path).Snapshot().Routes); n != 1 {
		t.Errorf("%d routes after removing one, want 1", n)
	}
}

func TestRouterRouteRejectsBadBodies(t *testing.T) {
	srv, ctx, _ := configServer(t, nil)
	for _, c := range []struct{ body, want string }{
		{`{"mode":"sometimes"}`, "auto or dedicated"},
		{`{"modo":"auto"}`, "not a route"}, // a typo'd field must not be silently dropped
		{`no es json`, "not a route"},
	} {
		text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": routeKeyPrefix + "*", "value": c.body})
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("body %s = %s (error=%v), want an error containing %q", c.body, text, isErr, c.want)
		}
	}
	if text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": routeKeyPrefix, "value": `{"mode":"auto"}`}); !isErr || !strings.Contains(text, "needs a match") {
		t.Errorf("a route with no match = %s (error=%v)", text, isErr)
	}
}

func TestDefaultTerminalIDIsWrittenToTheConfigFileAndAppliesOnRestart(t *testing.T) {
	t.Setenv(defaultTerminalEnv, "")
	dir := t.TempDir()
	srv, ctx, _ := configServer(t, func(d *Deps) { d.ConfigFileDir = dir; d.PrincipalTerminalID = "term-viejo" })

	text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "default_terminal_id", "value": "term-nuevo"})
	if isErr || !strings.Contains(text, `"applied": "restart"`) {
		t.Fatalf("set default_terminal_id = %s (error=%v), want applied=restart", text, isErr)
	}
	if got := config.FileValue(dir, defaultTerminalEnv); got != "term-nuevo" {
		t.Errorf("piumy-config.json holds %q, want term-nuevo", got)
	}
	got := configEntries(t, ctx, srv, map[string]any{"prefix": "default_terminal_id"})["default_terminal_id"]
	if got.Value != "term-nuevo" || !strings.Contains(got.Source, "pending restart") {
		t.Errorf("get_config = %+v, want the new value flagged as pending a restart", got)
	}

	if text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "default_terminal_id", "value": ""}); isErr {
		t.Fatalf("clearing it: %s", text)
	}
	if got := config.FileValue(dir, defaultTerminalEnv); got != "" {
		t.Errorf("\"\" left %q in the file", got)
	}
}

func TestDefaultTerminalIDWarnsWhenTheEnvironmentWins(t *testing.T) {
	dir := t.TempDir()
	srv, ctx, _ := configServer(t, func(d *Deps) { d.ConfigFileDir = dir })

	t.Setenv(defaultTerminalEnv, "desde-el-entorno")
	text, _ := configCall(t, ctx, srv, "set_config", map[string]any{"key": "default_terminal_id", "value": "otro"})
	if !strings.Contains(text, "environment") {
		t.Errorf("a real environment variable overrides the file, the response must say so: %s", text)
	}

	// The value startup copied FROM the file into the environment is not "the
	// environment winning" — warning about it would be noise on every change.
	if err := config.SetFileValue(dir, defaultTerminalEnv, "del-archivo"); err != nil {
		t.Fatal(err)
	}
	t.Setenv(defaultTerminalEnv, "del-archivo")
	text, _ = configCall(t, ctx, srv, "set_config", map[string]any{"key": "default_terminal_id", "value": "nuevo"})
	if strings.Contains(text, "environment") {
		t.Errorf("warned although the environment value came from the file itself: %s", text)
	}
}

func TestDefaultTerminalIDWithoutAConfigDirRefusesClearly(t *testing.T) {
	srv, ctx, _ := configServer(t, nil)
	text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "default_terminal_id", "value": "x"})
	if !isErr || !strings.Contains(text, "not reachable") {
		t.Errorf("no config dir = %s (error=%v), want a clear refusal", text, isErr)
	}
}

// The anti-ban limits an agent asks about are the real ones the governor runs
// with, shown read-only and labelled as coming from the environment.
func TestRateLimitsShowTheRealNumbersAndStayReadOnly(t *testing.T) {
	srv, ctx, _ := configServer(t, func(d *Deps) { d.RateLimitPerMin, d.RateLimitPerDay = 7, 70 })
	got := configEntries(t, ctx, srv, map[string]any{"prefix": "rate_limit"})
	if e := got["rate_limit_per_min"]; e.Value != "7" || e.Source != "env" || e.Writable {
		t.Errorf("rate_limit_per_min = %+v, want 7 from env, read-only", e)
	}
	if e := got["rate_limit_per_day"]; e.Value != "70" || e.Source != "env" || e.Writable {
		t.Errorf("rate_limit_per_day = %+v, want 70 from env, read-only", e)
	}
	text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "rate_limit_per_min", "value": "100000"})
	if !isErr || !strings.Contains(text, antiBanNote) {
		t.Errorf("raising the send limit = %s (error=%v), want the anti-ban refusal", text, isErr)
	}
}

func TestAutoUpdateShowsItsDefaultUntilTouched(t *testing.T) {
	var st *store.Store
	srv, ctx, _ := configServer(t, func(d *Deps) { st, d.AutoUpdateDefault = d.Store, true })
	if e := configEntries(t, ctx, srv, map[string]any{"prefix": "auto_update"})["auto_update"]; e.Value != "true" || e.Source != "default" {
		t.Errorf("untouched auto_update = %+v, want true from the default", e)
	}
	if text, isErr := configCall(t, ctx, srv, "set_config", map[string]any{"key": "auto_update", "value": "false"}); isErr {
		t.Fatalf("set auto_update: %s", text)
	}
	if e := configEntries(t, ctx, srv, map[string]any{"prefix": "auto_update"})["auto_update"]; e.Value != "false" || e.Source != "kv" {
		t.Errorf("auto_update after set = %+v, want false from kv", e)
	}
	// The updater reads the same setting on every look.
	if st.SettingBool(store.SettingAutoUpdate, true) {
		t.Error("the stored choice must win over the default the updater falls back to")
	}
}
