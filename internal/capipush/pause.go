package capipush

import (
	"fmt"
	"log"
	"strings"
	"time"

	"piumy-gateway/internal/store"
)

// PausedError is CleverCoder's answer when the chat's client is paused by its
// cuentakilómetros (T175, ct-2026-11-10-2019): 200 {"paused":true,...} and
// NOTHING reached an agent. Not a delivery failure — dispatch closes the burst
// and answers the chat itself (answerPausedClient). It travels as the error of
// Injector.Inject so the interface (3 implementers) stays as it was.
type PausedError struct {
	Client   string // CleverCoder's key for the client (first field of the envelope's "de:")
	ReopenAt string // when the pause ends, RFC 3339 with offset; "" if CleverCoder didn't say
	Text     string // the reply CleverCoder wrote; "" from a CleverCoder older than T175
	PayURL   string // payment link, "" if the client has none
}

func (e *PausedError) Error() string {
	return fmt.Sprintf("clever_injector: cliente %q pausado hasta %q", e.Client, e.ReopenAt)
}

// notice is what the chat gets: CleverCoder's text, plus the payment link when
// the text doesn't already carry it (its default text has the link filled in —
// appending it again would send it twice). "" when CleverCoder sent no text:
// Piumy never invents one.
func (e *PausedError) notice() string {
	text := strings.TrimSpace(e.Text)
	if text == "" {
		return ""
	}
	if e.PayURL != "" && !strings.Contains(text, e.PayURL) {
		text += "\n" + e.PayURL
	}
	return text
}

// pauseNoticeKey is the KV key holding, per chat, which pause (its reopen_at)
// was already answered — persisted so a restart doesn't repeat the notice.
func pauseNoticeKey(chatJID string) string { return "capipush.pause_notice:" + chatJID }

// answerPausedClient closes the burst (otherwise the next sweep would
// re-dispatch it in a loop) and answers the chat ONCE per pause: the notice
// goes through the outbox, so the governor's anti-ban pacing applies like to
// any other reply. A later message in the same pause is closed in silence.
//
// Order matters: notice, then marker, then close. A failure in between
// retries on the next sweep, which can at worst repeat the notice — never
// leave the chat without one.
func (p *Pusher) answerPausedClient(chatJID, sender string, burst []store.Message, pause *PausedError) error {
	marker := pause.ReopenAt
	if marker == "" {
		marker = "-" // keeps "answered once" true when CleverCoder doesn't say when the pause ends
	}
	answered, err := p.store.KVGet(pauseNoticeKey(chatJID))
	if err != nil {
		return err
	}
	if answered != marker {
		if notice := pause.notice(); notice != "" {
			if err := p.store.Enqueue(chatJID, notice, time.Now().Unix()); err != nil {
				return err
			}
			log.Printf("capipush: cliente %s pausado hasta %s — aviso encolado al chat %s", pause.Client, marker, chatJID)
		} else {
			log.Printf("capipush: cliente %s pausado hasta %s, pero CleverCoder no mandó el texto del aviso (¿versión anterior a T175?) — el chat %s queda sin aviso", pause.Client, marker, chatJID)
		}
		if err := p.store.KVSet(pauseNoticeKey(chatJID), marker); err != nil {
			return err
		}
	}
	newest := burst[len(burst)-1].TS
	if sender != "" {
		return p.store.MarkHandledBeforeForSender(chatJID, sender, newest)
	}
	return p.store.MarkHandledBefore(chatJID, newest)
}

// clearPauseNotice forgets the answered pause once a dispatch of the chat
// reaches an agent again: the client was reopened ("Reabrir ahora", a higher
// cap), so a new exhaustion with the SAME reopen_at (same week) is a new pause
// and deserves its own notice. Without this it would stay silent — the defect
// T175 exists to fix.
func (p *Pusher) clearPauseNotice(chatJID string) {
	answered, err := p.store.KVGet(pauseNoticeKey(chatJID))
	if err != nil || answered == "" {
		return
	}
	if err := p.store.KVSet(pauseNoticeKey(chatJID), ""); err != nil {
		log.Printf("capipush: limpiar el aviso de pausa de %s: %v", chatJID, err)
	}
}
