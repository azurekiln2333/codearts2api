package server

import "net/http"

// handleConnections is registered behind the existing admin API-key guard. It
// reads in-memory metadata only; opening the view cannot launch upstream work.
func (h *Handler) handleConnections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, h.cfg.Audit.Connections())
}
