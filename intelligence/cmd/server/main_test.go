// Package main — tests for cmd/server wiring helpers.
//
// Purpose: Verify dual-listener wiring logic and env-var helpers without
//          starting a real server or joining a Tailscale network.
// Constraints: No network calls; uses in-process net.Pipe for the listener seam.
// SPORT: REGISTRY-SERVICES.md — clawde-intelligence, Tailscale mesh.
package main

import (
	"net"
	"testing"

	"log/slog"
	"os"
)

// TestEnvOr verifies fallback behaviour.
func TestEnvOr(t *testing.T) {
	t.Setenv("_TEST_ENVVAR_XYZ", "hello")
	if got := envOr("_TEST_ENVVAR_XYZ", "default"); got != "hello" {
		t.Errorf("want %q, got %q", "hello", got)
	}
	if got := envOr("_TEST_ENVVAR_NOT_SET_XYZ", "default"); got != "default" {
		t.Errorf("want %q, got %q", "default", got)
	}
}

// mockGRPCSrv satisfies the interface expected by serveOnListener without
// starting a real gRPC server.
type mockGRPCSrv struct {
	serveCalled bool
	returnErr   error
}

func (m *mockGRPCSrv) Serve(l net.Listener) error {
	m.serveCalled = true
	_ = l.Close() // drain the listener
	return m.returnErr
}

// TestServeOnListener_NilServer ensures a nil grpcSrv is a no-op and closes
// the listener, without panicking.
func TestServeOnListener_NilServer(t *testing.T) {
	// Create a net.Pipe pair; use one end as the "listener".
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	pipeListener := &singleConnListener{conn: server, done: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// Should not panic.
	serveOnListener(nil, pipeListener, logger)
	// Listener should be closed.
	select {
	case <-pipeListener.done:
		// good
	default:
		// give goroutine a moment — but serveOnListener with nil is synchronous
		// after the guard, so done should already be closed.
		t.Error("expected listener to be closed for nil grpcSrv")
	}
}

// TestServeOnListener_ValidServer ensures the goroutine calls Serve.
func TestServeOnListener_ValidServer(t *testing.T) {
	srv := &mockGRPCSrv{}
	_, client := net.Pipe()
	defer client.Close()

	lis := &trackingListener{done: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	serveOnListener(srv, lis, logger)

	// Wait for Serve to be called (goroutine).
	select {
	case <-lis.done:
	}
	if !srv.serveCalled {
		t.Error("expected Serve to be called on non-nil grpcSrv")
	}
}

// ── listener stubs ──────────────────────────────────────────────────────────

// singleConnListener returns one connection then blocks; Close closes it.
type singleConnListener struct {
	conn net.Conn
	done chan struct{}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	select {
	case <-l.done:
		return nil, net.ErrClosed
	}
}
func (l *singleConnListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}
func (l *singleConnListener) Addr() net.Addr { return &net.TCPAddr{} }

// trackingListener signals done when Serve() calls Accept() (which blocks),
// relying on mockGRPCSrv.Serve closing it.
type trackingListener struct {
	done chan struct{}
}

func (l *trackingListener) Accept() (net.Conn, error) {
	return nil, net.ErrClosed
}
func (l *trackingListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}
func (l *trackingListener) Addr() net.Addr { return &net.TCPAddr{} }
