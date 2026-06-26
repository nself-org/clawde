// health.go — GET /health handler for clawde-proxy.
// Purpose: Liveness probe; returns 200 {"status":"ok","uptime_s":N}.
// Inputs:  HTTP GET /health.
// Outputs: 200 JSON {"status":"ok","uptime_s":N} where N is integer seconds since start.
// Constraints: Always returns 200. No auth required (internal-only 127.0.0.1 binding).
package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// healthResponse is the JSON body for GET /health.
type healthResponse struct {
	Status   string `json:"status"`
	UptimeS  int    `json:"uptime_s"`
}

// handleHealth handles GET /health.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	resp := healthResponse{
		Status:  "ok",
		UptimeS: int(time.Since(s.startTime).Seconds()),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}
