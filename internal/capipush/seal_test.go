package capipush

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The official vector: CleverCoder's protocol §3 "Respuesta sellada" (their
// ct-2026-11-10-2253, commit e2927311), computed there with Python's hmac and
// hashlib. These are LITERALS on purpose — a test that compared sealOf against a
// constant sealOf also uses would pass with a wrong algorithm.
const (
	vectorStatus = 200
	vectorNonce  = "AAAAAAAAAAAAAAAA"
	vectorBody   = `{"ok":true,"resolved":{"agent":"alfa","project":"Proyecto","position":1}}`
	vectorSeal   = "v1=61ed62ab6b88350226128fbe64f801115b7125aeaaaa5cb9bd644047b4a8cb61"
)

// vectorKey is the session key of the vector: the 32 bytes 00 01 02 … 1f.
func vectorKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

// oracleSeal is a second, step-by-step implementation of the seal, apart from
// sealOf, for the fake CleverCoder below to answer with. TestSealOracle pins it
// to the same literal vector, so the fake server isn't an echo of production.
func oracleSeal(key []byte, status int, nonce string, body []byte) string {
	first := hmac.New(sha256.New, key)
	first.Write([]byte("capi-resp-v1"))
	sealKey := first.Sum(nil)
	data := append([]byte(strconv.Itoa(status)+"\n"+nonce+"\n"), body...)
	second := hmac.New(sha256.New, sealKey)
	second.Write(data)
	return "v1=" + hex.EncodeToString(second.Sum(nil))
}

// memKV is the store's KV for the seal memory, in memory.
type memKV struct {
	m      map[string]string
	getErr error
}

func newMemKV() *memKV { return &memKV{m: map[string]string{}} }

func (k *memKV) KVGet(key string) (string, error) { return k.m[key], k.getErr }
func (k *memKV) KVSet(key, val string) error      { k.m[key] = val; return nil }

func TestSealOfIsTheProtocolVector(t *testing.T) {
	if got := sealOf(vectorKey(), vectorStatus, vectorNonce, []byte(vectorBody)); got != vectorSeal {
		t.Errorf("sealOf = %s, want the protocol vector %s", got, vectorSeal)
	}
}

func TestSealOracleIsTheProtocolVector(t *testing.T) {
	if got := oracleSeal(vectorKey(), vectorStatus, vectorNonce, []byte(vectorBody)); got != vectorSeal {
		t.Errorf("oracleSeal = %s, want the protocol vector %s", got, vectorSeal)
	}
}

// TestSealCoversEveryInput: change any one of the four inputs and the seal
// changes — otherwise a middleman could change that part unseen.
func TestSealCoversEveryInput(t *testing.T) {
	otherKey := vectorKey()
	otherKey[31] ^= 1
	variants := map[string]string{
		"key":    sealOf(otherKey, vectorStatus, vectorNonce, []byte(vectorBody)),
		"status": sealOf(vectorKey(), 404, vectorNonce, []byte(vectorBody)),
		"nonce":  sealOf(vectorKey(), vectorStatus, "AAAAAAAAAAAAAAAB", []byte(vectorBody)),
		"body":   sealOf(vectorKey(), vectorStatus, vectorNonce, []byte(strings.Replace(vectorBody, "alfa", "alfb", 1))),
	}
	seen := map[string]string{vectorSeal: "the vector"}
	for name, seal := range variants {
		if other, dup := seen[seal]; dup {
			t.Errorf("changing the %s gives the same seal as %s — that input isn't covered", name, other)
		}
		seen[seal] = name
	}
}

func TestCheckSealVerdicts(t *testing.T) {
	const tid = "capi-proyecto-agente-1a2b3c4d"
	key := vectorKey()
	const nonce = "bm9uY2Utb2Yta2luZA=="
	body := []byte(`{"ok":true}`)

	headerFor := func(kind string, status int) string {
		switch kind {
		case "valid":
			return oracleSeal(key, status, nonce, body)
		case "wrong":
			return oracleSeal(key, status, nonce, []byte(`{"ok":false}`))
		}
		return ""
	}
	// sealedBefore false: the antenna never sealed. true: it did.
	for _, status := range []int{200, 404, 400, 401, 500, 503} {
		unsealedAllowed := status == 401 || status >= 500
		cases := []struct {
			name         string
			header       string
			sealedBefore bool
			wantTamper   bool
			wantSealed   bool // remembered afterwards
		}{
			{"valid seal, never sealed: accepted and learned", "valid", false, false, true},
			{"valid seal, sealed before: accepted", "valid", true, false, true},
			{"wrong seal, never sealed: tampered (no TOFU needed)", "wrong", false, true, false},
			{"wrong seal, sealed before: tampered", "wrong", true, true, true},
			{"no seal, never sealed: as before T179", "", false, false, false},
			{"no seal, sealed before", "", true, !unsealedAllowed, true},
		}
		for _, tc := range cases {
			t.Run(strconv.Itoa(status)+" "+tc.name, func(t *testing.T) {
				kv := newMemKV()
				if tc.sealedBefore {
					kv.m[sealSeenKey(tid)] = "2026-11-11T00:00:00Z"
				}
				inj := &CleverInjector{seals: kv}

				err := inj.checkSeal(tid, key, status, nonce, body, headerFor(tc.header, status))

				if tampered := errors.Is(err, errResponseTampered); tampered != tc.wantTamper {
					t.Errorf("tampered = %v (err %v), want %v", tampered, err, tc.wantTamper)
				}
				if got := kv.m[sealSeenKey(tid)] != ""; got != tc.wantSealed {
					t.Errorf("remembered after = %v, want %v", got, tc.wantSealed)
				}
			})
		}
	}
}

// TestTruncatedSealIsTampered: a seal that is only the start of the right one
// (or empty after "v1=") doesn't count — the whole 64 hex must match.
func TestTruncatedSealIsTampered(t *testing.T) {
	key := vectorKey()
	const nonce = "bm9uY2Utb2Yta2luZA=="
	body := []byte(`{"ok":true}`)
	right := oracleSeal(key, 200, nonce, body)
	for name, header := range map[string]string{
		"half the seal":     right[:len(right)/2],
		"all but one digit": right[:len(right)-1],
		"just the version":  "v1=",
		"another version":   "v2=" + strings.TrimPrefix(right, "v1="),
	} {
		t.Run(name, func(t *testing.T) {
			inj := &CleverInjector{seals: newMemKV()}
			if err := inj.checkSeal("capi-x", key, 200, nonce, body, header); !errors.Is(err, errResponseTampered) {
				t.Errorf("checkSeal(%q) = %v, want errResponseTampered", header, err)
			}
		})
	}
}

// TestSealIsOverTheExactBytes: the seal covers the body as it arrived, spaces
// and newline included — not the JSON read back and re-serialized.
func TestSealIsOverTheExactBytes(t *testing.T) {
	fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{200, "{\"ok\": true }  \n", "ok"} })

	if err := fake.injector(newMemKV()).Inject("", "555001, Grupo, caution", "payload"); err != nil {
		t.Errorf("Inject = %v, want nil: the seal was made over the body's exact bytes", err)
	}
}

func TestSealMemory(t *testing.T) {
	const a, b = "capi-a", "capi-b"
	kv := newMemKV()

	if sealRemembered(kv, a) {
		t.Fatal("setup: nothing was remembered yet")
	}
	rememberSeal(kv, a)
	if !sealRemembered(kv, a) || sealRemembered(kv, b) {
		t.Errorf("after remembering %s: a=%v b=%v, want a only", a, sealRemembered(kv, a), sealRemembered(kv, b))
	}

	kv.m[sealSeenKey(a)] = "first-time"
	rememberSeal(kv, a)
	if kv.m[sealSeenKey(a)] != "first-time" {
		t.Errorf("a second valid seal rewrote the first-seen time: %q", kv.m[sealSeenKey(a)])
	}

	rememberSeal(kv, b)
	ForgetSeal(kv, a)
	if sealRemembered(kv, a) || !sealRemembered(kv, b) {
		t.Errorf("after forgetting %s: a=%v b=%v, want b only", a, sealRemembered(kv, a), sealRemembered(kv, b))
	}

	empty := newMemKV()
	rememberSeal(empty, "")
	ForgetSeal(empty, "")
	if len(empty.m) != 0 {
		t.Errorf("an empty chat_id wrote %v, want nothing", empty.m)
	}

	var none KV
	rememberSeal(none, a) // no memory: must not panic
	ForgetSeal(none, a)
	if sealRemembered(none, a) {
		t.Error("with no memory, sealRemembered = true")
	}

	broken := newMemKV()
	broken.m[sealSeenKey(a)] = "x"
	broken.getErr = errors.New("db locked")
	if sealRemembered(broken, a) {
		t.Error("a failed read said the antenna sealed; it must fail open, as before T179")
	}
}

// TestTamperedErrorLeaksNothing: the error carries the status and 8 hex of each
// seal — never the session key, the token, nor a whole seal.
func TestTamperedErrorLeaksNothing(t *testing.T) {
	key := vectorKey()
	const nonce = "bm9uY2Utb2Yta2luZA=="
	body := []byte(`{"ok":true}`)
	got := oracleSeal(key, 200, nonce, []byte(`{"ok":false}`))
	want := oracleSeal(key, 200, nonce, body)
	inj := &CleverInjector{seals: newMemKV()}

	err := inj.checkSeal("capi-x", key, 200, nonce, body, got)
	if !errors.Is(err, errResponseTampered) {
		t.Fatalf("err = %v, want tampered", err)
	}
	msg := err.Error()
	for name, secret := range map[string]string{
		"the whole received seal": got,
		"the whole expected seal": want,
		"the received hex":        strings.TrimPrefix(got, "v1="),
		"the expected hex":        strings.TrimPrefix(want, "v1="),
		"the session key":         hex.EncodeToString(key),
	} {
		if strings.Contains(msg, secret) {
			t.Errorf("error leaks %s: %q", name, msg)
		}
	}
	if !strings.Contains(msg, strings.TrimPrefix(got, "v1=")[:8]) {
		t.Errorf("error should name the first 8 hex of the received seal to be diagnosable: %q", msg)
	}
}

// ─── a fake CleverCoder that answers /message with a seal ───

// fakeAnswer is what the fake says to one /message. seal picks what goes in
// X-Capi-Seal: "ok" (right for this status, nonce and body), "none", "body"
// (sealed over a body that isn't the one sent), "nonce" (sealed for another
// message's nonce — a replayed answer) or "status" (sealed as a 200).
type fakeAnswer struct {
	status int
	body   string
	seal   string
}

const (
	fakeToken  = "0123456789abcdef0123456789abcdef"
	fakePin    = "fake-pinpass-base64=="
	fakeTermID = "capi-proyecto-agente-1a2b3c4d"
)

type fakeCleverCoder struct {
	*httptest.Server
	mu         sync.Mutex
	handshakes int
	messages   int
}

// newFakeCleverCoder serves a real handshake and, for the nth /message, the
// answer(n). It derives the session key itself with crypto/sha256 and seals
// with oracleSeal — nothing from production's deriveKey or sealOf.
func newFakeCleverCoder(t *testing.T, answer func(n int) fakeAnswer) *fakeCleverCoder {
	t.Helper()
	f := &fakeCleverCoder{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/handshake":
			f.mu.Lock()
			f.handshakes++
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"token": fakeToken})
		case "/message":
			var req struct {
				Nonce string `json:"nonce"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.messages++
			n := f.messages
			f.mu.Unlock()
			a := answer(n)
			sum := sha256.Sum256([]byte(fakePin + ":" + fakeToken))
			key := sum[:]
			switch a.seal {
			case "ok":
				w.Header().Set(sealHeader, oracleSeal(key, a.status, req.Nonce, []byte(a.body)))
			case "body":
				w.Header().Set(sealHeader, oracleSeal(key, a.status, req.Nonce, []byte(a.body+" ")))
			case "nonce":
				w.Header().Set(sealHeader, oracleSeal(key, a.status, "b2xkLW5vbmNlLW9mLWFub3RoZXI=", []byte(a.body)))
			case "status":
				w.Header().Set(sealHeader, oracleSeal(key, 200, req.Nonce, []byte(a.body)))
			}
			w.WriteHeader(a.status)
			_, _ = w.Write([]byte(a.body))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeCleverCoder) counts() (handshakes, messages int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handshakes, f.messages
}

func (f *fakeCleverCoder) injector(kv KV) *CleverInjector {
	inj := NewCleverInjector(f.URL, fakeTermID, fakePin, kv)
	inj.scanPorts = []int{} // no loopback sweep: these tests are about the answer, not the port
	return inj
}

func sealedBefore(kv *memKV) *memKV {
	kv.m[sealSeenKey(fakeTermID)] = "2026-11-11T00:00:00Z"
	return kv
}

func TestInjectLearnsTheFirstSealThenRequiresIt(t *testing.T) {
	fake := newFakeCleverCoder(t, func(n int) fakeAnswer {
		if n == 1 {
			return fakeAnswer{200, `{"ok":true}`, "ok"}
		}
		return fakeAnswer{200, `{"ok":true}`, "none"} // a middleman strips the seal
	})
	kv := newMemKV()
	inj := fake.injector(kv)

	if err := inj.Inject("", "555001, Grupo, caution", "payload"); err != nil {
		t.Fatalf("first, sealed answer: Inject = %v, want nil", err)
	}
	if kv.m[sealSeenKey(fakeTermID)] == "" {
		t.Fatal("the first valid seal wasn't remembered")
	}

	err := inj.Inject("", "555001, Grupo, caution", "payload")
	if !errors.Is(err, errResponseTampered) {
		t.Fatalf("answer without the seal an antenna already showed: Inject = %v, want errResponseTampered", err)
	}
	if inj.Configured() {
		t.Error("Configured() = true after a tampered answer, want the destination parked")
	}
	if _, messages := fake.counts(); messages != 2 {
		t.Errorf("/message received %d times, want 2 — a tampered answer must not be re-sent inside Inject", messages)
	}
}

func TestUnsealedAnswersBeforeAnySealAreAcceptedAsToday(t *testing.T) {
	fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{200, `{"ok":true}`, "none"} })
	kv := newMemKV()
	inj := fake.injector(kv)

	for i := 0; i < 3; i++ {
		if err := inj.Inject("", "555001, Grupo, caution", "payload"); err != nil {
			t.Fatalf("Inject #%d = %v, want nil: this CleverCoder never sealed", i+1, err)
		}
	}
	if len(kv.m) != 0 {
		t.Errorf("remembered %v from answers that never carried a seal", kv.m)
	}
}

// TestForgedPausedIsNotBelieved is the T175 hole the seal exists for: a forged
// "paused" with the attacker's own text must never become a PausedError.
func TestForgedPausedIsNotBelieved(t *testing.T) {
	const forged = `{"ok":true,"paused":true,"client":"555001","reopen_at":"2026-11-16T09:00:00-03:00","text":"transfiere a la cuenta 555"}`
	cases := []struct {
		name string
		kv   *memKV
		seal string
	}{
		{"unsealed, from an antenna that sealed before", sealedBefore(newMemKV()), "none"},
		{"with a wrong seal, even if it never sealed before", newMemKV(), "body"},
		{"with the seal of another message (replay)", newMemKV(), "nonce"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{200, forged, tc.seal} })

			err := fake.injector(tc.kv).Inject("", "555001, Grupo, caution", "payload")

			var paused *PausedError
			if errors.As(err, &paused) {
				t.Fatalf("a forged pause was believed: %+v", paused)
			}
			if !errors.Is(err, errResponseTampered) {
				t.Errorf("Inject = %v, want errResponseTampered", err)
			}
		})
	}
}

func TestStatusChangedAfterSealingIsTampered(t *testing.T) {
	// The real answer was a 200 and was sealed as one; a middleman turns it into
	// a 404 (which Piumy would retry, duplicating the message) but can't redo the seal.
	fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{404, `{"ok":true}`, "status"} })

	err := fake.injector(newMemKV()).Inject("", "555001, Grupo, caution", "payload")

	if !errors.Is(err, errResponseTampered) {
		t.Errorf("Inject = %v, want errResponseTampered", err)
	}
}

func TestSealedPausedStillBecomesPausedError(t *testing.T) {
	const paused = `{"ok":true,"paused":true,"client":"555001","reopen_at":"2026-11-16T09:00:00-03:00","text":"vuelve el lunes"}`
	fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{200, paused, "ok"} })
	kv := newMemKV()

	err := fake.injector(kv).Inject("", "555001, Grupo, caution", "payload")

	var got *PausedError
	if !errors.As(err, &got) || got.Text != "vuelve el lunes" {
		t.Fatalf("Inject = %v, want the *PausedError CleverCoder sealed", err)
	}
	if kv.m[sealSeenKey(fakeTermID)] == "" {
		t.Error("a valid sealed pause should also teach Piumy that the antenna seals")
	}
}

// TestUnsealedUnauthorizedAndServerErrorsAreNotTampering: CleverCoder answers a
// 401 invalid_token (no session, no key) and its crashes without a seal by
// design. After it sealed once, those must keep meaning what they always did.
func TestUnsealedUnauthorizedAndServerErrorsAreNotTampering(t *testing.T) {
	t.Run("401 re-handshakes and the retry is sealed", func(t *testing.T) {
		fake := newFakeCleverCoder(t, func(n int) fakeAnswer {
			if n == 1 {
				return fakeAnswer{401, `{"error":"invalid_token"}`, "none"}
			}
			return fakeAnswer{200, `{"ok":true}`, "ok"}
		})
		inj := fake.injector(sealedBefore(newMemKV()))

		if err := inj.Inject("", "555001, Grupo, caution", "payload"); err != nil {
			t.Fatalf("Inject = %v, want nil after the re-handshake", err)
		}
		if handshakes, _ := fake.counts(); handshakes != 2 {
			t.Errorf("handshakes = %d, want 2 (first + the one the 401 triggered)", handshakes)
		}
	})
	t.Run("500 stays a plain failure, destination not parked", func(t *testing.T) {
		fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{500, `{"error":"boom"}`, "none"} })
		inj := fake.injector(sealedBefore(newMemKV()))

		err := inj.Inject("", "555001, Grupo, caution", "payload")

		if err == nil || errors.Is(err, errResponseTampered) {
			t.Errorf("Inject = %v, want a plain error, not errResponseTampered", err)
		}
		if !inj.Configured() {
			t.Error("a 500 parked the destination; it must keep retrying on its own")
		}
	})
}

func TestForged404IsTamperedButASealed404IsNot(t *testing.T) {
	t.Run("unsealed 404 after sealing: forged, a retry would duplicate", func(t *testing.T) {
		fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{404, `{"error":"delivery_unavailable"}`, "none"} })

		err := fake.injector(sealedBefore(newMemKV())).Inject("", "555001, Grupo, caution", "payload")

		if !errors.Is(err, errResponseTampered) {
			t.Errorf("Inject = %v, want errResponseTampered", err)
		}
	})
	t.Run("sealed 404: CleverCoder really couldn't deliver", func(t *testing.T) {
		fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{404, `{"error":"delivery_unavailable"}`, "ok"} })
		inj := fake.injector(sealedBefore(newMemKV()))

		err := inj.Inject("", "555001, Grupo, caution", "payload")

		if err == nil || errors.Is(err, errResponseTampered) {
			t.Errorf("Inject = %v, want the ordinary 'no escucha' failure", err)
		}
		if !inj.Configured() {
			t.Error("an honest 404 parked the destination")
		}
	})
}

func TestBodyEditedAfterSealingIsTampered(t *testing.T) {
	fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{200, `{"ok":true}`, "body"} })

	err := fake.injector(newMemKV()).Inject("", "555001, Grupo, caution", "payload")

	if !errors.Is(err, errResponseTampered) {
		t.Errorf("Inject = %v, want errResponseTampered", err)
	}
}

// TestSealMemorySurvivesARestartAndSetConfigForgetsIt: the requirement lives in
// the KV, not in the injector (injectors are rebuilt on every upsert); a boot
// builds a new one that still requires the seal; the owner re-saving the
// destination is the way out.
func TestSealMemorySurvivesARestartAndSetConfigForgetsIt(t *testing.T) {
	fake := newFakeCleverCoder(t, func(int) fakeAnswer { return fakeAnswer{200, `{"ok":true}`, "none"} })
	kv := sealedBefore(newMemKV())

	restarted := fake.injector(kv) // a new injector over the same KV: what main.go builds at boot
	if err := restarted.Inject("", "555001, Grupo, caution", "payload"); !errors.Is(err, errResponseTampered) {
		t.Fatalf("after a restart Inject = %v, want errResponseTampered: the seal is still required", err)
	}
	if restarted.Configured() {
		t.Fatal("setup: the tampered answer should have parked the destination")
	}

	restarted.SetConfig(fake.URL, fakeTermID, fakePin) // the owner re-saves the destination

	if !restarted.Configured() {
		t.Error("SetConfig didn't unpark the destination")
	}
	if kv.m[sealSeenKey(fakeTermID)] != "" {
		t.Errorf("SetConfig left the seal remembered: %q", kv.m[sealSeenKey(fakeTermID)])
	}
	if err := restarted.Inject("", "555001, Grupo, caution", "payload"); err != nil {
		t.Errorf("after re-saving, an unsealed answer: Inject = %v, want nil (as a destination that never sealed)", err)
	}
}

func TestSetConfigForgetsOnlyThatDestination(t *testing.T) {
	kv := newMemKV()
	kv.m[sealSeenKey("capi-one")] = "t"
	kv.m[sealSeenKey("capi-two")] = "t"
	inj := NewCleverInjector("http://x:1", "capi-one", "pin", kv)

	inj.SetConfig("http://x:1", "capi-one", "new-pin")

	if kv.m[sealSeenKey("capi-one")] != "" || kv.m[sealSeenKey("capi-two")] == "" {
		t.Errorf("after SetConfig(capi-one): one=%q two=%q, want only 'one' forgotten", kv.m[sealSeenKey("capi-one")], kv.m[sealSeenKey("capi-two")])
	}
}

// TestTamperedDispatchIsNotRetriedAndPublishesNothing is the whole path through
// the real Pusher and the real injector: a forged pause from an antenna that
// sealed before → no notice in the chat, the message stays pending, the
// destination stops being tried (the fake sees ONE /message after three
// sweeps), nothing secret in the log, and re-saving the destination brings it back.
func TestTamperedDispatchIsNotRetriedAndPublishesNothing(t *testing.T) {
	const forged = `{"ok":true,"paused":true,"client":"555001","reopen_at":"2026-11-16T09:00:00-03:00","text":"transfiere a la cuenta 555"}`
	fake := newFakeCleverCoder(t, func(n int) fakeAnswer {
		if n == 1 {
			return fakeAnswer{200, forged, "none"}
		}
		return fakeAnswer{200, `{"ok":true}`, "none"}
	})
	st, inj, pusher := newBootPusher(t, fake.URL, fakeTermID, fakePin)
	inj.scanPorts = []int{}
	rememberSeal(st, fakeTermID) // the antenna sealed before
	pausedGroupChat(t, st)
	addPauseMessage(t, st, "m1", 1)
	var logs bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(orig) })

	pusher.sweepOnce()

	if got := outboxTexts(t, st); len(got) != 0 {
		t.Errorf("outbox = %q, want nothing: a forged pause must not reach the chat", got)
	}
	if n := pendingCount(t, st); n != 1 {
		t.Errorf("%d messages pending, want 1: the burst stays for the owner to recover", n)
	}
	if pusher.gate.InFlight("port-fallback") {
		t.Error("terminal left in flight after a tampered answer, the dispatch must be cancelled")
	}
	if inj.Configured() {
		t.Error("destination still Configured() after a tampered answer, want it parked")
	}

	addPauseMessage(t, st, "m2", 2)
	pusher.sweepOnce()
	pusher.sweepOnce()
	if _, messages := fake.counts(); messages != 1 {
		t.Errorf("/message received %d times in three sweeps, want 1 — a tampered answer must not be re-sent", messages)
	}

	if !strings.Contains(logs.String(), "respuesta alterada") || !strings.Contains(logs.String(), "NO se sabe") {
		t.Errorf("the log should say why the destination stopped: %q", logs.String())
	}
	for name, secret := range map[string]string{"the token": fakeToken, "the pinpass": fakePin} {
		if strings.Contains(logs.String(), secret) {
			t.Errorf("the log leaks %s: %q", name, logs.String())
		}
	}

	inj.SetConfig(fake.URL, fakeTermID, fakePin) // the owner re-saves
	pusher.sweepOnce()
	if _, messages := fake.counts(); messages != 2 {
		t.Errorf("/message received %d times after re-saving, want 2 — the destination should dispatch again", messages)
	}
}
