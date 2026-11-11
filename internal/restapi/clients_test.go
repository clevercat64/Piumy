package restapi

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/store"
)

// clientsFileFor puts a synthetic clients.json where GET /api/clients will
// look for it (CLEVERCODER_APPDATA_DIR takes the place of %LOCALAPPDATA%).
func clientsFileFor(t *testing.T, content string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("CLEVERCODER_APPDATA_DIR", dir)
	path := filepath.Join(dir, "CleverCoderData", "clientes", "clients.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func freshClientsFile(clients ...string) string {
	return fmt.Sprintf(`{"schema_version":1,"generated_at":%q,"count":%d,"clients":[%s]}`,
		time.Now().UTC().Format(time.RFC3339), len(clients), strings.Join(clients, ","))
}

func clientEntry(key, name string) string {
	return fmt.Sprintf(`{"key":%q,"name":%q,"state":"abierto","week":{"lines":{"used":10,"limit":500}}}`, key, name)
}

func newClientsServer(t *testing.T) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	t.Cleanup(srv.Close)
	return st, srv
}

type clientsResponse struct {
	Status      string           `json:"status"`
	GeneratedAt string           `json:"generated_at"`
	Clients     []map[string]any `json:"clients"`
}

// TestClientsEndpointCrossesEachClientWithItsGroup: the file's fields pass
// through; Piumy adds chat_jid and agent_id, and only for a client that is a
// GROUP it knows — whichever way the key is written.
func TestClientsEndpointCrossesEachClientWithItsGroup(t *testing.T) {
	st, srv := newClientsServer(t)
	assigned := "555000000000001@g.us"
	bare := "555000000000002@g.us"
	person := "555000000000004@s.whatsapp.net"
	for _, jid := range []string{assigned, bare, person} {
		if err := st.TouchChat(jid, "Chat "+jid, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetStatus(assigned, store.AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	// A 1:1 chat can be assigned too — a client is still never one.
	if err := st.SetStatus(person, store.AgentExclusiveStatus("term-a")); err != nil {
		t.Fatal(err)
	}
	clientsFileFor(t, freshClientsFile(
		clientEntry(assigned, "Grupo Uno"),           // key written as the full JID
		clientEntry("555000000000002", "Grupo Dos"),  // key written as the JID's user part (what capipush's "de:" carries)
		clientEntry("555000000000003", "Grupo Tres"), // a group Piumy never saw
		clientEntry(person, "Persona"),               // not a group
	))

	var out clientsResponse
	resp := getJSON(t, srv.URL+"/api/clients", &out)

	if resp.StatusCode != http.StatusOK || out.Status != "ok" || len(out.Clients) != 4 {
		t.Fatalf("GET /api/clients = %d %+v, want 200 ok with 4 clients", resp.StatusCode, out)
	}
	want := []struct{ name, chatJID, agentID string }{
		{"Grupo Uno", assigned, "term-a"},
		{"Grupo Dos", bare, ""},
		{"Grupo Tres", "", ""},
		{"Persona", "", ""},
	}
	for i, w := range want {
		c := out.Clients[i]
		if c["name"] != w.name || c["chat_jid"] != w.chatJID || c["agent_id"] != w.agentID {
			t.Errorf("client %d = name %v chat_jid %v agent_id %v, want %q %q %q", i, c["name"], c["chat_jid"], c["agent_id"], w.name, w.chatJID, w.agentID)
		}
	}
	if _, ok := out.Clients[0]["week"].(map[string]any); !ok {
		t.Errorf("week = %v, want the file's own field passed through", out.Clients[0]["week"])
	}
}

// TestClientsEndpointWithoutDataStillAnswersAList: the dashboard iterates
// "clients" in every state, so a state with no data returns [], never null.
func TestClientsEndpointWithoutDataStillAnswersAList(t *testing.T) {
	cases := map[string]struct {
		content string // "" = no file
		want    string
	}{
		"no file":         {"", "missing"},
		"fresh and empty": {freshClientsFile(), "empty"},
		"a newer schema":  {`{"schema_version":2,"generated_at":"x","clients":{"otra":"forma"}}`, "unknown_version"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, srv := newClientsServer(t)
			if tc.content == "" {
				t.Setenv("CLEVERCODER_APPDATA_DIR", t.TempDir())
			} else {
				clientsFileFor(t, tc.content)
			}

			resp, err := http.Get(srv.URL + "/api/clients")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)

			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, text)
			}
			if !strings.Contains(text, `"status":"`+tc.want+`"`) || !strings.Contains(text, `"clients":[]`) {
				t.Errorf("body = %s, want status %q and \"clients\":[]", text, tc.want)
			}
		})
	}
}

func TestClientsEndpointCorruptFileIs500(t *testing.T) {
	_, srv := newClientsServer(t)
	clientsFileFor(t, "{esto no es json")

	var out map[string]string
	resp := getJSON(t, srv.URL+"/api/clients", &out)

	if resp.StatusCode != http.StatusInternalServerError || out["error"] == "" {
		t.Errorf("corrupt clients.json = %d %v, want 500 with an error (no sixth status)", resp.StatusCode, out)
	}
}

// The route carries the same gate as the rest of the dashboard REST: with an
// API key set, no key is a 401 — the data is names and spend of the boss's
// clients.
func TestClientsEndpointIsBehindAuth(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "clave-de-prueba"}))
	defer srv.Close()
	clientsFileFor(t, freshClientsFile(clientEntry("555000000000001", "Grupo Uno")))

	var denied map[string]string
	if resp := getJSON(t, srv.URL+"/api/clients", &denied); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("no key: status = %d, want 401", resp.StatusCode)
	}
	var out clientsResponse
	if resp := getJSON(t, srv.URL+"/api/clients?key=clave-de-prueba", &out); resp.StatusCode != http.StatusOK || len(out.Clients) != 1 {
		t.Errorf("with the key: %d %+v, want 200 with the client", resp.StatusCode, out)
	}
}

func TestClientsEndpointWithoutStoreIs503(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	var out map[string]string
	if resp := getJSON(t, srv.URL+"/api/clients", &out); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}
