package restapi

// T187 (ct-2026-11-11-0953): qr_error is the contract between the adapter
// and the dashboard's QR modal (T188). Before it, a pairing round that
// could not produce a QR failed in silence and the modal said "Generando
// QR…" forever. These tests pin the WIRE shape the dashboard matches on —
// the adapter-side behaviour is covered by internal/whatsmeow's own
// qrreset_test.go.

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/state"
)

// fakeQRStatus is a hand-wired QRStatus: the adapter's own QRState is
// exercised in internal/whatsmeow, so this only has to prove the four
// values reach the JSON.
type fakeQRStatus struct {
	code      string
	expiresAt time.Time
	expired   bool
	errCode   string
}

func (f fakeQRStatus) QRState() (string, time.Time, bool, string) {
	return f.code, f.expiresAt, f.expired, f.errCode
}

type statusQRFields struct {
	QRCode    string `json:"qr_code"`
	QRExpired bool   `json:"qr_expired"`
	QRError   string `json:"qr_error"`
}

func TestStatusEndpointReportsQRError(t *testing.T) {
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	srv := httptest.NewServer(NewMux(Deps{
		State:    sm,
		QRStatus: fakeQRStatus{errCode: "qr_unavailable"},
	}))
	defer srv.Close()

	var out statusQRFields
	resp := getJSON(t, srv.URL+"/api/status", &out)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if out.QRError != "qr_unavailable" {
		t.Errorf("qr_error = %q, want %q — el modal no podría decir por qué no hay QR", out.QRError, "qr_unavailable")
	}
	// A round that failed has no code to show: the modal must not draw an
	// <img> for it (T188's own half of this).
	if out.QRCode != "" {
		t.Errorf("qr_code = %q, want empty on a failed round", out.QRCode)
	}
}

// TestStatusEndpointQRErrorEmptyWhenHealthy is the control: the field has
// to be absent-as-empty on a normal round, or the modal would show an
// error permanently. Both the wired-and-fine case and the not-wired case
// (nil QRStatus, same degrade-one-piece convention as every other
// optional Deps field).
func TestStatusEndpointQRErrorEmptyWhenHealthy(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps func(*state.Manager) Deps
	}{
		{
			name: "ronda sana",
			deps: func(sm *state.Manager) Deps {
				return Deps{State: sm, QRStatus: fakeQRStatus{
					code:      "2@fake-qr-payload",
					expiresAt: time.Now().Add(time.Minute),
				}}
			},
		},
		{
			name: "sin QRStatus cableado",
			deps: func(sm *state.Manager) Deps { return Deps{State: sm} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
			srv := httptest.NewServer(NewMux(tc.deps(sm)))
			defer srv.Close()

			var out statusQRFields
			resp := getJSON(t, srv.URL+"/api/status", &out)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			if out.QRError != "" {
				t.Errorf("qr_error = %q, want empty", out.QRError)
			}
		})
	}
}
