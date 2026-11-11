package autoupdate

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Everything here is the Linux/macOS side that does not call the OS, so it is
// compiled — and tested — on every platform. The calls that do (systemd-run,
// Setsid) are in install_unix.go.

// packageFiles are the only entries a Piumy package may carry (build-all.sh,
// T113), each with the most bytes it may have. Anything else is refused.
var packageFiles = map[string]int64{
	"install.sh":    1 << 20,
	"eula.txt":      1 << 20,
	"piumy-gateway": maxPackage,
}

// extractPackage unpacks a verified Piumy tar.gz flat into dest and returns the
// path of its install.sh. The archive was already checked by hash and
// signature; this is the second wall: a hostile or odd archive (a path out of
// the folder, a link, an extra file) never gets written, not even by accident.
func extractPackage(tarGz, dest string) (installScript string, err error) {
	top := strings.TrimSuffix(filepath.Base(tarGz), ".tar.gz") // Piumy-<v>-<os>-<arch>
	f, err := os.Open(tarGz)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return "", err
	}

	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := path.Clean(h.Name) // tar names always use forward slashes
		if name == top && h.Typeflag == tar.TypeDir {
			continue
		}
		file := strings.TrimPrefix(name, top+"/")
		limit, allowed := packageFiles[file]
		if h.Typeflag != tar.TypeReg || file == name || !allowed || h.Size > limit {
			return "", fmt.Errorf("el paquete trae algo inesperado: %q", h.Name)
		}
		if seen[file] {
			return "", fmt.Errorf("el paquete trae %q dos veces", file)
		}
		seen[file] = true
		if err := writeFile(filepath.Join(dest, file), tr, h.Size); err != nil {
			return "", err
		}
	}
	for _, need := range []string{"install.sh", "piumy-gateway"} {
		if !seen[need] {
			return "", fmt.Errorf("al paquete le falta %s", need)
		}
	}
	return filepath.Join(dest, "install.sh"), nil
}

func writeFile(path string, r io.Reader, size int64) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, io.LimitReader(r, size)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// shellArgs runs install.sh with no terminal on stdin (its prompts check for
// one: with none it accepts the EULA and, on an existing install, asks for
// nothing) and everything it prints in log.
func shellArgs(script, log string) []string {
	return []string{"-c", `exec /bin/sh "$1" >"$2" 2>&1 </dev/null`, "piumy-update", script, log}
}

// systemdRunArgs puts that command in its own transient systemd unit. It has to
// be: install.sh begins with `systemctl --user stop piumy`, which kills the
// service's whole cgroup — a child of Piumy would die mid-update and, because
// Restart=on-failure does not restart a stop, leave Piumy down. setsid is NOT
// enough on Linux: systemd kills by cgroup, not by session.
func systemdRunArgs(unit, script, log string) []string {
	return append([]string{"--user", "--collect", "--quiet", "--unit=" + unit, "--", "/bin/sh"}, shellArgs(script, log)...)
}

// posixInstalled is "" when this process is the installed Piumy service: the
// binary sits where install.sh puts it, and the service manager started it. A
// Piumy launched by hand — or a development build — is not replaced behind
// its owner's back (install.sh itself says it cannot see those).
func posixInstalled(exe, dataDir, goos string, getenv func(string) string) string {
	if !samePath(exe, filepath.Join(dataDir, "piumy-gateway")) {
		return BlockedNotInstalled
	}
	switch goos {
	case "linux":
		if getenv("INVOCATION_ID") != "" { // systemd sets it for every unit's processes
			return ""
		}
	case "darwin":
		if getenv("XPC_SERVICE_NAME") == "app.piumy.gateway" { // launchd's label, install.sh's LABEL
			return ""
		}
	}
	return BlockedNotInstalled
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
