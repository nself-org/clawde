// Package repointel — repo watcher, tree-sitter AST extractor, symbol ingestion pipeline.
//
// Purpose: Watch source files in a workspace for changes, extract symbols via
//          tree-sitter AST parsing, and upsert them into clawde_symbols + call edges
//          into clawde_graph_edges. Provides both incremental (on-change) and full-scan
//          (workspace init) ingestion modes.
// Inputs:  workspace root path, workspace_id UUID, DB ingester.
// Outputs: FileChangeEvent stream; SymbolRecord slices; DB upserts.
// Constraints: File ≤500 lines. Per-file debounce 200ms. No external brokers.
//
// SPORT: REGISTRY-FUNCTIONS.md → repointel.Watcher, REGISTRY-PACKAGES.md → repointel.
package repointel

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// FileOp represents the kind of filesystem change.
type FileOp int

const (
	FileOpWrite  FileOp = iota // file created or modified
	FileOpRemove               // file deleted
)

// FileChangeEvent carries a single file-change notification after debounce.
type FileChangeEvent struct {
	Path string
	Op   FileOp
}

// supportedExts is the set of file extensions this watcher cares about.
var supportedExts = map[string]bool{
	".go":   true,
	".rs":   true,
	".ts":   true,
	".tsx":  true,
	".py":   true,
	".dart": true,
}

// debounceDelay is the per-file quiet period before emitting a change event.
const debounceDelay = 200 * time.Millisecond

// ── Watcher ───────────────────────────────────────────────────────────────────

// Watcher watches a directory tree for source-file changes and emits
// debounced FileChangeEvent values on Events().
type Watcher struct {
	root   string
	fswatcher *fsnotify.Watcher
	events chan FileChangeEvent
	mu     sync.Mutex
	timers map[string]*time.Timer
}

// NewWatcher creates a Watcher for the given root directory.
// Returns an error if fsnotify cannot be initialised.
func NewWatcher(root string) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		root:      root,
		fswatcher: fsw,
		events:    make(chan FileChangeEvent, 256),
		timers:    make(map[string]*time.Timer),
	}, nil
}

// Events returns the channel on which debounced FileChangeEvents are emitted.
func (w *Watcher) Events() <-chan FileChangeEvent { return w.events }

// Run starts the watcher loop, recursively watching the root directory,
// and blocks until ctx is cancelled or a fatal fsnotify error occurs.
func (w *Watcher) Run(ctx context.Context) error {
	if err := w.addRecursive(w.root); err != nil {
		return err
	}
	slog.Info("repointel: watcher started", "root", w.root)

	for {
		select {
		case <-ctx.Done():
			_ = w.fswatcher.Close()
			return ctx.Err()

		case ev, ok := <-w.fswatcher.Events:
			if !ok {
				return nil
			}
			w.handleEvent(ev)

		case err, ok := <-w.fswatcher.Errors:
			if !ok {
				return nil
			}
			slog.Warn("repointel: fsnotify error", "err", err)
		}
	}
}

// Close releases fsnotify resources.
func (w *Watcher) Close() error { return w.fswatcher.Close() }

// ── private helpers ───────────────────────────────────────────────────────────

// addRecursive walks root and adds every directory to the fsnotify watcher.
func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if d.IsDir() {
			// Skip hidden directories (.git, node_modules, etc.)
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return w.fswatcher.Add(path)
		}
		return nil
	})
}

// handleEvent debounces a raw fsnotify event, emitting a FileChangeEvent
// exactly once per file per quiet period.
func (w *Watcher) handleEvent(ev fsnotify.Event) {
	path := ev.Name
	if !supportedExts[filepath.Ext(path)] {
		return
	}

	var op FileOp
	switch {
	case ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename):
		op = FileOpRemove
	default:
		op = FileOpWrite
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Cancel any existing debounce timer for this path.
	if t, ok := w.timers[path]; ok {
		t.Stop()
	}

	capturedOp := op
	capturedPath := path
	w.timers[path] = time.AfterFunc(debounceDelay, func() {
		select {
		case w.events <- FileChangeEvent{Path: capturedPath, Op: capturedOp}:
		default:
			slog.Warn("repointel: event channel full, dropping event", "path", capturedPath)
		}
		w.mu.Lock()
		delete(w.timers, capturedPath)
		w.mu.Unlock()
	})
}
