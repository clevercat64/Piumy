package main

// Wires internal/wanotice to the real world (T190, ct-2026-11-11-101756):
// it decides WHEN to warn, this decides WHO gets it and in WHAT WORDS.
//
// Lives here, next to autoupdate_wire.go and for the same reason: it
// carries logic (text, language, routing, the empty-principal case), so it
// belongs somewhere a test can reach it. main.go keeps ONE line.

import (
	"context"
	"fmt"
	"log"
	"time"

	"piumy-gateway/internal/capipush"
	"piumy-gateway/internal/i18n"
	"piumy-gateway/internal/state"
	"piumy-gateway/internal/store"
	"piumy-gateway/internal/wanotice"
)

// injectorResolver hands back the antenna of a given agent —
// capipush.Pusher satisfies it. An interface so this wiring is testable
// without a live antenna.
type injectorResolver interface {
	InjectorFor(agentID string) (capipush.Injector, bool)
}

// noticeFrom is the `from` every WhatsApp-down notice carries. Its own
// value, not "piumy-dashboard" (the ping's) nor a chat's JID: the agent
// must be able to tell an automatic alert from a dispatched message
// without reading the text, because an alert needs no get_instructions and
// no reply.
const noticeFrom = "piumy-alert"

// startWANotice starts watching WhatsApp's state and warning the principal
// agent. Returns without starting — saying why — when there is nobody to
// warn: with no principal terminal configured, a watcher would spend the
// process's life deciding to send notices into the void.
func startWANotice(ctx context.Context, sm *state.Manager, st *store.Store, res injectorResolver, principal string) {
	if sm == nil || st == nil || res == nil {
		return
	}
	if principal == "" {
		log.Println("wanotice: sin terminal principal configurado (PIUMY_DEFAULT_TERMINAL_ID) — no hay a quién avisar si WhatsApp se cae")
		return
	}
	w := wanotice.New(sm, waNoticeSender(st, res, principal), func() time.Duration {
		return st.SettingDuration(store.SettingWADownNoticeAfter, wanotice.DefaultThreshold)
	})
	wanotice.Watch(ctx, w)
}

// waNoticeSender renders a Notice and injects it into the principal's
// antenna — the same path a dispatch takes, which is what the contract
// asked for ("mismo camino que un despacho").
//
// The text is the owner's configured dashboard language (the same global
// setting capipush's own automatic notices use, never a per-chat guess),
// and it ends with a stable machine line, "WA:<code>", mirroring the
// ping's own "NC:" convention: the prose is for whoever reads it, the code
// is so the agent can act without parsing a language.
//
// Nothing personal travels in here: no JID, no number, no name — the only
// variable parts are a closed reason code, a clock time and a duration.
func waNoticeSender(st *store.Store, res injectorResolver, principal string) wanotice.Sender {
	return func(n wanotice.Notice) error {
		inj, ok := res.InjectorFor(principal)
		if !ok {
			return fmt.Errorf("el agente principal %q no tiene antena registrada", principal)
		}

		raw, _ := st.KVGet(store.SettingLanguage)
		lang := i18n.Resolve(raw)

		var text, code string
		switch n.Kind {
		case wanotice.KindBack:
			text = i18n.T(lang, "server.wa_back_notice", "duration", humanDownFor(n.Down))
			code = "back"
		default:
			text = i18n.T(lang, "server.wa_down_notice",
				"reason", downReasonText(n.Reason, lang),
				"since", n.Since.Format("15:04"))
			code = string(n.Reason)
			if code == "" {
				// An unclassified outage still gets a code, so the agent
				// never receives a bare "WA:" it has to guess about.
				code = "unknown"
			}
		}
		return inj.Inject(principal, noticeFrom, text+"\nWA:"+code+"\n")
	}
}

// humanDownFor rounds a duration to the minute for the notice. Known
// ceiling, declared rather than hidden: it renders as Go's own "3h0m0s",
// not as "3 horas". Saying it in words needs plural rules per language and
// nobody asked for that — when someone does, it belongs in i18n, not here.
func humanDownFor(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}
