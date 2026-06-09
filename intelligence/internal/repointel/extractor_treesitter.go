//go:build treesitter

// extractor_treesitter.go — real tree-sitter AST extractor (cgo required).
//
// Purpose: Parse Go/Rust/TypeScript/Python/Dart source using tree-sitter grammars.
//          Extracts function_declaration, method_declaration, class_declaration,
//          type_declaration, const_declaration, function_definition, def_statement.
//          Extracts CALLS edges from call_expression nodes.
// Inputs:  workspace_id UUID, file path, file content bytes.
// Outputs: []SymbolRecord, []CallEdge.
// Constraints: Grammars compiled at init(); panic on grammar compile error (misconfiguration).
//
// Build: CGO_ENABLED=1 CC="$(xcrun --find cc)" \
//        CGO_CFLAGS="-isysroot $(xcrun --show-sdk-path)" \
//        CGO_LDFLAGS="-isysroot $(xcrun --show-sdk-path)" \
//        go build -tags treesitter ./...
//
// SPORT: REGISTRY-FUNCTIONS.md → repointel.ExtractSymbols (real).
package repointel

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

// ── Grammar registry ──────────────────────────────────────────────────────────

// langGrammar maps Language → compiled tree-sitter language.
var langGrammar map[Language]*sitter.Language

func init() {
	langGrammar = map[Language]*sitter.Language{
		LangGo:         golang.GetLanguage(),
		LangRust:       rust.GetLanguage(),
		LangTypeScript: typescript.GetLanguage(),
		LangPython:     python.GetLanguage(),
	}
	// TSX uses the tsx grammar (TypeScript with JSX).
	// Dart grammar is not bundled in smacker/go-tree-sitter; skip without panic.
	for lang, g := range langGrammar {
		if g == nil {
			panic("repointel: tree-sitter grammar compiled to nil for language " + langName(lang))
		}
	}
	slog.Info("repointel: tree-sitter grammars compiled", "languages", "Go,Rust,TypeScript,Python")
}

// ── Node-type matchers ────────────────────────────────────────────────────────

// symbolNodeTypes lists tree-sitter node types that produce SymbolRecords.
// Note: TypeScript shares "function_declaration"/"class_declaration" node names with Go.
// The language-specific grammar will only produce its own node types.
var symbolNodeTypes = map[string]string{
	// Go + TypeScript (shared node type names, handled per grammar)
	"function_declaration": "function",
	"method_declaration":   "method",
	"class_declaration":    "class",
	"type_declaration":     "type",
	"const_declaration":    "const",
	// TypeScript-specific
	"method_definition":   "method",
	"lexical_declaration": "const", // handles 'const foo = ...'
	// Rust
	"function_item": "function",
	"impl_item":     "class",
	"struct_item":   "type",
	"enum_item":     "type",
	"const_item":    "const",
	"trait_item":    "type",
	// Python
	"function_definition": "function",
	"class_definition":    "class",
	// Generic fallback (older Python grammar variants)
	"def_statement": "function",
}

// callNodeTypes lists tree-sitter node types that represent call expressions.
var callNodeTypes = map[string]bool{
	"call_expression": true,
	"call":            true,
}

// ── TSExtractor ───────────────────────────────────────────────────────────────

// TSExtractor is the real tree-sitter backed Extractor.
type TSExtractor struct {
	parser *sitter.Parser
}

// NewExtractor returns a TSExtractor backed by real cgo tree-sitter grammars.
func NewExtractor() Extractor {
	return &TSExtractor{parser: sitter.NewParser()}
}

// ExtractSymbols parses content at filePath and returns all symbols and call edges.
func (e *TSExtractor) ExtractSymbols(workspaceID uuid.UUID, filePath string, content []byte) ([]SymbolRecord, []CallEdge, error) {
	lang := detectLang(filePath)
	if lang == LangUnknown {
		return nil, nil, nil
	}

	grammar, ok := langGrammar[lang]
	if !ok {
		// Dart: grammar not bundled; return empty without error.
		return nil, nil, nil
	}

	e.parser.SetLanguage(grammar)
	tree, err := e.parser.ParseCtx(context.Background(), nil, content)
	if err != nil {
		return nil, nil, err
	}
	defer tree.Close()

	root := tree.RootNode()
	lines := bytes.Split(content, []byte("\n"))

	var symbols []SymbolRecord
	var calls []CallEdge
	var currentFunc string

	walkNode(root, func(n *sitter.Node) {
		t := n.Type()

		// ── Symbol extraction ─────────────────────────────────────────────────
		if kind, ok := symbolNodeTypes[t]; ok {
			name := extractName(n, content)
			if name == "" {
				return
			}
			sig := extractSignature(n, content)
			doc := extractDoc(n, lines)

			sym := SymbolRecord{
				WorkspaceID: workspaceID,
				FilePath:    filePath,
				LineStart:   int(n.StartPoint().Row) + 1,
				LineEnd:     int(n.EndPoint().Row) + 1,
				Name:        name,
				Kind:        kind,
				Signature:   sig,
				DocComment:  doc,
			}
			symbols = append(symbols, sym)

			if kind == "function" || kind == "method" {
				currentFunc = name
			}
			return
		}

		// ── Call edge extraction ──────────────────────────────────────────────
		if callNodeTypes[t] {
			callee := extractCalleeName(n, content)
			if callee != "" && currentFunc != "" {
				calls = append(calls, CallEdge{
					WorkspaceID:  workspaceID,
					CallerSymbol: currentFunc,
					CalleeName:   callee,
					FilePath:     filePath,
					Line:         int(n.StartPoint().Row) + 1,
				})
			}
		}
	})

	return symbols, calls, nil
}

// ── AST helpers ───────────────────────────────────────────────────────────────

// walkNode performs a depth-first pre-order traversal of the tree-sitter AST.
func walkNode(n *sitter.Node, fn func(*sitter.Node)) {
	if n == nil {
		return
	}
	fn(n)
	for i := 0; i < int(n.ChildCount()); i++ {
		walkNode(n.Child(i), fn)
	}
}

// extractName returns the identifier name from a declaration node.
func extractName(n *sitter.Node, content []byte) string {
	for i := 0; i < int(n.ChildCount()); i++ {
		child := n.Child(i)
		if child.Type() == "identifier" || child.Type() == "name" || child.Type() == "field_identifier" {
			return string(content[child.StartByte():child.EndByte()])
		}
	}
	return ""
}

// extractSignature returns the first line of the node content as a signature approximation.
func extractSignature(n *sitter.Node, content []byte) string {
	nodeContent := string(content[n.StartByte():n.EndByte()])
	// Trim to the first line (before the opening brace / body).
	if idx := strings.IndexAny(nodeContent, "{\n"); idx > 0 {
		nodeContent = strings.TrimSpace(nodeContent[:idx])
	}
	if len(nodeContent) > 200 {
		nodeContent = nodeContent[:200] + "..."
	}
	return nodeContent
}

// extractDoc looks for a leading comment immediately before node's start line.
func extractDoc(n *sitter.Node, lines [][]byte) string {
	startLine := int(n.StartPoint().Row)
	if startLine == 0 {
		return ""
	}
	// Walk backwards collecting comment lines.
	var docLines []string
	for i := startLine - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(string(lines[i]))
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "///") {
			docLines = append([]string{trimmed}, docLines...)
		} else {
			break
		}
	}
	return strings.Join(docLines, "\n")
}

// extractCalleeName returns the function/method name from a call_expression node.
func extractCalleeName(n *sitter.Node, content []byte) string {
	// First child of call_expression is typically the function being called.
	if n.ChildCount() == 0 {
		return ""
	}
	fn := n.Child(0)
	raw := string(content[fn.StartByte():fn.EndByte()])
	// For member calls (a.b()), return just the method name.
	if idx := strings.LastIndex(raw, "."); idx >= 0 {
		raw = raw[idx+1:]
	}
	// Strip whitespace and trailing parens.
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "()"))
	return raw
}

// detectLang maps a file path to Language using its extension.
func detectLang(path string) Language {
	switch filepath.Ext(path) {
	case ".go":
		return LangGo
	case ".rs":
		return LangRust
	case ".ts":
		return LangTypeScript
	case ".tsx":
		// tsx grammar variant — mapped to TypeScript for smacker library.
		_ = tsx.GetLanguage() // compiled; kept for completeness
		return LangTypeScript
	case ".py":
		return LangPython
	case ".dart":
		return LangDart
	default:
		return LangUnknown
	}
}

// langName returns a human-readable language name for error messages.
func langName(l Language) string {
	switch l {
	case LangGo:
		return "Go"
	case LangRust:
		return "Rust"
	case LangTypeScript:
		return "TypeScript"
	case LangPython:
		return "Python"
	case LangDart:
		return "Dart"
	default:
		return "Unknown"
	}
}
