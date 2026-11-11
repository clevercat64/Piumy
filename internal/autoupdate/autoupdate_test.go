package autoupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/releasesig"
)

// memKV is the KV the updater keeps its state in, in memory.
type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func (k *memKV) KVGet(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.m[key], nil
}

func (k *memKV) KVSet(key, val string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.m == nil {
		k.m = map[string]string{}
	}
	k.m[key] = val
	return nil
}

// fixture is a fake GitHub: the releases/latest answer and the download
// endpoint, serving a VALID signed release unless a test breaks one piece.
// Requests are recorded: what Piumy asks for is part of what it guarantees.
type fixture struct {
	t          *testing.T
	priv       ed25519.PrivateKey
	version    string
	name       string            // the package this platform installs
	assets     []string          // the names the API lists
	served     map[string][]byte // what the download endpoint serves, by file name
	apiDigest  string            // what the API says the package's digest is
	apiSize    int64             // what the API says its size is (-1 = the real one)
	draft      bool              // the API marks the release as a draft
	prerelease bool              // ... or as a pre-release
	srv        *httptest.Server

	mu    sync.Mutex
	paths []string
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func newFixture(t *testing.T, version string) *fixture {
	t.Helper()
	f := &fixture{t: t, version: version, apiSize: -1}
	f.priv = ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, ed25519.SeedSize))
	f.name = "Piumy-Setup-" + version + ".exe"
	pkg := []byte("MZ pretend installer for " + version)
	sums := releasesig.FormatSums(map[string]string{f.name: sha256Hex(pkg)})
	f.served = map[string][]byte{f.name: pkg, "SHA256SUMS": sums, "SHA256SUMS.sig": releasesig.Sign(f.priv, sums)}
	f.assets = []string{f.name, "SHA256SUMS", "SHA256SUMS.sig"}
	f.apiDigest = "sha256:" + sha256Hex(pkg)

	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.Path)
	f.mu.Unlock()

	if r.URL.Path == "/repos/clevercat64/Piumy/releases/latest" {
		type asset struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			Digest string `json:"digest,omitempty"`
		}
		rel := struct {
			TagName    string  `json:"tag_name"`
			Draft      bool    `json:"draft"`
			Prerelease bool    `json:"prerelease"`
			Assets     []asset `json:"assets"`
		}{TagName: "v" + f.version, Draft: f.draft, Prerelease: f.prerelease}
		for _, name := range f.assets {
			a := asset{Name: name, Size: int64(len(f.served[name]))}
			if name == f.name {
				a.Digest = f.apiDigest
				if f.apiSize >= 0 {
					a.Size = f.apiSize
				}
			}
			rel.Assets = append(rel.Assets, a)
		}
		json.NewEncoder(w).Encode(rel)
		return
	}
	prefix := "/clevercat64/Piumy/releases/download/v" + f.version + "/"
	if name, ok := strings.CutPrefix(r.URL.Path, prefix); ok {
		if data, found := f.served[name]; found {
			w.Write(data)
			return
		}
	}
	http.NotFound(w, r)
}

func (f *fixture) requested() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.paths...)
}

func (f *fixture) downloadedPackage() bool {
	for _, p := range f.requested() {
		if strings.HasSuffix(p, "/"+f.name) {
			return true
		}
	}
	return false
}

// resign replaces SHA256SUMS and its signature, with whichever key and content.
func (f *fixture) resign(key ed25519.PrivateKey, sums []byte) {
	f.served["SHA256SUMS"] = sums
	f.served["SHA256SUMS.sig"] = releasesig.Sign(key, sums)
}

// harness is the machine the updater runs on: the gates a test flips, and what
// the updater did to it.
type harness struct {
	kv            *memKV
	enabled       bool
	idle          string
	notInstalled  string
	otherInstance string
	launchErr     error
	launched      []Plan
	stops         int
	now           time.Time
	dataDir       string
}

func (f *fixture) updater(current string) (*Updater, *harness) {
	f.t.Helper()
	return f.updaterWith(current, nil)
}

// updaterWith lets a test change the Config main.go would hand over.
func (f *fixture) updaterWith(current string, tweak func(*Config)) (*Updater, *harness) {
	f.t.Helper()
	h := &harness{kv: &memKV{}, enabled: true, now: time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC), dataDir: f.t.TempDir()}
	cfg := Config{
		Enabled:        func() bool { return h.enabled },
		CurrentVersion: current,
		DataDir:        h.dataDir,
		KV:             h.kv,
		Idle:           func() string { return h.idle },
		Stop:           func() { h.stops++ },
	}
	if tweak != nil {
		tweak(&cfg)
	}
	u := New(cfg)
	u.pub = f.priv.Public().(ed25519.PublicKey)
	u.apiBase, u.downloadBase = f.srv.URL, f.srv.URL
	u.http = f.srv.Client()
	u.goos, u.goarch = "windows", "amd64"
	u.exe = filepath.Join(h.dataDir, "Piumy.exe")
	u.launch = func(p Plan) error { h.launched = append(h.launched, p); return h.launchErr }
	u.installed = func() string { return h.notInstalled }
	u.onlyInstance = func() string { return h.otherInstance }
	u.now = func() time.Time { return h.now }
	u.min, u.max, u.retry = time.Hour, time.Hour, 5*time.Minute
	return u, h
}

// TestANewSignedReleaseIsVerifiedDownloadedAndInstalled is the whole chain, and
// what Piumy asks GitHub for: only the pinned repo, only the exact files.
func TestANewSignedReleaseIsVerifiedDownloadedAndInstalled(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")

	wait := u.tick(context.Background())

	if len(h.launched) != 1 {
		t.Fatalf("installer launched %d times, want 1 (state %+v)", len(h.launched), u.load())
	}
	plan := h.launched[0]
	if plan.Version != "9.9.9" || filepath.Base(plan.Package) != f.name {
		t.Errorf("plan = %+v, want version 9.9.9 and the package %s", plan, f.name)
	}
	if got, _ := os.ReadFile(plan.Package); !bytes.Equal(got, f.served[f.name]) {
		t.Error("the file handed to the installer is not the verified package")
	}
	if want := filepath.Join(h.dataDir, "logs", "update-9.9.9.log"); plan.Log != want {
		t.Errorf("log = %q, want %q", plan.Log, want)
	}
	if h.stops != 1 {
		t.Errorf("Stop called %d times, want exactly once right after the launch", h.stops)
	}
	if u.load().Pending != "9.9.9" {
		t.Errorf("pending = %q, want it set so the NEW process can tell it took", u.load().Pending)
	}
	if wait != time.Hour {
		t.Errorf("next look in %s, want the normal window", wait)
	}
	wantPaths := []string{
		"/repos/clevercat64/Piumy/releases/latest",
		"/clevercat64/Piumy/releases/download/v9.9.9/SHA256SUMS",
		"/clevercat64/Piumy/releases/download/v9.9.9/SHA256SUMS.sig",
		"/clevercat64/Piumy/releases/download/v9.9.9/" + f.name,
	}
	if got := f.requested(); strings.Join(got, "\n") != strings.Join(wantPaths, "\n") {
		t.Errorf("requests = %v, want exactly %v", got, wantPaths)
	}
}

func TestNothingIsDownloadedForAReleaseThatIsNotNewer(t *testing.T) {
	for _, version := range []string{"0.12.5", "0.12.4", "0.11.99"} {
		t.Run("latest is "+version, func(t *testing.T) {
			f := newFixture(t, version)
			u, h := f.updater("0.12.5")

			u.tick(context.Background())

			if len(h.launched) != 0 || f.downloadedPackage() || len(f.requested()) != 1 {
				t.Errorf("launched %d, requests %v: an equal or older release must cost one API call and nothing else", len(h.launched), f.requested())
			}
			if st := u.Status(); st.Latest != version || st.Available {
				t.Errorf("status = %+v, want latest %s and not available", st, version)
			}
		})
	}
}

// releases/latest already leaves drafts and pre-releases out; this is the
// second wall, in case that ever changes — and a tag that is not x.y.z.
func TestADraftAPrereleaseOrAnOddTagIsNeverAnUpdate(t *testing.T) {
	cases := map[string]func(f *fixture){
		"a draft":       func(f *fixture) { f.draft = true },
		"a pre-release": func(f *fixture) { f.prerelease = true },
		"an odd tag":    func(f *fixture) { f.version = "9.9.9-rc1" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "9.9.9")
			mutate(f)
			u, h := f.updater("0.12.5")

			u.tick(context.Background())

			if len(h.launched) != 0 || f.downloadedPackage() || u.load().Latest != "" {
				t.Errorf("launched %d, package downloaded %v, latest %q: it must not even be noted", len(h.launched), f.downloadedPackage(), u.load().Latest)
			}
		})
	}
}

// Each of these is a release Piumy must refuse: nothing launched, nothing kept
// as ready, one failure on record.
func TestAReleaseThatCannotBeVerifiedIsRefused(t *testing.T) {
	otherKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	cases := map[string]struct {
		break_          func(f *fixture)
		packageDownload bool // whether the refusal can only happen after the package was fetched
	}{
		"no SHA256SUMS in the release": {func(f *fixture) { f.assets = []string{f.name, "SHA256SUMS.sig"} }, false},
		"no signature in the release":  {func(f *fixture) { f.assets = []string{f.name, "SHA256SUMS"} }, false},
		"the package is not in the release": {func(f *fixture) {
			f.assets = []string{"SHA256SUMS", "SHA256SUMS.sig"}
		}, false},
		"signed by another key": {func(f *fixture) {
			f.resign(otherKey, f.served["SHA256SUMS"])
		}, false},
		"SHA256SUMS edited after signing": {func(f *fixture) {
			f.served["SHA256SUMS"] = bytes.Replace(f.served["SHA256SUMS"], []byte(f.name), []byte("Piumy-Setup-9.9.8.exe"), 1)
		}, false},
		"validly signed, but it does not list this platform's package": {func(f *fixture) {
			f.resign(f.priv, releasesig.FormatSums(map[string]string{"Piumy-Setup-1.0.0.exe": sha256Hex([]byte("x"))}))
		}, false},
		"GitHub's digest disagrees with the signed sums": {func(f *fixture) {
			f.apiDigest = "sha256:" + sha256Hex([]byte("otro"))
		}, false},
		"GitHub publishes no digest": {func(f *fixture) { f.apiDigest = "" }, false},
		"the digest is not sha256": {func(f *fixture) {
			f.apiDigest = "md5:" + sha256Hex(f.served[f.name])[:32]
		}, false},
		"the package served is not the signed one": {func(f *fixture) {
			f.served[f.name] = []byte("MZ pretend installer for 9.9.9 -- but tampered")
			f.apiSize = int64(len(f.served[f.name]))
		}, true},
		"a package that claims an absurd size": {func(f *fixture) { f.apiSize = maxPackage + 1 }, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "9.9.9")
			tc.break_(f)
			u, h := f.updater("0.12.5")

			u.tick(context.Background())

			if len(h.launched) != 0 || h.stops != 0 {
				t.Fatalf("launched %d times and stopped %d: a release that can't be verified must install nothing", len(h.launched), h.stops)
			}
			st := u.load()
			if st.Ready != nil {
				t.Errorf("ready = %+v, want nothing kept", st.Ready)
			}
			if st.Failure == nil || st.Failure.Version != "9.9.9" || st.Failure.Count != 1 {
				t.Errorf("failure = %+v, want one on record for 9.9.9", st.Failure)
			}
			if !tc.packageDownload && f.downloadedPackage() {
				t.Error("the package was downloaded before the chain was verified")
			}
			if left, _ := filepath.Glob(filepath.Join(h.dataDir, "updates", "9.9.9", "*")); len(left) != 0 {
				t.Errorf("left files behind: %v", left)
			}
		})
	}
}

// A truncated or inflated download is the network's doing, not a hostile
// release: retried on the next window, not counted against the version.
func TestADownloadWithTheWrongLengthIsRetriedNotHeld(t *testing.T) {
	f := newFixture(t, "9.9.9")
	f.apiSize = int64(len(f.served[f.name])) + 10
	u, h := f.updater("0.12.5")

	u.tick(context.Background())

	if len(h.launched) != 0 || u.load().Failure != nil {
		t.Errorf("launched %d, failure %+v: a short download is not a failed version", len(h.launched), u.load().Failure)
	}
	if u.Status().LastError == "" {
		t.Error("no last error: the owner can't tell why nothing happened")
	}
}

func TestWithoutAPublicKeyNothingIsInstalledAndItSaysWhy(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	u.pub = nil

	u.tick(context.Background())

	if len(h.launched) != 0 || f.downloadedPackage() {
		t.Errorf("launched %d / package downloaded %v with no key to verify with", len(h.launched), f.downloadedPackage())
	}
	if st := u.Status(); st.Blocked != BlockedNoKey || !st.Available {
		t.Errorf("status = %+v, want the update shown as available but blocked by %s", st, BlockedNoKey)
	}
	if u.load().Failure != nil {
		t.Error("an unconfigured build must not count the release as a failure")
	}
}

// Every gate the machine has: nothing is installed while one says no, the
// verified file waits, and the first quiet moment installs it.
func TestTheInstallWaitsForEveryGate(t *testing.T) {
	gates := map[string]func(h *harness){
		"the queue is busy":        func(h *harness) { h.idle = BlockedBusy },
		"another Piumy is running": func(h *harness) { h.otherInstance = BlockedOtherInstance },
	}
	for name, shut := range gates {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "9.9.9")
			u, h := f.updater("0.12.5")
			shut(h)

			wait := u.tick(context.Background())

			if len(h.launched) != 0 || h.stops != 0 {
				t.Fatalf("installed with a gate closed (%d launches)", len(h.launched))
			}
			if u.load().Ready == nil {
				t.Error("the verified update was thrown away instead of waiting")
			}
			if wait != 5*time.Minute {
				t.Errorf("next look in %s, want the short retry: a ready update is waiting for a quiet moment", wait)
			}

			h.idle, h.otherInstance = "", ""
			u.tick(context.Background())
			if len(h.launched) != 1 {
				t.Errorf("launches = %d once every gate was open, want 1", len(h.launched))
			}
		})
	}
}

func TestACopyThatCannotUpdateItselfNeverInstallsAndNeverAsksGitHub(t *testing.T) {
	cases := map[string]func(u *Updater, h *harness){
		"not an installed copy": func(u *Updater, h *harness) { h.notInstalled = BlockedNotInstalled },
		"not the default account": func(u *Updater, h *harness) {
			u.cfg.Account = "trabajo"
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "9.9.9")
			u, h := f.updater("0.12.5")
			setup(u, h)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			done := make(chan struct{})
			go func() { u.Run(ctx); close(done) }()
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("Run did not return for a copy that can never update itself")
			}

			if len(f.requested()) != 0 || len(h.launched) != 0 {
				t.Errorf("requests %v, launches %d: such a copy must not even look", f.requested(), len(h.launched))
			}
			if u.Status().Supported {
				t.Error("status says it is supported")
			}
		})
	}
}

func TestTheSwitchOffMeansNoLookAtAll(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	h.enabled = false

	wait := u.tick(context.Background())

	if len(f.requested()) != 0 || len(h.launched) != 0 {
		t.Errorf("requests %v, launches %d with the switch off", f.requested(), len(h.launched))
	}
	if wait != time.Hour {
		t.Errorf("wait = %s, want a normal window (the switch is live: it is asked again)", wait)
	}
}

// If the owner switches it off WHILE a verified update waits for a quiet
// moment, it must not install when the moment comes.
func TestSwitchingOffWhileAnUpdateWaitsCancelsIt(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	h.idle = BlockedBusy
	u.tick(context.Background())
	h.idle, h.enabled = "", false

	// Both ways in: the next look, and an install already past its own check
	// (the owner can switch it off during a long download).
	u.tick(context.Background())
	if got := u.apply(); got != applyBlocked {
		t.Errorf("apply = %v with the switch off, want it blocked", got)
	}

	if len(h.launched) != 0 {
		t.Error("installed after the owner switched it off")
	}
}

func TestAVersionIsTriedAtMostThreeTimesAndNotMoreOftenThanEvery24Hours(t *testing.T) {
	f := newFixture(t, "9.9.9")
	f.apiDigest = "" // refused every time
	u, h := f.updater("0.12.5")
	count := func() int {
		if fl := u.load().Failure; fl != nil {
			return fl.Count
		}
		return 0
	}

	u.tick(context.Background())
	h.now = h.now.Add(23 * time.Hour)
	u.tick(context.Background())
	if count() != 1 {
		t.Fatalf("failures = %d after 23 h, want 1: a refused version is held for 24 h", count())
	}
	for i := 2; i <= 3; i++ {
		h.now = h.now.Add(25 * time.Hour)
		u.tick(context.Background())
		if count() != i {
			t.Fatalf("failures = %d, want %d", count(), i)
		}
	}
	before := len(f.requested())
	h.now = h.now.Add(48 * time.Hour)
	u.tick(context.Background())

	if got := len(f.requested()) - before; got != 1 {
		t.Errorf("made %d requests after giving up, want 1 (the API look, no package)", got)
	}
	if count() != 3 || !u.Status().GivenUp {
		t.Errorf("failures %d, given up %v: after 3 it stops", count(), u.Status().GivenUp)
	}
	if u.Status().Failure == "" {
		t.Error("status gives no reason for the failed version: the owner can't tell what went wrong")
	}

	// A newer release is a new version: the count starts over.
	f.version = "9.9.10"
	f.name = "Piumy-Setup-9.9.10.exe"
	pkg := []byte("MZ next")
	f.served = map[string][]byte{f.name: pkg}
	sums := releasesig.FormatSums(map[string]string{f.name: sha256Hex(pkg)})
	f.served["SHA256SUMS"], f.served["SHA256SUMS.sig"] = sums, releasesig.Sign(f.priv, sums)
	f.assets = []string{f.name, "SHA256SUMS", "SHA256SUMS.sig"}
	f.apiDigest = "sha256:" + sha256Hex(pkg)
	u.tick(context.Background())
	if len(h.launched) != 1 || h.launched[0].Version != "9.9.10" {
		t.Errorf("launched %+v, want the newer release to be tried afresh", h.launched)
	}
}

// What was verified is what runs: the file is hashed again right before launch.
func TestAFileChangedAfterVerificationIsNotLaunched(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	h.idle = BlockedBusy
	u.tick(context.Background())
	ready := u.load().Ready
	if ready == nil {
		t.Fatal("setup: nothing ready")
	}
	if err := os.WriteFile(ready.File, []byte("otra cosa"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.idle = ""

	u.tick(context.Background())

	if len(h.launched) != 0 || h.stops != 0 {
		t.Error("launched a file that no longer matches what was verified")
	}
	if u.load().Ready != nil || u.load().Failure == nil {
		t.Errorf("state %+v: want it dropped and counted", u.load())
	}
}

// The last check before an installer runs comes from the SIGNATURE, not from
// what the state says: whoever can edit the stored state still cannot make
// Piumy run something the release key did not sign.
func TestATamperedStateIsNeverLaunched(t *testing.T) {
	otherKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize))
	evil := []byte("MZ not signed by anyone")
	cases := map[string]func(f *fixture, r *Ready){
		"a File pointing somewhere else, with its own hash": func(f *fixture, r *Ready) {
			other := filepath.Join(filepath.Dir(r.File), "evil.exe")
			if err := os.WriteFile(other, evil, 0o600); err != nil {
				t.Fatal(err)
			}
			r.File, r.SHA256 = other, sha256Hex(evil)
		},
		"the package replaced and its stored hash edited to match": func(f *fixture, r *Ready) {
			if err := os.WriteFile(r.File, evil, 0o600); err != nil {
				t.Fatal(err)
			}
			r.SHA256 = sha256Hex(evil)
		},
		"the stored SHA256SUMS swapped, the old signature kept": func(f *fixture, r *Ready) {
			if err := os.WriteFile(r.File, evil, 0o600); err != nil {
				t.Fatal(err)
			}
			r.SHA256 = sha256Hex(evil)
			r.Sums = string(releasesig.FormatSums(map[string]string{f.name: r.SHA256}))
		},
		"SHA256SUMS and signature both replaced, signed by another key": func(f *fixture, r *Ready) {
			if err := os.WriteFile(r.File, evil, 0o600); err != nil {
				t.Fatal(err)
			}
			r.SHA256 = sha256Hex(evil)
			sums := releasesig.FormatSums(map[string]string{f.name: r.SHA256})
			r.Sums, r.Sig = string(sums), string(releasesig.Sign(otherKey, sums))
		},
		"no signature at all": func(f *fixture, r *Ready) { r.Sig = "" },
		// Everything else checks out here — only the folder is wrong. The
		// installer is extracted next to the file, so it must be where the
		// updater put it.
		"the right bytes, in a folder the updater did not use": func(f *fixture, r *Ready) {
			data, err := os.ReadFile(r.File)
			if err != nil {
				t.Fatal(err)
			}
			elsewhere := filepath.Join(filepath.Dir(filepath.Dir(r.File)), "elsewhere")
			if err := os.MkdirAll(elsewhere, 0o700); err != nil {
				t.Fatal(err)
			}
			r.File = filepath.Join(elsewhere, f.name)
			if err := os.WriteFile(r.File, data, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, "9.9.9")
			u, h := f.updater("0.12.5")
			h.idle = BlockedBusy
			u.tick(context.Background())
			ready := u.load().Ready
			if ready == nil {
				t.Fatal("setup: nothing ready")
			}
			tamper(f, ready)
			u.update(func(s *State) { s.Ready = ready })
			h.idle = ""

			u.tick(context.Background())

			if len(h.launched) != 0 || h.stops != 0 {
				t.Fatalf("launched %d times: a stored state that does not verify must never run", len(h.launched))
			}
			if st := u.load(); st.Ready != nil || st.Failure == nil {
				t.Errorf("state %+v: want it dropped and counted", st)
			}
		})
	}
}

// The owner can update by hand while a verified update waits for a quiet
// moment: what waits is then no longer newer, and must not be installed over it.
func TestAReadyUpdateThatIsNoLongerNewerIsNotLaunched(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	h.idle = BlockedBusy
	u.tick(context.Background())
	if u.load().Ready == nil {
		t.Fatal("setup: nothing ready")
	}
	u.cfg.CurrentVersion = "9.9.9" // installed by hand in the meantime
	h.idle = ""

	u.tick(context.Background())

	if len(h.launched) != 0 || h.stops != 0 {
		t.Error("installed a version that is not newer than the one running")
	}
	if u.load().Ready != nil {
		t.Error("kept waiting with an update that can never be installed")
	}
}

// main.go always hands over an Idle check; if one ever goes missing, not
// knowing is not a reason to install.
func TestWithoutAnIdleCheckNothingIsInstalled(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updaterWith("0.12.5", func(c *Config) { c.Idle = nil })

	u.tick(context.Background())

	if len(h.launched) != 0 {
		t.Error("installed with no way to know the queue was empty")
	}
}

func TestAnInstallerThatWillNotStartIsAFailureAndPiumyKeepsRunning(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	h.launchErr = os.ErrPermission

	u.tick(context.Background())

	if h.stops != 0 {
		t.Error("Piumy shut itself down although the installer never started")
	}
	st := u.load()
	if st.Pending != "" || st.Failure == nil || st.Failure.Count != 1 {
		t.Errorf("state %+v: want pending cleared and one failure", st)
	}
}

// The process that launched the installer is gone; the NEW one settles it.
func TestTheNewProcessSettlesWhatTheOldOneStarted(t *testing.T) {
	f := newFixture(t, "9.9.9")

	t.Run("the installer took", func(t *testing.T) {
		u, h := f.updater("9.9.9")
		u.update(func(s *State) { s.Pending, s.Failure = "9.9.9", &Failure{Version: "9.9.9", Count: 2} })
		if err := os.MkdirAll(filepath.Join(h.dataDir, "updates", "9.9.9"), 0o700); err != nil {
			t.Fatal(err)
		}

		u.reconcile()

		st := u.load()
		if st.Pending != "" || st.InstalledVersion != "9.9.9" || st.InstalledAt != h.now.Unix() || st.Failure != nil {
			t.Errorf("state = %+v, want it recorded as installed now, with the failures forgotten", st)
		}
		if _, err := os.Stat(filepath.Join(h.dataDir, "updates")); err == nil {
			t.Error("the downloads were left behind")
		}
	})
	t.Run("the installer did not take", func(t *testing.T) {
		u, _ := f.updater("0.12.5")
		u.update(func(s *State) { s.Pending = "9.9.9" })

		u.reconcile()

		st := u.load()
		if st.Pending != "" || st.InstalledVersion != "" || st.Failure == nil || st.Failure.Count != 1 {
			t.Errorf("state = %+v, want it counted as a failed try, not as installed", st)
		}
	})
	t.Run("nothing pending", func(t *testing.T) {
		u, h := f.updater("0.12.5")
		u.reconcile()
		if h.kv.m != nil {
			t.Errorf("wrote state %v for nothing", h.kv.m)
		}
	})
}

// Run's own loop: the first look comes on its own and installs.
func TestRunLooksOnItsOwnAndInstalls(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	u.firstMin, u.firstMax = time.Millisecond, time.Millisecond
	launched := make(chan struct{}, 1)
	u.launch = func(p Plan) error { h.launched = append(h.launched, p); launched <- struct{}{}; return nil }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { u.Run(ctx); close(done) }()

	select {
	case <-launched:
	case <-time.After(30 * time.Second): // only a positive wait: this never runs the clock down
		t.Fatal("Run never installed the release")
	}
	cancel()
	<-done
}

func TestStatusReportsWhatTheDashboardShows(t *testing.T) {
	f := newFixture(t, "9.9.9")
	u, h := f.updater("0.12.5")
	h.idle = BlockedBusy
	u.tick(context.Background())

	st := u.Status()

	want := Status{
		Enabled: true, Supported: true, Blocked: BlockedBusy, Current: "0.12.5", Latest: "9.9.9",
		Available: true, Ready: true, CheckedAt: h.now.Unix(),
	}
	if st != want {
		t.Errorf("status = %+v\n want %+v", st, want)
	}
}

// A redirect may change host (the CDN) but never leave https.
func TestARedirectNeverLeavesHTTPS(t *testing.T) {
	mustURL := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	via := []*http.Request{{URL: mustURL("https://github.com/x")}}

	if err := refuseDowngrade(&http.Request{URL: mustURL("https://objects.githubusercontent.com/y")}, via); err != nil {
		t.Errorf("https to another https host refused: %v", err)
	}
	if err := refuseDowngrade(&http.Request{URL: mustURL("http://objects.githubusercontent.com/y")}, via); err == nil {
		t.Error("https → http redirect allowed")
	}
	tooMany := make([]*http.Request, 5)
	for i := range tooMany {
		tooMany[i] = via[0]
	}
	if err := refuseDowngrade(&http.Request{URL: mustURL("https://a/b")}, tooMany); err == nil {
		t.Error("a redirect chain of 5 allowed")
	}
}

func TestBetweenStaysInsideTheWindow(t *testing.T) {
	for i := 0; i < 200; i++ {
		if d := between(6*time.Hour, 12*time.Hour); d < 6*time.Hour || d > 12*time.Hour {
			t.Fatalf("between = %s, outside [6h, 12h]", d)
		}
	}
	if between(time.Hour, time.Hour) != time.Hour || between(2*time.Hour, time.Hour) != 2*time.Hour {
		t.Error("a degenerate window must return its lower bound")
	}
}
