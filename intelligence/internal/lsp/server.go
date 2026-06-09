// server.go — LSPServer interface and cross-file resolution logic.
//
// Purpose: Defines the LSPServer interface (seam for gopls, tsserver, and mocks).
//          CrossFileResolver iterates clawde_symbols rows, queries definition+references
//          via the server, and stores cross-file edges in clawde_graph_edges.
// Inputs:  LSPServer interface, SymbolSource (DB seam), EdgeStore (DB seam).
// Outputs: Cross-file edges upserted into clawde_graph_edges (RESOLVES/REFERENCES).
// Constraints: File ≤500 lines. No pgx import — DB seam only.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.LSPServer, lsp.CrossFileResolver.
package lsp

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// ── LSPServer interface ───────────────────────────────────────────────────────

// LSPServer is the interface any language server adapter must implement.
// Implemented by GoplsServer, TSServer, and MockServer (tests).
type LSPServer interface {
	// Name returns a human-readable identifier ("gopls", "tsserver", "mock").
	Name() string

	// Definition returns the definition location(s) for a symbol at pos.
	// Returns nil, nil when the server provides no result.
	Definition(ctx context.Context, fileURI string, pos Position) ([]Location, error)

	// References returns all reference locations for the symbol at pos.
	// Returns nil, nil when there are no references.
	References(ctx context.Context, fileURI string, pos Position) ([]Location, error)

	// Stop shuts down the server and releases resources.
	Stop(ctx context.Context) error
}

// ── DB seams ──────────────────────────────────────────────────────────────────

// SymbolRow is a minimal projection of clawde_symbols sufficient for LSP queries.
type SymbolRow struct {
	ID          uuid.UUID
	WorkspaceID uuid.UUID
	FilePath    string
	Name        string
	LineStart   int // 0-based line for LSP Position.Line
}

// SymbolSource is a read seam over clawde_symbols.
type SymbolSource interface {
	// ListSymbols returns all symbols for the workspace. Implementations
	// may paginate; the full slice is returned here for simplicity.
	ListSymbols(ctx context.Context, workspaceID uuid.UUID) ([]SymbolRow, error)
}

// CrossEdge is a single directed edge ready for DB upsert.
type CrossEdge struct {
	WorkspaceID uuid.UUID
	SrcSymbolID uuid.UUID
	DstFilePath string // destination file (different from src)
	DstLine     int
	DstChar     int
	Kind        string // EdgeKindResolves | EdgeKindReferences
	Metadata    string // JSON
}

// EdgeStore is a write seam over clawde_graph_edges.
type EdgeStore interface {
	// UpsertCrossEdges batch-inserts cross-file LSP edges.
	// ON CONFLICT (workspace_id, src_id, dst_type, dst_id, edge_kind) DO NOTHING.
	UpsertCrossEdges(ctx context.Context, edges []CrossEdge) error
}

// ── CrossFileResolver ─────────────────────────────────────────────────────────

// CrossFileResolver walks symbols in the DB, sends LSP definition+references
// requests, and persists cross-file edges.
type CrossFileResolver struct {
	server      LSPServer
	symbols     SymbolSource
	edges       EdgeStore
	workspaceID uuid.UUID
	rootURI     string // "file:///abs/path/to/root"
}

// NewCrossFileResolver creates a CrossFileResolver.
// rootURI should be a "file:///" URI for the workspace root.
func NewCrossFileResolver(
	server LSPServer,
	symbols SymbolSource,
	edges EdgeStore,
	workspaceID uuid.UUID,
	rootURI string,
) *CrossFileResolver {
	return &CrossFileResolver{
		server:      server,
		symbols:     symbols,
		edges:       edges,
		workspaceID: workspaceID,
		rootURI:     rootURI,
	}
}

// Resolve iterates all workspace symbols and resolves cross-file locations.
// Stats returned: (totalSymbols, crossFileEdges, errors).
func (r *CrossFileResolver) Resolve(ctx context.Context) (total, crossFile, errs int, err error) {
	syms, err := r.symbols.ListSymbols(ctx, r.workspaceID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("lsp: list symbols: %w", err)
	}
	total = len(syms)
	slog.Info("lsp: resolving cross-file edges", "server", r.server.Name(), "symbols", total)

	var batch []CrossEdge
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if uErr := r.edges.UpsertCrossEdges(ctx, batch); uErr != nil {
			return uErr
		}
		batch = batch[:0]
		return nil
	}

	for _, sym := range syms {
		if ctx.Err() != nil {
			break
		}
		fileURI := filePathToURI(sym.FilePath)
		pos := Position{Line: sym.LineStart, Character: 0}

		// textDocument/definition
		defs, dErr := r.server.Definition(ctx, fileURI, pos)
		if dErr != nil {
			errs++
			slog.Debug("lsp: definition error", "symbol", sym.Name, "err", dErr)
		}
		for _, loc := range defs {
			dstFile := uriToFilePath(loc.URI)
			if isSameFile(sym.FilePath, dstFile) {
				continue
			}
			crossFile++
			batch = append(batch, CrossEdge{
				WorkspaceID: r.workspaceID,
				SrcSymbolID: sym.ID,
				DstFilePath: dstFile,
				DstLine:     loc.Range.Start.Line,
				DstChar:     loc.Range.Start.Character,
				Kind:        EdgeKindResolves,
				Metadata:    fmt.Sprintf(`{"uri":%q}`, loc.URI),
			})
		}

		// textDocument/references
		refs, rErr := r.server.References(ctx, fileURI, pos)
		if rErr != nil {
			errs++
			slog.Debug("lsp: references error", "symbol", sym.Name, "err", rErr)
		}
		for _, loc := range refs {
			dstFile := uriToFilePath(loc.URI)
			if isSameFile(sym.FilePath, dstFile) {
				continue
			}
			crossFile++
			batch = append(batch, CrossEdge{
				WorkspaceID: r.workspaceID,
				SrcSymbolID: sym.ID,
				DstFilePath: dstFile,
				DstLine:     loc.Range.Start.Line,
				DstChar:     loc.Range.Start.Character,
				Kind:        EdgeKindReferences,
				Metadata:    fmt.Sprintf(`{"uri":%q}`, loc.URI),
			})
		}

		if len(batch) >= 200 {
			if fErr := flush(); fErr != nil {
				slog.Warn("lsp: edge flush error", "err", fErr)
			}
		}
	}
	_ = flush()
	slog.Info("lsp: resolution complete",
		"server", r.server.Name(),
		"total", total, "cross_file", crossFile, "errors", errs)
	return total, crossFile, errs, nil
}

// ── URI helpers ───────────────────────────────────────────────────────────────

// filePathToURI converts an absolute POSIX path to a file:// URI.
func filePathToURI(path string) string {
	if strings.HasPrefix(path, "file://") {
		return path
	}
	return "file://" + path
}

// uriToFilePath strips the "file://" prefix.
func uriToFilePath(uri string) string {
	return strings.TrimPrefix(uri, "file://")
}

// isSameFile returns true when two path strings point to the same file,
// handling URI vs plain-path differences.
func isSameFile(a, b string) bool {
	a = uriToFilePath(a)
	b = uriToFilePath(b)
	return a == b
}
