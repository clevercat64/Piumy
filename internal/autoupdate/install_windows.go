//go:build windows

package autoupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	createNoWindow        = 0x08000000
	createNewProcessGroup = 0x00000200
)

// restartGraceSeconds is how long the wrapper gives the installer's own
// relaunch before it checks whether Piumy came back. A variable only so a test
// does not have to wait it out.
var restartGraceSeconds = 20

// platformInstalled is "" when Piumy.exe sits next to the Inno uninstaller —
// the mark of a copy the installer put there. A development build (go run,
// dist\) has none, and must never download a Setup and run it over the owner's
// real installation.
func platformInstalled(dataDir string) string {
	exe, err := os.Executable()
	if err != nil {
		return BlockedNotInstalled
	}
	return installedAt(exe)
}

func installedAt(exe string) string {
	if _, err := os.Stat(filepath.Join(filepath.Dir(exe), "unins000.exe")); err != nil {
		return BlockedNotInstalled
	}
	return ""
}

// platformOnlyInstance is "" when this is the only Piumy.exe running. The
// Setup closes EVERY Piumy.exe by image name and relaunches just the default
// account: updating with another one alive would shut it down for good. If the
// process list can't be read, it is not safe to assume nobody else is there.
func platformOnlyInstance() string {
	exe, err := os.Executable()
	if err != nil {
		return BlockedOtherInstance
	}
	if n, err := countImage(filepath.Base(exe)); err != nil || n != 1 {
		return BlockedOtherInstance
	}
	return ""
}

// countImage is how many running processes have this image name.
func countImage(image string) (int, error) {
	snap, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer syscall.CloseHandle(snap)
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	n := 0
	for err = syscall.Process32First(snap, &entry); err == nil; err = syscall.Process32Next(snap, &entry) {
		if strings.EqualFold(syscall.UTF16ToString(entry.ExeFile[:]), image) {
			n++
		}
	}
	if err != syscall.ERROR_NO_MORE_FILES {
		return 0, err
	}
	return n, nil
}

// platformLaunch writes update-<v>.cmd next to the log and starts it detached.
// Piumy does NOT relaunch itself: the Setup already stops the old one (and
// checks it is gone) before it copies, and starts the new one at the end, in
// silent mode too. Relaunching from here earlier would be worse than useless:
// T59's mutex makes a second instance exit quietly, so if it started before
// the old one died, neither would be left. The wrapper only starts Piumy when,
// after the installer ends, nothing is running — a Setup that fails after it
// killed the old one must not leave the gateway down.
//
// CREATE_NO_WINDOW | NEW_PROCESS_GROUP, and no breakaway: measured in T176, a
// child survives its parent dying, and CleverCoder's terminal job rejects
// CREATE_BREAKAWAY_FROM_JOB outright.
func platformLaunch(p Plan) error {
	if err := os.MkdirAll(filepath.Dir(p.Log), 0o700); err != nil {
		return err
	}
	wrapper := strings.TrimSuffix(p.Log, ".log") + ".cmd"
	if err := os.WriteFile(wrapper, []byte(cmdScript(p.Package, p.Log, p.Exe, restartGraceSeconds)), 0o600); err != nil {
		return err
	}
	cmd := exec.Command(wrapper)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup, HideWindow: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("no se pudo lanzar %s: %w", wrapper, err)
	}
	return cmd.Process.Release()
}

// cmdScript is the wrapper. A "%" in a path would be expanded by cmd, so it is
// doubled; quotes cannot occur in a Windows path. The system tools are called
// by full path: measured in T176, a PATH that starts with Git for Windows'
// usr\bin (anyone who started Piumy from Git Bash) resolves "find" to GNU find,
// which would make "is Piumy running?" always answer no.
func cmdScript(setup, log, exe string, graceSeconds int) string {
	const sys = `%SystemRoot%\System32\`
	image := filepath.Base(exe)
	lines := []string{
		"@echo off",
		"rem Generado por Piumy (T176): corre el instalador y se asegura de que Piumy quede corriendo.",
		fmt.Sprintf(`start "" /wait %s /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /LOG=%s`, q(setup), q(log)),
		fmt.Sprintf("%sping.exe -n %d 127.0.0.1 >nul", sys, graceSeconds+1),
		fmt.Sprintf(`%stasklist.exe /FI "IMAGENAME eq %s" /NH 2>nul | %sfind.exe /I "%s" >nul`, sys, image, sys, image),
		fmt.Sprintf(`if errorlevel 1 start "" /D %s %s`, q(filepath.Dir(exe)), q(exe)),
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

func q(path string) string { return `"` + strings.ReplaceAll(path, "%", "%%") + `"` }
