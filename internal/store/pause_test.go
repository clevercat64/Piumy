package store

import (
	"reflect"
	"testing"
)

// dispatchViews reads one chat's inbound backlog through every query the
// dispatch path trusts, each under its own name — so a pause that one of them
// forgets fails under THAT name instead of hiding behind the others (T5 and
// T67 were both a copy of the gate that one query missed).
func dispatchViews(t *testing.T, s *Store, chat string) map[string]int {
	t.Helper()
	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	total, err := s.CountPendingDedicated()
	if err != nil {
		t.Fatal(err)
	}
	since, err := s.CountPendingDedicatedSince(0)
	if err != nil {
		t.Fatal(err)
	}
	nonBoss, err := s.CountRecentPendingNonBoss(0)
	if err != nil {
		t.Fatal(err)
	}
	chats, err := s.PendingChatsDispatch(10, 1000)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := s.UnhandledIDs(chat, 0, 1<<40)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]int{
		"PendingDedicated":           len(pending),
		"CountPendingDedicated":      total,
		"CountPendingDedicatedSince": since,
		"CountRecentPendingNonBoss":  nonBoss,
		"PendingChatsDispatch":       len(chats),
		"UnhandledIDs":               len(ids),
	}
}

// pausedChatWithTwoMessages is a dedicated, active 1:1 chat with an inbound
// message at ts 100 and another at ts 200.
func pausedChatWithTwoMessages(t *testing.T) (*Store, string) {
	t.Helper()
	s := openTestStore(t)
	const chat = "555000000001@s.whatsapp.net"
	if err := s.TouchChat(chat, "Contacto Uno", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMode(chat, "dedicated"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetActive(chat, true); err != nil {
		t.Fatal(err)
	}
	for _, m := range []Message{
		{ChatJID: chat, ID: "m1", Text: "durante", TS: 100},
		{ChatJID: chat, ID: "m2", Text: "despues", TS: 200},
	} {
		if err := s.AddMessage(m); err != nil {
			t.Fatal(err)
		}
	}
	return s, chat
}

// TestPausedUntilIsAFloorEveryDispatchQueryHonors: a message is dispatchable
// only if it arrived AFTER paused_until — checked on all six queries, at the
// edges that matter: no pause, a pause that ended between the two messages,
// a pause ending exactly at the newest one, and a pause with no end.
func TestPausedUntilIsAFloorEveryDispatchQueryHonors(t *testing.T) {
	cases := []struct {
		name        string
		pausedUntil int64
		wantMsgs    int
	}{
		{"never paused", 0, 2},
		{"pause ended between the two messages", 150, 1},
		{"pause ends exactly at the newest message", 200, 0},
		{"pause with no end", PausedForever, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, chat := pausedChatWithTwoMessages(t)
			if err := s.SetPausedUntil(chat, c.pausedUntil); err != nil {
				t.Fatal(err)
			}
			wantChats := 0
			if c.wantMsgs > 0 {
				wantChats = 1
			}
			want := map[string]int{
				"PendingDedicated":           c.wantMsgs,
				"CountPendingDedicated":      c.wantMsgs,
				"CountPendingDedicatedSince": c.wantMsgs,
				"CountRecentPendingNonBoss":  c.wantMsgs,
				"PendingChatsDispatch":       wantChats,
				"UnhandledIDs":               c.wantMsgs,
			}
			if got := dispatchViews(t, s, chat); !reflect.DeepEqual(got, want) {
				t.Errorf("paused_until=%d: queries returned %v, want %v", c.pausedUntil, got, want)
			}
		})
	}
}

// TestPauseDoesNotTouchTheMessages: the pause is a gate, not a cleanup — what
// arrived during it keeps handled=0 (the conversation is never rewritten) and
// the chat's own config stays as the owner left it.
func TestPauseDoesNotTouchTheMessages(t *testing.T) {
	s, chat := pausedChatWithTwoMessages(t)
	if err := s.SetPausedUntil(chat, PausedForever); err != nil {
		t.Fatal(err)
	}
	var unhandled int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE chat_jid = ? AND handled = 0`, chat).Scan(&unhandled); err != nil {
		t.Fatal(err)
	}
	if unhandled != 2 {
		t.Errorf("unhandled messages after a pause = %d, want 2 — a pause must not mark anything handled", unhandled)
	}
	c, ok, err := s.GetChat(chat)
	if err != nil || !ok {
		t.Fatalf("GetChat: ok=%v err=%v", ok, err)
	}
	if !c.Active || c.Mode != "dedicated" {
		t.Errorf("chat after pause = active %v mode %q, want active dedicated — \"siempre\" is a pause, not SetActive(false)", c.Active, c.Mode)
	}
	if c.PausedUntil != PausedForever {
		t.Errorf("Chat.PausedUntil = %d, want %d", c.PausedUntil, PausedForever)
	}
}

// TestResumeChat: ending a running pause moves the floor to now (what came in
// during the pause is NOT dispatched on resume); a chat that is not paused, or
// whose pause already ended, is left exactly as it was.
func TestResumeChat(t *testing.T) {
	const now = 500
	cases := []struct {
		name  string
		start int64
		want  int64
	}{
		{"never paused", 0, 0},
		{"pause already ended", 300, 300},
		{"pause running", 1000, now},
		{"pause with no end", PausedForever, now},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, chat := pausedChatWithTwoMessages(t)
			if err := s.SetPausedUntil(chat, c.start); err != nil {
				t.Fatal(err)
			}
			if err := s.ResumeChat(chat, now); err != nil {
				t.Fatal(err)
			}
			got, _, err := s.GetChat(chat)
			if err != nil {
				t.Fatal(err)
			}
			if got.PausedUntil != c.want {
				t.Errorf("paused_until after ResumeChat(now=%d) from %d = %d, want %d", now, c.start, got.PausedUntil, c.want)
			}
		})
	}

	// The point of moving the floor instead of clearing it: after a resume the
	// pause's messages (ts 100, 200) stay closed to the agent, a newer one passes.
	s, chat := pausedChatWithTwoMessages(t)
	if err := s.SetPausedUntil(chat, PausedForever); err != nil {
		t.Fatal(err)
	}
	if err := s.ResumeChat(chat, now); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMessage(Message{ChatJID: chat, ID: "m3", Text: "ya reanudado", TS: now + 100}); err != nil {
		t.Fatal(err)
	}
	pending, err := s.PendingDedicated(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != "m3" {
		t.Errorf("pending after resume = %+v, want only m3 — what arrived during the pause must not come back", pending)
	}
}

// TestSetPausedUntilUnknownChatIsNoop: pausing a chat that does not exist must
// not create it (an upsert here would conjure a chat row out of a typo).
func TestSetPausedUntilUnknownChatIsNoop(t *testing.T) {
	s := openTestStore(t)
	const ghost = "555000000009@s.whatsapp.net"
	if err := s.SetPausedUntil(ghost, PausedForever); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetChat(ghost); err != nil || ok {
		t.Errorf("GetChat(%s) after SetPausedUntil: ok=%v err=%v, want no chat", ghost, ok, err)
	}
}
