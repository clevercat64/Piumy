// Package clientsfile reads CleverCoder's clients.json (T172c, ct-2026-11-10-
// 2254): the read-only snapshot of its clients (one client = one WhatsApp
// group) that the dashboard's Clientes tab shows. Piumy never writes it.
// It knows the FILE and nothing of Piumy — matching a client to a chat is the
// REST handler's job.
package clientsfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Status is what the file says right now, for the dashboard to word it.
const (
	StatusMissing        = "missing"         // no file: CleverCoder not on this PC, or its module off
	StatusEmpty          = "empty"           // fresh photo, no clients yet
	StatusStale          = "stale"           // CleverCoder stopped rewriting it
	StatusUnknownVersion = "unknown_version" // a schema this Piumy doesn't know: nothing is interpreted
	StatusOK             = "ok"
)

const (
	// schemaVersion is the only clients.json version this reader understands.
	schemaVersion = 1
	// staleAfter: CleverCoder rewrites the file every minute (even with no
	// clients), so two minutes without a new generated_at means it stopped.
	staleAfter = 2 * time.Minute
	// appDataEnv moves the file in tests; it takes the place of %LOCALAPPDATA%,
	// exactly as CleverCoder's own AppDataPaths.LocalRoot does.
	appDataEnv = "CLEVERCODER_APPDATA_DIR"
)

// Snapshot is the file as the dashboard needs it. Clients keep their fields
// as the file has them (numbers untouched); the reader interprets none.
type Snapshot struct {
	Status      string
	GeneratedAt string // as written in the file; "" for missing / unknown_version
	Clients     []map[string]any
}

// Path is where CleverCoder writes the file on this PC, or "" when there is no
// place it could be (no %LOCALAPPDATA%, e.g. Linux/macOS). Read on every call:
// the environment is not cached.
func Path() string {
	root := strings.TrimSpace(os.Getenv(appDataEnv))
	if root == "" {
		root = os.Getenv("LOCALAPPDATA")
	}
	if root == "" {
		return ""
	}
	return filepath.Join(root, "CleverCoderData", "clientes", "clients.json")
}

// Read opens path, reads it and closes it right away: CleverCoder skips its own
// write if it finds the file taken, so it is never held. A missing file is
// StatusMissing; a file that exists but can't be read or doesn't parse is an
// error — there is no honest sixth status for it.
func Read(path string, now time.Time) (Snapshot, error) {
	if path == "" {
		return nothing(StatusMissing), nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nothing(StatusMissing), nil
	}
	if err != nil {
		return Snapshot{}, err
	}

	// The version is read BEFORE anything else: a future schema may shape
	// "clients" differently, and that must come out as unknown_version, not as
	// a parse error.
	var head struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return Snapshot{}, fmt.Errorf("clientsfile: %s: %w", path, err)
	}
	if head.SchemaVersion != schemaVersion {
		return nothing(StatusUnknownVersion), nil
	}

	var body struct {
		GeneratedAt string           `json:"generated_at"`
		Clients     []map[string]any `json:"clients"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber() // numbers go back out exactly as CleverCoder wrote them
	if err := dec.Decode(&body); err != nil {
		return Snapshot{}, fmt.Errorf("clientsfile: %s: %w", path, err)
	}
	generated, err := time.Parse(time.RFC3339, body.GeneratedAt)
	if err != nil {
		return Snapshot{}, fmt.Errorf("clientsfile: %s: generated_at %q: %w", path, body.GeneratedAt, err)
	}
	if body.Clients == nil {
		body.Clients = []map[string]any{}
	}

	status := StatusOK
	switch {
	case now.Sub(generated) > staleAfter:
		status = StatusStale
	case len(body.Clients) == 0:
		status = StatusEmpty
	}
	return Snapshot{Status: status, GeneratedAt: body.GeneratedAt, Clients: body.Clients}, nil
}

// nothing is a snapshot with no data: the status alone says why.
func nothing(status string) Snapshot {
	return Snapshot{Status: status, Clients: []map[string]any{}}
}
