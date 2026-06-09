// protocol.go — LSP protocol types for initialize, textDocument/definition,
//               and textDocument/references requests.
//
// Purpose: Minimal LSP protocol structs. Only the subset needed for cross-file
//          symbol resolution (initialize handshake, definition, references).
// Inputs:  Used by gopls.go and tsserver.go adapters.
// Outputs: Typed structs serialized to JSON-RPC params.
// Constraints: No external deps. File ≤500 lines.
//
// SPORT: REGISTRY-FUNCTIONS.md → lsp.InitializeParams, lsp.Location.
package lsp

// ── Initialize handshake ──────────────────────────────────────────────────────

// InitializeParams is sent as the first request to the server.
type InitializeParams struct {
	ProcessID    *int               `json:"processId"`
	RootURI      string             `json:"rootUri"`
	Capabilities ClientCapabilities `json:"capabilities"`
}

// ClientCapabilities declares what the client supports.
type ClientCapabilities struct {
	TextDocument TextDocumentClientCapabilities `json:"textDocument"`
}

// TextDocumentClientCapabilities lists per-feature capability declarations.
type TextDocumentClientCapabilities struct {
	Definition DynamicRegistrationCapability `json:"definition"`
	References DynamicRegistrationCapability `json:"references"`
}

// DynamicRegistrationCapability is the common { dynamicRegistration: bool } shape.
type DynamicRegistrationCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
}

// InitializeResult is the server's response to initialize.
type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

// ServerCapabilities is a partial decode of what the server advertises.
type ServerCapabilities struct {
	DefinitionProvider bool `json:"definitionProvider"`
	ReferencesProvider bool `json:"referencesProvider"`
}

// ── TextDocument position ─────────────────────────────────────────────────────

// TextDocumentIdentifier wraps a document URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// Position is a zero-based line/character offset.
type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

// Range spans two positions within a document.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location is a (uri, range) pair returned by definition/references.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// ── textDocument/definition ───────────────────────────────────────────────────

// DefinitionParams is the request payload.
type DefinitionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// ── textDocument/references ───────────────────────────────────────────────────

// ReferenceContext controls whether the declaration itself is included.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// ReferenceParams is the request payload.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// ── textDocument/didOpen (notification) ──────────────────────────────────────

// TextDocumentItem is the document content sent with didOpen.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

// DidOpenTextDocumentParams wraps the document for the didOpen notification.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// ── LSIF edge kinds ───────────────────────────────────────────────────────────

// GraphEdgeKind maps LSP operations to clawde_graph_edges.edge_kind values.
const (
	EdgeKindResolves   = "resolves"   // textDocument/definition cross-file result
	EdgeKindReferences = "references" // textDocument/references cross-file result
)
