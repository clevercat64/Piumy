package mcpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func newTestSet(t *testing.T) Set {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	routerPath := filepath.Join(dir, "router.json")
	if err := os.WriteFile(routerPath, []byte(`{"default_mode":"dedicated"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewSet(ctx, Deps{
		Store: st, State: state.NewManager(filepath.Join(dir, "status.json"), 8),
		Router: router.NewManager(routerPath), AgentIdle: time.Minute,
	})
}

// A tool nobody put in a profile is unreachable from /mcp/operator and
// /mcp/manager and nobody would notice — so every registered tool must be
// claimed, and every claimed name must exist (a typo here claims nothing).
func TestEveryToolBelongsToAProfile(t *testing.T) {
	all := newTestSet(t).All.ListTools()
	if _, ok := all["send_message"]; !ok { // known case: the instrument itself works
		t.Fatalf("send_message missing from All — ListTools is not seeing the registry")
	}
	for name := range all {
		if !operatorTools[name] && !managerTools[name] {
			t.Errorf("tool %q is in neither operatorTools nor managerTools (profiles.go)", name)
		}
	}
	for _, profile := range []map[string]bool{operatorTools, managerTools} {
		for name := range profile {
			if _, ok := all[name]; !ok {
				t.Errorf("profile names %q, which is not a registered tool", name)
			}
		}
	}
}

func TestProfilesSeparateTheSurface(t *testing.T) {
	set := newTestSet(t)
	op, mg := set.Operator.ListTools(), set.Manager.ListTools()
	for _, c := range []struct {
		tool        string
		inOp, inMgr bool
	}{
		{"send_message", true, false},
		{"set_is_boss", false, true},
		{"set_kill_switch", false, true},
		{"get_status", true, true},
		{"set_chat_rules", true, true},
	} {
		if _, ok := op[c.tool]; ok != c.inOp {
			t.Errorf("operator has %s = %v, want %v", c.tool, ok, c.inOp)
		}
		if _, ok := mg[c.tool]; ok != c.inMgr {
			t.Errorf("manager has %s = %v, want %v", c.tool, ok, c.inMgr)
		}
	}
}

// rpc posts one JSON-RPC call and returns the decoded "result" and the
// session id the server handed back.
func rpc(t *testing.T, url, key, session, body string) (map[string]any, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+key)
	if session != "" {
		req.Header.Set("Mcp-Session-Id", session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var env struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("%s: not JSON-RPC (%d): %s", url, resp.StatusCode, raw)
	}
	return env.Result, resp.Header.Get("Mcp-Session-Id")
}

func toolNames(t *testing.T, url, key string) map[string]bool {
	t.Helper()
	_, session := rpc(t, url, key, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`)
	result, _ := rpc(t, url, key, session, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	names := map[string]bool{}
	for _, tool := range result["tools"].([]any) {
		names[tool.(map[string]any)["name"].(string)] = true
	}
	return names
}

// One key, three doors: each path serves its own tool list over real HTTP,
// and a wrong key opens none of them.
func TestHandlerServesThreeEndpointsWithOneKey(t *testing.T) {
	set := newTestSet(t)
	ts := httptest.NewServer(Handler(set, "k"))
	defer ts.Close()

	for _, c := range []struct {
		path string
		want int
		has  string
		not  string
	}{
		{PathAll, len(set.All.ListTools()), "send_message", ""},
		{PathOperator, len(operatorTools), "send_message", "set_is_boss"},
		{PathManager, len(managerTools), "set_is_boss", "send_message"},
	} {
		names := toolNames(t, ts.URL+c.path, "k")
		if len(names) != c.want {
			t.Errorf("%s serves %d tools, want %d", c.path, len(names), c.want)
		}
		if !names[c.has] {
			t.Errorf("%s lacks %s", c.path, c.has)
		}
		if c.not != "" && names[c.not] {
			t.Errorf("%s serves %s, must not", c.path, c.not)
		}

		resp, err := http.Post(ts.URL+c.path, "application/json", strings.NewReader(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a key = %d, want 401", c.path, resp.StatusCode)
		}
	}
}
