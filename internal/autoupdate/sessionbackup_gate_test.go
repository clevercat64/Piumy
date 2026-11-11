package autoupdate

// T193 parte B (ct-2026-11-11-1039): antes de lanzar el instalador, el
// actualizador pide una copia de la sesión de WhatsApp; si no la consigue,
// no instala. Boss: «piumy se tiene que auto actualizar y auto instalar sin
// perder la sesion de whatsapp».

import (
	"context"
	"errors"
	"testing"
)

// withSessionBackup arma el updater del fixture con un respaldo que cuenta
// sus llamadas y falla si se le pide.
func withSessionBackup(t *testing.T, f *fixture, fail error) (*Updater, *harness, *int) {
	t.Helper()
	u, h := f.updater("0.12.5")
	calls := 0
	u.SetSessionBackup(func() error {
		calls++
		return fail
	})
	return u, h, &calls
}

func TestNoInstallWhenTheSessionCannotBeBackedUp(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h, calls := withSessionBackup(t, f, errors.New("disco lleno"))

	u.tick(context.Background())

	if *calls != 1 {
		t.Errorf("el respaldo debería haberse intentado 1 vez, se intentó %d", *calls)
	}
	// Lo que importa: el instalador NO se lanzó.
	if len(h.launched) != 0 {
		t.Fatalf("se lanzó el instalador sin respaldo de la sesión: %+v", h.launched)
	}
	if h.stops != 0 {
		t.Errorf("no se debería haber apagado Piumy: stops=%d", h.stops)
	}
	if u.blocked != BlockedNoSessionBackup {
		t.Errorf("blocked = %q, want %q — el tablero no diría por qué no se actualiza", u.blocked, BlockedNoSessionBackup)
	}
	// La versión no queda marcada como "instalándose": si quedara, el
	// tablero diría que hay una instalación en curso que nunca empezó.
	if pending := u.load().Pending; pending != "" {
		t.Errorf("Pending = %q, debería estar vacío: no se instaló nada", pending)
	}
	// Y lo verificado sigue guardado, listo para el próximo intento: el
	// respaldo que falla es una espera, no un descarte.
	if u.load().Ready == nil {
		t.Error("lo descargado y verificado no debería tirarse por un respaldo fallido")
	}
}

func TestInstallProceedsWhenTheSessionIsBackedUp(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h, calls := withSessionBackup(t, f, nil)

	u.tick(context.Background())

	if *calls != 1 {
		t.Errorf("el respaldo debería haberse pedido 1 vez, se pidió %d", *calls)
	}
	if len(h.launched) != 1 {
		t.Fatalf("con el respaldo hecho, el instalador debería lanzarse: %d lanzamientos", len(h.launched))
	}
	if u.blocked != "" {
		t.Errorf("blocked = %q, debería estar vacío", u.blocked)
	}
}

// TestInstallProceedsWithNoSessionBackupWired es el control de que esto no
// rompe a quien no tiene respaldo configurado: sin clave de backup el dueño
// ya eligió correr sin copias, y negarle las actualizaciones para siempre
// sería una decisión de producto que nadie tomó.
func TestInstallProceedsWithNoSessionBackupWired(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5") // sin SetSessionBackup

	u.tick(context.Background())

	if len(h.launched) != 1 {
		t.Fatalf("sin respaldo cableado debería instalar igual que antes de T193: %d lanzamientos", len(h.launched))
	}
	if u.blocked != "" {
		t.Errorf("blocked = %q, no debería bloquear por algo que nadie cableó", u.blocked)
	}
}

// TestTheBackupIsNotTakenOnEveryLook es la razón por la que esto vive en
// apply() y NO en gateBlock(): ese se documenta como «every reason not to
// install right now, cheapest first» y corre en CADA tick, así que una
// copia cifrada de la base de sesión se estaría tomando una y otra vez por
// una actualización que puede faltar horas.
func TestTheBackupIsNotTakenOnEveryLook(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h, calls := withSessionBackup(t, f, nil)

	// Piumy está ocupado: descarga y verifica, pero no instala.
	h.idle = BlockedBusy
	u.tick(context.Background())
	u.tick(context.Background())
	u.tick(context.Background())

	if *calls != 0 {
		t.Fatalf("se respaldó %d veces mientras solo se miraba; debería ser 0", *calls)
	}
	if len(h.launched) != 0 {
		t.Fatalf("control: no debería instalar con la cola ocupada")
	}

	// Llega el momento tranquilo: ahí sí, UNA copia.
	h.idle = ""
	u.tick(context.Background())

	if *calls != 1 {
		t.Errorf("en el intento real debería respaldar exactamente 1 vez, respaldó %d", *calls)
	}
	if len(h.launched) != 1 {
		t.Errorf("debería haber instalado: %d lanzamientos", len(h.launched))
	}
}
