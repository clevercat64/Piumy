package clientsfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 11, 10, 22, 10, 0, 0, time.UTC)

// clientJSON follows the clients.json v1 shape (ct-2026-11-10-1922 notes 3-4)
// with a synthetic 555 key. cost_usd.used is written "1.2500" on purpose: a
// float64 round trip would print 1.25, so it proves the numbers pass untouched.
const clientJSON = `{"key":"555000000000001","name":"Grupo Uno","tier":"base","state":"abierto","paused_until":null,"reopened":false,"resets_at":"2026-11-16T09:00:00-03:00","week":{"lines":{"used":120,"limit":500},"cost_usd":{"used":1.2500,"limit":10}},"previous_week":{"lines":{"used":3,"limit":500},"cost_usd":{"used":0.5,"limit":10}},"history":[{"starts_at":"2026-11-09T09:00:00-03:00","lines":120,"cost_usd":1.25}],"campo_nuevo":"lo trae una version futura"}`

// fileJSON builds a whole clients.json; generated is written as RFC 3339.
func fileJSON(schema int, generated time.Time, clients ...string) string {
	return fmt.Sprintf(`{"schema_version":%d,"generated_at":%q,"count":%d,"clients":[%s]}`,
		schema, generated.Format(time.RFC3339), len(clients), strings.Join(clients, ","))
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "clients.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReadStatuses(t *testing.T) {
	cases := []struct {
		name        string
		content     string // "" = no file
		wantStatus  string
		wantClients int
		wantGen     bool // GeneratedAt carries the file's value
	}{
		{"no file", "", StatusMissing, 0, false},
		{"fresh and empty", fileJSON(1, now.Add(-30*time.Second)), StatusEmpty, 0, true},
		// The dashboard iterates "clients": absent or null must still come out as a list.
		{"clients key absent", `{"schema_version":1,"generated_at":"` + now.Format(time.RFC3339) + `"}`, StatusEmpty, 0, true},
		{"clients null", `{"schema_version":1,"generated_at":"` + now.Format(time.RFC3339) + `","clients":null}`, StatusEmpty, 0, true},
		{"fresh with a client", fileJSON(1, now.Add(-30*time.Second), clientJSON), StatusOK, 1, true},
		{"exactly 2 minutes old is still fresh", fileJSON(1, now.Add(-2*time.Minute), clientJSON), StatusOK, 1, true},
		{"over 2 minutes old is stale, and keeps the last photo", fileJSON(1, now.Add(-2*time.Minute-time.Second), clientJSON), StatusStale, 1, true},
		{"stale wins over empty", fileJSON(1, now.Add(-5*time.Minute)), StatusStale, 0, true},
		// A future schema may shape "clients" differently: must be
		// unknown_version, not a parse error, and nothing is interpreted.
		{"a newer schema", `{"schema_version":2,"generated_at":"` + now.Format(time.RFC3339) + `","clients":{"otra":"forma"}}`, StatusUnknownVersion, 0, false},
		{"no schema_version at all", `{"generated_at":"` + now.Format(time.RFC3339) + `","clients":[` + clientJSON + `]}`, StatusUnknownVersion, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clients.json")
			if tc.content != "" {
				path = writeFile(t, tc.content)
			}

			snap, err := Read(path, now)

			if err != nil {
				t.Fatalf("Read: %v", err)
			}
			if snap.Status != tc.wantStatus {
				t.Errorf("status = %q, want %q", snap.Status, tc.wantStatus)
			}
			if snap.Clients == nil || len(snap.Clients) != tc.wantClients {
				t.Errorf("clients = %v, want a non-nil list of %d", snap.Clients, tc.wantClients)
			}
			if (snap.GeneratedAt != "") != tc.wantGen {
				t.Errorf("generated_at = %q, want it filled: %v", snap.GeneratedAt, tc.wantGen)
			}
		})
	}
}

func TestReadWithNoPathIsMissing(t *testing.T) {
	snap, err := Read("", now)
	if err != nil || snap.Status != StatusMissing {
		t.Errorf("Read(\"\") = %+v, %v, want missing (no %%LOCALAPPDATA%%: CleverCoder can't be here)", snap, err)
	}
}

// TestReadPassesTheFieldsAsWritten: the reader interprets nothing, so a field
// it has never heard of reaches the dashboard and the numbers keep their text.
func TestReadPassesTheFieldsAsWritten(t *testing.T) {
	snap, err := Read(writeFile(t, fileJSON(1, now, clientJSON)), now)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(snap.Clients[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"used":1.2500`, `"campo_nuevo":"lo trae una version futura"`, `"key":"555000000000001"`, `"paused_until":null`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("client = %s, want it to contain %s", out, want)
		}
	}
}

// TestReadFailsOnWhatItCannotTrust: a file that exists but is garbage is an
// error — never "missing" (that would read as "CleverCoder isn't here").
func TestReadFailsOnWhatItCannotTrust(t *testing.T) {
	cases := map[string]string{
		"not json":                  "{esto no es json",
		"empty file":                "",
		"generated_at missing":      `{"schema_version":1,"clients":[]}`,
		"generated_at not a time":   `{"schema_version":1,"generated_at":"ayer","clients":[]}`,
		"clients with another type": `{"schema_version":1,"generated_at":"` + now.Format(time.RFC3339) + `","clients":"no"}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "clients.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			snap, err := Read(path, now)
			if err == nil {
				t.Errorf("Read = %+v, want an error", snap)
			}
		})
	}
}

// TestReadLetsGoOfTheFile is the "taken file" case from CleverCoder's side:
// it replaces clients.json every minute and SKIPS the write when the file is
// held. After Read, replacing it and deleting it must work — on Windows (where
// this runs) an open handle would make the rename fail.
func TestReadLetsGoOfTheFile(t *testing.T) {
	path := writeFile(t, fileJSON(1, now, clientJSON))
	if _, err := Read(path, now); err != nil {
		t.Fatal(err)
	}

	next := path + ".tmp"
	if err := os.WriteFile(next, []byte(fileJSON(1, now)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(next, path); err != nil {
		t.Fatalf("CleverCoder's replace failed after a Read: %v — the file is still held", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove after Read: %v — the file is still held", err)
	}
}

func TestPath(t *testing.T) {
	t.Run("the test variable takes the place of LOCALAPPDATA", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CLEVERCODER_APPDATA_DIR", dir)
		t.Setenv("LOCALAPPDATA", filepath.Join(dir, "no-es-esta"))

		want := filepath.Join(dir, "CleverCoderData", "clientes", "clients.json")
		if got := Path(); got != want {
			t.Errorf("Path() = %q, want %q", got, want)
		}
	})
	t.Run("without it, LOCALAPPDATA", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("CLEVERCODER_APPDATA_DIR", "")
		t.Setenv("LOCALAPPDATA", dir)

		want := filepath.Join(dir, "CleverCoderData", "clientes", "clients.json")
		if got := Path(); got != want {
			t.Errorf("Path() = %q, want %q", got, want)
		}
	})
	t.Run("with neither there is no place for the file", func(t *testing.T) {
		t.Setenv("CLEVERCODER_APPDATA_DIR", "")
		t.Setenv("LOCALAPPDATA", "")
		if got := Path(); got != "" {
			t.Errorf("Path() = %q, want \"\"", got)
		}
	})
}

// The real first-run case: CleverCoder never created the clientes folder (its
// module is off). Windows answers that with PATH_NOT_FOUND, not FILE_NOT_FOUND:
// it must be "missing" too, not an error.
func TestReadMissingInsideMissingDirectory(t *testing.T) {
	snap, err := Read(filepath.Join(t.TempDir(), "no", "existe", "clients.json"), now)
	if err != nil || snap.Status != StatusMissing {
		t.Errorf("Read = %+v, %v, want missing", snap, err)
	}
}
