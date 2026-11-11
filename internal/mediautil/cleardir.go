package mediautil

import (
	"os"
	"path/filepath"
)

// ClearDir removes every entry INSIDE dir, leaving dir itself in
// place — os.RemoveAll(dir) would also delete the directory, which a
// concurrent writer (the media-download worker) could then fail to
// recreate mid-write. A missing dir is not an error (nothing to clear).
func ClearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}
