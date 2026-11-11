package whatsmeow

// T193 (ct-2026-11-11-1039), parte A: la copia de la sesión antes de que la
// librería migre su esquema.
//
// Lo que estos tests PUEDEN y NO pueden probar: no se puede migrar de
// verdad desde un esquema viejo, porque eso necesitaría una base creada por
// la versión ANTERIOR de whatsmeow, que este árbol ya no tiene. Forzar el
// número de versión hacia atrás no da una base v15: da una base v16 que
// dice 15, y pedirle a dbutil que vuelva a aplicar la migración fallaría
// por razones del test, no del producto.
//
// Así que lo que se prueba es lo que de verdad protege la sesión: que la
// copia se toma exactamente cuando hay una migración por delante, que la
// copia ABRE y TIENE el dispositivo pareado dentro (es decir, sirve para
// recuperar la sesión), que un arranque normal no deja basura, y que si la
// copia no se puede tomar Piumy NO migra.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	"piumy-gateway/internal/store"
)

// openSessionDB abre la base de sesión como lo hace New, para poder mirarla
// o manosearla desde el test.
func openSessionDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

// seedPairedSessionAtOlderSchema deja una base con un dispositivo pareado y
// el número de versión en el anterior al objetivo: el estado de la
// instalación del boss justo antes de que la 0.13.1 migre v15 → v16.
func seedPairedSessionAtOlderSchema(t *testing.T) (dbPath string, older int) {
	t.Helper()
	dbPath = filepath.Join(t.TempDir(), "wm.db")
	seedFakeDeviceWithJID(t, dbPath, "555000001.0:15@s.whatsapp.net")

	// Conexión local, cerrada al salir de la siembra: más abajo hay un test
	// que comprueba que el ARCHIVO no queda tomado tras un New fallido, y
	// un handle del propio test lo haría fallar por el motivo equivocado.
	db, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	target := targetSchemaVersion()
	if _, err := db.ExecContext(ctx, "UPDATE whatsmeow_version SET version = ?", target-1); err != nil {
		t.Fatalf("simular la versión anterior: %v", err)
	}
	got, err := schemaVersion(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if got != target-1 {
		t.Fatalf("control: la siembra debería dejar la versión en %d, dejó %d", target-1, got)
	}
	return dbPath, got
}

// countCopies es cuántas copias previas hay junto a la base.
func countCopies(t *testing.T, dbPath string) int {
	t.Helper()
	paths, err := filepath.Glob(premigrationGlob(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	return len(paths)
}

func TestBackupBeforeMigrationCopiesASessionThatCanBeRecovered(t *testing.T) {
	ctx := context.Background()
	dbPath, older := seedPairedSessionAtOlderSchema(t)
	db := openSessionDB(t, dbPath)

	dest, err := backupBeforeMigration(ctx, db, dbPath, premigrationKeepDefault)
	if err != nil {
		t.Fatalf("backupBeforeMigration: %v", err)
	}
	if dest == "" {
		t.Fatal("con una migración por delante se tiene que tomar una copia")
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("la copia no quedó escrita: %v", err)
	}
	// El nombre lleva la versión en la que se tomó, para que un humano sepa
	// a qué versión volver sin abrirla.
	if want := fmt.Sprintf(".premigration-v%d-", older); !strings.Contains(dest, want) {
		t.Errorf("el nombre de la copia debería llevar %q, es %q", want, dest)
	}

	// Lo que de verdad importa: la copia ABRE y el dispositivo pareado está
	// DENTRO. Una copia que no se puede abrir, o que no trae la sesión, no
	// sirve para nada y se vería igual de bien en un os.Stat.
	copyDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dest))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	copyDB.SetMaxOpenConns(1)

	if v, err := schemaVersion(ctx, copyDB); err != nil {
		t.Fatalf("leer la versión de la copia: %v", err)
	} else if v != older {
		t.Errorf("la copia debería estar en la versión %d, está en %d", older, v)
	}

	container := sqlstore.NewWithDB(copyDB, "sqlite", waLog.Noop)
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatalf("abrir la copia como store de whatsmeow: %v", err)
	}
	if device.ID == nil {
		t.Fatal("la copia no trae el dispositivo pareado: no serviría para recuperar la sesión")
	}
	t.Logf("la copia abre y trae la sesión (jid %s) en la versión %d", device.ID, older)
}

// TestBackupBeforeMigrationSkipsWhenNothingWillMigrate son los dos controles
// que impiden que esto deje una copia en CADA arranque: una base ya al día
// (el arranque ordinario) y una instalación limpia (no hay sesión que
// perder todavía).
func TestBackupBeforeMigrationSkipsWhenNothingWillMigrate(t *testing.T) {
	ctx := context.Background()

	t.Run("base ya en la versión objetivo", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "wm.db")
		seedFakeDeviceWithJID(t, dbPath, "555000002.0:15@s.whatsapp.net")
		db := openSessionDB(t, dbPath)

		if v, err := schemaVersion(ctx, db); err != nil || v != targetSchemaVersion() {
			t.Fatalf("control: la siembra debería dejar la versión al día (%d), dejó %d (err %v)", targetSchemaVersion(), v, err)
		}
		dest, err := backupBeforeMigration(ctx, db, dbPath, premigrationKeepDefault)
		if err != nil {
			t.Fatalf("backupBeforeMigration: %v", err)
		}
		if dest != "" {
			t.Errorf("un arranque ordinario no debería copiar nada, copió %q", dest)
		}
		if n := countCopies(t, dbPath); n != 0 {
			t.Errorf("quedaron %d copias en un arranque ordinario", n)
		}
	})

	t.Run("instalación limpia, sin tabla de versión", func(t *testing.T) {
		dbPath := filepath.Join(t.TempDir(), "wm.db")
		db := openSessionDB(t, dbPath)
		// Ni siquiera existe la tabla: es lo que ve un PC recién instalado.
		if v, err := schemaVersion(ctx, db); err != nil || v != 0 {
			t.Fatalf("control: una base limpia debería dar versión 0 sin error, dio %d (err %v)", v, err)
		}
		dest, err := backupBeforeMigration(ctx, db, dbPath, premigrationKeepDefault)
		if err != nil {
			t.Fatalf("backupBeforeMigration: %v", err)
		}
		if dest != "" {
			t.Errorf("una instalación limpia no tiene sesión que respaldar, copió %q", dest)
		}
	})
}

// TestNewRefusesToMigrateWhenTheBackupFails es el corazón del contrato: si
// la sesión no se puede respaldar, Piumy NO migra. Verifica las dos mitades
// — que New falla, y que la base quedó SIN migrar (que es lo que permite
// reinstalar la versión vieja y seguir con la misma sesión).
func TestNewRefusesToMigrateWhenTheBackupFails(t *testing.T) {
	ctx := context.Background()
	dbPath, older := seedPairedSessionAtOlderSchema(t)

	boom := errors.New("disco lleno")
	original := premigrationSnapshot
	premigrationSnapshot = func(context.Context, string, string) error { return boom }
	t.Cleanup(func() { premigrationSnapshot = original })

	a, err := New(ctx, Config{DBPath: dbPath})
	if err == nil {
		t.Cleanup(a.Stop)
		t.Fatal("New debería fallar si no puede respaldar la sesión antes de migrar")
	}
	if !errors.Is(err, boom) {
		t.Errorf("el error debería envolver el motivo real, es: %v", err)
	}

	// Y la prueba que de verdad importa: NADIE migró la base.
	db := openSessionDB(t, dbPath)
	v, verr := schemaVersion(ctx, db)
	if verr != nil {
		t.Fatal(verr)
	}
	if v != older {
		t.Fatalf("la base se migró a %d pese a que el respaldo falló: la sesión quedó sin red de seguridad (esperaba %d)", v, older)
	}
}

func TestRotatePremigrationCopiesKeepsTheNewest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wm.db")
	if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Cinco copias con fechas distintas y conocidas: la más nueva primero
	// en lo que debería sobrevivir.
	const total = 5
	keep := 2
	now := time.Now()
	made := make([]string, 0, total)
	for i := 0; i < total; i++ {
		p := premigrationName(dbPath, 10+i, now.Add(-time.Duration(i)*time.Hour))
		if err := os.WriteFile(p, []byte("copia"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now, now.Add(-time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		made = append(made, p)
	}
	if n := countCopies(t, dbPath); n != total {
		t.Fatalf("control: la siembra debería dejar %d copias, dejó %d", total, n)
	}

	if err := rotatePremigrationCopies(dbPath, keep); err != nil {
		t.Fatalf("rotar: %v", err)
	}
	if n := countCopies(t, dbPath); n != keep {
		t.Fatalf("deberían quedar %d copias, quedaron %d", keep, n)
	}
	// Las que sobreviven son las dos MÁS NUEVAS, no dos cualquiera: el
	// orden por nombre pondría "v14" antes que "v9" y borraría la que no es.
	for i := 0; i < keep; i++ {
		if _, err := os.Stat(made[i]); err != nil {
			t.Errorf("la copia más nueva %q debería sobrevivir: %v", made[i], err)
		}
	}
	for i := keep; i < total; i++ {
		if _, err := os.Stat(made[i]); err == nil {
			t.Errorf("la copia vieja %q debería haberse borrado", made[i])
		}
	}
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("la rotación no debe tocar la base: %v", err)
	}
}

// TestPremigrationKeepComesFromConfig: el número de copias sale de la
// configuración, no del código. El default es el piso cuando no hay nada
// configurado o lo configurado no tiene sentido.
func TestPremigrationKeepComesFromConfig(t *testing.T) {
	if got := premigrationKeep(nil); got != premigrationKeepDefault {
		t.Errorf("sin store debería caer al default %d, dio %d", premigrationKeepDefault, got)
	}
	// Con store de verdad: el valor configurado manda, y la basura no.
	st := openTestAvatarStore(t)
	if got := premigrationKeep(st); got != premigrationKeepDefault {
		t.Errorf("sin valor configurado debería dar el default %d, dio %d", premigrationKeepDefault, got)
	}
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"7", 7},
		{"1", 1},
		{"0", premigrationKeepDefault},
		{"-3", premigrationKeepDefault},
		{"muchas", premigrationKeepDefault},
		{"", premigrationKeepDefault},
	} {
		if err := st.KVSet(store.SettingSessionPreMigrationKeep, tc.raw); err != nil {
			t.Fatal(err)
		}
		if got := premigrationKeep(st); got != tc.want {
			t.Errorf("configurado %q → esperaba %d, dio %d", tc.raw, tc.want, got)
		}
	}
}

// TestNewClosesTheSessionFileOnEveryFailurePath cubre el arreglo de los tres
// caminos de error de New que dejaban la conexión abierta (T193, pedido por
// Citrino: los dos de adapter.go venían de F5). En Windows un handle abierto
// IMPIDE borrar el archivo, así que os.Remove es la prueba directa —  y es
// justo lo que bloquearía restaurar una de estas copias sobre la base.
func TestNewClosesTheSessionFileOnEveryFailurePath(t *testing.T) {
	ctx := context.Background()

	t.Run("la migración del esquema falla", func(t *testing.T) {
		// Un archivo que no es una base SQLite: sql.Open no se queja (abre
		// en diferido), container.Upgrade sí. Ese es adapter.go:498.
		dbPath := filepath.Join(t.TempDir(), "wm.db")
		if err := os.WriteFile(dbPath, []byte("esto no es una base sqlite"), 0o600); err != nil {
			t.Fatal(err)
		}
		a, err := New(ctx, Config{DBPath: dbPath})
		if err == nil {
			t.Cleanup(a.Stop)
			t.Fatal("control: New no debería poder abrir una base inválida")
		}
		if err := os.Remove(dbPath); err != nil {
			t.Errorf("el archivo de sesión quedó tomado tras fallar New: %v", err)
		}
	})

	t.Run("el respaldo previo falla", func(t *testing.T) {
		dbPath, _ := seedPairedSessionAtOlderSchema(t)

		original := premigrationSnapshot
		premigrationSnapshot = func(context.Context, string, string) error {
			return errors.New("disco lleno")
		}
		t.Cleanup(func() { premigrationSnapshot = original })

		a, err := New(ctx, Config{DBPath: dbPath})
		if err == nil {
			t.Cleanup(a.Stop)
			t.Fatal("control: New debería fallar si no puede respaldar")
		}
		if err := os.Remove(dbPath); err != nil {
			t.Errorf("el archivo de sesión quedó tomado tras fallar New: %v", err)
		}
	})
}

// TestSessionSnapshotCopiesTheWhatsAppSessionNotAnotherDatabase es el test
// del defecto real que tuvo la primera versión de T193 parte B: el freno
// del actualizador se cableó al Backuper de sessionbackup, que apunta al
// store PROPIO de Piumy (piumy.db), no a la base de whatsmeow — habría
// frenado las actualizaciones por una copia del archivo equivocado mientras
// le decía al dueño que le protegía la sesión de WhatsApp.
//
// Por eso acá hay un SEÑUELO: una segunda base en la misma carpeta, como en
// la instalación real. Copiar la equivocada deja de ser un error invisible.
func TestSessionSnapshotCopiesTheWhatsAppSessionNotAnotherDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	// La sesión de WhatsApp: la base con el dispositivo pareado.
	dbPath := filepath.Join(dir, "whatsmeow.db")
	seedFakeDeviceWithJID(t, dbPath, "555000003.0:15@s.whatsapp.net")

	// El señuelo, al lado: otra base SQLite que NO es la sesión.
	decoy := filepath.Join(dir, "piumy.db")
	decoyDB, err := sql.Open("sqlite", "file:"+decoy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decoyDB.ExecContext(ctx, "CREATE TABLE chats (id TEXT)"); err != nil {
		t.Fatal(err)
	}
	decoyDB.Close()

	a, err := New(ctx, Config{DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Stop)

	dest, err := a.SessionSnapshot(ctx)
	if err != nil {
		t.Fatalf("SessionSnapshot: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("la copia no quedó escrita: %v", err)
	}
	// Queda junto a la base de la sesión, no junto al señuelo.
	if !strings.HasPrefix(dest, dbPath) {
		t.Errorf("la copia debería quedar junto a %q, quedó en %q", dbPath, dest)
	}

	// Y la prueba que de verdad distingue una base de la otra: la copia
	// tiene DENTRO el dispositivo pareado. El señuelo no podría pasarla.
	copyDB, err := sql.Open("sqlite", fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", dest))
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	copyDB.SetMaxOpenConns(1)
	container := sqlstore.NewWithDB(copyDB, "sqlite", waLog.Noop)
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		t.Fatalf("la copia no abre como store de whatsmeow — ¿se copió la base equivocada?: %v", err)
	}
	if device.ID == nil {
		t.Fatal("la copia no trae el dispositivo pareado: no es la sesión de WhatsApp")
	}

	// CONTROL: el señuelo sigue intacto y SIN copias propias al lado.
	if _, err := os.Stat(decoy); err != nil {
		t.Errorf("el señuelo debería seguir ahí: %v", err)
	}
	if decoyCopies, _ := filepath.Glob(premigrationGlob(decoy)); len(decoyCopies) != 0 {
		t.Errorf("se copió la base equivocada: %v", decoyCopies)
	}
}
