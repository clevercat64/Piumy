package restapi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"piumy-gateway/internal/store"
)

// T177: every REST entry point that takes an antenna endpoint rewrites THIS
// machine's own LAN IP to loopback, and the value that lands in the store is
// the very value wired to the live injector. 192.0.2.10 stands for "this PC's
// current LAN address" (RFC 5737 documentation range).
const (
	ownLAN      = "http://192.0.2.10:8787"
	ownLoopback = "http://127.0.0.1:8787"
)

func isOwnFake(ip net.IP) bool { return ip.Equal(net.ParseIP("192.0.2.10")) }

func localizingServer(t *testing.T, d Deps) *httptest.Server {
	t.Helper()
	d.IsOwnIP = isOwnFake
	srv := httptest.NewServer(NewMux(d))
	t.Cleanup(srv.Close)
	return srv
}

func okStatus(t *testing.T, resp *http.Response) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		t.Fatalf("status = %d (%v), want 200", resp.StatusCode, body)
	}
}

func TestConnectorLineLocalizesOwnIP(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := localizingServer(t, Deps{Store: st, Connector: conn})

	resp := postJSON(t, srv.URL+"/api/admin/capi-connector-line", map[string]any{"line": "192.0.2.10:8787 chat_id:x pin:y"})
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	okStatus(t, resp)

	if ep, _ := st.KVGet("capi_endpoint"); ep != ownLoopback {
		t.Errorf("stored endpoint = %q, want %q", ep, ownLoopback)
	}
	if conn.lastEndpoint != ownLoopback {
		t.Errorf("live injector got %q, want %q — the stored value and the wired one must match", conn.lastEndpoint, ownLoopback)
	}
	if body["endpoint"] != ownLoopback {
		t.Errorf("response endpoint = %q, want %q", body["endpoint"], ownLoopback)
	}
}

// TestConnectorLineKeepsAnotherMachinesIP is S6's case surviving T177: only
// an address that is OURS is rewritten.
func TestConnectorLineKeepsAnotherMachinesIP(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := localizingServer(t, Deps{Store: st, Connector: conn})

	okStatus(t, postJSON(t, srv.URL+"/api/admin/capi-connector-line", map[string]any{"line": "10.9.8.7:8787 chat_id:x pin:y"}))

	if ep, _ := st.KVGet("capi_endpoint"); ep != "http://10.9.8.7:8787" {
		t.Errorf("stored endpoint = %q, want the other machine's IP untouched", ep)
	}
}

func TestCreateSecondaryAgentLocalizesOwnIP(t *testing.T) {
	st := newTestStore(t)
	var wired string
	srv := localizingServer(t, Deps{Store: st, OnAgentUpsert: func(_, endpoint, _, _ string) { wired = endpoint }})

	okStatus(t, postJSON(t, srv.URL+"/api/admin/agent-create", map[string]any{
		"agent_id": "term-a", "endpoint": ownLAN, "antenna_terminal_id": "ant", "pinpass": "x",
	}))

	if a, _, _ := st.GetAgent("term-a"); a.Endpoint != ownLoopback {
		t.Errorf("stored endpoint = %q, want %q", a.Endpoint, ownLoopback)
	}
	if wired != ownLoopback {
		t.Errorf("OnAgentUpsert endpoint = %q, want %q", wired, ownLoopback)
	}
}

func TestCreatePrincipalLocalizesOwnIP(t *testing.T) {
	st := newTestStore(t)
	conn := &mockConnector{}
	srv := localizingServer(t, Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn})

	okStatus(t, postJSON(t, srv.URL+"/api/admin/agent-create", map[string]any{
		"agent_id": "principal-term", "endpoint": ownLAN, "antenna_terminal_id": "ant", "pinpass": "x",
	}))

	if p, _, _ := st.PrincipalAgent("principal-term"); p.Endpoint != ownLoopback {
		t.Errorf("stored principal endpoint = %q, want %q", p.Endpoint, ownLoopback)
	}
	if conn.lastEndpoint != ownLoopback {
		t.Errorf("live injector got %q, want %q", conn.lastEndpoint, ownLoopback)
	}
}

func TestUpdateSecondaryAgentLocalizesOwnIP(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Endpoint: "http://10.9.8.7:8787", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	var wired string
	srv := localizingServer(t, Deps{Store: st, OnAgentUpsert: func(_, endpoint, _, _ string) { wired = endpoint }})

	okStatus(t, postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{"agent_id": "term-a", "endpoint": ownLAN}))

	if a, _, _ := st.GetAgent("term-a"); a.Endpoint != ownLoopback {
		t.Errorf("stored endpoint = %q, want %q", a.Endpoint, ownLoopback)
	}
	if wired != ownLoopback {
		t.Errorf("OnAgentUpsert endpoint = %q, want %q", wired, ownLoopback)
	}
}

func TestUpdatePrincipalLocalizesOwnIP(t *testing.T) {
	st := newTestStore(t)
	if err := st.SetPrincipalAgent("Boss", "http://10.9.8.7:8787", "principal-term", "pin"); err != nil {
		t.Fatal(err)
	}
	conn := &mockConnector{}
	srv := localizingServer(t, Deps{Store: st, PrincipalTerminalID: "principal-term", Connector: conn})

	okStatus(t, postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{"agent_id": "principal-term", "endpoint": ownLAN}))

	if p, _, _ := st.PrincipalAgent("principal-term"); p.Endpoint != ownLoopback {
		t.Errorf("stored principal endpoint = %q, want %q", p.Endpoint, ownLoopback)
	}
	if conn.lastEndpoint != ownLoopback {
		t.Errorf("live injector got %q, want %q", conn.lastEndpoint, ownLoopback)
	}
}

// TestUpdateWithoutEndpointTouchesNoEndpoint: omitting the field keeps the
// stored one, localizing must not turn "omit" into an empty string.
func TestUpdateWithoutEndpointKeepsTheStoredOne(t *testing.T) {
	st := newTestStore(t)
	if err := st.UpsertAgent(store.Agent{AgentID: "term-a", Endpoint: "http://10.9.8.7:8787", Role: "secondary"}); err != nil {
		t.Fatal(err)
	}
	srv := localizingServer(t, Deps{Store: st})

	okStatus(t, postJSON(t, srv.URL+"/api/admin/agent-update", map[string]any{"agent_id": "term-a", "name": "Nuevo"}))

	if a, _, _ := st.GetAgent("term-a"); a.Endpoint != "http://10.9.8.7:8787" || a.Name != "Nuevo" {
		t.Errorf("agent = %+v, want the endpoint kept and the name changed", a)
	}
}
