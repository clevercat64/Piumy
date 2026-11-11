package main

import (
	"bytes"
	"image/color"
	"strings"
	"testing"

	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/state"
)

const trayTestTitle = "Piumy Gateway"
const trayTestJID = "555000001@s.whatsapp.net"

func TestTrayShowsNoAlertWhileConnected(t *testing.T) {
	look := appearanceFor(state.Status{OwnJID: trayTestJID, WAConnected: true, Mood: "idle"}, trayTestTitle, i18n.Lang("es"))
	if look.Alert {
		t.Error("conectado no debe marcar alerta")
	}
	if look.Tooltip != trayTestTitle {
		t.Errorf("conectado el tooltip debe quedar como estaba; fue %q", look.Tooltip)
	}
}

func TestTrayShowsTheMotiveWhileDown(t *testing.T) {
	look := appearanceFor(
		state.Status{OwnJID: trayTestJID, WAConnected: false, Mood: "error", WADownReason: state.WADownLoggedOut},
		trayTestTitle, i18n.Lang("es"))

	if !look.Alert {
		t.Fatal("WhatsApp caído debe marcar alerta")
	}
	// The hover text has to say BOTH things: which account and what is
	// wrong. The account identity is never dropped (boss's rule) and the
	// mouse hovers before the click.
	if !strings.Contains(look.Tooltip, trayTestTitle) {
		t.Errorf("el tooltip perdió la identidad de la cuenta: %q", look.Tooltip)
	}
	if !strings.Contains(look.Tooltip, "desvinculado") {
		t.Errorf("el tooltip no dice el motivo: %q", look.Tooltip)
	}
}

func TestTrayFallsBackToTheGenericMotive(t *testing.T) {
	look := appearanceFor(
		state.Status{OwnJID: trayTestJID, WAConnected: false, Mood: "error", WADownReason: ""},
		trayTestTitle, i18n.Lang("es"))

	if !look.Alert {
		t.Fatal("caído sin motivo clasificado sigue siendo una caída")
	}
	if !strings.Contains(look.Tooltip, "desconectado") {
		t.Errorf("sin motivo debe caer al genérico: %q", look.Tooltip)
	}
}

func TestTrayDoesNotAlertWhileWaitingForTheFirstQR(t *testing.T) {
	cases := map[string]state.Status{
		"sin JID":        {WAConnected: false},
		"mostrando QR":   {OwnJID: trayTestJID, WAConnected: false, ShowQR: true},
		"con cara de QR": {OwnJID: trayTestJID, WAConnected: false, Mood: "qr"},
	}
	for name, st := range cases {
		t.Run(name, func(t *testing.T) {
			if appearanceFor(st, trayTestTitle, i18n.Lang("es")).Alert {
				t.Error("esperar el primer QR no es una caída: no debe marcar alerta")
			}
		})
	}
}

// The default account (no account name, hue 0) is the most common install.
// This is the same trap AlertTrayIcon's own test guards, checked one level
// up: at the wiring, where the tray actually asks for its icon.
func TestTrayIconForMarksTheDefaultAccount(t *testing.T) {
	base := makeTestICO(t, uniformTestIcon(32, color.NRGBA{R: 0x20, G: 0x80, B: 0xC0, A: 0xFF}))

	plain := trayIconFor(base, "", false)
	if !bytes.Equal(plain, base) {
		t.Error("sin alerta y sin cuenta no hay nada que pintar: debe devolver el ícono tal cual")
	}

	alerted := trayIconFor(base, "", true)
	if bytes.Equal(alerted, base) {
		t.Fatal("la cuenta POR DEFECTO quedó sin marca de alerta en el cableado del tray")
	}
	if red, _ := countMarkPixels(t, decodeICOFirstImage(t, alerted)); red == 0 {
		t.Error("no se pintó la marca para la cuenta por defecto")
	}
}

// A broken icon must leave the tray with the brand icon, never with none.
func TestTrayIconForFallsBackToTheBaseIcon(t *testing.T) {
	broken := []byte{1, 2, 3, 4, 5}
	if got := trayIconFor(broken, "", true); !bytes.Equal(got, broken) {
		t.Errorf("con un ícono ilegible debe devolver la base; devolvió %v", got)
	}
}
