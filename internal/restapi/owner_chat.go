package restapi

import (
	"log"
	"net/http"
	"strings"
	"time"

	"piumy-gateway/internal/store"
)

// untilResume is POST /api/admin/pause's "until" for ending a pause.
const untilResume = -1

// handleOwnerSend queues before marking messages handled, so a failed
// enqueue leaves them available to the agent.
func (d Deps) handleOwnerSend(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	if !decode(w, r, &body) {
		return
	}
	chatID := store.StripDeviceSuffix(body.ChatID)
	if chatID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_id is required"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "text is required"})
		return
	}
	// Status/Stories is not a conversation: a send there would PUBLISH a status.
	if chatID == store.StatusBroadcastJID {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "status@broadcast cannot be written to"})
		return
	}
	if !d.chatExists(w, chatID) {
		return
	}
	now := time.Now().Unix()
	if err := d.Store.EnqueueWithModel(chatID, text, now, store.OwnerModel); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := d.Store.MarkHandledBefore(chatID, now); err != nil {
		log.Printf("restapi: owner send: mark_handled_before %s: %v", chatID, err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "queued"})
}

// handlePauseChat rejects a missing until because zero means a pause with
// no end.
func (d Deps) handlePauseChat(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	var body struct {
		ChatID string `json:"chat_id"`
		Until  *int64 `json:"until"`
	}
	if !decode(w, r, &body) {
		return
	}
	chatID := store.StripDeviceSuffix(body.ChatID)
	if chatID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "chat_id is required"})
		return
	}
	if body.Until == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "until is required"})
		return
	}
	if !d.chatExists(w, chatID) {
		return
	}
	now := time.Now().Unix()
	until := *body.Until
	var (
		status string
		err    error
	)
	switch {
	case until == untilResume:
		status, err = "resumed", d.Store.ResumeChat(chatID, now)
	case until == 0:
		status, err = "paused", d.Store.SetPausedUntil(chatID, store.PausedForever)
	case until > now:
		status, err = "paused", d.Store.SetPausedUntil(chatID, min(until, store.PausedForever))
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "until must be a future unix time, 0 or -1"})
		return
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// chatExists rejects a missing chat so sending or pausing cannot silently
// do nothing.
func (d Deps) chatExists(w http.ResponseWriter, chatID string) bool {
	_, ok, err := d.Store.GetChat(chatID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "chat not found"})
		return false
	}
	return true
}
