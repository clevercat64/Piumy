package capipush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

// CleverInjector is the real Injector: it speaks CleverCoder's
// external-agent-api (ct-2026-07-05-2141) to deliver a dispatch into one
// CleverCoder terminal's PTY. Protocol replicated exactly from
// C:\proyectos\Piumy\cAPI\external-agent-protocol.md §2-§3.
//
// The AES-256-GCM sealing below (deriveKey/postMessage) IS the real
// protection — the cAPI session key, negotiated per terminal in the
// handshake, covers the LAN hop to CleverCoder's server end to end.
// piumy-gateway used to wrap a SECOND, separate ciphertext inside the
// envelope's "message" field (internal/capi, keyed by PIUMY_CAPI_KEY) —
// removed in T28 (ct-2026-08-05-2242, boss decision): that inner layer
// only protected the payload from CleverCoder itself, and CleverCoder is
// the boss's own program, on his own machine — nothing to protect against.
// Inject's payload argument is carried opaque, verbatim, as the envelope's
// "message" field, same as before; it's just plain text now, not
// ciphertext-of-ciphertext.
//
// Ceilings, not solved here (protocol constraints, not injector bugs):
//   - Delivery is instantaneous (protocol §5, ct-2026-07-08-1852): the
//     server injects into the destination PTY within the /message request —
//     no queue, no timer, nothing buffered server-side. A 404 means the
//     terminal isn't listening (antenna off / tab closed); capipush just
//     retries on the next sweep.
//   - pinpass is ephemeral (dies when the antenna is switched off) —
//     permanent provisioning is post-MVP.
type CleverInjector struct {
	mu sync.Mutex // protects Endpoint/TerminalID/Pinpass/token/key/dead

	Endpoint   string // e.g. http://192.168.1.50:8787
	TerminalID string // the antenna's chat_id/guid
	Pinpass    string // the antenna's pinpass, verbatim as copied (base64 form)

	httpClient *http.Client

	// seals remembers which antennas have already sealed their answers (T179,
	// seal.go). Set once by the constructor; nil = no memory.
	seals KV

	token string
	key   []byte

	// dead is set once a handshake comes back terminal_gone (T32,
	// ct-2026-08-06-1109) or an answer's seal is wrong (T179) — see
	// markDead/Configured. Only SetConfig, a fresh credential, clears it.
	dead bool

	// scanPorts are the loopback ports relocate() sweeps for this terminal's
	// antenna; nil = CleverCoder's own range (cleverPorts). A test seam: an
	// empty non-nil slice turns the sweep off.
	scanPorts []int
}

// NewCleverInjector builds a ready-to-use injector; sessions start empty and
// handshake lazily on the first Inject call. seals is where "this antenna
// already seals its answers" lives between restarts (T179); nil = none.
func NewCleverInjector(endpoint, terminalID, pinpass string, seals KV) *CleverInjector {
	return &CleverInjector{
		Endpoint:   endpoint,
		TerminalID: terminalID,
		Pinpass:    pinpass,
		seals:      seals,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// SetConfig updates the antenna credentials and forces a re-handshake on the
// next Inject call. Safe to call concurrently with Inject (dashboard HTTP
// handler vs sweep goroutine). Re-saving a destination also makes the injector
// forget that it sealed (ForgetSeal, T179): the next valid seal teaches it again.
func (c *CleverInjector) SetConfig(endpoint, terminalID, pinpass string) {
	c.mu.Lock()
	c.Endpoint, c.TerminalID, c.Pinpass = endpoint, terminalID, pinpass
	c.token, c.key = "", nil
	c.dead = false
	c.mu.Unlock()
	ForgetSeal(c.seals, terminalID)
}

// Configured reports whether this injector has real credentials wired yet
// (S6, ct-2026-07-30-031048) — false right after main.go builds one with an
// empty endpoint (cAPI never configured, or credentials cleared). Lets
// dispatch() treat an unconfigured CleverInjector exactly like LogInjector
// (retained, no attempt) instead of trying a real Inject() against an empty
// endpoint. This is what makes hot-reload actually hot: main.go always
// registers this SAME live pointer as the principal's injector, whether or
// not it starts configured, so set_capi_connector's SetConfig call reaches
// the object dispatch() actually uses — no restart, no orphaned instance.
func (c *CleverInjector) Configured() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Endpoint != "" && !c.dead
}

// markDead discards this injector's credential (T32, ct-2026-08-06-1109):
// a terminal_gone handshake means the id will never resolve — CleverCoder's
// own classification (external-agent-protocol.md §2, ct-2026-08-06-0221),
// not a guess on this side. Treating it exactly like "never configured"
// reuses Configured()'s existing check instead of adding a second flag
// dispatch() would have to know about — future sweeps skip this injector
// quietly (the same "sin antena" path an unconfigured one takes), no
// separate wiring needed. Only SetConfig (a fresh credential) clears it.
// T179 parks a destination whose answer's seal is wrong the same way: a
// channel nobody can vouch for gets no further message until its owner
// re-saves it.
func (c *CleverInjector) markDead() {
	c.mu.Lock()
	c.dead = true
	c.mu.Unlock()
}

// TestHandshake probes the current config with a single handshake and returns
// any error. Does NOT update the live session token — probe only. Relocates
// like Inject does (T177), so a probe never reports "down" for an antenna the
// next dispatch would find.
func (c *CleverInjector) TestHandshake() error {
	err := c.probe()
	if relocatable(err) && c.relocate() {
		err = c.probe()
	}
	return err
}

func (c *CleverInjector) probe() error {
	endpoint, terminalID, pinpass, _, _ := c.snapshot()
	if endpoint == "" {
		return fmt.Errorf("clever_injector: endpoint not configured")
	}
	_, _, err := c.handshake(endpoint, terminalID, pinpass)
	return err
}

// relocatable says whether a failure may just mean the antenna moved: nobody
// answers where we knocked (refused, timeout — any net.Error), or the server
// that answered doesn't know the terminal (terminal_gone, which on a port
// another CleverCoder project now holds is not final).
func relocatable(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) || errors.Is(err, errTerminalGone)
}

// relocate looks for this terminal's antenna on the loopback ports and, when
// it sits somewhere other than the configured endpoint, points the injector
// there — in memory only: the registered endpoint in the DB stays as the
// owner left it, and a restart re-discovers on the first failure. Reports
// whether the injector moved (so the caller retries once).
func (c *CleverInjector) relocate() bool {
	endpoint, terminalID, _, _, _ := c.snapshot()
	ports := c.scanPorts
	if ports == nil {
		ports = cleverPorts()
	}
	found, ok := discoverLoopback(ports, terminalID)
	if !ok || found == endpoint {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// A fresh SetConfig may have landed while we swept: its credential wins.
	if c.Endpoint != endpoint || c.TerminalID != terminalID {
		return false
	}
	c.Endpoint = found
	c.token, c.key = "", nil
	log.Printf("capipush: la antena de %s cambió de lugar: %s -> %s (hallada en loopback)", terminalID, endpoint, found)
	return true
}

var errUnauthorized = errors.New("clever_injector: unauthorized")

// errTerminalGone marks a handshake failure as PERMANENT (T32,
// ct-2026-08-06-1109 — protocol §2, ct-2026-08-06-0221): the id CleverCoder
// was handed doesn't correspond to anything it knows — not a live GUID, not
// a resolvable project/agent/position. Retrying can't fix that, unlike
// antenna_off/position_empty (the id IS known, just nothing's there right
// now) or any code this side doesn't recognize (an older CleverCoder, or a
// future one) — both of those fall through to the existing generic,
// unbounded retry (see handshake below), same as before this contract.
var errTerminalGone = errors.New("clever_injector: terminal_gone — no corresponde a nada, no se reintenta")

// Inject satisfies the Injector interface. terminalID (piumy's own routing
// id, e.g. "smoke-term") is deliberately ignored — every dispatch goes to
// c.TerminalID, the one antenna this injector was built for. from is the
// envelope's dynamic sender identity (ct-2026-07-18-1851-B), carried through
// to postMessage verbatim — this method has no opinion on its content.
// ponytail: single-terminal MVP; a route->creds map lands if/when multi-agent happens.
//
// T177: the antenna may have changed port since the endpoint was registered.
// A failure that can mean that (relocatable) makes the injector look for the
// terminal on loopback and retry once where it finds it — and only after
// that does terminal_gone discard the credential (T32 stays as it was when
// nothing is found).
func (c *CleverInjector) Inject(_ string, from, payload string) error {
	err := c.injectOnce(from, payload)
	if relocatable(err) && c.relocate() {
		err = c.injectOnce(from, payload)
	}
	if errors.Is(err, errTerminalGone) || errors.Is(err, errResponseTampered) {
		c.markDead()
	}
	return err
}

func (c *CleverInjector) injectOnce(from, payload string) error {
	endpoint, terminalID, pinpass, token, key := c.snapshot()
	if token == "" {
		var err error
		token, key, err = c.handshake(endpoint, terminalID, pinpass)
		if err != nil {
			return err
		}
		c.updateSession(token, key)
	}
	err := c.postMessage(endpoint, terminalID, token, key, from, payload)
	if !errors.Is(err, errUnauthorized) {
		return err
	}
	// Re-handshake once (protocol §5: 401 -> re-handshake, retry) — a stale
	// or invalidated token, not a permanent failure.
	token, key, err = c.handshake(endpoint, terminalID, pinpass)
	if err != nil {
		return fmt.Errorf("clever_injector: re-handshake after 401: %w", err)
	}
	c.updateSession(token, key)
	return c.postMessage(endpoint, terminalID, token, key, from, payload)
}

// snapshot reads all mutable config fields under the lock, releasing before
// any I/O so SetConfig never blocks on a slow network call.
func (c *CleverInjector) snapshot() (endpoint, terminalID, pinpass, token string, key []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Endpoint, c.TerminalID, c.Pinpass, c.token, c.key
}

// updateSession writes the new session token+key under the lock.
func (c *CleverInjector) updateSession(token string, key []byte) {
	c.mu.Lock()
	c.token, c.key = token, key
	c.mu.Unlock()
}

// handshake negotiates a fresh token+key (protocol §2). Receives all params
// by value (caller holds a snapshot) so the lock is not held during the HTTP
// call. Returns the new session values; does NOT write to c fields.
func (c *CleverInjector) handshake(endpoint, terminalID, pinpass string) (token string, key []byte, err error) {
	reqBody, err := json.Marshal(map[string]string{
		"terminal_id": terminalID,
		"pinpass":     pinpass,
	})
	if err != nil {
		return "", nil, fmt.Errorf("clever_injector: marshal handshake: %w", err)
	}
	resp, err := c.httpClient.Post(endpoint+"/handshake", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", nil, fmt.Errorf("clever_injector: handshake request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// T32 (ct-2026-08-06-1109): a failed handshake now names WHY
		// (protocol §2's three codes) instead of one generic
		// terminal_not_listening — best-effort decode, an older CleverCoder
		// (pre v1.6.68.191) or a response with no body at all just leaves
		// errBody.Error empty and falls through to the plain status-code
		// error exactly as before this contract (compat requirement #2).
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&errBody)
		if errBody.Error == "terminal_gone" {
			return "", nil, errTerminalGone
		}
		if errBody.Error != "" {
			return "", nil, fmt.Errorf("clever_injector: handshake status %d (%s)", resp.StatusCode, errBody.Error)
		}
		return "", nil, fmt.Errorf("clever_injector: handshake status %d", resp.StatusCode)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || out.Token == "" {
		return "", nil, fmt.Errorf("clever_injector: handshake response missing token: %w", err)
	}

	key = deriveKey(pinpass, out.Token)
	return out.Token, key, nil
}

// deriveKey replicates the protocol's contract byte-for-byte (§2): pinpass
// in the exact base64 form it was copied in, token as the lowercase hex
// string from the handshake response, joined by a literal ":" — any
// deviation here and the server-side AES-GCM open will simply fail.
func deriveKey(pinpass, token string) []byte {
	sum := sha256.Sum256([]byte(pinpass + ":" + token))
	return sum[:]
}

// cleverEnvelope is the plaintext JSON that gets AES-256-GCM sealed into
// /message's ciphertext field (protocol §3).
type cleverEnvelope struct {
	Header  cleverHeader `json:"header"`
	From    string       `json:"from"`
	Message string       `json:"message"`
}

type cleverHeader struct {
	SentAt    string `json:"sent_at"`
	SenderApp string `json:"sender_app"`
	Topic     string `json:"topic"`
	Rules     string `json:"rules"`
}

// postMessage seals payload (piumy's own dispatch text, opaque here) into
// the protocol's envelope and posts it. Returns errUnauthorized on a 401
// so Inject can re-handshake exactly once. SenderApp/From
// (ct-2026-07-18-1851-B, boss: shorten the dispatch — "piumy" no longer
// repeats in the body) render as the antenna's "app:"/"de:" lines;
// SenderApp names the app+channel (fixed), From carries numero/nivel
// (dynamic, computed by capipush per dispatch).
//
// T179: the answer's seal is checked on the raw bytes before anything reads its
// status or JSON, so a forged "paused" never gets to be believed.
func (c *CleverInjector) postMessage(endpoint, terminalID, token string, key []byte, from, payload string) error {
	envelope := cleverEnvelope{
		Header: cleverHeader{
			SentAt:    time.Now().UTC().Format(time.RFC3339),
			SenderApp: "piumy|whatsapp",
			Topic:     "dispatch",
		},
		From:    from,
		Message: payload,
	}
	plain, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("clever_injector: marshal envelope: %w", err)
	}

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("clever_injector: nonce: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("clever_injector: aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("clever_injector: gcm: %w", err)
	}
	sealed := gcm.Seal(nil, nonce, plain, nil)

	nonceB64 := base64.StdEncoding.EncodeToString(nonce)
	reqBody, err := json.Marshal(map[string]string{
		"token":      token,
		"nonce":      nonceB64,
		"ciphertext": base64.StdEncoding.EncodeToString(sealed),
	})
	if err != nil {
		return fmt.Errorf("clever_injector: marshal message: %w", err)
	}
	resp, err := c.httpClient.Post(endpoint+"/message", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("clever_injector: message request: %w", err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswerBytes))
	if err != nil {
		return fmt.Errorf("clever_injector: message response: %w", err)
	}
	if err := c.checkSeal(terminalID, key, resp.StatusCode, nonceB64, answer, resp.Header.Get(sealHeader)); err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return errUnauthorized
	}
	if resp.StatusCode == http.StatusNotFound {
		// Protocol §5: 404 = terminal not listening (antenna off / tab
		// closed), not a corrupt request. Spell it out so a failed smoke
		// points straight at "encendé la antena", not a generic status.
		return fmt.Errorf("clever_injector: terminal %s no escucha (antena apagada o tab cerrado) — encendé la antena en el terminal destino", c.TerminalID)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("clever_injector: message status %d", resp.StatusCode)
	}
	var out struct {
		OK       bool   `json:"ok"`
		Paused   bool   `json:"paused"`
		Client   string `json:"client"`
		ReopenAt string `json:"reopen_at"`
		Text     string `json:"text"`
		PayURL   string `json:"pay_url"`
	}
	if err := json.Unmarshal(answer, &out); err != nil {
		return fmt.Errorf("clever_injector: message response not ok: %w", err)
	}
	// T175: a paused client gets a 200 (a 404 would make us retry) with
	// nothing injected — before the OK check, whatever "ok" says.
	if out.Paused {
		return &PausedError{Client: out.Client, ReopenAt: out.ReopenAt, Text: out.Text, PayURL: out.PayURL}
	}
	if !out.OK {
		return fmt.Errorf("clever_injector: message response not ok")
	}
	return nil
}
