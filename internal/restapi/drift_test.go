package restapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"piumy-gateway/internal/store"
)

// TestDriftReportsStartOffAndTheOwnersChoiceWins (T174a): reports are opt-in,
// so with the config default off the switch reads off; the stored choice —
// either way — beats the default, and it lands in the SAME setting the
// Reporter asks on every report (that is what makes it apply with no restart).
func TestDriftReportsStartOffAndTheOwnersChoiceWins(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, DriftReportsDefault: false}))
	defer srv.Close()

	var out struct {
		Enabled bool `json:"enabled"`
	}
	get := func() bool {
		t.Helper()
		resp := getJSON(t, srv.URL+"/api/admin/drift-reports", &out)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET status = %d, want 200", resp.StatusCode)
		}
		return out.Enabled
	}

	if get() {
		t.Fatal("reports read ON before the owner ever touched the switch: they are opt-in")
	}

	resp := postJSON(t, srv.URL+"/api/admin/drift-reports", map[string]any{"enabled": true})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST enable status = %d, want 200", resp.StatusCode)
	}
	if !get() {
		t.Error("GET after switching on still says off")
	}
	// The Reporter reads this exact setting; checked through the store, not the endpoint.
	if !st.SettingBool(store.SettingDriftReports, false) {
		t.Error("the switch did not reach store.SettingDriftReports, which is what the Reporter asks")
	}

	resp = postJSON(t, srv.URL+"/api/admin/drift-reports", map[string]any{"enabled": false})
	resp.Body.Close()
	if get() {
		t.Error("GET after switching off still says on")
	}
}

// A machine nobody will click on can be switched on from the environment: the
// config default then shows ON until the owner's stored choice says otherwise.
func TestDriftReportsConfigDefaultOnYieldsToAStoredOff(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, DriftReportsDefault: true}))
	defer srv.Close()

	var out struct {
		Enabled bool `json:"enabled"`
	}
	resp := getJSON(t, srv.URL+"/api/admin/drift-reports", &out)
	resp.Body.Close()
	if !out.Enabled {
		t.Fatal("config default ON did not show as on")
	}
	resp = postJSON(t, srv.URL+"/api/admin/drift-reports", map[string]any{"enabled": false})
	resp.Body.Close()
	resp = getJSON(t, srv.URL+"/api/admin/drift-reports", &out)
	resp.Body.Close()
	if out.Enabled {
		t.Error("the owner's explicit OFF lost to the config default")
	}
}

// A body that forgets "enabled" must be refused, not read as "off" (or "on").
func TestDriftReportsRefusesABodyWithoutTheField(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	resp := postJSON(t, srv.URL+"/api/admin/drift-reports", map[string]any{"enable": true}) // typo
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a body without \"enabled\"", resp.StatusCode)
	}
	if v, _ := st.KVGet(store.SettingDriftReports); v != "" {
		t.Errorf("a refused request still stored %q", v)
	}
}

func TestDriftReportsNeedTheAPIKeyWhenSet(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, APIKey: "k-prueba"}))
	defer srv.Close()

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		req, _ := http.NewRequest(method, srv.URL+"/api/admin/drift-reports", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s without a key: status = %d, want 401", method, resp.StatusCode)
		}
	}
}

// The switch tells the tablero where a report would go, exactly as configured:
// part of being honest about what the opt-in means.
func TestDriftReportsShowsTheConfiguredDestination(t *testing.T) {
	st := newTestStore(t)
	srv := httptest.NewServer(NewMux(Deps{Store: st, DriftReportURL: "https://reportes.ejemplo.test/lic/report"}))
	defer srv.Close()

	var out driftReportsView
	resp := getJSON(t, srv.URL+"/api/admin/drift-reports", &out)
	resp.Body.Close()
	if out.URL != "https://reportes.ejemplo.test/lic/report" {
		t.Errorf("url = %q, want the configured destination as is", out.URL)
	}
}
