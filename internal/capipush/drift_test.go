package capipush

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/driftreport"
	"piumy-gateway/internal/store"
)

// driftSink stands in for CleverCoder's /lic/report: it keeps every raw body.
type driftSink struct {
	srv    *httptest.Server
	mu     sync.Mutex
	bodies []string
}

func newDriftSink(t *testing.T) *driftSink {
	t.Helper()
	s := &driftSink{}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(raw))
		s.mu.Unlock()
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *driftSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.bodies)
}

// waitFor blocks until the sink has n reports (the send is asynchronous).
func (s *driftSink) waitFor(t *testing.T, n int) {
	t.Helper()
	// 30 s is a deadlock guard for a POSITIVE wait, not a measurement: polling
	// every 5 ms means it costs nothing when the send is quick.
	deadline := time.Now().Add(30 * time.Second)
	for s.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("sink got %d reports, want %d", s.count(), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s *driftSink) report(t *testing.T, i int) (raw string, m map[string]any) {
	t.Helper()
	s.mu.Lock()
	raw = s.bodies[i]
	s.mu.Unlock()
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("report %d is not JSON: %v", i, err)
	}
	return raw, m
}

// wireDrift gives p a real Reporter aimed at sink, with the "sustained" bar set
// to after. enabled=false wires the same Reporter switched off.
func wireDrift(t *testing.T, st *store.Store, p *Pusher, sink *driftSink, after time.Duration, enabled bool) {
	t.Helper()
	if err := st.SetSettingDuration(store.SettingDriftSustainedAfter, after); err != nil {
		t.Fatal(err)
	}
	p.SetDrift(driftreport.New(driftreport.Config{
		Enabled: func() bool { return enabled }, URL: sink.srv.URL, Version: "0.0.0-test", DataDir: t.TempDir(), KV: st,
	}))
}

// sweepsAcross runs two sweeps with a gap longer than the (1 ms) "sustained"
// bar, so the second one is past it whatever the machine's speed.
func sweepsAcross(p *Pusher) {
	p.sweepOnce()
	time.Sleep(10 * time.Millisecond)
	p.sweepOnce()
}

// The end-to-end privacy check the contract asks for: real chat, real route,
// real dispatch path, a real HTTP request — and not one planted identifier in
// the bytes that left. "@" is checked on the whole payload because nothing
// else in it can contain one (hex ids, a timestamp, "email":null).
func TestNoAntennaDriftIsReportedWithoutPersonalData(t *testing.T) {
	const (
		chat = "55500000091@c.us"
		text = "hola, mi clave es 55500000091-secreta"
	)
	st, _, pusher := newBootPusher(t, "", "", "") // unconfigured antenna = nobody can take the dispatch
	sink := newDriftSink(t)
	wireDrift(t, st, pusher, sink, time.Millisecond, true)
	if err := st.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: text, TS: 1}); err != nil {
		t.Fatal(err)
	}

	sweepsAcross(pusher)
	sink.waitFor(t, 1)
	pusher.sweepOnce()
	pusher.sweepOnce()
	time.Sleep(50 * time.Millisecond)
	if sink.count() != 1 {
		t.Fatalf("reports = %d after 4 sweeps of the same drift, want exactly 1 (the cap)", sink.count())
	}

	raw, m := sink.report(t, 0)
	if m["type"] != "piumy-drift" || m["title"] != "Piumy drift: dispatch_retained" {
		t.Errorf("type/title = %v / %v", m["type"], m["title"])
	}
	body, _ := m["body"].(string)
	for _, line := range []string{"kind: dispatch_retained", "cause: no_antenna", "count: 1"} {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("body missing %q, body = %q", line, body)
		}
	}
	for _, leaked := range []string{chat, "55500000091", text, "Contacto Uno", "port-fallback", "@"} {
		if strings.Contains(raw, leaked) {
			t.Errorf("the report leaked %q:\n%s", leaked, raw)
		}
	}
}

func TestNoRouteDriftIsClassifiedAsNoRoute(t *testing.T) {
	st, _, _, _, pusher := newTestPusher(t, "")
	pusher.cfg.PortFallback = "" // nothing to fall back to
	pusher.injectors = map[string]Injector{}
	sink := newDriftSink(t)
	wireDrift(t, st, pusher, sink, time.Millisecond, true)
	chat := "55500000093@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	sweepsAcross(pusher)
	sink.waitFor(t, 1)
	_, m := sink.report(t, 0)
	if body, _ := m["body"].(string); !strings.Contains(body, "cause: no_route\n") {
		t.Errorf("body = %q, want cause no_route", body)
	}
}

func TestHeldDispatchBelowTheSustainedBarSendsNothing(t *testing.T) {
	st, _, pusher := newBootPusher(t, "", "", "")
	sink := newDriftSink(t)
	wireDrift(t, st, pusher, sink, time.Hour, true)
	chat := "55500000094@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	sweepsAcross(pusher)
	time.Sleep(100 * time.Millisecond)
	if sink.count() != 0 {
		t.Fatalf("a hold of milliseconds was reported (%d): a boot before the antenna is set would spend the window", sink.count())
	}
	if pusher.retention.since.IsZero() {
		t.Error("the hold clock is not running")
	}
}

// Holding stops the moment nothing is held; a sweep that merely could not SEE
// the held chats (non-boss chats skipped while swamped — which is what a
// missing antenna causes) must leave the clock alone.
func TestHoldClockResetsWhenFreeButNotWhenSwampedBlind(t *testing.T) {
	st, _, pusher := newBootPusher(t, "", "", "")
	sink := newDriftSink(t)
	wireDrift(t, st, pusher, sink, time.Hour, true)
	chatA, chatB := "55500000095@c.us", "55500000096@c.us"
	for _, c := range []string{chatA, chatB} {
		if err := st.TouchChat(c, "C", 1); err != nil {
			t.Fatal(err)
		}
		dedicate(t, st, c)
	}
	// An OLD message: a fresh one would still be inside the debounce window and
	// never reach dispatch(), so it would not count as held.
	if err := st.AddMessage(store.Message{ChatJID: chatA, ID: "a1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	pusher.sweepOnce()
	started := pusher.retention.since
	if started.IsZero() {
		t.Fatal("hold clock did not start")
	}

	// Backpressure trips (one RECENT non-boss message is enough at threshold
	// 1): non-boss chats are skipped, so nothing is "held" this sweep — and the
	// clock must NOT be read as "the antenna is back".
	if err := st.AddMessage(store.Message{ChatJID: chatB, ID: "b1", Text: "hola", TS: time.Now().Unix()}); err != nil {
		t.Fatal(err)
	}
	if err := st.KVSet(store.SettingCapipushSwampedAt, "1"); err != nil {
		t.Fatal(err)
	}
	pusher.sweepOnce()
	if !pusher.retention.since.Equal(started) {
		t.Fatalf("a swamped (blind) sweep moved the hold clock: %v -> %v", started, pusher.retention.since)
	}

	// Truly free: nothing pending and not swamped → the clock stops.
	if err := st.KVSet(store.SettingCapipushSwampedAt, "1000"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHandled(chatA, "a1"); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkHandled(chatB, "b1"); err != nil {
		t.Fatal(err)
	}
	pusher.sweepOnce()
	if !pusher.retention.since.IsZero() {
		t.Fatalf("hold clock still running with nothing pending: %v", pusher.retention.since)
	}
}

func TestFailingAntennaDriftIsReportedOncePerOutageWithoutTheCause(t *testing.T) {
	const chat = "55500000092@c.us"
	st, _, _, inj, pusher := newTestPusher(t, "")
	inj.setErr(errors.New("handshake status 404 contra 55500000092@c.us"))
	sink := newDriftSink(t)
	wireDrift(t, st, pusher, sink, time.Millisecond, true)
	if err := st.TouchChat(chat, "Contacto Dos", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	sweepsAcross(pusher)
	sink.waitFor(t, 1)
	pusher.sweepOnce()
	pusher.sweepOnce()
	time.Sleep(50 * time.Millisecond)
	if sink.count() != 1 {
		t.Fatalf("reports = %d for one long outage, want exactly 1", sink.count())
	}

	raw, m := sink.report(t, 0)
	if m["title"] != "Piumy drift: antenna_failing" {
		t.Errorf("title = %v", m["title"])
	}
	body, _ := m["body"].(string)
	for _, line := range []string{"kind: antenna_failing", "cause: send_failing"} {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("body missing %q, body = %q", line, body)
		}
	}
	// The injector's own error text carries a JID here, as a real one can.
	for _, leaked := range []string{chat, "handshake", "Contacto Dos", "port-fallback", "@"} {
		if strings.Contains(raw, leaked) {
			t.Errorf("the report leaked %q:\n%s", leaked, raw)
		}
	}
}

func TestFailingAntennaBelowTheSustainedBarSendsNothing(t *testing.T) {
	st, _, _, inj, pusher := newTestPusher(t, "")
	inj.setErr(errors.New("handshake status 404"))
	sink := newDriftSink(t)
	wireDrift(t, st, pusher, sink, time.Hour, true)
	chat := "55500000097@c.us"
	if err := st.TouchChat(chat, "C", 1); err != nil {
		t.Fatal(err)
	}
	dedicate(t, st, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 1}); err != nil {
		t.Fatal(err)
	}

	sweepsAcross(pusher)
	time.Sleep(100 * time.Millisecond)
	if sink.count() != 0 {
		t.Fatalf("a blip was reported (%d): it would spend the 6–9 h window", sink.count())
	}
}

// Reports off: the hooks do no bookkeeping at all and nothing is posted.
func TestDriftHooksAreInertWhenReportsAreOff(t *testing.T) {
	for name, wire := range map[string]func(*testing.T, *store.Store, *Pusher, *driftSink){
		"no reporter wired": func(*testing.T, *store.Store, *Pusher, *driftSink) {},
		"reporter switched off": func(t *testing.T, st *store.Store, p *Pusher, s *driftSink) {
			wireDrift(t, st, p, s, time.Millisecond, false)
		},
	} {
		t.Run(name, func(t *testing.T) {
			st, _, pusher := newBootPusher(t, "", "", "")
			sink := newDriftSink(t)
			wire(t, st, pusher, sink)
			chat := "55500000098@c.us"
			if err := st.TouchChat(chat, "C", 1); err != nil {
				t.Fatal(err)
			}
			dedicate(t, st, chat)
			if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: 1}); err != nil {
				t.Fatal(err)
			}

			sweepsAcross(pusher)
			time.Sleep(50 * time.Millisecond)
			if sink.count() != 0 {
				t.Errorf("posted %d reports with reports off", sink.count())
			}
			if !pusher.retention.since.IsZero() {
				t.Error("the hold clock ran with reports off")
			}
		})
	}
}
