// compiler.go — Compiler orchestrates the auto-context pipeline.
//
// Purpose: Wire SessionSignals → ExtractQuery → RetrieveContext → Allocate →
//          format into a single CompileContext call. Adds a per-workspace 30s
//          TTL cache keyed by (workspace_id, query): an identical query within
//          the TTL returns CacheHit=true. Honors CLAWDE_AUTO_CONTEXT (default
//          true); when disabled CompileContext returns Enriched=false with no
//          retrieval. The formatted block labels every chunk with its source
//          file, score, and method (dense/lexical/rerank) for provenance.
// Inputs:  ContextRetriever, optional SymbolStore, PolicyGate.
// Outputs: CompileContextResponse.
// Constraints: File ≤500 lines. No bypass of the ADR-003 dispatch chain — the
//              PolicyGate is evaluated before retrieval.
// SPORT: REGISTRY-FUNCTIONS.md → compiler.Compiler, compiler.CompileContext.
package compiler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// autoContextEnv toggles enrichment; default true.
	autoContextEnv = "CLAWDE_AUTO_CONTEXT"
	// cacheTTL is the per-workspace context cache lifetime.
	cacheTTL = 30 * time.Second

	contextOpen  = "<clawde_context>"
	contextClose = "</clawde_context>"
	// emptyBlock is produced when retrieval yields nothing.
	emptyBlock = contextOpen + "\n" + contextClose
)

// ContextRetriever fetches provenance-bearing context for a query. The
// production impl is a gRPC client to the retrieval kernel (8090); tests inject
// an in-process mock. A non-nil error degrades gracefully (Enriched=false).
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.ContextRetriever.
type ContextRetriever interface {
	RetrieveContext(ctx context.Context, workspaceID, query string) (*RetrievalResult, error)
}

// PolicyGate is the ADR-003 dispatch-chain seam. CompileContext MUST call
// Evaluate before retrieval so enrichment routes through the same
// auth→trust→policy→supply-chain gate as MCP tool calls (the AllowAll stub is
// an acceptable PolicyEngine impl; bypassing the gate is not).
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.PolicyGate.
type PolicyGate interface {
	Evaluate(ctx context.Context, workspaceID, action string) error
}

// cacheEntry is one cached CompileContext result.
type cacheEntry struct {
	query     string
	block     string
	tokens    int
	enriched  bool
	expiresAt time.Time
}

// Compiler is the auto-context compiler.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.Compiler.
type Compiler struct {
	retriever ContextRetriever
	symbols   SymbolStore
	policy    PolicyGate
	now       func() time.Time
	cache     sync.Map // workspace_id → cacheEntry
}

// NewCompiler constructs a Compiler. retriever is required; symbols and policy
// may be nil (extraction proceeds without symbol biasing; a nil policy allows).
func NewCompiler(retriever ContextRetriever, symbols SymbolStore, policy PolicyGate) *Compiler {
	return &Compiler{
		retriever: retriever,
		symbols:   symbols,
		policy:    policy,
		now:       time.Now,
	}
}

// AutoContextEnabled reports whether enrichment is active (CLAWDE_AUTO_CONTEXT,
// default true). Only an explicit falsey value disables it.
func AutoContextEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(autoContextEnv))) {
	case "false", "0", "no", "off":
		return false
	default:
		return true
	}
}

// CompileContext runs the full enrichment pipeline.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.CompileContext.
//        REGISTRY-ENDPOINTS.md → CompileContext RPC.
func (c *Compiler) CompileContext(ctx context.Context, req CompileContextRequest) (CompileContextResponse, error) {
	if !AutoContextEnabled() {
		return CompileContextResponse{Enriched: false}, nil
	}

	// Route through the dispatch chain (PolicyEngine) before any retrieval.
	if c.policy != nil {
		if err := c.policy.Evaluate(ctx, req.WorkspaceID, "compile_context"); err != nil {
			return CompileContextResponse{Enriched: false}, fmt.Errorf("policy denied: %w", err)
		}
	}

	query := ExtractQuery(ctx, c.symbols, req.WorkspaceID, req.Signals)

	// Cache lookup: same workspace + same query within TTL → hit.
	if entry, ok := c.lookup(req.WorkspaceID, query); ok {
		return CompileContextResponse{
			ContextBlock: entry.block,
			Enriched:     entry.enriched,
			TokenCount:   entry.tokens,
			CacheHit:     true,
		}, nil
	}

	if c.retriever == nil {
		return CompileContextResponse{Enriched: false}, fmt.Errorf("no context retriever configured")
	}
	rr, err := c.retriever.RetrieveContext(ctx, req.WorkspaceID, query)
	if err != nil {
		// Graceful degradation: turn proceeds unenriched, never crashes.
		return CompileContextResponse{Enriched: false}, fmt.Errorf("retrieve context: %w", err)
	}
	if rr == nil {
		rr = &RetrievalResult{}
	}

	budgeted := Allocate(tokenLimitFromEnv(), *rr)
	block := formatBlock(budgeted)
	tokens := CountTokens(block)
	enriched := block != emptyBlock

	c.store(req.WorkspaceID, query, block, tokens, enriched)
	return CompileContextResponse{
		ContextBlock: block,
		Enriched:     enriched,
		TokenCount:   tokens,
		CacheHit:     false,
	}, nil
}

// lookup returns a live cache entry for (workspace, query) if present and unexpired.
func (c *Compiler) lookup(workspaceID, query string) (cacheEntry, bool) {
	v, ok := c.cache.Load(workspaceID)
	if !ok {
		return cacheEntry{}, false
	}
	e := v.(cacheEntry)
	if e.query != query || c.now().After(e.expiresAt) {
		return cacheEntry{}, false
	}
	return e, true
}

// store writes a cache entry with a fresh TTL.
func (c *Compiler) store(workspaceID, query, block string, tokens int, enriched bool) {
	c.cache.Store(workspaceID, cacheEntry{
		query:     query,
		block:     block,
		tokens:    tokens,
		enriched:  enriched,
		expiresAt: c.now().Add(cacheTTL),
	})
}

// formatBlock renders a BudgetedContext with provenance labels on every item.
//
// Chunk format (provenance: source file, score, method):
//
//	<clawde_context>
//	### Relevant code
//	{file_path} (score={score} method={method})
//	```{lang}
//	{content}
//	```
//	### Symbols
//	- {kind} {name} ({signature}) — {file} (score={score})
//	### Findings
//	- [{severity}] {rule} {file}:{line} — {message} (score={score})
//	</clawde_context>
func formatBlock(b BudgetedContext) string {
	if len(b.Chunks) == 0 && len(b.Symbols) == 0 && len(b.Findings) == 0 {
		return emptyBlock
	}
	var sb strings.Builder
	sb.WriteString(contextOpen)
	sb.WriteString("\n")

	if len(b.Chunks) > 0 {
		sb.WriteString("### Relevant code\n")
		for _, c := range b.Chunks {
			dt := c.DocType
			if dt == "" {
				dt = "code"
			}
			fmt.Fprintf(&sb, "%s (score=%.4f method=%s type=%s)\n", c.FilePath, c.Score, c.Method, dt)
			// Docstring/markdown chunks are prose, not code: render as plain text
			// rather than a fenced code block.
			if dt == "docstring" || dt == "markdown" || dt == "comment" {
				fmt.Fprintf(&sb, "%s\n", c.Content)
			} else {
				fmt.Fprintf(&sb, "```%s\n%s\n```\n", c.Lang, c.Content)
			}
		}
	}

	if len(b.Symbols) > 0 {
		sb.WriteString("### Symbols\n")
		for _, s := range b.Symbols {
			sig := s.Signature
			if sig == "" {
				sig = s.Name
			}
			fmt.Fprintf(&sb, "- %s %s (%s) — %s (score=%.4f)\n",
				s.Kind, s.Name, sig, s.FilePath, s.Score)
		}
	}

	if len(b.Findings) > 0 {
		sb.WriteString("### Findings\n")
		for _, f := range b.Findings {
			fmt.Fprintf(&sb, "- [%s] %s %s:%d — %s (score=%.4f)\n",
				f.Severity, f.Rule, f.FilePath, f.Line, f.Message, f.Score)
		}
	}

	sb.WriteString(contextClose)
	return sb.String()
}
