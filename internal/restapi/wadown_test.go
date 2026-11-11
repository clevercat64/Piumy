package restapi

// T190 (ct-2026-11-11-101756): wa_down_reason is the contract between
// whatsmeow's handleDisconnect and the dashboard's status line. Before it,
// every way of being down looked identical on screen ("desconectado"), so
// an unlink could sit there unnoticed. These tests pin the WIRE shape
// app.js matches on; the deciding behaviour lives in internal/whatsmeow
// (who writes the code) and internal/wanotice (who warns about it).

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"piumy-gateway/internal/state"
)

type statusWADownFields struct {
	Connected    bool   `json:"connected"`
	WADownReason string `json:"wa_down_reason"`
}

func TestStatusEndpointReportsWhyWhatsAppIsDown(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.UpdateMood(func(s *state.Status) {
		s.WAConnected = false
		s.Mood = "error"
		s.WADownReason = state.WADownLoggedOut
	}); err != nil {
		t.Fatalf("preparando el estado: %v", err)
	}

	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	var out statusWADownFields
	resp := getJSON(t, srv.URL+"/api/status", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if out.WADownReason != string(state.WADownLoggedOut) {
		t.Errorf("wa_down_reason = %q, want %q — el tablero no podría decir el motivo real", out.WADownReason, state.WADownLoggedOut)
	}
}

// Connected means there is no motive to report: a stale reason would make
// the dashboard keep naming a cause for a connection that is back.
func TestStatusEndpointReportsNoReasonWhileConnected(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	if err := sm.UpdateMood(func(s *state.Status) {
		s.WAConnected = true
		s.Mood = "idle"
	}); err != nil {
		t.Fatalf("preparando el estado: %v", err)
	}

	srv := httptest.NewServer(NewMux(Deps{State: sm}))
	defer srv.Close()

	var out statusWADownFields
	resp := getJSON(t, srv.URL+"/api/status", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if !out.Connected {
		t.Fatal("preparación: el estado debía quedar conectado")
	}
	if out.WADownReason != "" {
		t.Errorf("conectado no debe traer motivo; trajo %q", out.WADownReason)
	}
}
