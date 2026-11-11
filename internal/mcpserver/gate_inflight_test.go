package mcpserver

import "testing"

// TestAnyInFlightSeesADispatchOnAnyTerminal (T176): the self-updater asks this
// before it restarts Piumy. The control is the empty gate (false) and the
// per-terminal InFlight, which must agree with it.
func TestAnyInFlightSeesADispatchOnAnyTerminal(t *testing.T) {
	gate := NewGate()
	if gate.AnyInFlight() {
		t.Fatal("an empty gate reports a dispatch in flight")
	}

	if err := gate.RegisterDispatch("nonce-a", "55500000104@c.us", LevelCaution, "term-a", 0, ""); err != nil {
		t.Fatal(err)
	}
	if !gate.AnyInFlight() || !gate.InFlight("term-a") {
		t.Error("a registered dispatch is not reported as in flight")
	}
	if gate.InFlight("term-b") {
		t.Error("setup: term-b has nothing in flight, AnyInFlight must be about term-a")
	}

	gate.CancelDispatch("nonce-a", "term-a")
	if gate.AnyInFlight() {
		t.Error("still in flight after the dispatch was cancelled: the updater would wait forever")
	}
}
