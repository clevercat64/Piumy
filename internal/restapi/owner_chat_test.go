package restapi

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"piumy-gateway/internal/store"
)

// ownerChatStore makes a dedicated, active 1:1 chat holding one unanswered
// inbound message from a minute ago.
func ownerChatStore(t *testing.T, chat string) *store.Store {
	t.Helper()
	st := newTestStore(t)
	if err := st.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", Text: "hola", TS: time.Now().Unix() - 60}); err != nil {
		t.Fatal(err)
	}
	return st
}

// postStatus posts body and returns only the HTTP status.
func postStatus(t *testing.T, url string, body any) int {
	t.Helper()
	resp := postJSON(t, url, body)
	defer resp.Body.Close()
	return resp.StatusCode
}

func pausedUntilOf(t *testing.T, st *store.Store, chat string) int64 {
	t.Helper()
	c, ok, err := st.GetChat(chat)
	if err != nil || !ok {
		t.Fatalf("GetChat(%s): ok=%v err=%v", chat, ok, err)
	}
	return c.PausedUntil
}

// TestOwnerSendQueuesAsTheOwnerAndClosesWhatWasPending: the owner's text goes
// into the outbox (so the governor paces it like any agent reply) tagged as the
// owner's, and what the chat had pending up to now is answered — but a message
// timestamped after the send stays pending for the agent.
func TestOwnerSendQueuesAsTheOwnerAndClosesWhatWasPending(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"
	st := ownerChatStore(t, chat)
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "later", Text: "despues", TS: time.Now().Unix() + 3600}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	if code := postStatus(t, srv.URL+"/api/admin/send", map[string]any{"chat_id": chat, "text": "  hola, soy yo  "}); code != http.StatusOK {
		t.Fatalf("POST /api/admin/send = %d, want 200", code)
	}

	out, err := st.PendingOutbox(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ToJID != chat || out[0].Text != "hola, soy yo" || out[0].Model != store.OwnerModel {
		t.Fatalf("outbox = %+v, want one item to %s, text trimmed, model %q", out, chat, store.OwnerModel)
	}
	pending, err := st.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "later" {
		t.Errorf("pending after the owner's send = %+v, want only \"later\" — m1 is answered, a newer message is not", pending)
	}
}

func TestOwnerSendKeepsPendingWhenEnqueueFails(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"
	path := filepath.Join(t.TempDir(), "piumy.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.AddMessage(store.Message{ChatJID: chat, ID: "m1", TS: 1}); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TRIGGER fail_owner_enqueue BEFORE INSERT ON outbox
		BEGIN SELECT RAISE(ABORT, 'injected enqueue failure'); END
	`); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()
	if code := postStatus(t, srv.URL+"/api/admin/send", map[string]string{"chat_id": chat, "text": "hola"}); code != http.StatusInternalServerError {
		t.Fatalf("POST /api/admin/send = %d, want 500", code)
	}
	if ids, err := st.UnhandledIDs(chat, 1, 1); err != nil || !ids["m1"] {
		t.Fatalf("pending IDs after failed enqueue = %v (err %v), want m1", ids, err)
	}
}

// TestOwnerSendRejectsWithoutQueueingOrClosing: every refusal leaves the outbox
// empty AND the pending message pending — a rejected send must not silence the
// agent.
func TestOwnerSendRejectsWithoutQueueingOrClosing(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"
	st := ownerChatStore(t, chat)
	// Exists on purpose: the refusal below must come from the broadcast check,
	// not from "chat not found".
	if err := st.TouchChat(store.StatusBroadcastJID, "Status", 1); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"empty text", map[string]any{"chat_id": chat, "text": ""}, http.StatusBadRequest},
		{"blank text", map[string]any{"chat_id": chat, "text": "  \n "}, http.StatusBadRequest},
		{"no chat_id", map[string]any{"text": "hola"}, http.StatusBadRequest},
		{"unknown chat", map[string]any{"chat_id": "555000000009@s.whatsapp.net", "text": "hola"}, http.StatusNotFound},
		{"status broadcast", map[string]any{"chat_id": store.StatusBroadcastJID, "text": "hola"}, http.StatusBadRequest},
	}
	for _, c := range cases {
		if code := postStatus(t, srv.URL+"/api/admin/send", c.body); code != c.want {
			t.Errorf("%s: POST /api/admin/send = %d, want %d", c.name, code, c.want)
		}
	}
	if out, err := st.PendingOutbox(10); err != nil || len(out) != 0 {
		t.Errorf("outbox after refusals = %+v (err %v), want empty", out, err)
	}
	if pending, err := st.PendingDedicated(10); err != nil || len(pending) != 1 {
		t.Errorf("pending after refusals = %+v (err %v), want m1 still pending", pending, err)
	}
}

// TestPauseEndpoint walks the three forms of "until" and the refusals.
func TestPauseEndpoint(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"
	st := ownerChatStore(t, chat)
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()
	pause := func(body map[string]any) int { return postStatus(t, srv.URL+"/api/admin/pause", body) }

	// 0 = no end. End to end: the unanswered message stops being dispatchable.
	if code := pause(map[string]any{"chat_id": chat, "until": 0}); code != http.StatusOK {
		t.Fatalf("until=0: %d, want 200", code)
	}
	if got := pausedUntilOf(t, st, chat); got != store.PausedForever {
		t.Errorf("until=0 stored %d, want PausedForever", got)
	}
	if pending, err := st.PendingDedicated(10); err != nil || len(pending) != 0 {
		t.Errorf("pending while paused = %+v (err %v), want none", pending, err)
	}

	// A future unix second is stored as given.
	future := time.Now().Unix() + 3600
	if code := pause(map[string]any{"chat_id": chat, "until": future}); code != http.StatusOK {
		t.Fatalf("until=future: %d, want 200", code)
	}
	if got := pausedUntilOf(t, st, chat); got != future {
		t.Errorf("until=future stored %d, want %d", got, future)
	}

	// Past the end of the calendar it clamps to "no end".
	if code := pause(map[string]any{"chat_id": chat, "until": store.PausedForever + 1000}); code != http.StatusOK {
		t.Fatalf("until beyond PausedForever: %d, want 200", code)
	}
	if got := pausedUntilOf(t, st, chat); got != store.PausedForever {
		t.Errorf("until beyond PausedForever stored %d, want PausedForever", got)
	}

	// -1 resumes: the floor moves to now.
	before := time.Now().Unix()
	if code := pause(map[string]any{"chat_id": chat, "until": -1}); code != http.StatusOK {
		t.Fatalf("until=-1: %d, want 200", code)
	}
	after := time.Now().Unix()
	resumedAt := pausedUntilOf(t, st, chat)
	if resumedAt < before || resumedAt > after {
		t.Fatalf("resume stored %d, want a value in [%d, %d]", resumedAt, before, after)
	}
	// Resuming again leaves an ended pause alone.
	if code := pause(map[string]any{"chat_id": chat, "until": -1}); code != http.StatusOK {
		t.Fatalf("second until=-1: %d, want 200", code)
	}
	if got := pausedUntilOf(t, st, chat); got != resumedAt {
		t.Errorf("second resume changed %d to %d, want it untouched", resumedAt, got)
	}

	// Refusals change nothing.
	refusals := []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing until", map[string]any{"chat_id": chat}, http.StatusBadRequest},
		{"until below -1", map[string]any{"chat_id": chat, "until": -2}, http.StatusBadRequest},
		{"until in the distant past", map[string]any{"chat_id": chat, "until": 1}, http.StatusBadRequest},
		{"until a moment ago", map[string]any{"chat_id": chat, "until": time.Now().Unix() - 10}, http.StatusBadRequest},
		{"no chat_id", map[string]any{"until": 0}, http.StatusBadRequest},
		{"unknown chat", map[string]any{"chat_id": "555000000009@s.whatsapp.net", "until": 0}, http.StatusNotFound},
	}
	for _, r := range refusals {
		if code := pause(r.body); code != r.want {
			t.Errorf("%s: %d, want %d", r.name, code, r.want)
		}
	}
	if got := pausedUntilOf(t, st, chat); got != resumedAt {
		t.Errorf("paused_until after the refusals = %d, want %d — a refused request must change nothing (a missing until above all must not read as 0 = no end)", got, resumedAt)
	}
}

// TestChatsEndpointExposesPausedUntil: the conversation view reads the pause
// from the chat list.
func TestChatsEndpointExposesPausedUntil(t *testing.T) {
	const chat = "555000000001@s.whatsapp.net"
	st := ownerChatStore(t, chat)
	if err := st.SetPausedUntil(chat, store.PausedForever); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewMux(Deps{Store: st}))
	defer srv.Close()

	var out []struct {
		JID         string `json:"jid"`
		PausedUntil int64  `json:"paused_until"`
	}
	if resp := getJSON(t, srv.URL+"/api/chats", &out); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/chats = %d, want 200", resp.StatusCode)
	}
	for _, c := range out {
		if c.JID == chat {
			if c.PausedUntil != store.PausedForever {
				t.Errorf("GET /api/chats paused_until = %d, want %d", c.PausedUntil, store.PausedForever)
			}
			return
		}
	}
	t.Fatalf("GET /api/chats = %+v, want the chat listed", out)
}
