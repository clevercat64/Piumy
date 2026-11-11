// Package driftreport sends the maintainers a short, anonymous note when
// Piumy's own picture of the world differs from reality (T174a,
// ct-2026-11-10-210158). One job: take an Event, decide whether it may leave
// — switch, per-kind window, daily cap, all persisted — and POST it. Noticing
// the drift is the caller's job (capipush, WatchWhatsApp): this package never
// reads Piumy's state, only what an Event carries.
//
// What can leave is closed IN CODE, not by convention (constitution §2b): an
// Event holds a Kind and a Cause from fixed vocabularies, a count and a
// duration. There is no string field a JID, a phone number, a name, a chat
// text or a disk path could ride in on, and an unknown Cause is replaced, not
// forwarded.
package driftreport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"math/rand"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind is one of the three drifts the contract names.
type Kind string

const (
	// KindDispatchRetained: due dispatches held because the route or the
	// agent has no antenna (capipush.dispatch).
	KindDispatchRetained Kind = "dispatch_retained"
	// KindWADisconnected: WhatsApp not connected for longer than a random
	// 20–40 min window (WatchWhatsApp).
	KindWADisconnected Kind = "wa_disconnected"
	// KindAntennaFailing: dispatches failing against a registered antenna
	// (capipush.recordChannelDown).
	KindAntennaFailing Kind = "antenna_failing"
)

// Cause narrows a Kind. A closed set: anything else becomes CauseUnknown.
type Cause string

const (
	CauseUnknown     Cause = "unknown"
	CauseNoRoute     Cause = "no_route"
	CauseNoAntenna   Cause = "no_antenna"
	CauseWADown      Cause = "wa_down"
	CauseWAReplaced  Cause = "wa_replaced"
	CauseWAHalted    Cause = "wa_halted"
	CauseSendFailing Cause = "send_failing"
)

var knownKinds = map[Kind]bool{KindDispatchRetained: true, KindWADisconnected: true, KindAntennaFailing: true}

var knownCauses = map[Cause]bool{
	CauseUnknown: true, CauseNoRoute: true, CauseNoAntenna: true,
	CauseWADown: true, CauseWAReplaced: true, CauseWAHalted: true, CauseSendFailing: true,
}

// Event is everything a report may say. Deliberately no free-text field.
type Event struct {
	Kind     Kind
	Count    int
	Duration time.Duration
	Cause    Cause
}

// KV is the slice of *store.Store the cap needs to survive a restart.
type KV interface {
	KVGet(key string) (string, error)
	KVSet(key, val string) error
}

// Config wires a Reporter. Reports are the owner's opt-in, so the switch is a
// function asked EVERY time (the dashboard flips it while Piumy runs), not a
// value fixed at boot. While it answers false — or if it is nil, or the URL,
// the data dir or the KV is missing — the Reporter does nothing at all: no KV
// read, no machine-id file, no request.
type Config struct {
	Enabled func() bool
	URL     string
	Version string // app_version in the report
	DataDir string // machine-id lives here; empty keeps the reporter off
	KV      KV
	// HTTPClient is optional (tests); the default carries sendTimeout.
	HTTPClient *http.Client
}

const (
	// wire format fixed by CleverCoder's /lic/report.
	reportType      = "piumy-drift"
	timestampLayout = "2006-01-02T15:04:05Z"
	// tokenSalt is a noise filter, NOT authentication: this source is public
	// (AGPL), so anyone can compute a valid token. The server must not trust it.
	tokenSalt = "cc-rpt-v1-3a9f7e21"

	// stateKey is the kv row holding the cap (see capState).
	stateKey = "drift_report_state"

	// One report per Kind every 6–9 h (random, never fixed — a fixed cadence
	// is a pattern), and at most 3 per rolling 24 h per install: the server
	// shares a 200/day cap with the owner's sales notices, and a Piumy stuck
	// in a loop must not eat it.
	windowMin   = 6 * time.Hour
	windowMax   = 9 * time.Hour
	maxPerDay   = 3
	dayWindow   = 24 * time.Hour
	sendTimeout = 10 * time.Second
)

// capState is the persisted cap. NextAllowed holds the ALREADY-DRAWN absolute
// time per Kind, not the last send: re-drawing the window on every read would
// let a restart loop re-roll the dice until one lands early. Sent keeps the
// recent send times (unix seconds) for the rolling daily cap.
type capState struct {
	NextAllowed map[Kind]int64 `json:"next_allowed"`
	Sent        []int64        `json:"sent"`
}

// Reporter is safe for concurrent use. A nil *Reporter is a valid, inert one.
type Reporter struct {
	cfg Config

	// usable: the wiring is complete (URL, data dir, KV). Decided once in New.
	usable bool

	mu         sync.Mutex
	state      capState
	loaded     bool // the persisted cap has been read (lazily, on the first allowed report)
	loadFailed bool // reading it failed: off for this run, so it is not retried (or logged) every sweep
	machineID  string

	// test seams
	now    func() time.Time
	window func() time.Duration
	wg     sync.WaitGroup
}

// New builds a Reporter. It reads nothing: the persisted cap is loaded the
// first time a report is actually allowed, so an install that never opts in
// never touches it.
func New(cfg Config) *Reporter {
	r := &Reporter{
		cfg:    cfg,
		now:    time.Now,
		window: randomWindow,
		state:  capState{NextAllowed: map[Kind]int64{}},
		usable: cfg.URL != "" && cfg.DataDir != "" && cfg.KV != nil,
	}
	if cfg.Enabled != nil && !r.usable {
		log.Printf("driftreport: sin URL, carpeta de datos o almacén — reportes apagados")
	}
	return r
}

// Enabled reports whether Report would act right now: the owner has switched
// reports on and the wiring is complete. Callers use it to skip even their own
// bookkeeping while reports are off. Asks the switch each call (a cheap read).
func (r *Reporter) Enabled() bool {
	return r != nil && r.usable && r.cfg.Enabled != nil && r.cfg.Enabled()
}

// Report asks to send e. Never blocks and never fails the caller: if the
// switch, the per-kind window or the daily cap says no, it returns quietly;
// the HTTP call runs in its own goroutine and a failure is logged and dropped.
// Callers may call it on every sweep while a drift lasts — the cap is what
// keeps that to one report per window.
func (r *Reporter) Report(e Event) {
	if !r.Enabled() {
		return
	}
	if !knownKinds[e.Kind] {
		log.Printf("driftreport: tipo desconocido %q — se descarta", e.Kind)
		return
	}
	if !knownCauses[e.Cause] {
		e.Cause = CauseUnknown
	}

	r.mu.Lock()
	allowed := r.ensureLoaded() && r.reserve(e.Kind)
	var id string
	if allowed {
		var err error
		if id, err = r.ensureMachineID(); err != nil {
			log.Printf("driftreport: sin machine-id (%v) — se descarta", err)
			allowed = false
		}
	}
	r.mu.Unlock()
	if !allowed {
		return
	}

	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.send(e, id)
	}()
}

// reserve spends one slot of the cap for kind, persisting it BEFORE anything
// is sent: a send that fails is never retried, and a restart mid-send cannot
// duplicate it. Caller holds r.mu.
func (r *Reporter) reserve(kind Kind) bool {
	now := r.now()
	if now.Unix() < r.state.NextAllowed[kind] {
		return false
	}
	var recent []int64
	for _, ts := range r.state.Sent {
		if now.Sub(time.Unix(ts, 0)) < dayWindow {
			recent = append(recent, ts)
		}
	}
	if len(recent) >= maxPerDay {
		return false
	}
	r.state.NextAllowed[kind] = now.Add(r.window()).Unix()
	r.state.Sent = append(recent, now.Unix())
	if err := r.saveState(); err != nil {
		log.Printf("driftreport: no se pudo persistir el tope (%v) — no se envía", err)
		return false
	}
	return true
}

// ensureLoaded reads the persisted cap once. If that fails, reports stay off
// for this run (logged once) rather than risk sending past a cap it could not
// read. Caller holds r.mu.
func (r *Reporter) ensureLoaded() bool {
	if r.loaded {
		return true
	}
	if r.loadFailed {
		return false
	}
	if err := r.loadState(); err != nil {
		log.Printf("driftreport: no se pudo leer el tope persistido (%v) — reportes apagados en esta corrida", err)
		r.loadFailed = true
		return false
	}
	r.loaded = true
	return true
}

func (r *Reporter) loadState() error {
	raw, err := r.cfg.KV.KVGet(stateKey)
	if err != nil {
		return err
	}
	if raw == "" {
		return nil
	}
	var st capState
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		// Not worth turning reports off over: start clean, the next send
		// overwrites it. Costs at most one extra window.
		log.Printf("driftreport: tope persistido ilegible (%v) — se empieza de cero", err)
		return nil
	}
	if st.NextAllowed == nil {
		st.NextAllowed = map[Kind]int64{}
	}
	r.state = st
	return nil
}

func (r *Reporter) saveState() error {
	raw, err := json.Marshal(r.state)
	if err != nil {
		return err
	}
	return r.cfg.KV.KVSet(stateKey, string(raw))
}

// randomWindow draws the wait before the same Kind may report again.
func randomWindow() time.Duration {
	//nolint:gosec // no-crypto random a propósito: es cadencia, no seguridad
	return windowMin + time.Duration(rand.Int63n(int64(windowMax-windowMin)+1))
}

// wire is the JSON /lic/report expects.
type wire struct {
	Type       string  `json:"type"`
	Title      string  `json:"title"`
	Body       string  `json:"body"`
	Email      *string `json:"email"` // always null: reports are anonymous
	AppVersion string  `json:"app_version"`
	MachineID  string  `json:"machine_id"`
	Timestamp  string  `json:"timestamp"`
	Token      string  `json:"token"`
}

// titleFor is fixed per Kind: nothing variable ever reaches the title.
func titleFor(k Kind) string { return "Piumy drift: " + string(k) }

// token is the first 12 hex of sha256(salt|timestamp|title).
func token(timestamp, title string) string {
	sum := sha256.Sum256([]byte(tokenSalt + "|" + timestamp + "|" + title))
	return hex.EncodeToString(sum[:])[:12]
}

// body renders only Event's closed fields plus GOOS/GOARCH.
func body(e Event) string {
	var b strings.Builder
	b.WriteString("kind: " + string(e.Kind) + "\n")
	b.WriteString("cause: " + string(e.Cause) + "\n")
	b.WriteString("count: " + strconv.Itoa(e.Count) + "\n")
	b.WriteString("duration: " + e.Duration.Round(time.Second).String() + "\n")
	b.WriteString("platform: " + runtime.GOOS + "/" + runtime.GOARCH + "\n")
	return b.String()
}

func (r *Reporter) send(e Event, machineID string) {
	title := titleFor(e.Kind)
	ts := r.now().UTC().Format(timestampLayout)
	data, err := json.Marshal(wire{
		Type: reportType, Title: title, Body: body(e),
		AppVersion: r.cfg.Version, MachineID: machineID,
		Timestamp: ts, Token: token(ts, title),
	})
	if err != nil {
		log.Printf("driftreport: no se pudo armar el reporte %s: %v", e.Kind, err)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.URL, bytes.NewReader(data))
	if err != nil {
		log.Printf("driftreport: URL inválida (%v) — se descarta %s", err, e.Kind)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := r.cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("driftreport: envío de %s falló, se descarta: %v", e.Kind, err)
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<10))
	if resp.StatusCode/100 != 2 {
		log.Printf("driftreport: el servidor contestó %d a %s, se descarta", resp.StatusCode, e.Kind)
	}
}
