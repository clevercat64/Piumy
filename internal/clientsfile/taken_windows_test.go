package clientsfile

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

// TestReadWhileCleverCoderHoldsTheFile is the other half of "taken": the file
// is open WITHOUT sharing (what a writer that holds it looks like). Read must
// fail promptly with a real error — not hang, and above all not come out as
// "missing", which would tell the boss CleverCoder isn't there — and work again
// once it is free. Windows-only: other systems don't lock like this.
func TestReadWhileCleverCoderHoldsTheFile(t *testing.T) {
	path := writeFile(t, fileJSON(1, now, clientJSON))
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("setup: can't take the file: %v", err)
	}

	snap, readErr := Read(path, now)
	if readErr == nil {
		syscall.CloseHandle(handle)
		t.Fatalf("Read of a taken file = %+v, want an error", snap)
	}
	if errors.Is(readErr, fs.ErrNotExist) {
		t.Errorf("Read error = %v: a taken file must not look like a missing one", readErr)
	}

	if err := syscall.CloseHandle(handle); err != nil {
		t.Fatal(err)
	}
	if snap, err := Read(path, now); err != nil || snap.Status != StatusOK {
		t.Errorf("Read once the file is free = %+v, %v, want ok", snap, err)
	}
}
