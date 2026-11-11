//go:build windows

package autoupdate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCmdScriptQuotesEveryPathAndRestartsOnlyIfNothingRuns(t *testing.T) {
	got := cmdScript(`C:\a b\Piumy-Setup-9.9.9.exe`, `C:\a b\logs\update.log`, `C:\a b\Piumy.exe`, 20)

	want := []string{
		`start "" /wait "C:\a b\Piumy-Setup-9.9.9.exe" /VERYSILENT /SUPPRESSMSGBOXES /NORESTART /SP- /LOG="C:\a b\logs\update.log"`,
		`%SystemRoot%\System32\ping.exe -n 21 127.0.0.1 >nul`,
		`%SystemRoot%\System32\tasklist.exe /FI "IMAGENAME eq Piumy.exe" /NH 2>nul | %SystemRoot%\System32\find.exe /I "Piumy.exe" >nul`,
		`if errorlevel 1 start "" /D "C:\a b" "C:\a b\Piumy.exe"`,
	}
	for _, line := range want {
		if !strings.Contains(got, line+"\r\n") {
			t.Errorf("script lacks the line %q:\n%s", line, got)
		}
	}
	if !strings.HasPrefix(got, "@echo off\r\n") || strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Errorf("script must use CRLF throughout and start quiet:\n%q", got)
	}
}

// cmd expands %NAME% inside a batch file, so a "%" in a path has to be doubled.
func TestCmdScriptDoublesPercentSigns(t *testing.T) {
	got := cmdScript(`C:\100%\setup.exe`, `C:\100%\u.log`, `C:\100%\Piumy.exe`, 1)
	if strings.Contains(strings.ReplaceAll(got, "%%", ""), `100%\`) {
		t.Errorf("a single %% reached the script, cmd would expand it:\n%s", got)
	}
}

func TestInstalledNeedsTheUninstallerNextToTheExecutable(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "Piumy.exe")

	if got := installedAt(exe); got != BlockedNotInstalled {
		t.Errorf("a dir without unins000.exe = %q, want not installed (a development build)", got)
	}
	if err := os.WriteFile(filepath.Join(dir, "unins000.exe"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := installedAt(exe); got != "" {
		t.Errorf("a dir with unins000.exe = %q, want installed", got)
	}
}

// The control is the test binary itself: it is running, so the instrument must
// see exactly one of it — a count that said 0 or 2 would make the gate lie.
func TestCountImageSeesThisProcessAndNothingElse(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := countImage(filepath.Base(self)); err != nil || n != 1 {
		t.Errorf("countImage(%s) = %d, %v, want exactly 1", filepath.Base(self), n, err)
	}
	if n, err := countImage("no-existe-piumy-xyz.exe"); err != nil || n != 0 {
		t.Errorf("countImage(an image nobody runs) = %d, %v, want 0", n, err)
	}
	if got := platformOnlyInstance(); got != "" {
		t.Errorf("platformOnlyInstance = %q for the only copy of this test binary, want it free", got)
	}
}

// fakeProgram stands in for BOTH the Setup and Piumy (it is a windowsgui
// program, so nothing flashes on the owner's screen): started with
// /VERYSILENT it is the Setup; otherwise it is Piumy and stays alive. What it
// does is read from fake.cfg next to it, and it leaves markers in that folder.
const fakeProgram = `package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func main() {
	self, _ := os.Executable()
	dir := filepath.Dir(self)
	cfg := map[string]string{}
	raw, _ := os.ReadFile(filepath.Join(dir, "fake.cfg"))
	for _, line := range strings.Split(string(raw), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			cfg[k] = v
		}
	}
	if len(os.Args) > 1 && os.Args[1] == "/VERYSILENT" {
		os.WriteFile(filepath.Join(dir, "setup-ran"), []byte(strings.Join(os.Args[1:], " ")), 0o600)
		if p := cfg["starts"]; p != "" {
			c := exec.Command(p)
			c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000 | 0x200}
			c.Start()
		}
		code, _ := strconv.Atoi(cfg["exit"])
		os.Exit(code)
	}
	os.WriteFile(filepath.Join(dir, "piumy-started-"+strconv.Itoa(os.Getpid())), nil, 0o600)
	time.Sleep(60 * time.Second)
}
`

func buildFakeProgram(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(fakeProgram), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "go.mod"), []byte("module fake\n\ngo 1.26\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(src, "fake.exe")
	cmd := exec.Command("go", "build", "-ldflags=-H=windowsgui", "-o", out, ".")
	cmd.Dir = src
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fake program: %v\n%s", err, b)
	}
	return out
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o700); err != nil {
		t.Fatal(err)
	}
}

func countMarkers(dir string) int {
	m, _ := filepath.Glob(filepath.Join(dir, "piumy-started-*"))
	return len(m)
}

// waitFor is a positive wait only: it returns as soon as cond holds, and the
// long limit is a guard against a hang, never a deadline the test depends on.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestPlatformLaunchRunsTheSetupAndRestartsPiumyOnlyIfItIsDown runs the REAL
// wrapper — detached, in a path with spaces — against fake programs. Decision 2
// of T176: a Setup that fails after killing the old Piumy must not leave the
// gateway down; and when the Setup's own relaunch worked, Piumy is not started
// twice.
func TestPlatformLaunchRunsTheSetupAndRestartsPiumyOnlyIfItIsDown(t *testing.T) {
	fake := buildFakeProgram(t)
	oldGrace := restartGraceSeconds
	restartGraceSeconds = 2
	t.Cleanup(func() { restartGraceSeconds = oldGrace })

	// Image names stay under 25 characters: tasklist truncates longer ones, and
	// the wrapper looks for the name in its output.
	launch := func(t *testing.T, label, cfgText string) (dir, image string) {
		dir = filepath.Join(t.TempDir(), "mi carpeta")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		image = "piumyfake-" + label + strconv.Itoa(os.Getpid()%1000000) + ".exe"
		setup, piumy := filepath.Join(dir, "fake setup.exe"), filepath.Join(dir, image)
		copyFile(t, fake, setup)
		copyFile(t, fake, piumy)
		cfgText = strings.ReplaceAll(cfgText, "PIUMY", piumy)
		if err := os.WriteFile(filepath.Join(dir, "fake.cfg"), []byte(cfgText), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { exec.Command("taskkill", "/F", "/IM", image).Run() }) // only this test's own image

		plan := Plan{Version: "9.9.9", Package: setup, Dir: dir, Log: filepath.Join(dir, "logs", "update-9.9.9.log"), Exe: piumy}
		if err := platformLaunch(plan); err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the Setup to run", func() bool { _, err := os.Stat(filepath.Join(dir, "setup-ran")); return err == nil })
		args, _ := os.ReadFile(filepath.Join(dir, "setup-ran"))
		for _, want := range []string{"/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/LOG=" + plan.Log} {
			if !strings.Contains(string(args), want) {
				t.Errorf("the Setup was run with %q, want it to contain %q", args, want)
			}
		}
		return dir, image
	}

	t.Run("the Setup fails and nothing is running: Piumy is started again", func(t *testing.T) {
		t.Parallel()
		dir, _ := launch(t, "a", "exit=1\n")

		waitFor(t, "Piumy to be restarted", func() bool { return countMarkers(dir) >= 1 })
		time.Sleep(3 * time.Second)

		if n := countMarkers(dir); n != 1 {
			t.Errorf("Piumy started %d times, want exactly once", n)
		}
	})

	t.Run("the Setup relaunched Piumy itself: the wrapper starts nothing", func(t *testing.T) {
		t.Parallel()
		dir, _ := launch(t, "b", "exit=0\nstarts=PIUMY\n")

		waitFor(t, "the Setup's own relaunch", func() bool { return countMarkers(dir) >= 1 })
		// The wrapper looks again after its 2 s grace: a wrongful second start
		// would show up well inside this wait.
		time.Sleep(10 * time.Second)

		if n := countMarkers(dir); n != 1 {
			t.Errorf("Piumy started %d times, want 1: it was already running", n)
		}
	})
}
