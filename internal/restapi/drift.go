package restapi

import (
	"net/http"

	"piumy-gateway/internal/store"
)

// Drift reports (T174a, ct-2026-11-10-210158) are the owner's opt-in: the
// tablero's switch reads and writes store.SettingDriftReports, and
// driftreport.Reporter asks the SAME setting on every report, so flipping it
// applies at once, no restart. What a report may carry is closed in code in
// internal/driftreport; the wording that tells the owner what leaves and what
// never does lives next to the switch in the tablero (docs/T174A-DRIFT-REPORT.md
// has the fragment) and in docs/MANUAL.md.

type driftReportsView struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"` // where a report would go, exactly as configured
}

func (d Deps) handleGetDriftReports(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
		return
	}
	// The EFFECTIVE value: the owner's stored choice if they ever made one, else
	// the config default (off unless PIUMY_DRIFT_REPORTS).
	enabled := d.Store.SettingBool(store.SettingDriftReports, d.DriftReportsDefault)
	writeJSON(w, http.StatusOK, driftReportsView{Enabled: enabled, URL: d.DriftReportURL})
}

func (d Deps) handleSetDriftReports(w http.ResponseWriter, r *http.Request) {
	if d.Store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "store not available"})
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
	if err := d.Store.SetSettingBool(store.SettingDriftReports, *body.Enabled); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, driftReportsView{Enabled: *body.Enabled, URL: d.DriftReportURL})
}
