// Package compiler — auto-context compiler (pre-prompt enrichment pipeline).
//
// Purpose:    Turn raw editor session signals into a budgeted, provenance-labeled
//             <clawde_context> block injected before the first LLM call. Pipeline:
//             SessionSignals → ExtractQuery → RetrieveContext → Allocate → format.
//             Routed through the ADR-003 dispatch chain (PolicyEngine) at the
//             SessionStart hook; never bypasses it.
// Inputs:     CompileContextRequest{WorkspaceID, Signals}.
// Outputs:    CompileContextResponse{ContextBlock, Enriched, TokenCount, CacheHit}.
// Constraints: 60/25/15 chunk/symbol/finding token split under
//              CLAWDE_CONTEXT_TOKEN_LIMIT (default 8192). CLAWDE_AUTO_CONTEXT
//              default true. 30s per-workspace cache TTL. File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → compiler.CompileContext, compiler.ExtractQuery,
//        compiler.Allocate. REGISTRY-ENDPOINTS.md → CompileContext RPC.
package compiler

// SessionSignals is the raw editor state a host emits at session start.
//
// Purpose: Coarse, cheap-to-collect editor signals that the heuristic extractor
//          condenses into a retrieval query without an LLM round-trip.
// SPORT:   REGISTRY-FUNCTIONS.md → compiler.SessionSignals.
type SessionSignals struct {
	// ActiveFilePath is the file currently focused in the editor.
	ActiveFilePath string
	// VisibleSymbols are symbol names visible in the viewport.
	VisibleSymbols []string
	// RecentDiff is the working-tree diff text (unified format).
	RecentDiff string
	// LastError is the most recent error/diagnostic text, if any.
	LastError string
}

// CompileContextRequest is the input to CompileContext.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.CompileContextRequest.
type CompileContextRequest struct {
	// WorkspaceID scopes retrieval and the per-workspace cache.
	WorkspaceID string
	// Signals are the raw editor signals to enrich from.
	Signals SessionSignals
}

// CompileContextResponse is the result of CompileContext.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.CompileContextResponse.
type CompileContextResponse struct {
	// ContextBlock is the formatted <clawde_context> block (may be empty).
	ContextBlock string
	// Enriched is true when context was produced (false when disabled or empty).
	Enriched bool
	// TokenCount is the token size of ContextBlock.
	TokenCount int
	// CacheHit is true when this result was served from the TTL cache.
	CacheHit bool
}

// RetrievedItem types carry provenance (score + method) so the compiler can
// label each line and trim lowest-scored items to fit the budget.

// ScoredChunk is a retrieved code chunk with provenance.
type ScoredChunk struct {
	FilePath  string
	LineStart int
	Lang      string
	Content   string
	// Score is the relevance score; higher is better.
	Score float64
	// Method is the retrieval lane: "dense" | "lexical" | "rerank".
	Method string
	// DocType is the chunk provenance: "code" | "markdown" | "docstring" |
	// "comment" (clawde_chunks.doc_type). Empty is treated as "code".
	DocType string
}

// ScoredSymbol is a matched symbol definition with provenance.
type ScoredSymbol struct {
	Name      string
	Kind      string
	Signature string
	FilePath  string
	Score     float64
}

// ScoredFinding is a static-analysis finding with provenance.
type ScoredFinding struct {
	Rule     string
	Severity string
	FilePath string
	Line     int
	Message  string
	Score    float64
}

// RetrievalResult is the provenance-bearing result the ContextRetriever returns.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.RetrievalResult.
type RetrievalResult struct {
	Chunks   []ScoredChunk
	Symbols  []ScoredSymbol
	Findings []ScoredFinding
}
