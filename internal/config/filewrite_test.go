package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSetFileValueRoundTripKeepsTheOtherKeys(t *testing.T) {
	dir := t.TempDir()
	if err := SetFileValue(dir, "PIUMY_MCP_KEY", "una-clave"); err != nil {
		t.Fatal(err)
	}
	if err := SetFileValue(dir, "PIUMY_DEFAULT_TERMINAL_ID", "term-1"); err != nil {
		t.Fatal(err)
	}
	if got := FileValue(dir, "PIUMY_DEFAULT_TERMINAL_ID"); got != "term-1" {
		t.Errorf("FileValue = %q, want term-1", got)
	}
	if got := FileValue(dir, "PIUMY_MCP_KEY"); got != "una-clave" {
		t.Errorf("the other key was lost: %q", got)
	}
}

func TestSetFileValueEmptyRemovesTheKey(t *testing.T) {
	dir := t.TempDir()
	SetFileValue(dir, "PIUMY_DEFAULT_TERMINAL_ID", "term-1")
	SetFileValue(dir, "PIUMY_MCP_KEY", "k")
	if err := SetFileValue(dir, "PIUMY_DEFAULT_TERMINAL_ID", ""); err != nil {
		t.Fatal(err)
	}
	if got := FileValue(dir, "PIUMY_DEFAULT_TERMINAL_ID"); got != "" {
		t.Errorf("key still there: %q", got)
	}
	if got := FileValue(dir, "PIUMY_MCP_KEY"); got != "k" {
		t.Errorf("removing one key took the other: %q", got)
	}
}

func TestFileValueOfAMissingFileIsEmpty(t *testing.T) {
	if got := FileValue(t.TempDir(), "PIUMY_DEFAULT_TERMINAL_ID"); got != "" {
		t.Errorf("FileValue of no file = %q, want empty", got)
	}
}

// What SetFileValue writes is what ApplyFileDefaultsIn reads back at the next
// startup — the whole point of the file.
func TestSetFileValueIsWhatTheNextStartupReads(t *testing.T) {
	dir := t.TempDir()
	if err := SetFileValue(dir, "PIUMY_DEFAULT_TERMINAL_ID", "term-arranque"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIUMY_DEFAULT_TERMINAL_ID", "")
	os.Unsetenv("PIUMY_DEFAULT_TERMINAL_ID")
	if err := ApplyFileDefaultsIn(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Unsetenv("PIUMY_DEFAULT_TERMINAL_ID") })
	if got := os.Getenv("PIUMY_DEFAULT_TERMINAL_ID"); got != "term-arranque" {
		t.Errorf("env after startup = %q, want term-arranque", got)
	}
}

func TestSetFileValueUnwritableDirFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "es-un-archivo")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SetFileValue(filepath.Join(blocker, "adentro"), "K", "v"); err == nil {
		t.Error("writing under a path that is a file must fail, not pretend")
	}
}
