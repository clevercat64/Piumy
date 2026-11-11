package whatsmeow

// T187 (ct-2026-11-11-0953): after WhatsApp logs the account out from
// another device, "Conectar QR" produced no QR until Piumy was restarted.
// whatsmeow DELETES the device on that event, and a deleted device can
// never connect again — see resetDeletedDevice's doc for the mechanism.
//
// What these tests can and cannot prove: a REAL QR code comes from
// WhatsApp's servers, so no unit test can assert "a code appeared"
// without a network dependency this suite deliberately does not have.
// What they do assert is everything up to that boundary — that the
// adapter ends up with a client able to pair, and that a round which
// cannot produce a QR says so instead of spinning forever. The fake
// connect used below is faithful, not convenient: it returns exactly what
// whatsmeow's own Connect returns for a deleted device
// (store.ErrDeviceDeleted, raised in client.go's unlockedConnect BEFORE
// any socket is opened), measured against the library before writing it.

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	wmstore "go.mau.fi/whatsmeow/store"
)

// newPairedTestAdapter builds a real Adapter over a real in-memory-backed
// session DB holding a paired-looking device — the state the owner's
// installation was in before WhatsApp logged it out. 555 JID: synthetic by
// project rule.
func newPairedTestAdapter(t *testing.T) *Adapter {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "wm.db")
	seedFakeDeviceWithJID(t, dbPath, "555000001.0:15@s.whatsapp.net")
	a, err := New(context.Background(), Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)
	if a.cli().Store.ID == nil {
		t.Fatal("control: el device sembrado debería verse pareado — la siembra del test está mal")
	}
	return a
}

// logoutExternally does to the store exactly what the library does on an
// external logout (connectionevents.go calls Store.Delete on a 401/
// device_removed) — not an approximation of it.
func logoutExternally(t *testing.T, a *Adapter) {
	t.Helper()
	if err := a.cli().Store.Delete(context.Background()); err != nil {
		t.Fatalf("simulating the external logout: %v", err)
	}
	if !a.cli().Store.Deleted {
		t.Fatal("control: tras Delete el device debería quedar marcado borrado")
	}
}

func TestResetDeletedDeviceSwapsInAHealthyClient(t *testing.T) {
	a := newPairedTestAdapter(t)
	old := a.cli()
	logoutExternally(t, a)

	if !a.resetDeletedDevice() {
		t.Fatal("resetDeletedDevice debería haber reparado un device borrado")
	}
	fresh := a.cli()
	if fresh == old {
		t.Fatal("el client debería haber sido reemplazado, no reparado en el lugar")
	}
	if fresh.Store.Deleted {
		t.Fatal("el client nuevo no debería ver su store borrado")
	}
	if fresh.Store.ID != nil {
		t.Fatal("el client nuevo debería estar sin parear (ID nil), listo para un QR")
	}
	// El viejo sigue borrado: la reparación no lo resucita, lo jubila. Es
	// lo que obliga a cambiar el puntero en vez de arreglar en el lugar.
	if !old.Store.Deleted {
		t.Fatal("control: el client viejo debería seguir con su device borrado")
	}
}

// TestResetDeletedDeviceIsNoOpOnHealthyDevice is the control case: the
// repair must not fire on a session that is merely unpaired or working, or
// it would throw away a good device on every pairing round.
func TestResetDeletedDeviceIsNoOpOnHealthyDevice(t *testing.T) {
	a := newPairedTestAdapter(t)
	before := a.cli()

	if a.resetDeletedDevice() {
		t.Fatal("no debería reparar un device sano")
	}
	if a.cli() != before {
		t.Fatal("no debería cambiar el client de un device sano")
	}
}

// TestReconnectAfterExternalLogoutCanPairAgain is the regression for the
// boss's report: the whole path through the dashboard's "Conectar QR"
// button (POST /api/admin/reconnect -> Reconnect -> pairLoop) must end up
// with a client that CAN pair, and with no error reported.
//
// Sabotage-checked: with the resetDeletedDevice call removed from pairLoop
// AND its ErrDeviceDeleted safety net removed, this test fails.
func TestReconnectAfterExternalLogoutCanPairAgain(t *testing.T) {
	a := newPairedTestAdapter(t)
	old := a.cli()
	logoutExternally(t, a)

	connected := make(chan struct{}, 1)
	a.connectFn = func() error {
		// Faithful to the library: a deleted device fails here, before
		// the network. Anything else connects.
		if a.cli().Store.Deleted {
			return wmstore.ErrDeviceDeleted
		}
		select {
		case connected <- struct{}{}:
		default:
		}
		return nil
	}

	if err := a.Reconnect(context.Background()); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	select {
	case <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("pairLoop nunca llegó a conectar — el QR no se habría generado nunca (el bug del boss)")
	}

	if a.cli() == old {
		t.Fatal("el adaptador se quedó con el client del device borrado")
	}
	if a.cli().Store.Deleted {
		t.Fatal("el adaptador se quedó con un device borrado: no puede parear")
	}
	if _, _, _, errCode := a.QRState(); errCode != "" {
		t.Fatalf("una ronda que sí puede parear no debería reportar error, reportó %q", errCode)
	}
}

// TestPairLoopReportsErrorWhenConnectFails covers the second half of the
// bug: a round that genuinely cannot connect used to log and return in
// silence, leaving the modal on "Generando QR…" forever. Now it reaches
// GET /api/status as qr_error.
func TestPairLoopReportsErrorWhenConnectFails(t *testing.T) {
	a := newPairedTestAdapter(t)
	logoutExternally(t, a)

	// A failure that is NOT ErrDeviceDeleted: no repair can fix it, so it
	// must surface instead of being retried.
	a.connectFn = func() error { return errors.New("sin red") }

	if err := a.Reconnect(context.Background()); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	errCode := waitForQRError(t, a)
	if errCode != qrErrUnavailable {
		t.Fatalf("qr_error debería ser %q, fue %q", qrErrUnavailable, errCode)
	}
	// The code must be the stable wire value the dashboard matches on
	// (contract with T188), not the library's error text.
	if errCode != "qr_unavailable" {
		t.Fatalf("el valor en el cable cambió: %q — rompe el contrato con el tablero", errCode)
	}
}

// TestDeletingAnUnpairedDeviceCannotHappen is why pairLoop has no
// ErrDeviceDeleted retry around its Connect (the design review asked for
// one). It pins the measurement that removed it: whatsmeow deletes a
// device only on a server 401/device_removed, which needs a live
// connection of a PAIRED device — so inside pairLoop, where the device is
// unpaired by definition, the mid-flight deletion that a retry would
// catch is not reachable. If a library change ever makes Delete succeed
// on an unpaired device, this test fails and the retry has to come back.
func TestDeletingAnUnpairedDeviceCannotHappen(t *testing.T) {
	a := newPairedTestAdapter(t)
	logoutExternally(t, a)
	if !a.resetDeletedDevice() {
		t.Fatal("control: debería haber reparado el device borrado")
	}

	// a.cli() is now a fresh, unpaired device — the state pairLoop works
	// in. A second logout cannot touch it.
	if err := a.cli().Store.Delete(context.Background()); err == nil && a.cli().Store.Deleted {
		t.Fatal("un device SIN parear no debería poder borrarse: si ahora se puede, pairLoop necesita el reintento de ErrDeviceDeleted de vuelta")
	}
}

// waitForQRError waits for pairLoop's goroutine to record why the round
// produced no QR. Polled, not signalled: setQRError runs after connectFn
// returns, so the fake cannot announce it.
func waitForQRError(t *testing.T, a *Adapter) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, errCode := a.QRState(); errCode != "" {
			return errCode
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pairLoop nunca reportó un error de QR — el modal se quedaría en «Generando QR…» para siempre")
	return ""
}
