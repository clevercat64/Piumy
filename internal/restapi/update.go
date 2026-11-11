package restapi

import (
	"net/http"

	"piumy-gateway/internal/store"
)

// Self-update (T176, ct-2026-11-10-2103). The tablero reads GET /api/update —
// the updater's own Status: is it on, is a newer version out, is it verified and
// waiting, why it is not installing right now, what was installed and when — and
// flips POST /api/admin/auto-update. The updater asks the SAME store setting on
// every look, so the switch applies at once, no restart. What makes installing
// safe (a signed release, an installed copy, a quiet queue) is code in
// internal/autoupdate; this is only the window onto it.

func (d Deps) handleGetUpdate(w http.ResponseWriter, r *http.Request) {
	if d.AutoUpdate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auto-update not available"})
		return
	}
	writeJSON(w, http.StatusOK, d.AutoUpdate.Status())
}

func (d Deps) handleSetAutoUpdate(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil || d.AutoUpdate == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "auto-update not available"})
		return
	}
	var body struct {
		// A pointer, so a body that forgets the field is refused instead of
		// silently reading as "off".
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `"enabled" (true/false) is required`})
		return
	}
	if err := d.Store.SetSettingBool(store.SettingAutoUpdate, *body.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, d.AutoUpdate.Status())
}
