// driver.go — EmbeddingDriver interface + BGE-M3 and Gemini implementations.
//
// Purpose: Provide a testable seam for embedding calls so metrics and recorder
//          can be verified without live HTTP endpoints.
// Inputs:  text string; dim int (expected vector dimension).
// Outputs: []float32 embedding; latency measured by the caller (metrics.go).
// Constraints: BGE-M3 talks to TEI at :8080; Gemini goes via gateway.Provider.Embed.
//              Neither is called in unit tests (skip-with-reason).
// SPORT: REGISTRY-FUNCTIONS.md → eval.EmbeddingDriver, eval.BGEDriver, eval.GeminiDriver.
package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/gateway"
)

// EmbeddingDriver is the common interface for embedding providers used in eval.
//
// Purpose: Seam so BGE-M3 (TEI HTTP) and Gemini (gateway) share one call site.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.EmbeddingDriver.
type EmbeddingDriver interface {
	// Name returns the canonical provider label stored in clawde_eval_runs.provider.
	Name() string

	// Embed returns a float32 vector for the given text.
	// Implementation must be safe for concurrent calls.
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ── BGE-M3 driver (TEI :8080) ─────────────────────────────────────────────────

// BGEDriver calls the Text-Embeddings-Inference sidecar at the configured
// endpoint (default http://localhost:8080) for BGE-M3 1024-dim embeddings.
//
// Purpose: Wrap TEI REST /embed endpoint so callers need not know the HTTP shape.
// Inputs:  endpoint URL; text string.
// Outputs: []float32 (1024-dim); error on HTTP or JSON failure.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.BGEDriver.
type BGEDriver struct {
	Endpoint string     // e.g. "http://localhost:8080"
	client   *http.Client
}

// NewBGEDriver constructs a BGEDriver with sensible timeout defaults.
func NewBGEDriver(endpoint string) *BGEDriver {
	return &BGEDriver{
		Endpoint: endpoint,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// Name returns the canonical label for clawde_eval_runs.provider.
func (d *BGEDriver) Name() string { return "bge-m3" }

// Embed sends a POST /embed request to TEI and returns the first embedding.
func (d *BGEDriver) Embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(map[string]string{"inputs": text})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		d.Endpoint+"/embed", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("bge driver: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bge driver: POST /embed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("bge driver: TEI returned %d: %s", resp.StatusCode, string(b))
	}

	// TEI returns [[float, …]] — outer array is batch.
	var result [][]float32
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("bge driver: decode response: %w", err)
	}
	if len(result) == 0 || len(result[0]) == 0 {
		return nil, fmt.Errorf("bge driver: empty embedding in response")
	}
	return result[0], nil
}

// ── Gemini driver (via gateway) ──────────────────────────────────────────────

// GeminiDriver wraps gateway.Provider.Embed for Gemini text-embedding-004.
//
// Purpose: Delegate Gemini embedding calls to the existing gateway layer so
//          key management, rate-limiting, and failover are handled centrally.
// Inputs:  gateway.Provider (resolved from registry); text string.
// Outputs: []float32 (768-dim for text-embedding-004); error.
// Constraints: ExpectedDim=0 lets Gemini return its native 768-dim vector.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.GeminiDriver.
type GeminiDriver struct {
	provider gateway.Provider
}

// NewGeminiDriver wraps any gateway.Provider as a GeminiDriver.
func NewGeminiDriver(p gateway.Provider) *GeminiDriver {
	return &GeminiDriver{provider: p}
}

// Name returns the canonical label for clawde_eval_runs.provider.
func (d *GeminiDriver) Name() string { return "gemini-text-embedding-004" }

// Embed calls Provider.Embed with expectedDim=0 (native dimension).
func (d *GeminiDriver) Embed(ctx context.Context, text string) ([]float32, error) {
	vec, err := d.provider.Embed(ctx, text, 0)
	if err != nil {
		return nil, fmt.Errorf("gemini driver: embed: %w", err)
	}
	return vec, nil
}
