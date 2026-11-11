package whatsmeow

// Pre-migration session backup (T193, ct-2026-11-11-1039). Boss, verbatim:
// «piumy se tiene que auto actualizar y auto instalar sin perder la sesion de
// whatsapp».
//
// The installer is not the risk — it never touches secrets/ and the 0.13.0
// install kept config, keys and agents. The risk is the first BOOT of a new
// version: whatsmeow migrates its own session DB in container.Upgrade (see
// New), and those migrations have no way back. If one fails, or the previous
// version has to be reinstalled, the session is gone and the owner has to
// scan the QR again — which, with the owner abroad, is not a small cost.
//
// So: before letting the library migrate, copy the DB. The copy is only
// taken when a migration is actually about to happen, and if it cannot be
// taken, Piumy does NOT migrate (New fails loudly instead of quietly
// spending a session).

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"go.mau.fi/whatsmeow/store/sqlstore/upgrades"

	"piumy-gateway/internal/sessionbackup"
	"piumy-gateway/internal/store"
)

// premigrationKeepDefault is the code-level fallback for
// store.SettingSessionPreMigrationKeep — same "safe even without a
// configured value" pattern New already uses for the reconnect delays.
// Three covers the realistic case (the previous schema, plus a couple of
// older ones) while a full copy of the session DB is not free on disk.
const premigrationKeepDefault = 3

// premigrationSnapshot is sessionbackup's VACUUM INTO, indirected so a test
// can make the copy fail and prove that Piumy then refuses to migrate —
// the one behaviour that protects the session, and the one that cannot be
// forced from outside (a real VACUUM INTO only fails on a full or
// unwritable disk). Same "inject the external dependency" seam as
// Adapter.connectFn.
var premigrationSnapshot = sessionbackup.SnapshotSQLite

// premigrationGlob / premigrationName build the name of a copy: the session
// DB's own path plus the schema version it was taken AT, so a human (or a
// restore) can tell which version each copy belongs to without opening it.
// The timestamp keeps two migrations of the same version apart and makes
// VACUUM INTO's "destination must not exist" rule a non-issue.
func premigrationName(dbPath string, version int, at time.Time) string {
	return fmt.Sprintf("%s.premigration-v%d-%s", dbPath, version, at.Format("20060102-150405"))
}

func premigrationGlob(dbPath string) string {
	return dbPath + ".premigration-v*"
}

// premigrationKeep resolves how many copies to keep: the dashboard-editable
// setting if it is there and sane, the constant otherwise. A missing store
// is not an error — it means nobody configured anything.
func premigrationKeep(st *store.Store) int {
	if st == nil {
		return premigrationKeepDefault
	}
	raw, err := st.KVGet(store.SettingSessionPreMigrationKeep)
	if err != nil || raw == "" {
		return premigrationKeepDefault
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return premigrationKeepDefault
	}
	return n
}

// schemaVersion reports the schema version recorded in whatsmeow's own
// session DB, or 0 when there is none yet (a clean install, or a DB that
// has never been migrated).
//
// It reads the table rather than asking the library because dbutil's own
// getVersion is unexported. The table NAME is not invented here: sqlstore
// sets it (container.go's wrapped.VersionTable = "whatsmeow_version") and
// dbutil creates it as (version INTEGER, compat INTEGER). Existence is
// checked through sqlite_master instead of matching on an error string,
// which would break the day the driver rewords it.
func schemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var present int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'whatsmeow_version'`,
	).Scan(&present)
	if err != nil {
		return 0, fmt.Errorf("whatsmeow: look for the schema version table: %w", err)
	}
	if present == 0 {
		return 0, nil
	}
	var version int
	err = db.QueryRowContext(ctx, `SELECT version FROM whatsmeow_version`).Scan(&version)
	if errors.Is(err, sql.ErrNoRows) {
		// The table exists but is empty: dbutil creates it before writing
		// the first version. Same meaning as not being there at all.
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("whatsmeow: read the schema version: %w", err)
	}
	return version, nil
}

// targetSchemaVersion is the version container.Upgrade will bring the DB to.
// len(upgrades.Table) IS that number — the table is whatsmeow's own
// embedded list of migrations (dbutil.UpgradeTable, a []Upgrade), so this
// rises on its own when the library is updated and never needs a constant
// here. Verified against the library before relying on it: after an Upgrade,
// the version written in the DB equals this length.
func targetSchemaVersion() int {
	return len(upgrades.Table)
}

// backupBeforeMigration copies the session DB when, and only when,
// container.Upgrade is about to migrate it to a newer schema. It returns the
// path of the copy it wrote, or "" when no copy was needed.
//
// Nothing is copied for a clean install (version 0 — there is no session to
// lose yet) nor for a DB already at the target, which is every ordinary
// boot: this costs one cheap query then.
//
// An error here is meant to STOP the caller from migrating. That is the
// whole point: migrating without the copy is the one outcome that can cost
// the owner their session, so failing to start is the cheaper failure.
func backupBeforeMigration(ctx context.Context, db *sql.DB, dbPath string, keep int) (string, error) {
	current, err := schemaVersion(ctx, db)
	if err != nil {
		return "", err
	}
	target := targetSchemaVersion()
	if current <= 0 || current >= target {
		return "", nil
	}

	dest := premigrationName(dbPath, current, time.Now())
	// SnapshotSQLite is sessionbackup's own VACUUM INTO, on its own
	// short-lived read-only connection — hot-backup-safe, so the
	// connection this function was handed stays usable. Reused rather than
	// re-implemented: one place where "copy a SQLite file consistently"
	// lives (and one place already verified against modernc.org/sqlite).
	if err := premigrationSnapshot(ctx, dbPath, dest); err != nil {
		return "", fmt.Errorf("whatsmeow: respaldo previo a la migración del esquema v%d→v%d: %w", current, target, err)
	}
	// Rotation failing is NOT a reason to refuse the migration: the copy
	// that matters is already written, and old copies left behind cost
	// disk, never the session. It is still said out loud — a disk quietly
	// filling with session copies is the kind of thing nobody notices
	// until it is the actual problem.
	if err := rotatePremigrationCopies(dbPath, keep); err != nil {
		log.Printf("whatsmeow: no se pudieron rotar las copias previas de la sesión: %v", err)
	}
	return dest, nil
}

// rotatePremigrationCopies keeps the newest `keep` copies and deletes the
// rest. Ordered by modification time, not by name: a name sorts
// "v15" before "v9", so lexical order would delete the wrong file the day
// the schema reaches two digits — which it already has.
func rotatePremigrationCopies(dbPath string, keep int) error {
	if keep <= 0 {
		return nil
	}
	paths, err := filepath.Glob(premigrationGlob(dbPath))
	if err != nil {
		return err
	}
	if len(paths) <= keep {
		return nil
	}
	type copyFile struct {
		path string
		mod  time.Time
	}
	files := make([]copyFile, 0, len(paths))
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		files = append(files, copyFile{path: p, mod: info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	var firstErr error
	for _, f := range files[min(keep, len(files)):] {
		if err := os.Remove(f.path); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SessionSnapshot copies the WhatsApp session DB right now, with the same
// mechanism, naming and rotation as the pre-migration copy above, and
// returns where it left it.
//
// This is what the updater calls before launching the installer (T193
// parte B): the installer replaces the binary, and the version it installs
// will migrate this DB on its first boot — so the copy belongs BEFORE the
// installer runs, not after. It deliberately does NOT go through
// sessionbackup.Backuper: that one is encrypted, periodic, and points at
// piumy's OWN store.db, not at this file (its package doc says so). Using
// it here would have frozen the updater over a copy of the wrong database
// while telling the owner it was protecting their WhatsApp session.
//
// No key, no configuration, nothing to turn on: this copy always runs.
func (a *Adapter) SessionSnapshot(ctx context.Context) (string, error) {
	if a.dbPath == "" {
		return "", errors.New("whatsmeow: no hay base de sesión que copiar")
	}
	// The schema is at the target version here: New only returns an Adapter
	// after container.Upgrade succeeded.
	dest := premigrationName(a.dbPath, targetSchemaVersion(), time.Now())
	if err := premigrationSnapshot(ctx, a.dbPath, dest); err != nil {
		return "", fmt.Errorf("whatsmeow: copia de la sesión: %w", err)
	}
	if err := rotatePremigrationCopies(a.dbPath, premigrationKeep(a.store)); err != nil {
		log.Printf("whatsmeow: no se pudieron rotar las copias de la sesión: %v", err)
	}
	return dest, nil
}
