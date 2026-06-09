// lifecycle.go — LSP server lifecycle management with crash recovery.
//
// Purpose: Manages starting an LSPServer on workspace open, stopping it on close,
//          and restarting within 5 s on unexpected crash.
// Inputs:  ServerFactory func (returns LSPServer or ErrServerUnavailable),
//          workspace open/close signals via channels.
// Outputs: A running LSPServer accessible via Active(). Nil when unavailable.
// Constraints: File ≤500 lines. No external deps. Thread-safe.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.LifecycleManager.
package lsp

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const restartDelay = 5 * time.Second

// ServerFactory is a constructor for any LSPServer.
// Should return ErrServerUnavailable when the binary is absent.
type ServerFactory func(ctx context.Context, root string) (LSPServer, error)

// LifecycleManager manages a single LSPServer instance.
type LifecycleManager struct {
	factory ServerFactory
	root    string

	mu     sync.RWMutex
	active LSPServer
	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewLifecycleManager creates a LifecycleManager.
// Call Open to start the server.
func NewLifecycleManager(factory ServerFactory, root string) *LifecycleManager {
	return &LifecycleManager{
		factory: factory,
		root:    root,
		stopCh:  make(chan struct{}),
	}
}

// Open starts the server (called on workspace open).
// Blocks until the first start attempt completes (success or ErrServerUnavailable).
// Subsequent restarts happen in the background.
func (m *LifecycleManager) Open(ctx context.Context) {
	srv, err := m.factory(ctx, m.root)
	if err != nil {
		slog.Warn("lsp: server unavailable on open", "root", m.root, "err", err)
		return
	}
	m.mu.Lock()
	m.active = srv
	m.mu.Unlock()
	slog.Info("lsp: server started", "name", srv.Name(), "root", m.root)

	m.wg.Add(1)
	go m.watchAndRestart(ctx, srv)
}

// Close stops the server (called on workspace close).
func (m *LifecycleManager) Close(ctx context.Context) {
	close(m.stopCh)
	m.wg.Wait()

	m.mu.Lock()
	srv := m.active
	m.active = nil
	m.mu.Unlock()

	if srv != nil {
		if err := srv.Stop(ctx); err != nil {
			slog.Warn("lsp: server stop error", "err", err)
		}
	}
}

// Active returns the current LSPServer, or nil if unavailable.
// Safe for concurrent use.
func (m *LifecycleManager) Active() LSPServer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// watchAndRestart monitors the server process and restarts on crash.
// Exits when m.stopCh is closed or ctx is cancelled.
func (m *LifecycleManager) watchAndRestart(ctx context.Context, initial LSPServer) {
	defer m.wg.Done()
	current := initial

	for {
		// Wait for crash (server's done channel).
		// We detect crash by polling Active() == current and process exit.
		// Since LSPServer does not expose a done channel, we use a health ticker.
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-serverDone(ctx, current):
			// Server exited unexpectedly.
		}

		slog.Warn("lsp: server crashed, restarting", "name", current.Name(), "delay", restartDelay)

		m.mu.Lock()
		m.active = nil
		m.mu.Unlock()

		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(restartDelay):
		}

		slog.Info("lsp: restarting server", "name", current.Name())
		srv, err := m.factory(ctx, m.root)
		if err != nil {
			slog.Warn("lsp: restart failed", "err", err)
			return
		}
		m.mu.Lock()
		m.active = srv
		current = srv
		m.mu.Unlock()
		slog.Info("lsp: server restarted", "name", srv.Name())
	}
}

// serverDone returns a channel that closes when the server is no longer
// responding. We implement this as a periodic health ping: attempt a
// textDocument/definition on a synthetic URI; if ctx is cancelled or
// ErrServerUnavailable arises, return closed.
//
// For servers that don't export a native done channel, this is the
// lightweight signal mechanism.
func serverDone(ctx context.Context, srv LSPServer) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// Ping: a request that always returns quickly (even if result is null).
				_, err := srv.Definition(ctx, "file:///ping", Position{})
				if err != nil {
					// Any transport-level error = server gone.
					return
				}
			}
		}
	}()
	return ch
}
