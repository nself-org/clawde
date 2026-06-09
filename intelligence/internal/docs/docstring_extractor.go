// docstring_extractor.go — extract leading doc comments for symbols.
//
// Purpose:    Reuse the W12-T01 repointel tree-sitter Extractor to pull symbols +
//             their leading doc comments, then batch-UPDATE clawde_symbols.docstring
//             keyed on (workspace_id, file_path, name). Docstrings are also surfaced
//             for enqueue to clawde_embed_queue (doc_type='docstring').
// Inputs:     repointel.Extractor (treesitter build-tag real / pure-Go stub),
//             SymbolDocStore (pgx in prod, stub in tests), workspace_id, file path.
// Outputs:    []DocstringRecord; batched UPDATE clawde_symbols SET docstring.
// Constraints: File ≤500 lines. No pgx import (interface seam). cgo only via the
//             repointel build-tag Extractor — build/test pass without cgo.
//
// SPORT: REGISTRY-FUNCTIONS.md → docs.DocstringExtractor.
package docs

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/repointel"
)

// DocstringRecord is one symbol's extracted doc comment.
type DocstringRecord struct {
	WorkspaceID uuid.UUID
	FilePath    string
	SymbolName  string
	Kind        string // function | method | class | type | const
	Docstring   string
	LineStart   int
}

// SymbolDocStore is the DB seam for persisting docstrings. The real
// implementation wraps pgx; tests inject a stub.
type SymbolDocStore interface {
	// UpdateDocstrings batch-applies:
	//   UPDATE clawde_symbols SET docstring = $d
	//   WHERE workspace_id = $w AND file_path = $f AND name = $n
	// for each record (no-op for records with an empty docstring).
	UpdateDocstrings(ctx context.Context, records []DocstringRecord) error
}

// DocstringExtractor extracts and persists symbol docstrings.
//
// It composes the existing repointel.Extractor seam (real tree-sitter under the
// `treesitter` build tag, pure-Go stub otherwise) so this package builds and
// tests without cgo.
type DocstringExtractor struct {
	extractor repointel.Extractor
	store     SymbolDocStore
}

// NewDocstringExtractor wires the repointel Extractor and a SymbolDocStore.
// Pass repointel.NewExtractor() for the build-tag-selected extractor.
func NewDocstringExtractor(extractor repointel.Extractor, store SymbolDocStore) *DocstringExtractor {
	return &DocstringExtractor{extractor: extractor, store: store}
}

// Extract parses content at filePath, returns every symbol that carries a
// non-empty leading doc comment, and (when a store is configured) batch-UPDATEs
// clawde_symbols.docstring. Symbols without a doc comment are skipped.
//
// lang is accepted for API symmetry with the wider docs pipeline; the underlying
// repointel.Extractor detects the language from the file extension.
func (e *DocstringExtractor) Extract(ctx context.Context, workspaceID uuid.UUID, filePath, lang string, content []byte) ([]DocstringRecord, error) {
	syms, _, err := e.extractor.ExtractSymbols(workspaceID, filePath, content)
	if err != nil {
		return nil, err
	}

	records := make([]DocstringRecord, 0, len(syms))
	for _, s := range syms {
		doc := normalizeDoc(s.DocComment)
		if doc == "" {
			continue
		}
		records = append(records, DocstringRecord{
			WorkspaceID: workspaceID,
			FilePath:    s.FilePath,
			SymbolName:  s.Name,
			Kind:        s.Kind,
			Docstring:   doc,
			LineStart:   s.LineStart,
		})
	}

	if len(records) > 0 && e.store != nil {
		if err := e.store.UpdateDocstrings(ctx, records); err != nil {
			return records, err
		}
	}
	return records, nil
}

// normalizeDoc strips comment markers and collapses a multi-line doc comment
// into clean prose suitable for embedding.
func normalizeDoc(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		// Strip Go/Rust/TS line markers and Python hash markers.
		t = strings.TrimPrefix(t, "///")
		t = strings.TrimPrefix(t, "//")
		t = strings.TrimPrefix(t, "#")
		// Strip block-comment fragments if present.
		t = strings.TrimPrefix(t, "*")
		t = strings.TrimPrefix(t, "/**")
		t = strings.TrimSuffix(t, "*/")
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		out = append(out, t)
	}
	return strings.TrimSpace(strings.Join(out, " "))
}

// AsChunk converts a DocstringRecord into a DocChunk tagged doc_type='docstring'
// so it can be enqueued to clawde_embed_queue alongside code chunks.
func (r DocstringRecord) AsChunk() DocChunk {
	return DocChunk{
		FilePath:  r.FilePath,
		Heading:   r.SymbolName,
		Content:   r.Docstring,
		DocType:   DocTypeDocstring,
		LineStart: r.LineStart,
		Level:     0,
	}
}
