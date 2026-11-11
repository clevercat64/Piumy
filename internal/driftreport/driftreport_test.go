package driftreport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func always() bool { return true }
func never() bool  { return false }

// memKV is an in-memory KV that counts its traffic and can be told to fail.
type memKV struct {
	mu           sync.Mutex
	m            map[string]string
	gets, sets   int
	getErr, setE error
}

func newMemKV() *memKV { return &memKV{m: map[string]string{}} }

func (k *memKV) KVGet(key string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.gets++
	return k.m[key], k.getErr
}

func (k *memKV) KVSet(key, val string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.sets++
	if k.setE != nil {
		return k.setE
	}
	k.m[key] = val
	return nil
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// t0 is the instant the token vectors below were computed for.
var t0 = time.Date(2026, 11, 10, 21, 30, 0, 0, time.UTC)

// capture is a stand-in for /lic/report: it records every request.
type capture struct {
	srv    *httptest.Server
	mu     sync.Mutex
	status int
	posts  []post
}

type post struct {
	method, contentType string
	raw                 []byte
}

func newCapture(t *testing.T) *capture {
	t.Helper()
	c := &capture{status: http.StatusOK}
	c.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.posts = append(c.posts, post{r.Method, r.Header.Get("Content-Type"), raw})
		status := c.status
		c.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(c.srv.Close)
	return c
}

func (c *capture) hits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.posts)
}

// decoded returns the i-th request body as a generic map.
func (c *capture) decoded(t *testing.T, i int) map[string]any {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var m map[string]any
	if err := json.Unmarshal(c.posts[i].raw, &m); err != nil {
		t.Fatalf("request %d is not JSON: %v", i, err)
	}
	return m
}

// newReporter builds an enabled Reporter on a fake clock whose window is the
// 6 h minimum, so boundaries are exact. dir "" = a fresh temp dir.
func newReporter(t *testing.T, c *capture, kv *memKV, clk *fakeClock, dir string) *Reporter {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	r := New(Config{Enabled: always, URL: c.srv.URL, Version: "9.9.9-test", DataDir: dir, KV: kv})
	r.now = clk.now
	r.window = func() time.Duration { return windowMin }
	return r
}

func report(r *Reporter, k Kind) {
	r.Report(Event{Kind: k, Count: 2, Duration: 25 * time.Minute, Cause: CauseWADown})
	r.wg.Wait()
}

// The expected tokens come from sha256sum, not from token(): a test that
// recomputes with the code under test could only ever agree with it.
//
//	printf 'cc-rpt-v1-3a9f7e21|2026-11-10T21:30:00Z|Piumy drift: wa_disconnected' | sha256sum | cut -c1-12
func TestTokenMatchesIndependentVector(t *testing.T) {
	ts := t0.Format(timestampLayout)
	if ts != "2026-11-10T21:30:00Z" {
		t.Fatalf("timestamp = %q, want yyyy-MM-ddTHH:mm:ssZ", ts)
	}
	for kind, want := range map[Kind]string{
		KindWADisconnected: "4816b20df9be",
		KindAntennaFailing: "c5b23e00ec1c",
	} {
		if got := token(ts, titleFor(kind)); got != want {
			t.Errorf("token(%s) = %s, want %s", kind, got, want)
		}
	}
}

func TestPayloadShape(t *testing.T) {
	c := newCapture(t)
	clk := &fakeClock{t: t0}
	r := newReporter(t, c, newMemKV(), clk, "")
	report(r, KindWADisconnected)

	if c.hits() != 1 {
		t.Fatalf("hits = %d, want 1", c.hits())
	}
	if p := c.posts[0]; p.method != http.MethodPost || !strings.HasPrefix(p.contentType, "application/json") {
		t.Errorf("request = %s %q, want POST application/json", p.method, p.contentType)
	}
	m := c.decoded(t, 0)
	if len(m) != 8 {
		t.Errorf("payload has %d keys %v, want exactly the 8 of /lic/report", len(m), m)
	}
	want := map[string]any{
		"type": "piumy-drift", "title": "Piumy drift: wa_disconnected",
		"app_version": "9.9.9-test", "timestamp": "2026-11-10T21:30:00Z",
		"token": "4816b20df9be", "email": nil,
	}
	for k, v := range want {
		got, present := m[k]
		if !present || got != v {
			t.Errorf("payload[%q] = %v (present=%v), want %v", k, got, present, v)
		}
	}
	if id, _ := m["machine_id"].(string); !guidPattern.MatchString(id) {
		t.Errorf("machine_id = %q, want a GUID", id)
	}
	b, _ := m["body"].(string)
	for _, line := range []string{
		"kind: wa_disconnected", "cause: wa_down", "count: 2", "duration: 25m0s",
		"platform: " + runtime.GOOS + "/" + runtime.GOARCH,
	} {
		if !strings.Contains(b, line+"\n") {
			t.Errorf("body missing line %q, body = %q", line, b)
		}
	}
}

func TestOneReportPerKindPerWindow(t *testing.T) {
	c := newCapture(t)
	clk := &fakeClock{t: t0}
	r := newReporter(t, c, newMemKV(), clk, "")

	report(r, KindWADisconnected)
	report(r, KindWADisconnected)
	if c.hits() != 1 {
		t.Fatalf("same kind twice in a row: hits = %d, want 1", c.hits())
	}
	report(r, KindAntennaFailing)
	if c.hits() != 2 {
		t.Fatalf("a different kind has its own window: hits = %d, want 2", c.hits())
	}
	clk.advance(windowMin - time.Minute)
	report(r, KindWADisconnected)
	if c.hits() != 2 {
		t.Fatalf("1 min before the window ends: hits = %d, want 2", c.hits())
	}
	clk.advance(time.Minute)
	report(r, KindWADisconnected)
	if c.hits() != 3 {
		t.Fatalf("window over: hits = %d, want 3", c.hits())
	}
}

func TestWindowIsRandomWithinSixToNineHours(t *testing.T) {
	seen := map[time.Duration]bool{}
	for i := 0; i < 2000; i++ {
		d := randomWindow()
		if d < 6*time.Hour || d > 9*time.Hour {
			t.Fatalf("window = %s, want within 6h–9h", d)
		}
		seen[d] = true
	}
	if len(seen) < 100 {
		t.Errorf("only %d distinct windows in 2000 draws — that is a fixed cadence, not a random one", len(seen))
	}
}

// Three sends in a ROLLING 24 h, not per calendar day: 3 at 23:59 plus 3 at
// 00:01 would be 6 in two minutes.
func TestDailyCapIsRollingNotCalendar(t *testing.T) {
	c := newCapture(t)
	clk := &fakeClock{t: t0}
	r := newReporter(t, c, newMemKV(), clk, "")

	report(r, KindDispatchRetained)
	clk.advance(time.Minute)
	report(r, KindWADisconnected)
	clk.advance(time.Minute)
	report(r, KindAntennaFailing)
	if c.hits() != 3 {
		t.Fatalf("three kinds: hits = %d, want 3", c.hits())
	}

	clk.advance(10 * time.Hour) // every per-kind window is over, the daily cap is not
	report(r, KindDispatchRetained)
	if c.hits() != 3 {
		t.Fatalf("a 4th send inside 24 h: hits = %d, want 3", c.hits())
	}

	clk.advance(14 * time.Hour) // t0+24h02m: the first send has left the window
	report(r, KindDispatchRetained)
	if c.hits() != 4 {
		t.Fatalf("after the first send aged out: hits = %d, want 4", c.hits())
	}
}

// The cap must hold across a restart, and the window must not be re-drawn: a
// loop of restarts would otherwise keep rolling the dice until one lands early.
func TestCapSurvivesRestartWithoutRedrawingTheWindow(t *testing.T) {
	c := newCapture(t)
	clk := &fakeClock{t: t0}
	kv := newMemKV()
	dir := t.TempDir()

	r1 := newReporter(t, c, kv, clk, dir)
	report(r1, KindWADisconnected)
	savedBefore := kv.m[stateKey]
	if savedBefore == "" {
		t.Fatal("cap was not persisted")
	}

	r2 := newReporter(t, c, kv, clk, dir)
	r2.window = func() time.Duration { return windowMax } // would change next_allowed if re-drawn
	clk.advance(time.Hour)
	report(r2, KindWADisconnected)
	if c.hits() != 1 {
		t.Fatalf("after a restart: hits = %d, want 1 (the cap was forgotten)", c.hits())
	}
	if kv.m[stateKey] != savedBefore {
		t.Errorf("persisted cap changed across a blocked report:\n before %s\n after  %s", savedBefore, kv.m[stateKey])
	}
	id1, _ := os.ReadFile(filepath.Join(dir, machineIDFile))
	clk.advance(windowMax)
	report(r2, KindWADisconnected)
	if c.hits() != 2 {
		t.Fatalf("after the window: hits = %d, want 2", c.hits())
	}
	if got := c.decoded(t, 1)["machine_id"]; got != strings.TrimSpace(string(id1)) {
		t.Errorf("machine_id changed across a restart: %v vs %q", got, id1)
	}
}

func TestDisabledDoesNothingAtAll(t *testing.T) {
	c := newCapture(t)
	kv := newMemKV()
	dir := t.TempDir()
	r := New(Config{Enabled: never, URL: c.srv.URL, DataDir: dir, KV: kv})
	r.Report(Event{Kind: KindWADisconnected, Count: 1, Cause: CauseWADown})
	r.wg.Wait()

	if c.hits() != 0 {
		t.Errorf("disabled sent %d requests", c.hits())
	}
	if kv.gets != 0 || kv.sets != 0 {
		t.Errorf("disabled touched the KV: gets=%d sets=%d", kv.gets, kv.sets)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("disabled wrote files into the data dir: %v", entries)
	}
	var nilReporter *Reporter
	nilReporter.Report(Event{Kind: KindWADisconnected}) // must not panic
	if nilReporter.Enabled() {
		t.Error("nil Reporter reports Enabled")
	}
}

// Reports are the owner's opt-in and the dashboard flips the switch while Piumy
// runs: it is read on EVERY report, and while it says no the Reporter must
// leave no trace — not even loading the cap.
func TestSwitchIsReadLiveAndOffLeavesNoTrace(t *testing.T) {
	c := newCapture(t)
	kv := newMemKV()
	dir := t.TempDir()
	clk := &fakeClock{t: t0}
	on := false
	r := New(Config{Enabled: func() bool { return on }, URL: c.srv.URL, Version: "9.9.9-test", DataDir: dir, KV: kv})
	r.now = clk.now
	r.window = func() time.Duration { return windowMin }

	report(r, KindWADisconnected)
	if c.hits() != 0 || kv.gets != 0 || kv.sets != 0 {
		t.Fatalf("off: hits=%d gets=%d sets=%d, want all 0", c.hits(), kv.gets, kv.sets)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("off: wrote %v", entries)
	}

	on = true // the owner switches it on in the dashboard
	report(r, KindWADisconnected)
	if c.hits() != 1 {
		t.Fatalf("switched on: hits = %d, want 1", c.hits())
	}

	on = false // and off again
	clk.advance(windowMax)
	report(r, KindWADisconnected)
	report(r, KindAntennaFailing)
	if c.hits() != 1 {
		t.Fatalf("switched off again: hits = %d, want still 1", c.hits())
	}
}

func TestNoSwitchFunctionMeansOff(t *testing.T) {
	c := newCapture(t)
	r := New(Config{URL: c.srv.URL, DataDir: t.TempDir(), KV: newMemKV()}) // Enabled nil
	report(r, KindWADisconnected)
	if r.Enabled() || c.hits() != 0 {
		t.Errorf("a nil switch must mean off: Enabled=%v hits=%d", r.Enabled(), c.hits())
	}
}

func TestMachineIDOnlyCreatedWhenAReportLeaves(t *testing.T) {
	c := newCapture(t)
	dir := t.TempDir()
	r := newReporter(t, c, newMemKV(), &fakeClock{t: t0}, dir)
	if _, err := os.Stat(filepath.Join(dir, machineIDFile)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("machine-id exists before any report (err=%v)", err)
	}
	report(r, KindWADisconnected)
	if _, err := os.Stat(filepath.Join(dir, machineIDFile)); err != nil {
		t.Fatalf("machine-id missing after the first report: %v", err)
	}
}

func TestMachineIDIsStableAndGUIDShaped(t *testing.T) {
	dir := t.TempDir()
	a, err := loadOrCreateMachineID(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := loadOrCreateMachineID(dir)
	if a != b || !guidPattern.MatchString(a) {
		t.Fatalf("ids %q and %q: want the same GUID twice", a, b)
	}
	if a[14] != '4' {
		t.Errorf("id %q is not a version-4 GUID", a)
	}

	if err := os.WriteFile(filepath.Join(dir, machineIDFile), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := loadOrCreateMachineID(dir)
	if err != nil || !guidPattern.MatchString(c) {
		t.Fatalf("a malformed file must be replaced by a valid GUID, got %q (err=%v)", c, err)
	}

	if _, err := loadOrCreateMachineID(""); err == nil {
		t.Error("an empty data dir must be refused: the file would land in the CWD")
	}
}

func TestEmptyDataDirKeepsTheReporterOff(t *testing.T) {
	c := newCapture(t)
	if r := New(Config{Enabled: always, URL: c.srv.URL, DataDir: "", KV: newMemKV()}); r.Enabled() {
		t.Error("Reporter enabled without a data dir")
	}
}

// A failed send is logged and dropped: no retry, no second request. The slot
// stays spent — that is what "never retries in a loop" costs.
func TestSendFailureIsDroppedAndNeverRetried(t *testing.T) {
	c := newCapture(t)
	c.status = http.StatusInternalServerError
	clk := &fakeClock{t: t0}
	r := newReporter(t, c, newMemKV(), clk, "")

	report(r, KindAntennaFailing)
	clk.advance(time.Minute)
	report(r, KindAntennaFailing)
	if c.hits() != 1 {
		t.Fatalf("after a 500: hits = %d, want 1 (no retry)", c.hits())
	}

	c.srv.Close()                 // connection refused from here on
	report(r, KindWADisconnected) // must neither panic nor block
}

func TestCannotReadOrPersistTheCapMeansNoSend(t *testing.T) {
	c := newCapture(t)

	kvRead := newMemKV()
	kvRead.getErr = errors.New("db locked")
	rRead := newReporter(t, c, kvRead, &fakeClock{t: t0}, "")
	report(rRead, KindWADisconnected)
	report(rRead, KindAntennaFailing)
	if c.hits() != 0 {
		t.Errorf("sent %d reports although the persisted cap could not be read", c.hits())
	}
	if kvRead.gets != 1 {
		t.Errorf("read the cap %d times, want 1: a failed read must not be retried (or logged) on every sweep", kvRead.gets)
	}

	kvWrite := newMemKV()
	kvWrite.setE = errors.New("disk full")
	r := newReporter(t, c, kvWrite, &fakeClock{t: t0}, "")
	report(r, KindWADisconnected)
	if c.hits() != 0 {
		t.Errorf("sent %d reports although the cap could not be persisted", c.hits())
	}
}

// Nothing a caller can put in an Event reaches the wire except the closed
// vocabulary. Identifiers are checked whole, and only in title/body: the
// machine_id and token are hex and could contain "555" by chance.
func TestNoFreeTextReachesTheWire(t *testing.T) {
	const jid = "55500000001@s.whatsapp.net"
	c := newCapture(t)
	r := newReporter(t, c, newMemKV(), &fakeClock{t: t0}, "")

	r.Report(Event{Kind: KindWADisconnected, Count: 1, Duration: time.Minute, Cause: Cause(jid)})
	r.wg.Wait()
	if c.hits() != 1 {
		t.Fatalf("hits = %d, want 1", c.hits())
	}
	m := c.decoded(t, 0)
	b, _ := m["body"].(string)
	title, _ := m["title"].(string)
	for _, field := range []string{b, title} {
		if strings.Contains(field, jid) || strings.Contains(field, "@") || strings.Contains(field, "555") {
			t.Errorf("planted identifier leaked into %q", field)
		}
	}
	if !strings.Contains(b, "cause: unknown\n") {
		t.Errorf("an unknown cause must be replaced by %q, body = %q", CauseUnknown, b)
	}

	before := c.hits()
	r.Report(Event{Kind: Kind(jid), Count: 1}) // an unknown Kind is dropped
	r.wg.Wait()
	if c.hits() != before {
		t.Error("an unknown Kind was sent")
	}
}

// No wall-clock deadline: under load a fixed "returns within N s" is flaky. The
// server holds every request until released, and a RoundTripper notes when the
// HTTP exchange ENDS; Report must have returned before that. A Report that
// waits for the network can only return after the exchange ended (here: after
// sendTimeout), so it fails — and an async one passes however slow the machine.
func TestReportDoesNotBlockOnASlowServer(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-release }))
	defer srv.Close()
	// Runs BEFORE srv.Close (LIFO), also on t.Fatal: Close waits for the
	// blocked handler, so without this a failing test hangs instead of failing.
	defer unblock()

	exchangeEnded := make(chan struct{})
	var endOnce sync.Once
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := http.DefaultTransport.RoundTrip(req)
		endOnce.Do(func() { close(exchangeEnded) })
		return resp, err
	})}
	r := New(Config{Enabled: always, URL: srv.URL, DataDir: t.TempDir(), KV: newMemKV(), HTTPClient: client})

	r.Report(Event{Kind: KindWADisconnected, Count: 1, Cause: CauseWADown})
	select {
	case <-exchangeEnded:
		t.Fatal("Report only returned after the HTTP exchange had ended: it would stall capipush's sweep")
	default:
	}

	unblock()
	r.wg.Wait()
	select {
	case <-exchangeEnded:
	default:
		t.Fatal("the request never went out (nothing to prove the point)")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
