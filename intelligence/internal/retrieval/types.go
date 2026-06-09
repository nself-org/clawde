// Package retrieval — shared result types for the hybrid retrieval kernel.
//
// Purpose: Define ScoredChunk, SymbolMatch, and RetrievalContext so dense.go,
//          lexical.go, rrf.go, graph_expander.go, and hybrid.go share one type set.
// Inputs:  none (type declarations only).
// Outputs: ScoredChunk, SymbolMatch, RetrievalContext.
// Constraints: File ≤500 lines. No imports except stdlib.
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.ScoredChunk, retrieval.SymbolMatch,
//        retrieval.RetrievalContext.
package retrieval

import "github.com/google/uuid"

// ScoredChunk is a single retrieved chunk with a normalised relevance score.
//
// Purpose: Uniform result type returned by DenseRetriever, LexicalRetriever, and
//          the RRF merge step so downstream callers need not distinguish sources.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.ScoredChunk.
type ScoredChunk struct {
	// ID is the primary key of clawde_chunks.
	ID uuid.UUID

	// Content is the raw chunk text.
	Content string

	// FilePath is the source file this chunk was extracted from.
	FilePath string

	// Score is the relevance score. Higher is better.
	// Dense:   cosine similarity in [0,1].
	// Lexical: ts_rank_cd score (unbounded positive).
	// RRF:     sum of 1/(k+rank_i) from contributing lanes.
	Score float64

	// Source labels which retrieval lane contributed this chunk.
	// Values: "dense", "lexical", "rrf".
	Source string
}

// SymbolMatch is a symbol from clawde_symbols that closely matches the query.
//
// Purpose: Carry symbol metadata alongside chunks so callers can inject
//          definition context into the LLM prompt without a second DB round-trip.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.SymbolMatch.
type SymbolMatch struct {
	// Name is the symbol identifier (function, type, const, etc.).
	Name string

	// Kind is the symbol kind: "function" | "type" | "const" | "var" | "method" | etc.
	Kind string

	// Signature is the full declaration as it appears in the source (may be empty).
	Signature string

	// FilePath is the source file where the symbol is defined.
	FilePath string
}

// RetrievalContext is the final result returned by HybridKernel.RetrieveContext.
//
// Purpose: Carry the fused chunk set and matched symbol metadata in one value
//          so the server layer can build an LLM context window without further
//          DB queries.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.RetrievalContext.
type RetrievalContext struct {
	// Chunks is the RRF-fused, graph-expanded, symbol-boosted result set.
	// Ordered by final score descending.
	Chunks []ScoredChunk

	// Symbols are the symbol matches from clawde_symbols for the query.
	// May be empty when no symbol names are detected in the query.
	Symbols []SymbolMatch
}
