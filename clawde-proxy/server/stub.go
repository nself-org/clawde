// stub.go — 501 stub handlers for all clawde-proxy routes not yet implemented.
// Purpose: Ensure every spec §6 route returns 501 (not 404) so clients get a clear
//   "not implemented" signal rather than a routing miss.
// Inputs:  any HTTP method on any registered stub path.
// Outputs: 501 {"error":"not_implemented"}.
// Constraints: These stubs are replaced by real handlers in subsequent tickets.
//   All paths listed here must match spec §6 exactly.
package server

import (
	"net/http"
)

// stubRoutes returns all route paths from spec §6 that are not yet implemented.
// These are registered on the mux by server.New() and return 501.
func stubRoutes() []string {
	return []string{
		// Chat completions proxy (OpenAI-compatible).
		"/v1/chat/completions",
		// Session management.
		"/v1/sessions",
		"/v1/sessions/",
		// Worktree registry.
		"/v1/worktrees",
		"/v1/worktrees/",
		// Routing table management.
		"/v1/routes",
		"/v1/routes/",
		// Request log.
		"/v1/logs",
		// Admin: reload routing table.
		"/v1/admin/reload",
		// Metrics endpoint (future Prometheus scrape).
		"/metrics",
	}
}

// handleStub returns 501 {"error":"not_implemented"} for unimplemented routes.
func handleStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	w.Write([]byte(`{"error":"not_implemented"}` + "\n"))
}
