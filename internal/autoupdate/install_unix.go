//go:build !windows

package autoupdate

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
)

func platformInstalled(dataDir string) string {
	exe, err := os.Executable()
	if err != nil {
		return BlockedNotInstalled
	}
	return posixInstalled(exe, dataDir, runtime.GOOS, os.Getenv)
}

// install.sh is a single service: there is no second account to protect.
func platformOnlyInstance() string { return "" }

// platformLaunch unpacks the verified tar.gz and runs its install.sh OUTSIDE
// the service that is about to be stopped (see systemdRunArgs).
func platformLaunch(p Plan) error {
	script, err := extractPackage(p.Package, filepath.Join(p.Dir, "pkg"))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p.Log), 0o700); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		out, err := exec.Command("systemd-run", systemdRunArgs("piumy-update-"+p.Version, script, p.Log)...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("systemd-run: %v: %s", err, out)
		}
		return nil
	}
	// macOS: launchd kills the job's process group on bootout, so the installer
	// gets its own session (and with it its own group).
	cmd := exec.Command("/bin/sh", shellArgs(script, p.Log)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
