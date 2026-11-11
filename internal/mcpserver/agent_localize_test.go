package mcpserver

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

// T177: every MCP entry point that takes an antenna endpoint rewrites THIS
// machine's own LAN IP to loopback, and the value that lands in the store is
// the very value wired to the live injector. 192.0.2.10 stands for "this PC's
// current LAN address" (RFC 5737 documentation range).
const (
	ownLAN      = "http://192.0.2.10:8787"
	ownLoopback = "http://127.0.0.1:8787"
)

func ownIPFake(ip net.IP) bool { return ip.Equal(net.ParseIP("192.0.2.10")) }

type recordingConnector struct{ endpoint string }

func (r *recordingConnector) SetConfig(endpoint, _, _ string) { r.endpoint = endpoint }

func localizingServer(t *testing.T, st *store.Store, d Deps) (context.Context, *server.MCPServer) {
	t.Helper()
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	d.Store = st
	d.State = state.NewManager(filepath.Join(dir, "status.json"), 8)
	d.Router = router.NewManager(filepath.Join(dir, "router.json"))
	d.AgentIdle = time.Minute
	d.Gate = NewGate()
	d.PrincipalTerminalID = principalTerm
	d.IsOwnIP = ownIPFake
	return ctx, New(ctx, d)
}

func mustSucceed(t *testing.T, out string) {
	t.Helper()
	if strings.Contains(out, `"isError":true`) {
		t.Fatalf("tool failed: %s", out)
	}
}

func TestRegisterAgentLocalizesOwnIP(t *testing.T) {
	st := openAgentDB(t)
	var wired string
	ctx, srv := localizingServer(t, st, Deps{OnAgentUpsert: func(_, endpoint, _, _ string) { wired = endpoint }})

	mustSucceed(t, callTool(t, agentCtx(ctx, secondaryTerm), srv, "register_agent", map[string]any{
		"endpoint": ownLAN, "antenna_terminal_id": "ant", "pinpass": "x",
	}))

	if a, _, _ := st.GetAgent(secondaryTerm); a.Endpoint != ownLoopback {
		t.Errorf("stored endpoint = %q, want %q", a.Endpoint, ownLoopback)
	}
	if wired != ownLoopback {
		t.Errorf("OnAgentUpsert endpoint = %q, want %q", wired, ownLoopback)
	}
}

// TestRegisterAgentKeepsAnotherMachinesIP is S6's case surviving T177: only
// an address that is OURS is rewritten.
func TestRegisterAgentKeepsAnotherMachinesIP(t *testing.T) {
	st := openAgentDB(t)
	ctx, srv := localizingServer(t, st, Deps{})

	mustSucceed(t, callTool(t, agentCtx(ctx, secondaryTerm), srv, "register_agent", map[string]any{
		"endpoint": "http://10.9.8.7:8787", "antenna_terminal_id": "ant", "pinpass": "x",
	}))

	if a, _, _ := st.GetAgent(secondaryTerm); a.Endpoint != "http://10.9.8.7:8787" {
		t.Errorf("stored endpoint = %q, want the other machine's IP untouched", a.Endpoint)
	}
}

func TestSetAgentCAPISecondaryLocalizesOwnIP(t *testing.T) {
	st := openAgentDB(t)
	if err := st.UpsertAgent(store.Agent{AgentID: secondaryTerm, Endpoint: "http://10.9.8.7:8787", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	var wired string
	ctx, srv := localizingServer(t, st, Deps{OnAgentUpsert: func(_, endpoint, _, _ string) { wired = endpoint }})

	mustSucceed(t, callTool(t, agentCtx(ctx, secondaryTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": secondaryTerm, "endpoint": ownLAN,
	}))

	if a, _, _ := st.GetAgent(secondaryTerm); a.Endpoint != ownLoopback {
		t.Errorf("stored endpoint = %q, want %q", a.Endpoint, ownLoopback)
	}
	if wired != ownLoopback {
		t.Errorf("OnAgentUpsert endpoint = %q, want %q", wired, ownLoopback)
	}
}

func TestSetCAPIConnectorLocalizesOwnIP(t *testing.T) {
	st := openAgentDB(t)
	conn := &recordingConnector{}
	ctx, srv := localizingServer(t, st, Deps{Connector: conn})

	mustSucceed(t, callTool(t, agentCtx(ctx, principalTerm), srv, "set_capi_connector", map[string]any{
		"connector_string": "192.0.2.10:8787 chat_id:x pin:y",
	}))

	if p, _, _ := st.PrincipalAgent(principalTerm); p.Endpoint != ownLoopback {
		t.Errorf("stored principal endpoint = %q, want %q", p.Endpoint, ownLoopback)
	}
	if conn.endpoint != ownLoopback {
		t.Errorf("live injector got %q, want %q — the stored value and the wired one must match", conn.endpoint, ownLoopback)
	}
}

// TestSetAgentCAPIPrincipalLocalizesOwnIP: set_agent_capi aimed at the
// principal shares set_capi_connector's write (updatePrincipalConnector).
func TestSetAgentCAPIPrincipalLocalizesOwnIP(t *testing.T) {
	st := openAgentDB(t)
	if err := st.SetPrincipalAgent("Boss", "http://10.9.8.7:8787", principalTerm, "pin"); err != nil {
		t.Fatal(err)
	}
	conn := &recordingConnector{}
	ctx, srv := localizingServer(t, st, Deps{Connector: conn})

	mustSucceed(t, callTool(t, agentCtx(ctx, principalTerm), srv, "set_agent_capi", map[string]any{
		"agent_id": principalTerm, "endpoint": ownLAN,
	}))

	if p, _, _ := st.PrincipalAgent(principalTerm); p.Endpoint != ownLoopback {
		t.Errorf("stored principal endpoint = %q, want %q", p.Endpoint, ownLoopback)
	}
	if conn.endpoint != ownLoopback {
		t.Errorf("live injector got %q, want %q", conn.endpoint, ownLoopback)
	}
}
