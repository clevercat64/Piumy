package whatsmeow

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	wstore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"piumy-gateway/internal/store"
)

// TestBackfillContactsSetsAgendaNameAndSkipsEmpty covers ct-2026-07-19-0115's
// core rule: chats.name gets PushName (the contact's own self-chosen name,
// via TouchChat), chats.contact_name gets the PHONE AGENDA name
// (FullName, falling back to FirstName) — never PushName — and an empty
// agenda name is never written (SetContactName not called at all), so a
// name already known from an earlier sweep can't be erased.
func TestBackfillContactsSetsAgendaNameAndSkipsEmpty(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &Adapter{store: st, actionDelayMin: time.Millisecond, actionDelayMax: 2 * time.Millisecond}

	withFullName := types.NewJID("111", "s.whatsapp.net")
	withFirstNameOnly := types.NewJID("222", "s.whatsapp.net")
	withNoAgendaName := types.NewJID("333", "s.whatsapp.net")

	contacts := map[types.JID]types.ContactInfo{
		withFullName:      {Found: true, FullName: "Juan Perez", FirstName: "Juan", PushName: "Juanito"},
		withFirstNameOnly: {Found: true, FirstName: "Maria", PushName: "Mari"},
		withNoAgendaName:  {Found: true, PushName: "SoloPushName"},
	}

	a.backfillContacts(context.Background(), contacts)

	c1, ok, err := st.GetChat(withFullName.String())
	if err != nil || !ok {
		t.Fatalf("GetChat(withFullName) = ok=%v err=%v", ok, err)
	}
	if c1.Name != "Juanito" {
		t.Errorf("c1.Name = %q, want the PushName \"Juanito\"", c1.Name)
	}
	if c1.ContactName != "Juan Perez" {
		t.Errorf("c1.ContactName = %q, want the agenda FullName \"Juan Perez\"", c1.ContactName)
	}

	c2, ok, err := st.GetChat(withFirstNameOnly.String())
	if err != nil || !ok {
		t.Fatalf("GetChat(withFirstNameOnly) = ok=%v err=%v", ok, err)
	}
	if c2.ContactName != "Maria" {
		t.Errorf("c2.ContactName = %q, want the FirstName fallback \"Maria\"", c2.ContactName)
	}

	c3, ok, err := st.GetChat(withNoAgendaName.String())
	if err != nil || !ok {
		t.Fatalf("GetChat(withNoAgendaName) = ok=%v err=%v", ok, err)
	}
	if c3.Name != "SoloPushName" {
		t.Errorf("c3.Name = %q, want the PushName \"SoloPushName\"", c3.Name)
	}
	if c3.ContactName != "" {
		t.Errorf("c3.ContactName = %q, want empty — no agenda name known, must not be written", c3.ContactName)
	}

	// A later re-sweep that finds no agenda name for a contact that DID
	// have one must not erase it (SetContactName is only ever called with
	// a non-empty name — the guard lives in backfillContacts itself).
	a.backfillContacts(context.Background(), map[types.JID]types.ContactInfo{
		withFullName: {Found: true, PushName: "Juanito"}, // FullName/FirstName now blank
	})
	c1Again, _, err := st.GetChat(withFullName.String())
	if err != nil {
		t.Fatal(err)
	}
	if c1Again.ContactName != "Juan Perez" {
		t.Errorf("after re-sweep with blank agenda name, ContactName = %q, want it to keep \"Juan Perez\"", c1Again.ContactName)
	}
}

// TestBackfillContactsPacesWithActionDelay verifies the anti-ban core of
// the sub: each contact incurs a real actionDelay().Sleep BEFORE its store
// write — not a batched/instant loop. A small-but-measurable window keeps
// the test fast while still proving pacing actually happens.
func TestBackfillContactsPacesWithActionDelay(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	const delay = 20 * time.Millisecond
	a := &Adapter{store: st, actionDelayMin: delay, actionDelayMax: delay}

	contacts := map[types.JID]types.ContactInfo{
		types.NewJID("1", "s.whatsapp.net"): {Found: true, PushName: "A"},
		types.NewJID("2", "s.whatsapp.net"): {Found: true, PushName: "B"},
		types.NewJID("3", "s.whatsapp.net"): {Found: true, PushName: "C"},
	}

	start := time.Now()
	a.backfillContacts(context.Background(), contacts)
	elapsed := time.Since(start)

	// 3 contacts * 20ms each, minus scheduler-jitter tolerance.
	if want := 2 * delay; elapsed < want {
		t.Errorf("backfillContacts(3 contacts) took %v, want at least %v (each contact must sleep actionDelay)", elapsed, want)
	}
}

// TestBackfillContactsRespectsCancellation verifies ct-2026-07-19-0115's
// "respeta ... cancelacion": an already-cancelled ctx stops the loop before
// touching the store at all — no partial/racy writes on shutdown.
func TestBackfillContactsRespectsCancellation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	a := &Adapter{store: st, actionDelayMin: time.Hour, actionDelayMax: time.Hour} // would hang the test if cancellation were ignored

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	jid := types.NewJID("111", "s.whatsapp.net")
	a.backfillContacts(ctx, map[types.JID]types.ContactInfo{
		jid: {Found: true, FullName: "Juan Perez", PushName: "Juanito"},
	})

	if _, ok, err := st.GetChat(jid.String()); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("backfillContacts wrote a chat despite an already-cancelled ctx")
	}
}

// TestSyncContactsNilStoreIsNoOp confirms the nil-safe convention (same as
// seedGroups): without a store, syncContacts must return before ever
// touching a.client — safe to call even with neither wired, same as every
// other optional-dependency guard in this package.
func TestSyncContactsNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.syncContacts(context.Background())
}

// TestScheduleContactsSyncNilStoreIsNoOp mirrors TestSyncContactsNilStoreIsNoOp
// for the debounced entry point — must not panic, must not arm a timer.
func TestScheduleContactsSyncNilStoreIsNoOp(t *testing.T) {
	a := &Adapter{}
	a.scheduleContactsSync()
	if a.contactsSyncTimer != nil {
		t.Error("scheduleContactsSync armed a timer despite a nil store")
	}
}

// contactsRunCounter counts syncContacts runs: every run opens with exactly
// one Store.Contacts.GetAllContacts call, so wrapping the real store shows
// how many runs a burst produced without a hook in production code.
type contactsRunCounter struct {
	wstore.ContactStore
	runs atomic.Int32
}

func (c *contactsRunCounter) GetAllContacts(ctx context.Context) (map[types.JID]types.ContactInfo, error) {
	c.runs.Add(1)
	return c.ContactStore.GetAllContacts(ctx)
}

// TestScheduleContactsSyncDebouncesBurstAndPicksUpData is the fix for
// ct-2026-07-31 ("no llegan contactos en una instalación nueva"), verified
// offline (no live WhatsApp account — Citrino's explicit condition):
// reproduces the real race with a genuine *whatsmeow.Client backed by an
// in-memory sqlite Store.Contacts, exactly like production, just without a
// live connection. 3 calls to scheduleContactsSync simulate a burst
// (multiple HistorySync/AppStateSyncComplete signals landing close together,
// as history.go's own doc records really happening — "4 chunks in ~7s"). A
// push name is written directly into Store.Contacts mid-burst (whatsmeow
// itself does this BEFORE dispatching the event we react to — see
// history.go's doc on handleHistoricalPushNames) to prove the eventual single
// syncContacts run picks up data that arrived DURING the debounce window,
// not just at schedule time.
//
// Wall-clock is the one thing this test cannot control, so it never bets on
// it (ct-2026-11-11-0126): the run is awaited by polling, runs are counted,
// and the timing each assertion depends on is measured — an attempt the host
// stalled past the window is retried, never failed.
func TestScheduleContactsSyncDebouncesBurstAndPicksUpData(t *testing.T) {
	const attempts = 5
	for i := 1; i <= attempts; i++ {
		if debounceBurstOnce(t) {
			return
		}
		t.Logf("attempt %d/%d: the host stalled the burst past the debounce window, retrying", i, attempts)
	}
	t.Fatalf("the host stalled the burst past the debounce window in all %d attempts", attempts)
}

// debounceBurstOnce runs one burst. It returns false — without judging
// anything — when the host paused long enough that a legitimate early run
// could be mistaken for a bug; every assertion failure is real and fatal.
func debounceBurstOnce(t *testing.T) bool {
	t.Helper()
	const (
		debounce = 100 * time.Millisecond
		// gap < debounce: each call lands before the previous timer is due,
		// so it must re-arm. 2*gap > debounce: without that re-arm the FIRST
		// timer would already have fired by the "not yet" check below — this
		// is what makes breaking the debounce turn the test red.
		gap = 60 * time.Millisecond
	)
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	client := newTestWmeowClient(t)
	counter := &contactsRunCounter{ContactStore: client.Store.Contacts}
	client.Store.Contacts = counter
	a := &Adapter{
		store:                st,
		client:               client,
		actionDelayMin:       time.Millisecond,
		actionDelayMax:       2 * time.Millisecond,
		contactsSyncDebounce: debounce,
	}

	jid := types.NewJID("111", "s.whatsapp.net")

	var began, returned [3]time.Time
	schedule := func(i int) {
		began[i] = time.Now()
		a.scheduleContactsSync()
		returned[i] = time.Now()
	}
	schedule(0)
	time.Sleep(gap)
	schedule(1)
	time.Sleep(gap)
	// The data "arrives" mid-burst, same order production sees: whatsmeow
	// writes Store.Contacts, THEN the signal we react to fires.
	if _, _, err := client.Store.Contacts.PutPushName(context.Background(), jid, "Recien Llegado"); err != nil {
		t.Fatal(err)
	}
	schedule(2) // re-arms again — the burst isn't over yet

	// A call that returned after the previous timer could have been due
	// means that timer may have run legitimately: the attempt proves nothing.
	for i := 1; i < len(began); i++ {
		if !returned[i].Before(began[i-1].Add(debounce)) {
			return false
		}
	}

	// Still inside the window from the LAST call, and past the one from the
	// FIRST: no run yet. Proves the burst coalesced instead of firing on the
	// first or second call.
	runs := counter.runs.Load()
	if !time.Now().Before(began[2].Add(debounce)) {
		return false // the read happened too late to say anything
	}
	if runs != 0 {
		t.Fatalf("syncContacts ran %d time(s) before the debounce window from the LAST call elapsed", runs)
	}

	// Wait for the run by polling, with a deadline far beyond any host stall:
	// the run is a paced, sqlite-backed pass whose duration is not ours.
	deadline := time.Now().Add(10 * time.Second)
	for {
		chat, ok, err := st.GetChat(jid.String())
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			if chat.Name != "Recien Llegado" {
				t.Errorf("chat.Name = %q, want the push name that arrived mid-burst", chat.Name)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("syncContacts never ran after the debounce window — the burst should have coalesced into exactly one run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if runs := counter.runs.Load(); runs != 1 {
		t.Errorf("the burst produced %d syncContacts runs, want exactly 1", runs)
	}
	return true
}
