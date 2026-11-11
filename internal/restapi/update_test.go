package restapi

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"piumy-gateway/internal/autoupdate"
	"piumy-gateway/internal/store"
)

// newUpdateServer builds the REST mux over a real Store and a real Updater that
// asks the SAME setting main.go wires it to; the updater is never Run, so
// nothing touches the network.
func newUpdateServer(t *testing.T, apiKey string, enabledByDefault bool) (*store.Store, *httptest.Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	up := autoupdate.New(autoupdate.Config{
		Enabled:        func() bool { return st.SettingBool(store.SettingAutoUpdate, enabledByDefault) },
		CurrentVersion: "0.12.5",
		DataDir:        t.TempDir(),
		KV:             st,
	})
	srv := httptest.NewServer(NewMux(Deps{Store: st, AutoUpdate: up, APIKey: apiKey}))
	t.Cleanup(srv.Close)
	return st, srv
}

// TestAutoUpdateStartsOnAndTheOwnersChoiceWins (T176): the boss decided it is on
// by default; the owner's switch, stored, wins and applies at once.
func TestAutoUpdateStartsOnAndTheOwnersChoiceWins(t *testing.T) {
	st, srv := newUpdateServer(t, "", true)

	var out autoupdate.Status
	if resp := getJSON(t, srv.URL+"/api/update", &out); resp.StatusCode != http.StatusOK || !out.Enabled || out.Current != "0.12.5" {
		t.Fatalf("GET /api/update = %d %+v, want it on (the default) and the running version", resp.StatusCode, out)
	}

	resp := postJSON(t, srv.URL+"/api/admin/auto-update", map[string]any{"enabled": false})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d, want 200", resp.StatusCode)
	}
	if st.SettingBool(store.SettingAutoUpdate, true) {
		t.Error("the switch did not reach store.SettingAutoUpdate, which is what the updater asks")
	}
	if resp := getJSON(t, srv.URL+"/api/update", &out); resp.StatusCode != http.StatusOK || out.Enabled {
		t.Errorf("after switching off: %d %+v, want enabled=false at once", resp.StatusCode, out)
	}

	postJSON(t, srv.URL+"/api/admin/auto-update", map[string]any{"enabled": true})
	if resp := getJSON(t, srv.URL+"/api/update", &out); resp.StatusCode != http.StatusOK || !out.Enabled {
		t.Errorf("after switching back on: %d %+v", resp.StatusCode, out)
	}
}

func TestAutoUpdateSwitchRefusesABodyWithoutTheField(t *testing.T) {
	st, srv := newUpdateServer(t, "", true)

	resp := postJSON(t, srv.URL+"/api/admin/auto-update", map[string]any{})

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400: a body that forgets the field must not read as \"off\"", resp.StatusCode)
	}
	if !st.SettingBool(store.SettingAutoUpdate, true) {
		t.Error("an empty body switched self-update off")
	}
}

// Both routes carry the dashboard's gate: with an API key set, no key is a 401.
func TestAutoUpdateRoutesAreBehindAuth(t *testing.T) {
	_, srv := newUpdateServer(t, "clave-de-prueba", true)

	var denied map[string]string
	if resp := getJSON(t, srv.URL+"/api/update", &denied); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET without a key = %d, want 401", resp.StatusCode)
	}
	if resp := postJSON(t, srv.URL+"/api/admin/auto-update", map[string]any{"enabled": false}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("POST without a key = %d, want 401", resp.StatusCode)
	}
	var out autoupdate.Status
	if resp := getJSON(t, srv.URL+"/api/update?key=clave-de-prueba", &out); resp.StatusCode != http.StatusOK {
		t.Errorf("GET with the key = %d, want 200", resp.StatusCode)
	}
}

func TestAutoUpdateRoutesWithoutAnUpdaterAre503(t *testing.T) {
	srv := httptest.NewServer(NewMux(Deps{}))
	defer srv.Close()

	var out map[string]string
	if resp := getJSON(t, srv.URL+"/api/update", &out); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("GET = %d, want 503", resp.StatusCode)
	}
	if resp := postJSON(t, srv.URL+"/api/admin/auto-update", map[string]any{"enabled": true}); resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("POST = %d, want 503", resp.StatusCode)
	}
}
