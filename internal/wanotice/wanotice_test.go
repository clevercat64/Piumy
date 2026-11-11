package wanotice

import (
	"testing"
	"time"

	"piumy-gateway/internal/state"
)

// testJID is the synthetic identifier the constitution requires (§2b): the
// 555 prefix, never a number that could belong to someone. A paired
// install is exactly "OwnJID is set and we are not showing a QR", so every
// case here needs one.
const testJID = "555000001@s.whatsapp.net"

const threshold = 5 * time.Minute

// collect builds a Watcher whose Sender appends to a slice, so a test
// asserts on WHAT was sent, not just how many — a notice with the wrong
// reason is a defect this contract cares about (the owner being told the
// wrong motive is the bug T190 exists to fix).
func collect(t *testing.T) (*Watcher, *[]Notice) {
	t.Helper()
	var got []Notice
	w := New(fakeSource{}, func(n Notice) error {
		got = append(got, n)
		return nil
	}, func() time.Duration { return threshold })
	return w, &got
}

// fakeSource satisfies StateSource for New; the tests drive Observe
// directly with their own clock and status, so Snapshot is never used.
type fakeSource struct{}

func (fakeSource) Snapshot() state.Status { return state.Status{} }

func downStatus(reason state.WADownReason) state.Status {
	return state.Status{OwnJID: testJID, WAConnected: false, Mood: "error", WADownReason: reason}
}

func upStatus() state.Status {
	return state.Status{OwnJID: testJID, WAConnected: true, Mood: "idle"}
}

func TestLoggedOutNotifiesOnTheFirstLookAndNeverAgain(t *testing.T) {
	w, got := collect(t)
	t0 := time.Now()

	// No threshold wait: WhatsApp unlinked the account, which cannot fix
	// itself — the case this contract was written for.
	w.Observe(t0, downStatus(state.WADownLoggedOut))
	if len(*got) != 1 {
		t.Fatalf("una desvinculación debe avisar en la primera mirada; avisos=%d", len(*got))
	}
	if (*got)[0].Kind != KindDown || (*got)[0].Reason != state.WADownLoggedOut {
		t.Errorf("aviso equivocado: %+v", (*got)[0])
	}

	// Twenty more looks over an hour: still one notice. "Una vez por
	// incidente, no en bucle" is in the contract's own words.
	for i := 1; i <= 20; i++ {
		w.Observe(t0.Add(time.Duration(i)*3*time.Minute), downStatus(state.WADownLoggedOut))
	}
	if len(*got) != 1 {
		t.Fatalf("el aviso se repitió: avisos=%d, esperado 1", len(*got))
	}
}

// TestABlipUnderTheThresholdStaysSilent is the "y no antes" half of the
// contract, and the control of this whole file: if the threshold guard ever
// breaks, THIS is the test that catches it. A result I know in advance.
func TestABlipUnderTheThresholdStaysSilent(t *testing.T) {
	w, got := collect(t)
	t0 := time.Now()

	w.Observe(t0, downStatus(state.WADownNoNetwork))
	w.Observe(t0.Add(1*time.Minute), downStatus(state.WADownNoNetwork))
	w.Observe(t0.Add(4*time.Minute), downStatus(state.WADownNoNetwork))
	if len(*got) != 0 {
		t.Fatalf("un parpadeo de red bajo el umbral no debe avisar; avisos=%d: %+v", len(*got), *got)
	}

	// Past the threshold, exactly one.
	w.Observe(t0.Add(threshold), downStatus(state.WADownNoNetwork))
	if len(*got) != 1 {
		t.Fatalf("pasado el umbral debe avisar una vez; avisos=%d", len(*got))
	}
	if (*got)[0].Reason != state.WADownNoNetwork {
		t.Errorf("motivo equivocado: %q", (*got)[0].Reason)
	}
}

func TestReconnectingSendsExactlyOneBackNotice(t *testing.T) {
	w, got := collect(t)
	t0 := time.Now()

	w.Observe(t0, downStatus(state.WADownLoggedOut))
	w.Observe(t0.Add(10*time.Minute), upStatus())
	// Three more connected looks: the "volvió" must not repeat either.
	w.Observe(t0.Add(11*time.Minute), upStatus())
	w.Observe(t0.Add(12*time.Minute), upStatus())

	if len(*got) != 2 {
		t.Fatalf("esperado 1 aviso de caída + 1 de vuelta; avisos=%d: %+v", len(*got), *got)
	}
	back := (*got)[1]
	if back.Kind != KindBack {
		t.Fatalf("el segundo aviso debe ser el de vuelta; fue %q", back.Kind)
	}
	if back.Down != 10*time.Minute {
		t.Errorf("la duración informada debe ser la medida (10m); fue %s", back.Down)
	}
}

// An outage nobody was told about must not produce a "volvió" either:
// announcing a recovery from a silent blip would tell the owner about a
// problem they never had.
func TestASilentBlipProducesNoNoticeAtAll(t *testing.T) {
	w, got := collect(t)
	t0 := time.Now()

	w.Observe(t0, downStatus(state.WADownNoNetwork))
	w.Observe(t0.Add(1*time.Minute), upStatus())

	if len(*got) != 0 {
		t.Fatalf("un parpadeo silencioso no debe avisar ni de ida ni de vuelta; avisos=%+v", *got)
	}
}

// Waiting for the first QR scan is not an outage. Without this guard a
// fresh install would tell the agent that a WhatsApp that was never linked
// had gone down.
func TestWaitingForTheFirstQRIsNotAnOutage(t *testing.T) {
	cases := map[string]state.Status{
		"sin JID":        {WAConnected: false},
		"mostrando QR":   {OwnJID: testJID, WAConnected: false, ShowQR: true},
		"con cara de QR": {OwnJID: testJID, WAConnected: false, Mood: "qr"},
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			w, got := collect(t)
			t0 := time.Now()
			for i := 0; i < 10; i++ {
				w.Observe(t0.Add(time.Duration(i)*2*time.Minute), st)
			}
			if len(*got) != 0 {
				t.Fatalf("no debe avisar sin cuenta vinculada; avisos=%+v", *got)
			}
		})
	}
}

// An outage that starts as a network drop and turns into a real unlink is
// NEW information the owner has to act on, so it earns one more notice —
// and only one.
func TestAnEscalatingReasonEarnsOneMoreNotice(t *testing.T) {
	w, got := collect(t)
	t0 := time.Now()

	w.Observe(t0, downStatus(state.WADownNoNetwork))
	w.Observe(t0.Add(threshold), downStatus(state.WADownNoNetwork))
	if len(*got) != 1 {
		t.Fatalf("preparación: esperaba 1 aviso por el corte de red, hubo %d", len(*got))
	}

	w.Observe(t0.Add(threshold+time.Minute), downStatus(state.WADownLoggedOut))
	w.Observe(t0.Add(threshold+2*time.Minute), downStatus(state.WADownLoggedOut))
	w.Observe(t0.Add(threshold+3*time.Minute), downStatus(state.WADownLoggedOut))

	if len(*got) != 2 {
		t.Fatalf("un motivo que escala debe avisar UNA vez más; avisos=%d: %+v", len(*got), *got)
	}
	if (*got)[1].Reason != state.WADownLoggedOut {
		t.Errorf("el segundo aviso debe traer el motivo nuevo; trajo %q", (*got)[1].Reason)
	}
}

// Flapping: five full down/up cycles, each one under the threshold. The
// owner must not get a single notice out of a flaky network.
func TestFlappingUnderTheThresholdNeverNotifies(t *testing.T) {
	w, got := collect(t)
	t0 := time.Now()

	for i := 0; i < 5; i++ {
		base := t0.Add(time.Duration(i) * 10 * time.Minute)
		w.Observe(base, downStatus(state.WADownNoNetwork))
		w.Observe(base.Add(2*time.Minute), downStatus(state.WADownNoNetwork))
		w.Observe(base.Add(3*time.Minute), upStatus())
	}
	if len(*got) != 0 {
		t.Fatalf("el flapping bajo el umbral no debe avisar; avisos=%d: %+v", len(*got), *got)
	}
}

// A failing Sender must not turn into a retry loop: one attempt per
// incident, which is what Citrino approved (the dashboard and the tray
// still show the outage).
func TestAFailedSendIsNotRetriedEveryLook(t *testing.T) {
	attempts := 0
	w := New(fakeSource{}, func(Notice) error {
		attempts++
		return errSendFailed
	}, func() time.Duration { return threshold })

	t0 := time.Now()
	for i := 0; i < 10; i++ {
		w.Observe(t0.Add(time.Duration(i)*time.Minute), downStatus(state.WADownLoggedOut))
	}
	if attempts != 1 {
		t.Fatalf("un envío fallido no debe reintentarse en cada mirada; intentos=%d", attempts)
	}
}

var errSendFailed = errSend("antena caída")

type errSend string

func (e errSend) Error() string { return string(e) }
