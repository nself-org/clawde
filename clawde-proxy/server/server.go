// Package server implements the clawde-proxy HTTP daemon lifecycle.
// Purpose: Bind HTTP mux on the configured address, serve /health, return 501 for all other routes.
// Inputs:  addr string (e.g. "127.0.0.1:3780") from config.
// Outputs: HTTP server ready to accept connections; Shutdown closes gracefully within deadline.
// Constraints: Uses stdlib net/http only. No external router dependency in this scaffold.
//   All non-health routes return 501 {"error":"not_implemented"}.
//   Graceful shutdown deadline enforced via context passed by caller.
// SPORT: F08-SERVICE-INVENTORY.md clawde-proxy row (port 3780, status=scaffolded)
package server

import (
	"context"
	"net/http"
	"time"
)

// Server wraps an *http.Server with clawde-proxy lifecycle helpers.
type Server struct {
	// addr is the bind address, e.g. "127.0.0.1:3780".
	addr string
	// srv is the underlying HTTP server.
	srv *http.Server
	// startTime is recorded when Start() is called for uptime reporting.
	startTime time.Time
}

// New creates a Server bound to addr with all routes registered.
// Purpose: Construct the HTTP mux with health + stub handlers; no I/O yet.
// Inputs:  addr — TCP address string, e.g. "127.0.0.1:3780".
// Outputs: *Server ready for Start().
// Constraints: Does not bind the port until Start() is called.
func New(addr string) *Server {
	s := &Server{addr: addr}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)

	// Stub all routes documented in spec §6.
	for _, path := range stubRoutes() {
		mux.HandleFunc(path, handleStub)
	}

	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	return s
}

// Start begins accepting connections. Blocks until the server closes.
// Purpose: Called in a goroutine by main; returns http.ErrServerClosed on clean shutdown.
// Inputs:  none (address set in New).
// Outputs: error from http.Server.ListenAndServe (http.ErrServerClosed on clean stop).
// Constraints: Sets startTime so /health can report uptime_s.
func (s *Server) Start() error {
	s.startTime = time.Now()
	return s.srv.ListenAndServe()
}

// Shutdown gracefully closes the HTTP server within ctx deadline.
// Purpose: Called by main after receiving SIGTERM/SIGINT.
// Inputs:  ctx with a deadline (caller sets 5s per CLAWDE_SHUTDOWN_TIMEOUT_S default).
// Outputs: error if shutdown does not complete within deadline.
// Constraints: Does NOT remove the PID file — caller is responsible.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
