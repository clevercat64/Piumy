package autoupdate

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"time"
)

const (
	stateKey = "autoupdate.state"
	// A version gets at most maxAttempts tries, attemptHold apart: a release
	// that keeps failing (refused, or an installer that doesn't take) must not
	// become a loop that kills and restarts Piumy every few hours.
	maxAttempts = 3
	attemptHold = 24 * time.Hour
)

// Ready is a package that was downloaded and fully verified, waiting for a
// quiet moment. Sums and Sig are the signed SHA256SUMS it was verified against:
// they are kept so the last check, right before the installer runs, is made
// from the signature again and not from a stored hash anyone could edit.
type Ready struct {
	Version string `json:"version"`
	File    string `json:"file"`
	SHA256  string `json:"sha256"`
	Sums    string `json:"sums"`
	Sig     string `json:"sig"`
}

// Failure counts the tries of ONE version.
type Failure struct {
	Version string `json:"version"`
	Count   int    `json:"count"`
	LastAt  int64  `json:"last_at"`
	Reason  string `json:"reason"`
}

// State is everything the updater remembers across restarts — it has to: the
// process that launches the installer is not the one that sees it finish.
type State struct {
	Latest           string   `json:"latest,omitempty"` // newest version GitHub reported
	CheckedAt        int64    `json:"checked_at,omitempty"`
	Ready            *Ready   `json:"ready,omitempty"`
	Pending          string   `json:"pending,omitempty"` // being installed right now
	InstalledVersion string   `json:"installed_version,omitempty"`
	InstalledAt      int64    `json:"installed_at,omitempty"`
	Failure          *Failure `json:"failure,omitempty"`
}

func (u *Updater) load() State {
	var st State
	raw, err := u.cfg.KV.KVGet(stateKey)
	if err != nil || raw == "" {
		return st
	}
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		log.Printf("autoupdate: estado ilegible, se empieza de cero: %v", err)
		return State{}
	}
	return st
}

// update is a read-modify-write of the state.
func (u *Updater) update(change func(*State)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	st := u.load()
	change(&st)
	raw, err := json.Marshal(st)
	if err == nil {
		err = u.cfg.KV.KVSet(stateKey, string(raw))
	}
	if err != nil {
		log.Printf("autoupdate: no se pudo guardar el estado: %v", err)
	}
}

// held reports whether version is not to be tried now: it failed maxAttempts
// times, or failed less than attemptHold ago.
func (u *Updater) held(st State, version string) bool {
	f := st.Failure
	if f == nil || f.Version != version {
		return false
	}
	return f.Count >= maxAttempts || u.now().Unix() < f.LastAt+int64(attemptHold/time.Second)
}

// fail records a failed try of version and drops whatever was ready for it.
func (u *Updater) fail(version, reason string) {
	log.Printf("autoupdate: la versión %s no se instala: %s", version, reason)
	u.update(func(s *State) {
		f := s.Failure
		if f == nil || f.Version != version {
			f = &Failure{Version: version}
		}
		f.Count++
		f.LastAt = u.now().Unix()
		f.Reason = reason
		s.Failure = f
		s.Ready = nil
	})
}

// updatesDir holds the downloads; it is removed once nothing needs it.
func (u *Updater) updatesDir() string { return filepath.Join(u.cfg.DataDir, "updates") }

// reconcile settles what the previous run left: it set Pending before it
// launched the installer, and only a process of the NEW version can say whether
// the installer took.
func (u *Updater) reconcile() {
	st := u.load()
	if st.Pending == "" {
		return
	}
	if !newer(st.Pending, u.cfg.CurrentVersion) {
		log.Printf("autoupdate: Piumy %s quedó instalado", u.cfg.CurrentVersion)
		u.update(func(s *State) {
			s.InstalledVersion, s.InstalledAt = u.cfg.CurrentVersion, u.now().Unix()
			s.Pending, s.Ready, s.Failure = "", nil, nil
		})
		os.RemoveAll(u.updatesDir())
		return
	}
	u.update(func(s *State) { s.Pending = "" })
	u.fail(st.Pending, "el instalador no dejó el programa nuevo (sigue la versión "+u.cfg.CurrentVersion+")")
}
