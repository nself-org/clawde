// ingestion.go — symbol + call-edge ingestion pipeline.
//
// Purpose: Upsert SymbolRecords into clawde_symbols and CallEdges into
//          clawde_graph_edges. Supports full-scan (workspace init) and
//          incremental (per FileChangeEvent) modes.
// Inputs:  Ingester interface (pgx in prod, stub in tests), workspace root,
//          workspace_id UUID, Extractor, Watcher event channel.
// Outputs: DB upserts; structured logs.
// Constraints: File ≤500 lines. Interface seam for DB — no pgx import here.
//
// SPORT: REGISTRY-FUNCTIONS.md → repointel.Ingester, repointel.Pipeline.
package repointel

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"
)

// ── DB interface seam ─────────────────────────────────────────────────────────

// SymbolStore is the storage interface the ingestion pipeline depends on.
// The real implementation wraps pgx; tests inject a stub.
type SymbolStore interface {
	// UpsertSymbols batch-inserts or updates symbols in clawde_symbols.
	// ON CONFLICT (workspace_id, file_path, name, kind) DO UPDATE.
	UpsertSymbols(ctx context.Context, symbols []SymbolRecord) error

	// DeleteSymbolsForFile removes all symbols for a given file (used on FileOpRemove).
	DeleteSymbolsForFile(ctx context.Context, workspaceID uuid.UUID, filePath string) error

	// UpsertEdges batch-inserts call edges into clawde_graph_edges.
	// ON CONFLICT DO NOTHING (edges are idempotent by src+dst+kind).
	UpsertEdges(ctx context.Context, workspaceID uuid.UUID, edges []CallEdge) error

	// DeleteEdgesForFile removes all graph edges whose file_path matches (via metadata).
	DeleteEdgesForFile(ctx context.Context, workspaceID uuid.UUID, filePath string) error
}

// ── Pipeline ──────────────────────────────────────────────────────────────────

// Pipeline wires a Watcher, Extractor, and SymbolStore into a running
// incremental symbol ingestion loop.
type Pipeline struct {
	workspaceID uuid.UUID
	root        string
	extractor   Extractor
	store       SymbolStore
}

// NewPipeline creates a Pipeline.
func NewPipeline(workspaceID uuid.UUID, root string, extractor Extractor, store SymbolStore) *Pipeline {
	return &Pipeline{
		workspaceID: workspaceID,
		root:        root,
		extractor:   extractor,
		store:       store,
	}
}

// FullScan extracts symbols from every supported file under root and upserts them.
// Runs on workspace init. Thread-safe; may be called from a goroutine.
func (p *Pipeline) FullScan(ctx context.Context) error {
	slog.Info("repointel: full scan started", "root", p.root)

	var mu sync.Mutex
	var allSymbols []SymbolRecord
	var allEdges []CallEdge

	err := filepath.WalkDir(p.root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip inaccessible
		}
		if d.IsDir() {
			base := filepath.Base(path)
			if strings.HasPrefix(base, ".") || base == "node_modules" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !supportedExts[filepath.Ext(path)] {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			slog.Warn("repointel: cannot read file", "path", path, "err", err)
			return nil
		}

		syms, edges, err := p.extractor.ExtractSymbols(p.workspaceID, path, content)
		if err != nil {
			slog.Warn("repointel: extraction error", "path", path, "err", err)
			return nil
		}

		mu.Lock()
		allSymbols = append(allSymbols, syms...)
		allEdges = append(allEdges, edges...)
		mu.Unlock()
		return nil
	})
	if err != nil {
		return err
	}

	if len(allSymbols) > 0 {
		if err := p.store.UpsertSymbols(ctx, allSymbols); err != nil {
			return err
		}
	}
	if len(allEdges) > 0 {
		if err := p.store.UpsertEdges(ctx, p.workspaceID, allEdges); err != nil {
			return err
		}
	}

	slog.Info("repointel: full scan complete",
		"symbols", len(allSymbols),
		"edges", len(allEdges),
	)
	return nil
}

// Run starts the incremental ingestion loop, consuming events from eventCh.
// Blocks until ctx is cancelled.
func (p *Pipeline) Run(ctx context.Context, eventCh <-chan FileChangeEvent) {
	slog.Info("repointel: incremental pipeline started", "workspace", p.workspaceID)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-eventCh:
			if !ok {
				return
			}
			p.handleChange(ctx, ev)
		}
	}
}

// handleChange processes a single FileChangeEvent: delete-on-remove, upsert-on-write.
func (p *Pipeline) handleChange(ctx context.Context, ev FileChangeEvent) {
	switch ev.Op {
	case FileOpRemove:
		if err := p.store.DeleteSymbolsForFile(ctx, p.workspaceID, ev.Path); err != nil {
			slog.Warn("repointel: delete symbols failed", "path", ev.Path, "err", err)
		}
		if err := p.store.DeleteEdgesForFile(ctx, p.workspaceID, ev.Path); err != nil {
			slog.Warn("repointel: delete edges failed", "path", ev.Path, "err", err)
		}
	case FileOpWrite:
		content, err := os.ReadFile(ev.Path)
		if err != nil {
			slog.Warn("repointel: cannot read file on change", "path", ev.Path, "err", err)
			return
		}
		syms, edges, err := p.extractor.ExtractSymbols(p.workspaceID, ev.Path, content)
		if err != nil {
			slog.Warn("repointel: extraction error on change", "path", ev.Path, "err", err)
			return
		}
		// Delete stale symbols for this file first (handles renames/deletions of symbols).
		_ = p.store.DeleteSymbolsForFile(ctx, p.workspaceID, ev.Path)
		_ = p.store.DeleteEdgesForFile(ctx, p.workspaceID, ev.Path)

		if len(syms) > 0 {
			if err := p.store.UpsertSymbols(ctx, syms); err != nil {
				slog.Warn("repointel: upsert symbols failed", "path", ev.Path, "err", err)
			}
		}
		if len(edges) > 0 {
			if err := p.store.UpsertEdges(ctx, p.workspaceID, edges); err != nil {
				slog.Warn("repointel: upsert edges failed", "path", ev.Path, "err", err)
			}
		}
		slog.Debug("repointel: file processed", "path", ev.Path, "symbols", len(syms), "edges", len(edges))
	}
}

// ── Utility ───────────────────────────────────────────────────────────────────

// extOf returns the lowercase file extension (e.g. ".go") or "" if none.
func extOf(path string) string {
	return strings.ToLower(filepath.Ext(path))
}
