package mediautil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestClearDirEmptiesTheDirButKeepsIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "huerfano.jpg"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sub", "hondo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ClearDir(dir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the directory itself must survive: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("entries after ClearDir = %v, want none", entries)
	}
}

func TestClearDirMissingDirIsNoOp(t *testing.T) {
	if err := ClearDir(filepath.Join(t.TempDir(), "never-existed")); err != nil {
		t.Errorf("ClearDir(missing dir) = %v, want nil", err)
	}
}
