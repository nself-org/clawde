// kb_ingestor.go — external knowledge-base URL ingestion.
//
// Purpose:    IngestDocURL fetches an external documentation URL, chunks it as
//             markdown, and enqueues the chunks to clawde_embed_queue tagged with
//             doc_type. Every call first passes the full ADR-003 dispatch chain
//             (auth → trust_registry → PolicyEngine.evaluate → SupplyChainPolicy);
//             a denied client never triggers a fetch.
// Inputs:     IngestDocURLRequest{WorkspaceID, URL, DocType}, client identity.
// Outputs:    IngestDocURLResponse{ChunksEnqueued, Skipped}; chunks on the embed queue.
// Constraints: File ≤500 lines. HTTP fetch has a timeout + response-size cap.
//             DB/embed enqueue and HTTP are interface seams (stubs in tests).
//
// SPORT: REGISTRY-FUNCTIONS.md → docs.IngestDocURL.
//        REGISTRY-ENDPOINTS.md → IngestDocURL RPC.
package docs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/hostadapter"
)

const (
	// ingestTool is the supply-chain tool name gated for KB ingestion.
	ingestTool = "ingest_doc_url"

	defaultFetchTimeout = 15 * time.Second
	defaultMaxBodyBytes = 4 << 20 // 4 MiB response cap
)

// EmbedEnqueuer is the seam for pushing chunks onto clawde_embed_queue.
// The real implementation wraps pgmq via pgx; tests inject a stub.
type EmbedEnqueuer interface {
	// EnqueueChunks pushes each DocChunk (with its doc_type) onto the embed queue
	// for the given workspace. Returns the number successfully enqueued.
	EnqueueChunks(ctx context.Context, workspaceID string, chunks []DocChunk) (int, error)
}

// Fetcher is the seam for HTTP retrieval; tests inject a stub to avoid network.
type Fetcher interface {
	// Fetch returns the body of url (already size-capped) or an error.
	Fetch(ctx context.Context, url string) (string, error)
}

// IngestDocURLRequest is the IngestDocURL RPC input.
type IngestDocURLRequest struct {
	WorkspaceID string
	URL         string
	DocType     string // defaults to "markdown" when empty (external docs are markdown)
}

// IngestDocURLResponse is the IngestDocURL RPC output.
type IngestDocURLResponse struct {
	ChunksEnqueued int
	Skipped        bool   // true when nothing was enqueued (empty/unsupported body)
	Reason         string // populated when Skipped
}

// KBIngestor wires the dispatch chain, fetcher, chunker, and embed enqueuer.
type KBIngestor struct {
	trust       hostadapter.TrustRegistry
	policy      hostadapter.PolicyEngine
	supplyChain hostadapter.SupplyChainPolicy
	fetcher     Fetcher
	chunker     *MarkdownChunker
	enqueuer    EmbedEnqueuer
}

// NewKBIngestor constructs a KBIngestor. The trust/policy/supplyChain trio is the
// same ADR-003 chain the MCP server uses; pass the production wiring or test stubs.
func NewKBIngestor(
	trust hostadapter.TrustRegistry,
	policy hostadapter.PolicyEngine,
	supplyChain hostadapter.SupplyChainPolicy,
	fetcher Fetcher,
	enqueuer EmbedEnqueuer,
) *KBIngestor {
	return &KBIngestor{
		trust:       trust,
		policy:      policy,
		supplyChain: supplyChain,
		fetcher:     fetcher,
		chunker:     NewMarkdownChunker(),
		enqueuer:    enqueuer,
	}
}

// IngestDocURL runs the full ADR-003 dispatch chain, then fetches + chunks +
// enqueues. clientID is the resolved MCP/RPC caller identity. A denied caller
// returns an error BEFORE any network fetch (gating is fetch-blocking).
func (k *KBIngestor) IngestDocURL(ctx context.Context, clientID string, req IngestDocURLRequest) (*IngestDocURLResponse, error) {
	// ── ADR-003 dispatch chain (deny-by-default) ───────────────────────────────
	// 1) auth — caller identity must be present.
	if clientID == "" {
		return nil, fmt.Errorf("denied: missing client identity")
	}
	// 2) trust_registry — unknown client_id is denied.
	if k.trust == nil || !k.trust.IsTrusted(clientID) {
		return nil, fmt.Errorf("denied: untrusted client_id")
	}
	// 3) PolicyEngine.evaluate.
	if k.policy != nil {
		if err := k.policy.Evaluate(ctx, clientID, ingestTool, req.WorkspaceID); err != nil {
			return nil, fmt.Errorf("denied by policy: %w", err)
		}
	}
	// 4) SupplyChainPolicy — tool must be allowlisted.
	if k.supplyChain != nil {
		if err := k.supplyChain.Permit(ingestTool); err != nil {
			return nil, fmt.Errorf("denied: %w", err)
		}
	}

	// ── Validated path: fetch → chunk → enqueue ────────────────────────────────
	if req.URL == "" {
		return nil, fmt.Errorf("ingest: empty url")
	}
	if k.fetcher == nil {
		return nil, fmt.Errorf("ingest: no fetcher configured")
	}
	body, err := k.fetcher.Fetch(ctx, req.URL)
	if err != nil {
		return nil, fmt.Errorf("ingest: fetch %s: %w", req.URL, err)
	}

	chunks := k.chunker.Chunk(body, req.URL)
	if len(chunks) == 0 {
		return &IngestDocURLResponse{Skipped: true, Reason: "no content"}, nil
	}

	// Tag chunks with the requested doc_type (default markdown for external docs).
	dt := DocType(req.DocType)
	if dt == "" {
		dt = DocTypeMarkdown
	}
	for i := range chunks {
		chunks[i].DocType = dt
	}

	enq := 0
	if k.enqueuer != nil {
		enq, err = k.enqueuer.EnqueueChunks(ctx, req.WorkspaceID, chunks)
		if err != nil {
			return nil, fmt.Errorf("ingest: enqueue: %w", err)
		}
	}
	return &IngestDocURLResponse{ChunksEnqueued: enq}, nil
}

// ── Default HTTP fetcher ────────────────────────────────────────────────────

// httpFetcher is the production Fetcher: timeout + response-size cap.
type httpFetcher struct {
	client   *http.Client
	maxBytes int64
}

// NewHTTPFetcher returns a size- and time-bounded Fetcher.
func NewHTTPFetcher() Fetcher {
	return &httpFetcher{
		client:   &http.Client{Timeout: defaultFetchTimeout},
		maxBytes: defaultMaxBodyBytes,
	}
}

// Fetch retrieves url, reading at most maxBytes of the response body.
func (h *httpFetcher) Fetch(ctx context.Context, url string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := h.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http status %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, h.maxBytes)
	b, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
