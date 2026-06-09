// heuristic_extractor.go — condense SessionSignals into a retrieval query.
//
// Purpose: Build a short (<=512 char) free-text retrieval query from raw editor
//          signals without an LLM. Combines top symbols for the active file,
//          visible symbols, function names parsed from the recent diff, and the
//          head of the last error.
// Inputs:  SessionSignals; an optional SymbolStore for per-file top symbols.
// Outputs: query string (<=512 chars).
// Constraints: Pure aside from the SymbolStore DB read. File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → compiler.ExtractQuery, compiler.SymbolStore.
package compiler

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/nself-org/clawde/intelligence/internal/retrieval/lanes"
)

const (
	// maxQueryLen caps the extracted query length.
	maxQueryLen = 512
	// maxErrorPrefix is how many chars of LastError are prepended.
	maxErrorPrefix = 100
	// topFileSymbols is how many per-file symbols to pull from the store.
	topFileSymbols = 5
)

// diffFuncRe matches added/removed Go func declarations in a unified diff line.
// Example match: "+ func (h *Kernel) Retrieve(" → captures "Retrieve".
var diffFuncRe = regexp.MustCompile(`(?m)^[+-]\s*func\s+(?:\([^)]*\)\s*)?([A-Za-z_][A-Za-z0-9_]*)`)

// SymbolStore returns the most-referenced symbol names for a file.
//
// Purpose: Seam over clawde_symbols so ExtractQuery can bias the query toward the
//          file's hottest symbols. The production impl uses lanes.DBQuerier; tests
//          inject a stub. A nil store is allowed — extraction proceeds without it.
// SPORT:   REGISTRY-FUNCTIONS.md → compiler.SymbolStore.
type SymbolStore interface {
	// TopSymbolsForFile returns up to limit symbol names for filePath,
	// ordered by occurrence_count descending.
	TopSymbolsForFile(ctx context.Context, workspaceID, filePath string, limit int) ([]string, error)
}

// ExtractQuery condenses SessionSignals into a retrieval query string.
//
// Order: {top file symbols} {visible symbols} {diff func names} {file stem}
// prefixed by the first 100 chars of LastError, truncated to 512 chars total.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.ExtractQuery.
func ExtractQuery(ctx context.Context, store SymbolStore, workspaceID string, s SessionSignals) string {
	var parts []string

	// Error head goes first so it is never truncated away.
	if e := strings.TrimSpace(s.LastError); e != "" {
		if len(e) > maxErrorPrefix {
			e = e[:maxErrorPrefix]
		}
		parts = append(parts, e)
	}

	// Top symbols for the active file (DB-backed, best-effort).
	if store != nil && s.ActiveFilePath != "" {
		if syms, err := store.TopSymbolsForFile(ctx, workspaceID, s.ActiveFilePath, topFileSymbols); err == nil {
			parts = append(parts, syms...)
		}
	}

	// Visible symbols from the viewport.
	parts = append(parts, s.VisibleSymbols...)

	// Function names parsed from the recent diff.
	parts = append(parts, diffFuncNames(s.RecentDiff)...)

	// File stem (basename without extension) for locality.
	if s.ActiveFilePath != "" {
		stem := strings.TrimSuffix(filepath.Base(s.ActiveFilePath), filepath.Ext(s.ActiveFilePath))
		if stem != "" {
			parts = append(parts, stem)
		}
	}

	query := strings.Join(dedupeNonEmpty(parts), " ")
	if len(query) > maxQueryLen {
		query = query[:maxQueryLen]
	}
	return strings.TrimSpace(query)
}

// diffFuncNames extracts func identifiers from added/removed diff lines.
func diffFuncNames(diff string) []string {
	if diff == "" {
		return nil
	}
	matches := diffFuncRe.FindAllStringSubmatch(diff, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}

// dedupeNonEmpty drops empty/whitespace entries and de-duplicates, preserving order.
func dedupeNonEmpty(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// pgSymbolStore is the production SymbolStore over clawde_symbols.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.pgSymbolStore.
type pgSymbolStore struct {
	db lanes.DBQuerier
}

// NewPgSymbolStore constructs a SymbolStore backed by the given DB querier.
func NewPgSymbolStore(db lanes.DBQuerier) SymbolStore {
	return &pgSymbolStore{db: db}
}

// topSymbolsSQL pulls the hottest symbols for a file within a workspace.
const topSymbolsSQL = `
SELECT name
FROM   clawde_symbols
WHERE  workspace_id = $1 AND file_path = $2
ORDER  BY occurrence_count DESC
LIMIT  $3`

// TopSymbolsForFile implements SymbolStore.
func (p *pgSymbolStore) TopSymbolsForFile(
	ctx context.Context, workspaceID, filePath string, limit int,
) ([]string, error) {
	rows, err := p.db.Query(ctx, topSymbolsSQL, workspaceID, filePath, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, nil
}
