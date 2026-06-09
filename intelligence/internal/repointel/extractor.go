// extractor.go — tree-sitter AST symbol extractor.
//
// Purpose: Parse source files using tree-sitter grammars and extract SymbolRecords
//          (functions, classes, methods, types, consts) and CALLS edges.
// Inputs:  File path + content bytes + workspace_id UUID.
// Outputs: []SymbolRecord, []CallEdge.
// Constraints: File ≤500 lines. Grammars compiled at init(); panic on compile error.
//              Build tag 'treesitter' enables real cgo grammars.
//              Without the tag, ExtractSymbols returns an empty slice (no panic).
//
// SPORT: REGISTRY-FUNCTIONS.md → repointel.ExtractSymbols.

package repointel

import (
	"github.com/google/uuid"
)

// ── Domain types ──────────────────────────────────────────────────────────────

// SymbolRecord represents a single code symbol ready for DB upsert.
type SymbolRecord struct {
	WorkspaceID uuid.UUID
	FilePath    string
	LineStart   int
	LineEnd     int
	Name        string
	Kind        string // function | class | method | type | const
	Signature   string // optional; may be empty
	DocComment  string // optional; may be empty
}

// CallEdge represents a caller → callee relationship between symbols.
type CallEdge struct {
	WorkspaceID  uuid.UUID
	CallerSymbol string // caller Name in the same file
	CalleeName   string // callee Name (may be in another file)
	FilePath     string // file where the call expression lives
	Line         int
}

// Language identifies the grammar to use for parsing.
type Language int

const (
	LangUnknown  Language = iota
	LangGo
	LangRust
	LangTypeScript
	LangPython
	LangDart
)

// DetectLanguage maps a file extension to a Language.
func DetectLanguage(path string) Language {
	ext := extOf(path)
	switch ext {
	case ".go":
		return LangGo
	case ".rs":
		return LangRust
	case ".ts", ".tsx":
		return LangTypeScript
	case ".py":
		return LangPython
	case ".dart":
		return LangDart
	default:
		return LangUnknown
	}
}

// Extractor is the interface for extracting symbols from source content.
// The real implementation is in extractor_treesitter.go (build tag: treesitter).
// The stub implementation is in extractor_stub.go (default build).
type Extractor interface {
	// ExtractSymbols parses content at filePath and returns all symbols found.
	ExtractSymbols(workspaceID uuid.UUID, filePath string, content []byte) ([]SymbolRecord, []CallEdge, error)
}
