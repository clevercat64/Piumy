package driftreport

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// machineIDFile sits at the root of the data dir, NOT in secrets/ and NOT in
// the DB. Not in secrets/: it is an identifier, not a credential. Not in the
// DB: sessionbackup snapshots the whole DB, so a restore onto another machine
// would clone the id.
const machineIDFile = "machine-id"

var guidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ensureMachineID returns this install's GUID, creating the file the first
// time a report is about to leave — a Piumy that never reports writes nothing.
// Caller holds r.mu.
func (r *Reporter) ensureMachineID() (string, error) {
	if r.machineID != "" {
		return r.machineID, nil
	}
	id, err := loadOrCreateMachineID(r.cfg.DataDir)
	if err != nil {
		return "", err
	}
	r.machineID = id
	return id, nil
}

func loadOrCreateMachineID(dir string) (string, error) {
	if dir == "" {
		// filepath.Join("", name) is relative to the CWD — the exact bug
		// T169 closed for every other data file. Refuse instead.
		return "", errors.New("sin carpeta de datos")
	}
	path := filepath.Join(dir, machineIDFile)
	raw, err := os.ReadFile(path)
	if err == nil {
		// A malformed file (hand-edited, truncated) is replaced: an id is not
		// precious, and sending a garbage one would be worse than a new one.
		if id := strings.TrimSpace(string(raw)); guidPattern.MatchString(id) {
			return id, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}

	id, err := newGUID()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return id, nil
}

// newGUID is a random (version 4) GUID, lower-case.
func newGUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
