package corepipeline

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"piumy-gateway/internal/gateway"
	"piumy-gateway/internal/governor"
	"piumy-gateway/internal/router"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
)

func TestControllerRunsPipelineEndToEnd(t *testing.T) {
	st, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()

	if !c.Status().Connected {
		t.Error("Status().Connected = false, want true (fakeGateway starts connected)")
	}

	fgw.inbound <- gateway.Inbound{ChatJID: "111@c.us", MsgID: "m1", Text: "hola", TS: 1}

	// No deadline of its own (T184): go test's -timeout bounds a wait for an
	// event that must happen, and dumps the stacks when it doesn't. A 2 s
	// bet lost to a CPU-starved machine, never to a bug.
	for {
		msgs, err := st.GetMessages("111@c.us", 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(msgs) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestControllerStartStopIdempotent(t *testing.T) {
	_, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c.Start(); err != nil { // second Start: no-op, not an error
		t.Fatal(err)
	}

	c.Stop()
	c.Stop() // second Stop: no-op, must not hang or panic
}

func TestControllerMarkReadNoOpWhenNotRunning(t *testing.T) {
	_, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)
	// Never started — MarkRead must return immediately, not panic on a nil
	// run context or block forever.
	c.MarkRead("111@c.us", nil)
}

// TestControllerStopWaitsForInFlightOutboxSend is a regression test: Run
// used to launch outboxLoop in its own untracked goroutine, so Stop() could
// return while a processOutbox call was still mid-flight — its trailing
// store writes (MarkSent/AddMessage) then race a caller's store.Close()
// right after Stop() returns ("database is closed", the flaky seen in
// openwa's TestEndToEndWithRealPipeline). Fixed by 6b326dc's sync.WaitGroup
// in Run (waits for outboxLoop too, not just inboundLoop) — verified still
// intact here.
//
// No wall-clock bet decides this test (T184, ct-2026-11-11-0845). It used to
// wait 2 s for Send to be entered and sleep a fixed 60 ms inside Send; under
// CPU starvation (24 spinning processes on 12 cores) it failed 9 runs in
// 100 — "Send was never entered", "Stop() never returned" and "Stop()
// returned before the in-flight Send finished" — while the quiet chain from
// Start to Send takes ~10 ms. No step of that chain was left unshrunk: the
// scheduler delayed the processes, not the code. So:
//   - the positive waits (Send entered, Stop returned) have no deadline of
//     their own — go test's -timeout is the bound, and it dumps the stacks;
//   - Send is held by fakeGateway.sendGate until the test releases it, so
//     Stop() cannot legitimately return meanwhile: the "still blocked" check
//     below can miss a bug under load (its goroutine woke late) but can
//     never fail a correct Stop().
//
// ct-2026-08-07: a failed run used to leave the Controller running, its
// outboxLoop ticking against a store the deferred st.Close() had closed
// ("database is closed"). stopOnce makes the deferred cleanup the SAME call
// as the mid-test Stop(), and releaseSend unwinds before it (LIFO) so a Send
// still held by the gate can never deadlock that cleanup — on every exit
// path, including an early t.Fatal.
func TestControllerStopWaitsForInFlightOutboxSend(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rt := router.NewManager(filepath.Join(t.TempDir(), "router.json"))
	gov := governor.NewLimiter(100, time.Minute)
	sm := state.NewManager(filepath.Join(t.TempDir(), "status.json"), 8)
	fgw := newFakeGateway()
	sendStarted := make(chan struct{}, 1)
	fgw.setSendStarted(sendStarted)
	sendGate := make(chan struct{})
	fgw.setSendGate(sendGate)

	if err := st.Enqueue("111@c.us", "hola", time.Now().Unix()); err != nil {
		t.Fatal(err)
	}

	p := New(fgw, st, rt, gov, sm, Config{
		OutboxPoll:       5 * time.Millisecond,
		DispatchDelayMin: time.Millisecond, DispatchDelayMax: 2 * time.Millisecond,
		ReadDelayMin: time.Millisecond, ReadDelayMax: 2 * time.Millisecond,
		ComposingMin: time.Millisecond, ComposingMax: 2 * time.Millisecond,
	})
	c := NewController(fgw, p)
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	stopOnce := sync.OnceFunc(func() {
		c.Stop()
		close(stopped)
	})
	releaseSend := sync.OnceFunc(func() { close(sendGate) })
	// Both run on EVERY exit path. Registered after defer st.Close(), so
	// they unwind first (LIFO), releaseSend before stopOnce: Stop() always
	// joins outboxLoop before the store closes.
	defer stopOnce()
	defer releaseSend()

	// Wait for outboxLoop to actually be blocked inside gw.Send — a real
	// event, not a timing guess.
	<-sendStarted

	go stopOnce()

	// Long enough for a Stop() that does NOT join to return; short enough
	// not to slow the passing run.
	const joinWindow = 50 * time.Millisecond
	select {
	case <-stopped:
		t.Fatal("Stop() returned before the in-flight Send finished — outboxLoop isn't joined")
	case <-time.After(joinWindow):
		// Still blocked while Send is held, as expected.
	}

	releaseSend()
	<-stopped

	if got := len(fgw.sentCalls()); got != 1 {
		t.Errorf("sent calls once Stop() returned = %d, want 1 — Stop() returned before the in-flight Send completed", got)
	}
}

func TestControllerResumeIsStart(t *testing.T) {
	_, _, _, fgw, p := newTestPipeline(t)
	c := NewController(fgw, p)
	if err := c.Resume(); err != nil {
		t.Fatal(err)
	}
	defer c.Stop()
	if !c.Status().Connected {
		t.Error("Status().Connected = false after Resume(), want true")
	}
}
