package capipush

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// listingServer is a fake CleverCoder antenna port that only answers GET
// /terminals, with the given app name and chat_ids.
func listingServer(t *testing.T, app string, chatIDs ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/terminals" {
			t.Errorf("discovery asked %s, want only GET /terminals (a probing handshake could open an approval in CleverCoder's window)", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		terminals := []map[string]string{}
		for _, id := range chatIDs {
			terminals = append(terminals, map[string]string{"chat_id": id, "name": "x"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"app": app, "terminals": terminals})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func portOf(srv *httptest.Server) int { return srv.Listener.Addr().(*net.TCPAddr).Port }

const antennaToken = "0123456789abcdef0123456789abcdef"

// fakeAntenna is a whole fake CleverCoder for one terminal: /terminals lists
// it, /handshake issues antennaToken, /message counts every post (posts) and
// accepts only that token (delivered); any other gets a 401.
func fakeAntenna(t *testing.T, terminalID string) (srv *httptest.Server, delivered, posts *atomic.Int32) {
	t.Helper()
	delivered, posts = new(atomic.Int32), new(atomic.Int32)
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/terminals":
			_ = json.NewEncoder(w).Encode(map[string]any{"app": "clevercoder-capi", "terminals": []map[string]string{{"chat_id": terminalID}}})
		case "/handshake":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": antennaToken})
		case "/message":
			posts.Add(1)
			var req struct {
				Token string `json:"token"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req.Token != antennaToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			delivered.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, delivered, posts
}

// deadEndpoint is the URL of a port nothing listens on any more.
func deadEndpoint(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	return url
}

// quickInjector is an injector whose connection attempts give up fast: on
// Windows a closed loopback port hangs instead of refusing, and the default
// 10 s would make every unreachable-endpoint test take that long.
func quickInjector(endpoint, terminalID string, scanPorts []int) *CleverInjector {
	inj := NewCleverInjector(endpoint, terminalID, "pin==", nil)
	inj.httpClient.Timeout = 300 * time.Millisecond
	inj.scanPorts = scanPorts
	return inj
}

// closedPort is a loopback port nothing listens on.
func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestDiscoverLoopback(t *testing.T) {
	const id = "capi-proyecto-agente-0a1b2c3d"
	mine := listingServer(t, "clevercoder-capi", "capi-otro-agente-ffffffff", id)
	other := listingServer(t, "clevercoder-capi", "capi-otro-agente-ffffffff")
	emptyChatID := listingServer(t, "clevercoder-capi", "")
	foreignApp := listingServer(t, "otra-cosa", id)
	notFound := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(notFound.Close)
	garbage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>no</html>")) }))
	t.Cleanup(garbage.Close)
	mineURL := "http://127.0.0.1:" + strconv.Itoa(portOf(mine))

	cases := []struct {
		name       string
		ports      []int
		terminalID string
		want       string
	}{
		// The known case: without it, every "not found" row below could be a
		// broken instrument instead of a real absence.
		{"found on its port, among others", []int{portOf(other), portOf(mine)}, id, mineURL},
		{"a dead port in the list doesn't stop the hit", []int{closedPort(t), portOf(mine)}, id, mineURL},
		{"terminal on no port", []int{portOf(other)}, id, ""},
		{"another app answering /terminals", []int{portOf(foreignApp)}, id, ""},
		{"a 404 on /terminals (older CleverCoder)", []int{portOf(notFound)}, id, ""},
		{"a body that isn't JSON", []int{portOf(garbage)}, id, ""},
		{"empty terminal id never matches, not even an empty chat_id", []int{portOf(emptyChatID)}, "", ""},
		{"no ports", nil, id, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := discoverLoopback(c.ports, c.terminalID)
			if got != c.want || ok != (c.want != "") {
				t.Errorf("discoverLoopback = (%q, %v), want (%q, %v)", got, ok, c.want, c.want != "")
			}
		})
	}
}

// TestCleverInjector_RelocatesWhenTheAntennaMovedPort is T177's own case: the
// registered endpoint is dead (CleverCoder reopened on another port), the
// terminal is found on loopback, and the SAME Inject call delivers.
func TestCleverInjector_RelocatesWhenTheAntennaMovedPort(t *testing.T) {
	const id = "capi-proyecto-agente-0a1b2c3d"
	antenna, delivered, posts := fakeAntenna(t, id)
	inj := quickInjector(deadEndpoint(t), id, []int{portOf(antenna)})
	// A session negotiated with the OLD listener: the new endpoint is a new
	// session, so it must not be offered to the antenna that never issued it.
	inj.token, inj.key = "token-del-puerto-viejo", deriveKey("pin==", "token-del-puerto-viejo")

	if err := inj.Inject("routing-id", "55500000046, boss", "payload"); err != nil {
		t.Fatalf("Inject = %v, want the dispatch delivered after relocating", err)
	}
	if delivered.Load() != 1 || posts.Load() != 1 {
		t.Errorf("at the new port: delivered=%d posts=%d, want 1/1 (a stale token would cost a 401 first)", delivered.Load(), posts.Load())
	}
	if inj.Endpoint != antenna.URL {
		t.Errorf("Endpoint = %q, want it moved to %q", inj.Endpoint, antenna.URL)
	}
}

func TestCleverInjector_TestHandshakeRelocatesToo(t *testing.T) {
	const id = "capi-proyecto-agente-0a1b2c3d"
	antenna, _, _ := fakeAntenna(t, id)
	inj := quickInjector(deadEndpoint(t), id, []int{portOf(antenna)})

	if err := inj.TestHandshake(); err != nil {
		t.Fatalf("TestHandshake = %v, want ok after relocating", err)
	}
	if inj.Endpoint != antenna.URL {
		t.Errorf("Endpoint = %q, want it moved to %q", inj.Endpoint, antenna.URL)
	}
}

// TestCleverInjector_TerminalGoneOnAForeignPortRelocatesInsteadOfDying: the
// old port is now held by another CleverCoder project, which answers
// terminal_gone for an id that is alive elsewhere. T32 would discard the
// credential for good; with the antenna found, it must live.
func TestCleverInjector_TerminalGoneOnAForeignPortRelocatesInsteadOfDying(t *testing.T) {
	const id = "capi-proyecto-agente-0a1b2c3d"
	foreign := handshakeErrorServer(t, "terminal_gone")
	defer foreign.Close()
	antenna, delivered, _ := fakeAntenna(t, id)
	inj := quickInjector(foreign.URL, id, []int{portOf(antenna)})

	if err := inj.Inject("routing-id", "55500000046, boss", "payload"); err != nil {
		t.Fatalf("Inject = %v, want delivered", err)
	}
	if !inj.Configured() {
		t.Error("Configured() = false: terminal_gone discarded a credential whose antenna was found")
	}
	if delivered.Load() != 1 {
		t.Errorf("delivered = %d, want 1", delivered.Load())
	}
}

// TestCleverInjector_TerminalGoneWithNoAntennaAnywhereStillDies is T32 intact:
// nothing found on loopback, so terminal_gone is final as before.
func TestCleverInjector_TerminalGoneWithNoAntennaAnywhereStillDies(t *testing.T) {
	foreign := handshakeErrorServer(t, "terminal_gone")
	defer foreign.Close()
	nobody := listingServer(t, "clevercoder-capi", "capi-otro-agente-ffffffff")
	inj := quickInjector(foreign.URL, "capi-proyecto-agente-0a1b2c3d", []int{portOf(nobody)})

	err := inj.Inject("routing-id", "55500000046, boss", "payload")
	if !errors.Is(err, errTerminalGone) {
		t.Fatalf("Inject = %v, want errTerminalGone", err)
	}
	if inj.Configured() {
		t.Error("Configured() = true, want the credential discarded (T32)")
	}
}

// TestCleverInjector_UnreachableAndNotFoundKeepsRetrying: no antenna found
// means exactly the pre-T177 behavior — error out, stay configured, keep the
// endpoint, let the next sweep try again.
func TestCleverInjector_UnreachableAndNotFoundKeepsRetrying(t *testing.T) {
	dead := deadEndpoint(t)
	nobody := listingServer(t, "clevercoder-capi")
	inj := quickInjector(dead, "capi-proyecto-agente-0a1b2c3d", []int{portOf(nobody)})

	err := inj.Inject("routing-id", "55500000046, boss", "payload")
	if err == nil || errors.Is(err, errTerminalGone) {
		t.Fatalf("Inject = %v, want a plain delivery error", err)
	}
	if !inj.Configured() || inj.Endpoint != dead {
		t.Errorf("Configured=%v Endpoint=%q, want still configured and untouched", inj.Configured(), inj.Endpoint)
	}
}

// TestCleverInjector_RelocateYieldsToAFreshSetConfig: while a sweep runs, the
// owner can save new credentials; the sweep's answer was about the OLD ones
// and must not overwrite them.
func TestCleverInjector_RelocateYieldsToAFreshSetConfig(t *testing.T) {
	const oldID = "capi-proyecto-agente-0a1b2c3d"
	var inj *CleverInjector
	sweeping := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		inj.SetConfig("http://nuevo:8787", "term-nuevo", "pin-nuevo")
		_ = json.NewEncoder(w).Encode(map[string]any{"app": "clevercoder-capi", "terminals": []map[string]string{{"chat_id": oldID}}})
	}))
	defer sweeping.Close()
	inj = quickInjector("http://viejo:8787", oldID, []int{portOf(sweeping)})

	if inj.relocate() {
		t.Error("relocate = true, want false: a newer SetConfig took over")
	}
	if inj.Endpoint != "http://nuevo:8787" || inj.TerminalID != "term-nuevo" {
		t.Errorf("credentials after the race = %q / %q, want the SetConfig ones", inj.Endpoint, inj.TerminalID)
	}
}

// TestCleverInjector_NoRetryWhenTheSweepFindsTheSameEndpoint: a server that
// says terminal_gone on /handshake yet lists the terminal on /terminals is
// the endpoint we already tried — relocating there would only repeat the
// failure, so the first answer stands (T32 included).
func TestCleverInjector_NoRetryWhenTheSweepFindsTheSameEndpoint(t *testing.T) {
	const id = "capi-proyecto-agente-0a1b2c3d"
	var handshakes atomic.Int32
	contradictory := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/terminals":
			_ = json.NewEncoder(w).Encode(map[string]any{"app": "clevercoder-capi", "terminals": []map[string]string{{"chat_id": id}}})
		case "/handshake":
			handshakes.Add(1)
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "terminal_gone"})
		}
	}))
	defer contradictory.Close()
	inj := quickInjector(contradictory.URL, id, []int{portOf(contradictory)})

	err := inj.Inject("routing-id", "55500000046, boss", "payload")
	if !errors.Is(err, errTerminalGone) {
		t.Fatalf("Inject = %v, want errTerminalGone", err)
	}
	if handshakes.Load() != 1 {
		t.Errorf("handshakes = %d, want 1 — no second try against the same endpoint", handshakes.Load())
	}
}

// TestCleverInjector_AntennaOffDoesNotSweep: antenna_off says the terminal IS
// known and simply isn't listening — not a moved port, so no sweep.
func TestCleverInjector_AntennaOffDoesNotSweep(t *testing.T) {
	var sweeps atomic.Int32
	listing := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { sweeps.Add(1) }))
	defer listing.Close()
	off := handshakeErrorServer(t, "antenna_off")
	defer off.Close()
	inj := quickInjector(off.URL, "capi-proyecto-agente-0a1b2c3d", []int{portOf(listing)})

	if err := inj.Inject("routing-id", "55500000046, boss", "payload"); err == nil {
		t.Fatal("Inject = nil, want the antenna_off failure")
	}
	if sweeps.Load() != 0 {
		t.Errorf("/terminals was asked %d time(s), want 0 — antenna_off is not a moved port", sweeps.Load())
	}
}
