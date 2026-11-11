// Package autoupdate lets Piumy notice a newer release and install it (T176,
// ct-2026-11-10-2103; design and diagram in docs/T176-AUTOUPDATE.md). It looks
// at the pinned GitHub repo on a random window, refuses anything not signed
// with the release key, downloads and hash-checks the package for THIS
// platform, waits for a quiet moment, and launches the platform's own
// installer detached — the installer, not this process, brings Piumy back up.
//
// Every refusal is code here, never a prompt or a skill: no valid signature, an
// uninstalled copy, a busy queue, a second running Piumy — nothing installs.
package autoupdate

import (
	"context"
	"crypto/ed25519"
	"log"
	"math/rand/v2"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"piumy-gateway/internal/releasesig"
)

// KV is the small persistent store the updater keeps its state in
// (*store.Store satisfies it — same seam internal/driftreport uses).
type KV interface {
	KVGet(key string) (string, error)
	KVSet(key, val string) error
}

// Why an update is not being installed right now. The dashboard words them.
const (
	BlockedNotInstalled  = "not_installed"  // a development copy, or Piumy not run as the installed service
	BlockedOtherAccount  = "other_account"  // the installer relaunches only the default account
	BlockedOtherInstance = "other_instance" // another Piumy.exe is running: the installer would kill it
	BlockedBusy          = "busy"           // messages pending, or a dispatch in flight
	BlockedNoKey         = "no_key"         // no release public key built in: nothing can be verified
	// BlockedNoSessionBackup (T193 parte B, ct-2026-11-11-1039): the
	// WhatsApp session could not be backed up, so the installer is not
	// launched. Losing that session costs the owner a QR re-scan; waiting
	// for the next look costs nothing.
	BlockedNoSessionBackup = "no_session_backup"
)

// Config is what main.go hands over; everything else is decided here.
type Config struct {
	// Enabled is asked on every tick, so the owner's switch applies at once.
	Enabled        func() bool
	CurrentVersion string
	DataDir        string
	// Account is PIUMY_ACCOUNT: only the default account ("") updates itself.
	Account string
	KV      KV
	// Idle returns "" when nothing is pending and nothing is in flight, else
	// why not. nil is treated as busy: not knowing is not a reason to install.
	Idle func() string
	// Stop is the graceful shutdown, called right after the installer is launched.
	Stop func()
}

// Plan is what a platform needs to run the installer it was handed.
type Plan struct {
	Version string
	Package string // the verified file: Setup .exe, or the .tar.gz with install.sh
	Dir     string // scratch directory for this version (removed once installed)
	Log     string // where the installer's output goes
	Exe     string // this running executable, for the Windows restart-if-down step
}

// Updater is safe to use from several goroutines.
type Updater struct {
	cfg Config
	pub ed25519.PublicKey

	// The fields below are only ever replaced by tests.
	apiBase, downloadBase string
	http                  *http.Client
	now                   func() time.Time
	goos, goarch          string
	exe                   string
	launch                func(Plan) error
	installed             func() string
	onlyInstance          func() string
	firstMin, firstMax    time.Duration
	min, max              time.Duration
	retry                 time.Duration

	mu        sync.Mutex // guards the KV read-modify-write, blocked and lastError
	blocked   string
	lastError string

	// sessionBackup takes an immediate backup of the WhatsApp session right
	// before the installer runs (T193 parte B), wired by SetSessionBackup.
	// Not in Config because the Backuper is built LATER than the Updater in
	// main.go — same reason pusher.SetState/SetDrift exist. nil means
	// nobody wired one, which is not a reason to refuse an update: see
	// SetSessionBackup.
	sessionBackup func() error
}

// SetSessionBackup wires the pre-install backup of the WhatsApp session
// (T193 parte B, ct-2026-11-11-1039). Called once at boot, before Run, so
// it needs no lock — the same convention pusher.SetState follows.
//
// What fn should return: nil when there is nothing to do (the owner has no
// backup key configured, so the periodic backup is off too — that is their
// decision, not a failure), and an error only when a backup was attempted
// and did not land. An error stops the install; see apply.
func (u *Updater) SetSessionBackup(fn func() error) {
	u.sessionBackup = fn
}

// New builds an Updater for this machine. It does nothing until Run.
func New(cfg Config) *Updater {
	exe, _ := os.Executable()
	u := &Updater{
		cfg:          cfg,
		apiBase:      "https://api.github.com",
		downloadBase: "https://github.com",
		http:         &http.Client{CheckRedirect: refuseDowngrade},
		now:          time.Now,
		goos:         runtime.GOOS,
		goarch:       runtime.GOARCH,
		exe:          exe,
		launch:       platformLaunch,
		installed:    func() string { return platformInstalled(cfg.DataDir) },
		onlyInstance: platformOnlyInstance,
		firstMin:     2 * time.Minute,
		firstMax:     20 * time.Minute,
		min:          6 * time.Hour,
		max:          12 * time.Hour,
		retry:        5 * time.Minute,
	}
	if pub, err := releasesig.ParsePublicKey(releasePublicKey); err == nil {
		u.pub = pub
	}
	if cfg.Idle == nil {
		u.cfg.Idle = func() string { return BlockedBusy }
	}
	return u
}

// Run is the updater's whole life: reconcile what the last run left behind,
// then look, on a random window, until ctx ends. A copy that can never update
// itself (development, another account) does not even ask GitHub.
func (u *Updater) Run(ctx context.Context) {
	u.reconcile()
	if code := u.staticBlock(); code != "" {
		log.Printf("autoupdate: esta copia no se actualiza sola (%s)", code)
		return
	}
	wait := between(u.firstMin, u.firstMax)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = u.tick(ctx)
	}
}

// tick is one look: check if nothing is waiting, then try to install what is
// ready. It returns how long until the next one.
func (u *Updater) tick(ctx context.Context) time.Duration {
	window := between(u.min, u.max)
	if !u.cfg.Enabled() {
		return window
	}
	if u.load().Ready == nil {
		u.check(ctx)
	}
	if u.load().Ready != nil && u.apply() == applyBlocked {
		return u.retry // a verified update is waiting for a quiet moment
	}
	return window
}

type applyResult int

const (
	applyBlocked applyResult = iota // a gate said not now: try again soon
	applyDone                       // the installer was launched
	applyDropped                    // the ready file was refused or the launch failed
)

// apply installs the ready update if every gate allows it right now.
func (u *Updater) apply() applyResult {
	ready := u.load().Ready
	if ready == nil {
		return applyDropped
	}
	if code := u.gateBlock(); code != "" || !u.cfg.Enabled() {
		u.setBlocked(code)
		return applyBlocked
	}
	u.setBlocked("")

	// What gets run is verified AGAIN, now, from the signature (see verifyReady).
	if err := u.verifyReady(ready); err != nil {
		u.fail(ready.Version, "lo guardado ya no se puede verificar: "+err.Error())
		return applyDropped
	}
	// T193 parte B (ct-2026-11-11-1039): the session gets backed up right
	// before the installer runs, and if that fails the install does NOT
	// happen. Boss: «piumy se tiene que auto actualizar y auto instalar sin
	// perder la sesion de whatsapp» — a postponed update costs one more
	// look; a lost session costs a QR re-scan, which with the owner abroad
	// is not a small thing.
	//
	// Here and not in gateBlock, deliberately: that one is documented as
	// "every reason not to install right now, cheapest first" and runs on
	// EVERY tick, so an encrypted copy of the session DB would be taken
	// over and over for an update that may still be hours away. This runs
	// once per real install attempt, after the package has been verified.
	if err := u.sessionBackupBeforeInstall(); err != nil {
		log.Printf("autoupdate: no se instala %s — no se pudo respaldar la sesión de WhatsApp: %v", ready.Version, err)
		u.setBlocked(BlockedNoSessionBackup)
		return applyBlocked
	}
	u.update(func(s *State) { s.Pending = ready.Version })
	plan := Plan{
		Version: ready.Version,
		Package: ready.File,
		Dir:     filepath.Dir(ready.File),
		Log:     filepath.Join(u.cfg.DataDir, "logs", "update-"+ready.Version+".log"),
		Exe:     u.exe,
	}
	if err := u.launch(plan); err != nil {
		u.update(func(s *State) { s.Pending = "" })
		u.fail(ready.Version, "no se pudo lanzar el instalador: "+err.Error())
		return applyDropped
	}
	log.Printf("autoupdate: instalando Piumy %s (el instalador sigue solo; registro en %s)", ready.Version, plan.Log)
	if u.cfg.Stop != nil {
		u.cfg.Stop()
	}
	return applyDone
}

// sessionBackupBeforeInstall runs the wired backup, if there is one. No
// backup wired (or one that decides there is nothing to do) is NOT a
// failure: the owner without a backup key has already chosen to run
// without one, and refusing to ever update them again would be a product
// decision nobody made. A backup that was attempted and failed IS a
// failure, and stops the install.
func (u *Updater) sessionBackupBeforeInstall() error {
	if u.sessionBackup == nil {
		return nil
	}
	return u.sessionBackup()
}

// staticBlock is what can never change while this process lives.
func (u *Updater) staticBlock() string {
	if u.cfg.Account != "" {
		return BlockedOtherAccount
	}
	return u.installed()
}

// gateBlock is every reason not to install right now, cheapest first.
func (u *Updater) gateBlock() string {
	if code := u.staticBlock(); code != "" {
		return code
	}
	if code := u.onlyInstance(); code != "" {
		return code
	}
	return u.cfg.Idle()
}

func (u *Updater) setBlocked(code string) {
	u.mu.Lock()
	u.blocked = code
	u.mu.Unlock()
}

func (u *Updater) setError(err error) {
	u.mu.Lock()
	u.lastError = ""
	if err != nil {
		u.lastError = err.Error()
		log.Printf("autoupdate: %v", err)
	}
	u.mu.Unlock()
}

// Status is the dashboard's picture of the updater.
type Status struct {
	Enabled bool `json:"enabled"`
	// Supported: this copy can replace itself (an installed default account).
	Supported bool `json:"supported"`
	// Blocked is why nothing is installing right now ("" = nothing in the way).
	Blocked string `json:"blocked,omitempty"`
	Current string `json:"current"`
	Latest  string `json:"latest,omitempty"`
	// Available: Latest is newer than Current. Ready: it is downloaded and verified.
	Available        bool   `json:"available"`
	Ready            bool   `json:"ready"`
	CheckedAt        int64  `json:"checked_at,omitempty"`
	Pending          string `json:"pending,omitempty"`
	InstalledVersion string `json:"installed_version,omitempty"`
	InstalledAt      int64  `json:"installed_at,omitempty"`
	// GivenUp: Latest failed 3 times; it waits for a newer release. Failure is
	// why the last try of Latest failed — it survives a restart, LastError does not.
	GivenUp   bool   `json:"given_up,omitempty"`
	Failure   string `json:"failure,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func (u *Updater) Status() Status {
	st := u.load()
	u.mu.Lock()
	blocked, lastError := u.blocked, u.lastError
	u.mu.Unlock()

	static := u.staticBlock()
	switch {
	case static != "":
		blocked = static
	case u.pub == nil:
		blocked = BlockedNoKey
	}
	failure := ""
	if st.Failure != nil && st.Failure.Version == st.Latest {
		failure = st.Failure.Reason
	}
	return Status{
		Enabled:          u.cfg.Enabled(),
		Supported:        static == "",
		Blocked:          blocked,
		Current:          u.cfg.CurrentVersion,
		Latest:           st.Latest,
		Available:        newer(st.Latest, u.cfg.CurrentVersion),
		Ready:            st.Ready != nil,
		CheckedAt:        st.CheckedAt,
		Pending:          st.Pending,
		InstalledVersion: st.InstalledVersion,
		InstalledAt:      st.InstalledAt,
		GivenUp:          st.Failure != nil && st.Failure.Version == st.Latest && st.Failure.Count >= maxAttempts,
		Failure:          failure,
		LastError:        lastError,
	}
}

// between is a random duration in [lo, hi]: every Piumy install must not look
// at GitHub at the same minute, and a fixed interval is a pattern.
func between(lo, hi time.Duration) time.Duration {
	if hi <= lo {
		return lo
	}
	return lo + time.Duration(rand.Int64N(int64(hi-lo)+1))
}
