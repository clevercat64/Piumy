package config

import (
	"os"
	"path/filepath"
)

// FileDir is where piumy-config.json lives: next to the running binary, the
// same place ApplyFileDefaults reads it from. "" if the binary's own path
// can't be resolved.
func FileDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exe)
}

// FileValue is key's value in piumy-config.json under dir — "" when the file,
// or the key, isn't there (or the file can't be read: the caller only ever
// asks "is something set").
func FileValue(dir, key string) string {
	vals, _ := loadConfigFile(filepath.Join(dir, configFileName))
	return vals[key]
}

// SetFileValue sets key in piumy-config.json under dir, or removes it when
// value is "", leaving every other key as it was. The gateway reads that file
// once, at startup (ApplyFileDefaults), so a change here applies on the next
// restart — and never while a real environment variable of the same name is
// set, which always wins.
func SetFileValue(dir, key, value string) error {
	path := filepath.Join(dir, configFileName)
	vals, err := loadConfigFile(path)
	if err != nil {
		return err
	}
	if vals == nil {
		vals = map[string]string{}
	}
	if value == "" {
		delete(vals, key)
	} else {
		vals[key] = value
	}
	return saveConfigFile(path, vals)
}
