package main

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"piumy-gateway/internal/capipush"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
	"piumy-gateway/internal/wanotice"
)

const principalID = "terminal-principal"

type sentInjection struct {
	terminalID, from, payload string
}

type fakeInjector struct{ sent *[]sentInjection }

func (f fakeInjector) Inject(terminalID, from, payload string) error {
	*f.sent = append(*f.sent, sentInjection{terminalID, from, payload})
	return nil
}

// fakeResolver hands back an antenna only for the id it was built with, so
// a test can also exercise "the principal has no antenna registered".
type fakeResolver struct {
	id   string
	sent *[]sentInjection
}

func (r fakeResolver) InjectorFor(agentID string) (capipush.Injector, bool) {
	if agentID != r.id {
		return nil, false
	}
	return fakeInjector{sent: r.sent}, true
}

// wireFixture: a real store (the sender reads the language from it) with
// the language PINNED. Never left to i18n.Detect(): that reads the host's
// locale, so the Spanish assertions below would silently measure an
// English catalog on an English machine.
func wireFixture(t *testing.T, lang string) (*store.Store, fakeResolver, *[]sentInjection) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "piumy.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.KVSet(store.SettingLanguage, lang); err != nil {
		t.Fatalf("fijando idioma: %v", err)
	}
	var sent []sentInjection
	return st, fakeResolver{id: principalID, sent: &sent}, &sent
}

func TestTheDownNoticeSaysTheMotiveAndCarriesTheStableCode(t *testing.T) {
	st, res, sent := wireFixture(t, "es")
	send := waNoticeSender(st, res, principalID)

	err := send(wanotice.Notice{
		Kind:   wanotice.KindDown,
		Reason: state.WADownLoggedOut,
		// Una fecha y hora inventadas, no las de un incidente real: lo que
		// se prueba es que la hora de inicio llega al texto.
		Since: time.Date(2026, 1, 2, 9, 30, 0, 0, time.Local),
		Down:  2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	if len(*sent) != 1 {
		t.Fatalf("esperaba 1 inyección, hubo %d", len(*sent))
	}
	got := (*sent)[0]

	if got.terminalID != principalID {
		t.Errorf("el aviso debe ir al principal; fue a %q", got.terminalID)
	}
	if got.from != noticeFrom {
		t.Errorf("from = %q, esperado %q (el agente distingue un aviso de un despacho sin leer el texto)", got.from, noticeFrom)
	}
	// El motivo, en palabras y en el idioma fijado — no el código crudo.
	if !strings.Contains(got.payload, "desvinculado") {
		t.Errorf("el aviso no dice el motivo en palabras: %q", got.payload)
	}
	// Y qué hacer.
	if !strings.Contains(got.payload, "Conectar QR") {
		t.Errorf("el aviso no dice qué hacer: %q", got.payload)
	}
	// La hora de inicio.
	if !strings.Contains(got.payload, "09:30") {
		t.Errorf("el aviso no dice desde cuándo: %q", got.payload)
	}
	// El código estable para que el agente actúe sin parsear idioma.
	if !strings.Contains(got.payload, "WA:logged_out") {
		t.Errorf("falta el código estable WA:logged_out: %q", got.payload)
	}
}

// The whole point of the closed reason set is that an automatic notice
// cannot leak anything personal. This asserts it on the payload itself
// rather than trusting the design.
func TestANoticeCarriesNoPersonalData(t *testing.T) {
	st, res, sent := wireFixture(t, "es")
	send := waNoticeSender(st, res, principalID)

	if err := send(wanotice.Notice{
		Kind:   wanotice.KindDown,
		Reason: state.WADownTempBan,
		Since:  time.Now(),
		Down:   time.Hour,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	payload := (*sent)[0].payload

	if strings.Contains(payload, "@") {
		t.Errorf("el aviso lleva un arroba — ¿un JID o un email se colaron?: %q", payload)
	}
	// Seven or more digits in a row is the shape of a phone number; the
	// notice's only numbers are a clock time and a duration.
	if m := regexp.MustCompile(`\d{7,}`).FindString(payload); m != "" {
		t.Errorf("el aviso lleva una cifra larga (%q), sospechosa de teléfono: %q", m, payload)
	}
}

func TestTheBackNoticeSaysHowLongItWasDown(t *testing.T) {
	st, res, sent := wireFixture(t, "es")
	send := waNoticeSender(st, res, principalID)

	if err := send(wanotice.Notice{
		Kind:   wanotice.KindBack,
		Reason: state.WADownNoNetwork,
		Since:  time.Now().Add(-10 * time.Minute),
		Down:   10 * time.Minute,
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	payload := (*sent)[0].payload

	if !strings.Contains(payload, "volvió a conectarse") {
		t.Errorf("el aviso de vuelta no dice que volvió: %q", payload)
	}
	if !strings.Contains(payload, "10m") {
		t.Errorf("el aviso de vuelta no dice cuánto estuvo caído: %q", payload)
	}
	if !strings.Contains(payload, "WA:back") {
		t.Errorf("el aviso de vuelta debe traer WA:back, no el motivo: %q", payload)
	}
}

// An outage nothing classified must still produce a usable notice: the
// generic motive and an explicit code, never a dangling "WA:".
func TestAnUnclassifiedOutageStillGetsACode(t *testing.T) {
	st, res, sent := wireFixture(t, "es")
	send := waNoticeSender(st, res, principalID)

	if err := send(wanotice.Notice{Kind: wanotice.KindDown, Reason: "", Since: time.Now()}); err != nil {
		t.Fatalf("send: %v", err)
	}
	payload := (*sent)[0].payload

	if !strings.Contains(payload, "WA:unknown") {
		t.Errorf("un motivo vacío debe viajar como WA:unknown: %q", payload)
	}
	if strings.Contains(payload, "WA:\n") {
		t.Errorf("quedó un código vacío colgando: %q", payload)
	}
	if !strings.Contains(payload, "desconectado") {
		t.Errorf("sin motivo clasificado debe decir el genérico: %q", payload)
	}
}

// The same notice in the other language, to prove the text really comes
// from the catalog and not from a string baked into the wiring.
func TestTheNoticeFollowsTheConfiguredLanguage(t *testing.T) {
	st, res, sent := wireFixture(t, "en")
	send := waNoticeSender(st, res, principalID)

	if err := send(wanotice.Notice{Kind: wanotice.KindDown, Reason: state.WADownLoggedOut, Since: time.Now()}); err != nil {
		t.Fatalf("send: %v", err)
	}
	payload := (*sent)[0].payload

	if !strings.Contains(payload, "unlinked") {
		t.Errorf("en inglés el motivo debe ser 'unlinked': %q", payload)
	}
	if strings.Contains(payload, "desvinculado") {
		t.Errorf("salió texto español con el idioma en inglés: %q", payload)
	}
}

// No antenna for the principal is an error the watcher logs — never a
// silent success that would make us believe the owner was warned.
func TestNoAntennaForThePrincipalIsAnError(t *testing.T) {
	st, res, sent := wireFixture(t, "es")
	send := waNoticeSender(st, res, "otro-terminal") // no es el id del resolver

	if err := send(wanotice.Notice{Kind: wanotice.KindDown, Reason: state.WADownLoggedOut, Since: time.Now()}); err == nil {
		t.Error("sin antena registrada el envío debe fallar, no pasar en silencio")
	}
	if len(*sent) != 0 {
		t.Errorf("no debió inyectar nada; inyectó %d", len(*sent))
	}
	_ = res
}
