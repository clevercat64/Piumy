package capipush

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

// The seal CleverCoder puts on the answer to POST /message (T179,
// ct-2026-11-10-2336 — protocol §3 "Respuesta sellada", CleverCoder's
// ct-2026-11-10-2253): the AES-GCM covers the request, so without this a
// machine between the two could change the answer (a 404 for a 200, a forged
// "paused" with its own text) and Piumy would believe it.
//
// Ceiling, said by CleverCoder's own protocol and true here: over a PIN the
// handshake carries pinpass and token in the clear, so whoever saw it can build
// a valid seal too. The seal narrows the T175 hole (an attacker who did not
// see the handshake, or who only rewrites the answer); it does not close it.
// Closing it takes a pair or TLS.

const sealHeader = "X-Capi-Seal"

// maxAnswerBytes caps how much of an answer is read before checking its seal.
// CleverCoder's real answers are tiny JSON; this only keeps a hostile endpoint
// from making Piumy buffer without limit.
const maxAnswerBytes = 64 << 10

// errResponseTampered: the answer's seal doesn't match, or it's missing from a
// destination that already sealed once. The message WAS sent and Piumy doesn't
// know whether it entered, so it must not be re-sent blindly (the agent could
// answer twice). Travels as Inject's error, like errTerminalGone.
var errResponseTampered = errors.New("clever_injector: respuesta alterada — su sello no es el del destino")

// sealOf is the seal for an answer: v1= + HMAC-SHA256 under a key derived from
// the session key, over "<status>\n<nonce>\n<exact body bytes>". nonceB64 is
// the base64 string exactly as it went in the request.
func sealOf(sessionKey []byte, status int, nonceB64 string, body []byte) string {
	derive := hmac.New(sha256.New, sessionKey)
	derive.Write([]byte("capi-resp-v1"))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	fmt.Fprintf(mac, "%d\n%s\n", status, nonceB64)
	mac.Write(body)
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

// checkSeal says whether the answer to /message can be believed. nil = yes.
//
//	seal present, matches        → ok; the first one for this antenna is remembered
//	seal present, doesn't match  → tampered, always
//	no seal, antenna sealed once → tampered — except 401 and 5xx: CleverCoder
//	                               answers a 401 invalid_token (no session, so no
//	                               key) and its crashes without a seal by design,
//	                               and rejecting those would park the destination
//	                               on every CleverCoder restart
//	no seal, never sealed        → ok, as before T179
//
// Errors and logs carry the status and 8 hex of each seal, never the key, the
// token or a whole seal.
func (c *CleverInjector) checkSeal(terminalID string, key []byte, status int, nonceB64 string, body []byte, got string) error {
	if got != "" {
		want := sealOf(key, status, nonceB64, body)
		if !hmac.Equal([]byte(got), []byte(want)) {
			return tampered(status, "el sello no calza", got, want)
		}
		rememberSeal(c.seals, terminalID)
		return nil
	}
	if status == 401 || status >= 500 {
		return nil
	}
	if sealRemembered(c.seals, terminalID) {
		return tampered(status, "llegó sin sello de un destino que ya sellaba", "", "")
	}
	return nil
}

func tampered(status int, why, got, want string) error {
	return fmt.Errorf("%w: %s (HTTP %d, recibido %q, esperado %q)", errResponseTampered, why, status, shortSeal(got), shortSeal(want))
}

// shortSeal is the only form of a seal that may reach an error or a log: the
// first 8 hex after "v1=".
func shortSeal(seal string) string {
	h := strings.TrimPrefix(seal, "v1=")
	return h[:min(8, len(h))]
}

// KV is the slice of the store the seal memory needs (the same two methods
// autoupdate and driftreport take). nil = no memory: every function below then
// does nothing and checkSeal only ever rejects a seal that doesn't match.
type KV interface {
	KVGet(key string) (string, error)
	KVSet(key, val string) error
}

// sealSeenKey is per ANTENNA chat_id, not per agent slot: injectors are rebuilt
// on every upsert and PromoteToPrincipal moves credentials between slots, but
// the destination — and whether it seals — travels with its chat_id.
func sealSeenKey(terminalID string) string { return "capipush.seal_seen:" + terminalID }

func sealRemembered(kv KV, terminalID string) bool {
	if kv == nil || terminalID == "" {
		return false
	}
	seen, err := kv.KVGet(sealSeenKey(terminalID))
	if err != nil {
		log.Printf("capipush: leer si %s ya sellaba: %v", terminalID, err)
		return false
	}
	return seen != ""
}

// rememberSeal writes the time of the first valid seal; later ones leave it be.
func rememberSeal(kv KV, terminalID string) {
	if kv == nil || terminalID == "" || sealRemembered(kv, terminalID) {
		return
	}
	if err := kv.KVSet(sealSeenKey(terminalID), time.Now().UTC().Format(time.RFC3339)); err != nil {
		log.Printf("capipush: recordar que %s sella: %v", terminalID, err)
		return
	}
	log.Printf("capipush: %s ya sella sus respuestas — desde ahora Piumy las exige", terminalID)
}

// ForgetSeal is the owner vouching for a destination again: every path that
// re-saves its credentials calls it (SetConfig for the principal, main.go's
// onAgentUpsert for a secondary), and boot does not. Without it a destination
// that went back to a CleverCoder build without the seal would be refused for
// good, with no screen to clear it.
func ForgetSeal(kv KV, terminalID string) {
	if kv == nil || terminalID == "" {
		return
	}
	if err := kv.KVSet(sealSeenKey(terminalID), ""); err != nil {
		log.Printf("capipush: olvidar el sello de %s: %v", terminalID, err)
	}
}
