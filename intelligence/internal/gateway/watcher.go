// Package gateway — fsnotify-based hot-reload watcher for model_registry.yaml.
//
// Purpose: Watch the registry file and atomically reload it in <500ms on change.
//          The active registry pointer is swapped under an RWMutex so concurrent
//          readers are never blocked longer than a pointer swap.
// Inputs:  path to model_registry.yaml; initial *Registry; change callback.
// Outputs: updated *Registry accessible via Get(); stops on ctx cancellation.
// Constraints: reload must complete in <500ms (log warning if exceeded).
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence gateway.
package gateway

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// RegistryWatcher holds the active registry and watches the YAML file for changes.
type RegistryWatcher struct {
	mu      sync.RWMutex
	current *Registry
	path    string
}

// NewRegistryWatcher creates a watcher seeded with initial (must be non-nil).
func NewRegistryWatcher(path string, initial *Registry) *RegistryWatcher {
	return &RegistryWatcher{path: path, current: initial}
}

// Get returns the current registry pointer. Safe for concurrent use.
func (w *RegistryWatcher) Get() *Registry {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.current
}

// Run starts the fsnotify watcher loop and blocks until ctx is cancelled.
// It should be called in a separate goroutine.
func (w *RegistryWatcher) Run(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(w.path); err != nil {
		return err
	}

	slog.Info("gateway: watching registry", "path", w.path)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				w.reload()
			}

		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("gateway: watcher error", "err", err)
		}
	}
}

// reload parses the YAML and swaps the registry pointer atomically.
// Logs a warning if the swap takes longer than 500ms.
func (w *RegistryWatcher) reload() {
	start := time.Now()

	reg, err := LoadRegistry(w.path)
	if err != nil {
		slog.Error("gateway: registry reload failed — keeping previous config", "err", err)
		return
	}

	w.mu.Lock()
	w.current = reg
	w.mu.Unlock()

	elapsed := time.Since(start)
	if elapsed > 500*time.Millisecond {
		slog.Warn("gateway: registry reload exceeded 500ms target", "elapsed_ms", elapsed.Milliseconds())
	} else {
		slog.Info("gateway: registry reloaded", "elapsed_ms", elapsed.Milliseconds())
	}
}
