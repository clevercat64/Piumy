package store

// PausedForever uses year 9999 so a pause with no end fits exactly in a JavaScript number.
const PausedForever int64 = 253402300799

// SetPausedUntil sets the dispatch floor for an existing chat.
func (s *Store) SetPausedUntil(chatJID string, until int64) error {
	_, err := s.db.Exec(`UPDATE chats SET paused_until = ? WHERE jid = ?`, until, StripDeviceSuffix(chatJID))
	return err
}

// ResumeChat moves an active pause's floor to now so its backlog stays skipped.
func (s *Store) ResumeChat(chatJID string, now int64) error {
	_, err := s.db.Exec(`UPDATE chats SET paused_until = ? WHERE jid = ? AND paused_until > ?`, now, StripDeviceSuffix(chatJID), now)
	return err
}
