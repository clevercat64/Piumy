package store

import (
	"database/sql"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// TestOpenCreatesSchemaAndIsIdempotent covers the smoke DoD (7 tables exist
// after Open) plus migrate()'s idempotency contract (a second Open on the
// same file must not error on "duplicate column name").
func TestOpenCreatesSchemaAndIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	wantTables := []string{"chats", "messages", "outbox", "kv", "media", "drafts", "agents"}
	for _, tbl := range wantTables {
		var name string
		if err := s.db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, tbl).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", tbl, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen: migrate() must be a no-op, not an error, on an already-migrated DB.
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("second Open (migrate idempotency): %v", err)
	}
	if err := s2.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestOpenCreatesMissingParentDirectory is T169's own regression
// (ct-2026-09-19-1433): path now usually resolves under config.DataDir(),
// which may not exist yet on a genuinely fresh install (no installer's
// ForceDirectories step ran first) — sqlite itself never creates a missing
// parent, only the file, so Open must.
func TestOpenCreatesMissingParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does", "not", "exist", "yet", "piumy.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open with a missing parent directory: %v", err)
	}
	defer s.Close()

	if _, err := s.db.Exec("SELECT 1"); err != nil {
		t.Errorf("DB not actually usable after Open: %v", err)
	}
}

// TestMigrateAddsDecryptRetryAtToExistingMessagesTable is the DoD regression
// for T35 (ct-2026-08-08-1258): CREATE TABLE IF NOT EXISTS is a no-op on a
// messages table that already exists — it never adds a new column. The real
// case is the owner's live install, not a fresh DB, so this hand-creates
// messages WITHOUT decrypt_retry_at (the pre-T35 shape) before Open() ever
// runs, then checks migrate() backfilled the column onto that existing table
// and both writing (MarkDecryptRetry) and reading (GetMessages) it work.
func TestMigrateAddsDecryptRetryAtToExistingMessagesTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE messages (
		chat_jid TEXT    NOT NULL,
		id       TEXT    NOT NULL,
		from_me  INTEGER NOT NULL,
		sender   TEXT,
		text     TEXT,
		ts       INTEGER NOT NULL,
		type     TEXT,
		handled  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (chat_jid, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO messages (chat_jid, id, from_me, text, ts) VALUES (?, ?, ?, ?, ?)`,
		"555000001@s.whatsapp.net", "wamid-pre", 1, "ya estaba antes de la migración", 50); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a pre-existing messages table without decrypt_retry_at: %v", err)
	}
	defer s.Close()

	if err := s.MarkDecryptRetry("555000001@s.whatsapp.net", "wamid-pre", 999); err != nil {
		t.Fatalf("MarkDecryptRetry after migration: %v", err)
	}
	msgs, err := s.GetMessages("555000001@s.whatsapp.net", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].DecryptRetryAt != 999 {
		t.Errorf("got %+v, want one pre-existing message with decrypt_retry_at=999 after migrate+mark", msgs)
	}
}

// TestMigrateAddsOriginTerminalIDToExistingOutboxAndMessagesTables is the DoD
// regression for T39 (ct-2026-08-08-1619): the owner's live install already
// has outbox/messages without origin_terminal_id — CREATE TABLE IF NOT
// EXISTS is a no-op there, same reasoning as T35's decrypt_retry_at
// migration test. Hand-creates BOTH tables in their pre-T39 shape before
// Open() ever runs, then checks migrate() backfilled both columns and that
// EnqueueFromAgent/AddMessage (write) and DueOutbox/GetMessages (read) work
// against the migrated tables.
func TestMigrateAddsOriginTerminalIDToExistingOutboxAndMessagesTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")

	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE messages (
		chat_jid TEXT    NOT NULL,
		id       TEXT    NOT NULL,
		from_me  INTEGER NOT NULL,
		sender   TEXT,
		text     TEXT,
		ts       INTEGER NOT NULL,
		type     TEXT,
		handled  INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (chat_jid, id)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE outbox (
		seq        INTEGER PRIMARY KEY AUTOINCREMENT,
		to_jid     TEXT    NOT NULL,
		text       TEXT    NOT NULL,
		created_ts INTEGER NOT NULL,
		sent       INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-existing outbox/messages tables without origin_terminal_id: %v", err)
	}
	defer s.Close()

	if err := s.EnqueueFromAgent("555000001@s.whatsapp.net", "[Agente Uno] hola", 100, "term-555"); err != nil {
		t.Fatalf("EnqueueFromAgent after migration: %v", err)
	}
	due, err := s.DueOutbox(10, 200)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].OriginTerminalID != "term-555" {
		t.Fatalf("got DueOutbox=%+v, want one item with origin_terminal_id=term-555", due)
	}

	if err := s.AddMessage(Message{
		ChatJID: "555000001@s.whatsapp.net", ID: "wamid-1", FromMe: true, Text: "[Agente Uno] hola", TS: 100,
		OriginTerminalID: "term-555",
	}); err != nil {
		t.Fatalf("AddMessage after migration: %v", err)
	}
	msgs, err := s.GetMessages("555000001@s.whatsapp.net", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].OriginTerminalID != "term-555" {
		t.Fatalf("got GetMessages=%+v, want one message with origin_terminal_id=term-555", msgs)
	}
}

// TestMigrateMimeInTextSwapsCorruptedRows is the regression for
// ct-2026-07-21-1727: a pre-fix build's handleMessage stored a media
// message's mime in messages.text and its caption in messages.type
// (swapped). Any row with a matching `media` row whose mime equals the
// corrupted text must come back swapped — text=caption (here empty),
// type=mime — so store.MediaKind(type) recognizes it again. An ordinary
// text message that happens to contain a slash, with no matching media
// row, must be left untouched (the EXISTS guard is what tells the two
// apart).
func TestMigrateMimeInTextSwapsCorruptedRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "piumy.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.AddMessage(Message{ChatJID: "1@lid", ID: "M1", Text: "image/jpeg", Type: "", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMedia(Media{MsgID: "M1", ChatJID: "1@lid", Path: "p", FullPath: "p", Mime: "image/jpeg", TS: 100}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: "1@lid", ID: "M2", Text: "no es un mime, solo 1/3 de la torta", Type: "text", TS: 200}); err != nil {
		t.Fatal(err)
	}

	if err := migrateMimeInText(s.db); err != nil {
		t.Fatal(err)
	}

	byID := func(id string) Message {
		t.Helper()
		msgs, err := s.GetMessages("1@lid", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range msgs {
			if m.ID == id {
				return m
			}
		}
		t.Fatalf("message %q not found", id)
		return Message{}
	}

	fixed := byID("M1")
	if fixed.Text != "" || fixed.Type != "image/jpeg" {
		t.Errorf("M1 = %+v, want Text=\"\" Type=image/jpeg", fixed)
	}

	untouched := byID("M2")
	if untouched.Text != "no es un mime, solo 1/3 de la torta" || untouched.Type != "text" {
		t.Errorf("M2 = %+v, want left untouched (no matching media row)", untouched)
	}

	// Idempotent: a second run must not re-corrupt the already-fixed row.
	if err := migrateMimeInText(s.db); err != nil {
		t.Fatal(err)
	}
	fixed2 := byID("M1")
	if fixed2.Text != "" || fixed2.Type != "image/jpeg" {
		t.Errorf("M1 after second migrate run = %+v, want unchanged", fixed2)
	}
}

// --- T199 (ct-2026-11-11-1359): hard gate against a destructive migration ---
//
// Every migration this package runs is additive (ALTER TABLE ... ADD COLUMN),
// which is exactly why Open() needs no copy of piumy.db before migrate() —
// unlike whatsmeow.db, whose schema the library itself can rewrite and which
// T193 therefore snapshots first. The day someone writes the first DROP or
// RENAME here that reasoning stops holding, and an upgrade destroys owner data
// with nothing to restore from. The three tests below are that gate: the first
// fails on a destructive statement, the other two prove the first can actually
// see one.

var (
	reDropTable   = regexp.MustCompile(`(?is)\bDROP\s+TABLE\b`)
	reDropColumn  = regexp.MustCompile(`(?is)\bDROP\s+COLUMN\b`)
	reRename      = regexp.MustCompile(`(?is)\bRENAME\s+(?:TO|COLUMN)\b`)
	reCreateTable = regexp.MustCompile(`(?is)\bCREATE\s+TABLE\s+`)
	reGuarded     = regexp.MustCompile(`(?is)^IF\s+NOT\s+EXISTS\b`)
)

// findDestructiveDDL names the destructive forms present in one SQL string.
// Empty means nothing destructive.
//
// DELETE and UPDATE are deliberately NOT listed: six non-test files of this
// package run them as ordinary runtime statements (agents, chat, group, media,
// reconcile, reset), and migrate() itself needs UPDATE for its own backfills.
// A gate that fires on every normal write is a gate someone deletes.
func findDestructiveDDL(sql string) []string {
	var found []string
	if reDropTable.MatchString(sql) {
		found = append(found, "DROP TABLE")
	}
	if reDropColumn.MatchString(sql) {
		found = append(found, "DROP COLUMN")
	}
	if reRename.MatchString(sql) {
		found = append(found, "RENAME")
	}
	// The create-copy-drop pattern (SQLite's classic way to change a column's
	// type or drop a constraint) starts by building a replacement table. That
	// CREATE is the one statement of the pattern carrying neither DROP nor
	// RENAME, so catching it is what makes the pattern visible from its first
	// line instead of its last. Every CREATE in the real schema is guarded by
	// IF NOT EXISTS, so the guard is what tells the two apart.
	for _, loc := range reCreateTable.FindAllStringIndex(sql, -1) {
		if !reGuarded.MatchString(sql[loc[1]:]) {
			found = append(found, "unguarded CREATE TABLE")
			break
		}
	}
	return found
}

type sqlLiteral struct {
	file string
	line int
	text string
}

// sqlLiteralsIn returns every string literal of one Go source, with its line.
// Comments are not literals, so prose that quotes a destructive statement
// cannot trip the gate — only SQL this package can actually execute.
func sqlLiteralsIn(fset *token.FileSet, name string, src any) ([]sqlLiteral, error) {
	f, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		return nil, err
	}
	var out []sqlLiteral
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(lit.Value)
		if err != nil {
			text = lit.Value // unparseable literal: scan it exactly as written
		}
		out = append(out, sqlLiteral{name, fset.Position(lit.Pos()).Line, text})
		return true
	})
	return out, nil
}

// packageSQLLiterals sweeps every non-test .go file of this package. It reads
// the SOURCE rather than the columnMigrations variable on purpose: migration
// SQL also lives in a const (confirmationModeMigration) and inline in four
// migrateX functions, and a migration written tomorrow in a brand-new function
// — or in another file of the package — would otherwise slip past the gate.
func packageSQLLiterals(t *testing.T) []sqlLiteral {
	t.Helper()

	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing package sources: %v", err)
	}
	fset := token.NewFileSet()
	var out []sqlLiteral
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		lits, err := sqlLiteralsIn(fset, p, nil)
		if err != nil {
			t.Fatalf("parsing %s: %v", p, err)
		}
		out = append(out, lits...)
	}
	// An empty sweep reads exactly like a clean one. Refuse it: a glob that
	// matched nothing is a broken instrument, not a schema without a
	// destructive migration.
	if len(out) == 0 {
		t.Fatal("swept 0 string literals — the detector is broken, not the schema")
	}
	return out
}

func TestNoDestructiveMigration(t *testing.T) {
	lits := packageSQLLiterals(t)
	// Printed so -v shows how much the gate actually looked at: a sweep that
	// quietly shrank to a handful of literals is a gate that stopped guarding.
	t.Logf("swept %d string literals of the package", len(lits))
	for _, lit := range lits {
		if forms := findDestructiveDDL(lit.text); len(forms) > 0 {
			t.Errorf("%s:%d runs %s against piumy.db.\n"+
				"Every migration here is additive, which is why Open() takes no copy of piumy.db first.\n"+
				"A destructive one loses owner data on upgrade with nothing to restore from: snapshot the\n"+
				"DB before migrate() — the way internal/whatsmeow/premigration.go does for whatsmeow.db\n"+
				"with sessionbackup.SnapshotSQLite (T193) — and only then relax this test.\n"+
				"Statement: %s",
				lit.file, lit.line, strings.Join(forms, " + "), strings.TrimSpace(lit.text))
		}
	}
}

// TestDestructiveDDLDetectorCatchesEachForm is the control for the gate above:
// without it a green sweep could mean "nothing destructive" or "the detector
// matches nothing at all", and those two read identically.
func TestDestructiveDDLDetectorCatchesEachForm(t *testing.T) {
	destructive := []struct{ name, sql string }{
		{"DROP TABLE", `DROP TABLE chats`},
		{"DROP COLUMN", `ALTER TABLE chats DROP COLUMN memory`},
		{"RENAME TO", `ALTER TABLE chats RENAME TO chats_old`},
		{"RENAME COLUMN", `ALTER TABLE chats RENAME COLUMN memory TO notes`},
		{"create-copy-drop, first half", `CREATE TABLE chats_new (jid TEXT PRIMARY KEY)`},
		{"lowercase, across newlines", "alter table chats\n\tdrop column memory"},
	}
	for _, c := range destructive {
		if forms := findDestructiveDDL(c.sql); len(forms) == 0 {
			t.Errorf("detector missed %s: %q", c.name, c.sql)
		}
	}

	legit := []struct{ name, sql string }{
		{"the shape of every real migration", `ALTER TABLE chats ADD COLUMN active INTEGER NOT NULL DEFAULT 0`},
		{"the real schema's guarded create", `CREATE TABLE IF NOT EXISTS kv (key TEXT PRIMARY KEY, value TEXT)`},
		{"migrate()'s own backfill", `UPDATE chats SET confirmation_mode = 'none' WHERE jid NOT LIKE '%@g.us'`},
		{"an ordinary runtime delete", `DELETE FROM messages WHERE chat_jid = ?`},
	}
	for _, c := range legit {
		if forms := findDestructiveDDL(c.sql); len(forms) > 0 {
			t.Errorf("false positive on %s (%v): %q", c.name, forms, c.sql)
		}
	}
}

// TestGateReadsSQLNotComments is the control for the sweep: schema.go already
// discusses destructive migrations in prose, and a gate that read comments
// would fire on the documentation instead of on the code — then get deleted
// for crying wolf. Only executable literals count.
func TestGateReadsSQLNotComments(t *testing.T) {
	const src = "package store\n" +
		"\n" +
		"// Dropped in an older build: DROP TABLE legacy_chats, and the\n" +
		"// ALTER TABLE chats DROP COLUMN advanced that went with it.\n" +
		"var q = `SELECT jid FROM chats`\n"

	lits, err := sqlLiteralsIn(token.NewFileSet(), "fake.go", src)
	if err != nil {
		t.Fatal(err)
	}
	if len(lits) != 1 || lits[0].text != "SELECT jid FROM chats" {
		t.Fatalf("literals = %+v, want exactly the SELECT", lits)
	}
	if forms := findDestructiveDDL(lits[0].text); len(forms) > 0 {
		t.Errorf("prose in a comment tripped the gate: %v", forms)
	}
}
