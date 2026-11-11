package autoupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type tarEntry struct {
	name     string
	typ      byte
	body     string
	linkname string
}

const top = "Piumy-9.9.9-linux-amd64"

func file(name, body string) tarEntry { return tarEntry{name: name, typ: tar.TypeReg, body: body} }
func dir(name string) tarEntry        { return tarEntry{name: name, typ: tar.TypeDir} }

// goodPackage is what build-all.sh makes (T113): the folder, then the three files.
func goodPackage() []tarEntry {
	return []tarEntry{
		dir(top + "/"),
		file(top+"/eula.txt", "condiciones"),
		file(top+"/install.sh", "#!/bin/sh\necho ok\n"),
		file(top+"/piumy-gateway", "ELF pretend"),
	}
}

// makeTar writes entries as <top>.tar.gz in a fresh directory and returns its
// path — the name matters, it is what the folder inside must be called.
func makeTar(t *testing.T, entries []tarEntry) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		h := &tar.Header{Name: e.name, Typeflag: e.typ, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.linkname}
		if e.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if e.typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), top+".tar.gz")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractPackageUnpacksTheThreeFilesFlat(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "pkg")

	script, err := extractPackage(makeTar(t, goodPackage()), dest)

	if err != nil {
		t.Fatal(err)
	}
	if script != filepath.Join(dest, "install.sh") {
		t.Errorf("script = %q, want dest/install.sh", script)
	}
	for name, want := range map[string]string{"install.sh": "#!/bin/sh\necho ok\n", "piumy-gateway": "ELF pretend", "eula.txt": "condiciones"} {
		if got, _ := os.ReadFile(filepath.Join(dest, name)); string(got) != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

func TestExtractPackageDoesNotNeedTheEULA(t *testing.T) {
	pkg := goodPackage()
	pkg = append(pkg[:1], pkg[2:]...) // no eula.txt
	if _, err := extractPackage(makeTar(t, pkg), filepath.Join(t.TempDir(), "pkg")); err != nil {
		t.Errorf("a package without eula.txt was refused: %v", err)
	}
}

// A hostile or odd archive is refused before anything lands outside dest.
func TestExtractPackageRefusesWhatIsNotAPiumyPackage(t *testing.T) {
	bigScript := strings.Repeat("x", 1<<20+1)
	cases := map[string][]tarEntry{
		"a path out of the folder":     {dir(top + "/"), file(top+"/../evil", "x"), file(top+"/install.sh", "x"), file(top+"/piumy-gateway", "x")},
		"an absolute path":             {dir(top + "/"), file("/tmp/evil", "x"), file(top+"/install.sh", "x"), file(top+"/piumy-gateway", "x")},
		"another top folder":           {dir("Otra-1.0/"), file("Otra-1.0/install.sh", "x"), file("Otra-1.0/piumy-gateway", "x")},
		"an extra file":                append(goodPackage(), file(top+"/extra.sh", "x")),
		"a nested file":                append(goodPackage(), file(top+"/sub/install.sh", "x")),
		"another directory":            append(goodPackage(), dir(top+"/sub/")),
		"a symlink":                    {dir(top + "/"), {name: top + "/install.sh", typ: tar.TypeSymlink, linkname: "/etc/passwd"}, file(top+"/piumy-gateway", "x")},
		"a hard link":                  {dir(top + "/"), {name: top + "/install.sh", typ: tar.TypeLink, linkname: "x"}, file(top+"/piumy-gateway", "x")},
		"a file named like the folder": {file(top, "x")},
		"the same file twice":          append(goodPackage(), file(top+"/install.sh", "otra")),
		"no install.sh":                {dir(top + "/"), file(top+"/piumy-gateway", "x")},
		"no binary":                    {dir(top + "/"), file(top+"/install.sh", "x")},
		"an oversized install.sh":      {dir(top + "/"), file(top+"/install.sh", bigScript), file(top+"/piumy-gateway", "x")},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			pkg := filepath.Join(base, top+".tar.gz")
			if err := os.WriteFile(pkg, mustTar(t, entries), 0o600); err != nil {
				t.Fatal(err)
			}
			dest := filepath.Join(base, "pkg")

			if _, err := extractPackage(pkg, dest); err == nil {
				t.Error("accepted, want a refusal")
			}
			if _, err := os.Stat(filepath.Join(base, "evil")); err == nil {
				t.Error("wrote a file outside the destination")
			}
		})
	}
}

// mustTar is makeTar's bytes without its own directory, to put them where the
// test wants them.
func mustTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	data, err := os.ReadFile(makeTar(t, entries))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExtractPackageRefusesNotGzip(t *testing.T) {
	path := filepath.Join(t.TempDir(), top+".tar.gz")
	if err := os.WriteFile(path, []byte("no soy un tar.gz"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractPackage(path, filepath.Join(t.TempDir(), "pkg")); err == nil {
		t.Error("accepted a file that is not a tar.gz")
	}
}

func TestSystemdRunPutsTheInstallerInItsOwnUnit(t *testing.T) {
	args := systemdRunArgs("piumy-update-9.9.9", "/x/pkg/install.sh", "/x/logs/update.log")

	got := strings.Join(args, "\x00")
	for _, want := range []string{"--user", "--collect", "--unit=piumy-update-9.9.9", "--", "/bin/sh", "-c", "/x/pkg/install.sh", "/x/logs/update.log"} {
		if !strings.Contains(got, want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	if args[len(args)-1] != "/x/logs/update.log" || args[len(args)-2] != "/x/pkg/install.sh" {
		t.Errorf("script and log must be positional arguments, not spliced into the shell text: %q", args)
	}
}

// What the shell wrapper actually does, run for real where /bin/sh exists (the
// Linux run in WSL): install.sh gets NO terminal and its output lands in the log.
func TestShellWrapperGivesInstallShNoTerminalAndLogsItsOutput(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh here")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "install.sh")
	logPath := filepath.Join(dir, "my log.log") // a space: it must survive as ONE argument
	body := "echo hola; if [ -t 0 ]; then echo TTY; else echo NOTTY; fi; echo error 1>&2\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}

	if out, err := exec.Command("/bin/sh", shellArgs(script, logPath)...).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	got, _ := os.ReadFile(logPath)
	for _, want := range []string{"hola", "NOTTY", "error"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("log = %q, want it to contain %q", got, want)
		}
	}
}

func TestPosixInstalledNeedsTheServiceAndTheInstalledPath(t *testing.T) {
	data := t.TempDir()
	exe := filepath.Join(data, "piumy-gateway")
	if err := os.WriteFile(exe, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "piumy-gateway")
	if err := os.WriteFile(elsewhere, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}

	cases := []struct {
		name string
		exe  string
		goos string
		env  func(string) string
		want string
	}{
		{"linux service", exe, "linux", env("INVOCATION_ID", "abc"), ""},
		{"linux started by hand (no systemd)", exe, "linux", env(), BlockedNotInstalled},
		{"linux, binary elsewhere (a dev build)", elsewhere, "linux", env("INVOCATION_ID", "abc"), BlockedNotInstalled},
		{"mac service", exe, "darwin", env("XPC_SERVICE_NAME", "app.piumy.gateway"), ""},
		{"mac, another launchd job", exe, "darwin", env("XPC_SERVICE_NAME", "com.otro"), BlockedNotInstalled},
		{"mac started by hand", exe, "darwin", env("XPC_SERVICE_NAME", "0"), BlockedNotInstalled},
		{"an OS install.sh does not cover", exe, "freebsd", env("INVOCATION_ID", "abc"), BlockedNotInstalled},
		{"the binary does not exist", filepath.Join(data, "nada"), "linux", env("INVOCATION_ID", "abc"), BlockedNotInstalled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := posixInstalled(c.exe, data, c.goos, c.env); got != c.want {
				t.Errorf("posixInstalled = %q, want %q", got, c.want)
			}
		})
	}
}
