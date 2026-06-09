// Package rerank — TEI BGE Reranker v2-m3 client.
//
// Purpose: HTTP client for the Text Embeddings Inference (TEI) reranker sidecar
//          running at 127.0.0.1:8092. Posts query+texts, receives flat float32 scores.
// Inputs:  query string, texts []string.
// Outputs: []float32 scores (index-parallel with texts), error.
// Constraints: 30s per-request timeout. 3-attempt startup retry at 2s intervals.
//              Connection refused → graceful error (no panic).
// SPORT: REGISTRY-FUNCTIONS.md → rerank.TEIRerankClient.Rerank.
package rerank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	// DefaultRerankAddr is the canonical TEI reranker bind address.
	// Distinct from embedder (8080). Per PPI port registry: 8092 = reranker.
	DefaultRerankAddr = "http://127.0.0.1:8092"

	teiRequestTimeout  = 30 * time.Second
	startupRetryCount  = 3
	startupRetryDelay  = 2 * time.Second
)

// TEIRerankClient calls the TEI /rerank endpoint.
//
// Purpose: Send query + candidate texts to the BGE Reranker v2-m3 sidecar and
//          receive per-text relevance scores in the same index order as texts.
// Inputs:  ctx, query string, texts []string.
// Outputs: []float32 scores (len == len(texts)), error.
// Constraints: POST http://<addr>/rerank; JSON {query, texts}; parse flat float32 array.
//              Connection refused → return error (caller degrades gracefully).
// SPORT: REGISTRY-FUNCTIONS.md → rerank.TEIRerankClient.Rerank.
type TEIRerankClient struct {
	addr       string
	httpClient *http.Client
}

// NewTEIRerankClient constructs a TEIRerankClient targeting addr.
// If addr is empty, DefaultRerankAddr is used.
func NewTEIRerankClient(addr string) *TEIRerankClient {
	if addr == "" {
		addr = rerankAddrFromEnv()
	}
	return &TEIRerankClient{
		addr: addr,
		httpClient: &http.Client{
			Timeout: teiRequestTimeout,
		},
	}
}

// rerankAddrFromEnv returns CLAWDE_RERANK_ADDR env or DefaultRerankAddr.
func rerankAddrFromEnv() string {
	if v := os.Getenv("CLAWDE_RERANK_ADDR"); v != "" {
		return v
	}
	return DefaultRerankAddr
}

// teiRerankRequest is the TEI /rerank request body.
type teiRerankRequest struct {
	Query  string   `json:"query"`
	Texts  []string `json:"texts"`
	// raw_scores=true → TEI returns raw logit scores (better for sorting).
	RawScores bool `json:"raw_scores"`
}

// teiRerankResponseItem is one element in the TEI /rerank response.
// TEI returns an array of {index, score} objects sorted by score descending.
type teiRerankResponseItem struct {
	Index int     `json:"index"`
	Score float32 `json:"score"`
}

// Rerank posts query+texts to TEI /rerank and returns scores parallel with texts.
//
// The returned slice has len == len(texts). scores[i] corresponds to texts[i].
// If texts is empty, returns nil, nil.
func (c *TEIRerankClient) Rerank(ctx context.Context, query string, texts []string) ([]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	payload := teiRerankRequest{
		Query:     query,
		Texts:     texts,
		RawScores: true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("tei-rerank: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.addr+"/rerank", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("tei-rerank: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tei-rerank: POST /rerank: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("tei-rerank: HTTP %d: %s", resp.StatusCode, b)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("tei-rerank: read response: %w", err)
	}

	// TEI returns [{index:N, score:F}, ...] sorted by score desc.
	var items []teiRerankResponseItem
	if err := json.Unmarshal(respBody, &items); err != nil {
		return nil, fmt.Errorf("tei-rerank: decode response: %w", err)
	}

	// Build index-parallel scores array: scores[i] = score for texts[i].
	scores := make([]float32, len(texts))
	for _, item := range items {
		if item.Index < 0 || item.Index >= len(texts) {
			return nil, fmt.Errorf("tei-rerank: response index %d out of range [0,%d)", item.Index, len(texts))
		}
		scores[item.Index] = item.Score
	}
	return scores, nil
}

// CheckStartup attempts startupRetryCount connections to verify TEI reranker is up.
// Returns nil on success, last error after all retries on failure.
// Non-blocking for the retrieval pipeline: HybridKernel continues without reranker
// if CheckStartup returns an error.
func (c *TEIRerankClient) CheckStartup(ctx context.Context) error {
	var lastErr error
	for i := 0; i < startupRetryCount; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(startupRetryDelay):
			}
		}
		if err := c.ping(ctx); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("tei-rerank: startup check failed after %d attempts: %w", startupRetryCount, lastErr)
}

// ping sends a GET /health or GET /info to confirm liveness.
func (c *TEIRerankClient) ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.addr+"/info", nil)
	if err != nil {
		return err
	}
	pCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req = req.WithContext(pCtx)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
