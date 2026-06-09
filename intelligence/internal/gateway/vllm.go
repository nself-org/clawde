// Package gateway — vLLM GPU inference lane provider.
//
// Purpose: Drive a local vLLM server as a DEEP/FAST/LOCAL-lane Provider.
//          Delegates all inference (Complete/Stream/Embed/Rerank) to an embedded
//          OpenAICompatProvider against vLLM's OpenAI-compatible endpoint, and
//          enforces the M6 loopback constraint at construction time so a
//          misconfigured deploy fails loudly rather than silently exposing the
//          GPU inference lane on a public interface.
//
// Inputs:  VLLM_HOST  (env, default "http://127.0.0.1:8093") — base URL.
//          VLLM_API_KEY (env, optional) — bearer token if vLLM is auth-gated.
//          Model name comes from model_registry.yaml.
// Outputs: LaneResponse / StreamChunk / []float32 / []int — same Provider interface
//          as OpenAICompatProvider; ErrUnavailable when the server is unreachable.
// Constraints: vLLM MUST bind 127.0.0.1 per M6 / ADR-001.
//              ValidateVLLMHost is called at NewVLLMProvider time; non-loopback
//              hosts panic so the deployment error is surfaced at startup.
//              Port canonical: 8093 (non-negotiable per F10-PORT-REGISTRY.md).
// SPORT: REGISTRY-FUNCTIONS.md → VLLMProvider, NewVLLMProvider;
//        REGISTRY-SERVICES.md → vLLM DEEP/FAST lane, port 8093 loopback.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// defaultVLLMHost is the canonical local vLLM base URL.
// Overridable via the VLLM_HOST environment variable.
const defaultVLLMHost = "http://127.0.0.1:8093"

// vllmHealthTimeout is the maximum time allowed for a health-check probe.
// Used by HealthCheck and the fallback-detection path before falling back
// to the next DEEP/FAST lane provider.
const vllmHealthTimeout = 2 * time.Second

// VLLMProvider implements Provider for a local vLLM GPU inference server.
// Inference is fully delegated to an embedded OpenAICompatProvider; this type
// adds the M6 loopback guard, ErrUnavailable mapping on connection-refused, and
// environment-variable bootstrap (VLLM_HOST / VLLM_API_KEY).
type VLLMProvider struct {
	host   string // validated loopback base URL (e.g. "http://127.0.0.1:8093")
	compat *OpenAICompatProvider
}

// NewVLLMProvider constructs a VLLMProvider.
//
// host is validated against ValidateVLLMHost; a non-loopback host panics
// because this is a startup misconfiguration (deploy error), not a runtime
// recoverable condition — the same approach used for DB connection failures in
// the nSelf stack.
//
// If host is empty, the VLLM_HOST environment variable is consulted; if also
// empty, defaultVLLMHost ("http://127.0.0.1:8093") is used.
// If apiKey is empty, the VLLM_API_KEY environment variable is consulted
// (vLLM is typically run without auth on loopback; the key is optional).
func NewVLLMProvider(host, apiKey, model string) (*VLLMProvider, error) {
	if host == "" {
		host = os.Getenv("VLLM_HOST")
	}
	if host == "" {
		host = defaultVLLMHost
	}
	if apiKey == "" {
		apiKey = os.Getenv("VLLM_API_KEY")
	}
	if model == "" {
		return nil, fmt.Errorf("vllm: model is required")
	}

	// M6 guard: panic on non-loopback — this is a deploy-time misconfiguration,
	// not a runtime error. Surface it immediately at startup.
	if err := ValidateVLLMHost(host); err != nil {
		panic(fmt.Sprintf("vLLM startup config error: %v", err))
	}

	compat, err := NewOpenAICompatProvider(host+"/v1", apiKey, model, "vllm")
	if err != nil {
		return nil, fmt.Errorf("vllm: failed to create compat provider: %w", err)
	}
	return &VLLMProvider{host: host, compat: compat}, nil
}

// Name returns the canonical provider identifier.
func (v *VLLMProvider) Name() string { return "vllm" }

// Complete delegates to the embedded OpenAICompatProvider.
// If the vLLM server is unreachable, the error is wrapped as ErrUnavailable
// so the gateway fallover layer can switch to the next DEEP/FAST entry.
func (v *VLLMProvider) Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error) {
	resp, err := v.compat.Complete(ctx, req)
	if err != nil {
		return nil, v.maybeUnavailable(err)
	}
	return resp, nil
}

// Stream delegates to the embedded OpenAICompatProvider.
func (v *VLLMProvider) Stream(ctx context.Context, req LaneRequest) (<-chan StreamChunk, error) {
	ch, err := v.compat.Stream(ctx, req)
	if err != nil {
		return nil, v.maybeUnavailable(err)
	}
	return ch, nil
}

// Embed delegates to the embedded OpenAICompatProvider.
func (v *VLLMProvider) Embed(ctx context.Context, text string, expectedDim int) ([]float32, error) {
	vec, err := v.compat.Embed(ctx, text, expectedDim)
	if err != nil {
		return nil, v.maybeUnavailable(err)
	}
	return vec, nil
}

// Rerank delegates to the embedded OpenAICompatProvider.
func (v *VLLMProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]int, error) {
	indices, err := v.compat.Rerank(ctx, query, documents, topN)
	if err != nil {
		return nil, v.maybeUnavailable(err)
	}
	return indices, nil
}

// HealthCheck probes GET /v1/models with a 2-second timeout (M6 fallback budget).
// Returns ErrUnavailable if the server does not respond within the deadline.
func (v *VLLMProvider) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, vllmHealthTimeout)
	defer cancel()

	client := &http.Client{Timeout: vllmHealthTimeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.host+"/v1/models", nil)
	if err != nil {
		return err
	}
	if v.compat.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+v.compat.apiKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: vllm health: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: vllm health: HTTP %d", ErrUnavailable, resp.StatusCode)
	}
	return nil
}

// maybeUnavailable wraps network / connection errors as ErrUnavailable so the
// gateway fallover path can detect them and try the next provider in the lane.
func (v *VLLMProvider) maybeUnavailable(err error) error {
	if err == nil {
		return nil
	}
	// If it's already ErrUnavailable, pass through.
	if errors.Is(err, ErrUnavailable) {
		return err
	}
	// Treat any transport / connection-refused error as unavailable.
	// We check for the string because Go's net package does not export a typed
	// "connection refused" error on all platforms.
	msg := err.Error()
	if isNetworkError(msg) {
		return fmt.Errorf("%w: vllm: %v", ErrUnavailable, err)
	}
	return err
}

// isNetworkError returns true for errors that indicate the remote server is
// simply not running (connection refused, no such host, network unreachable).
func isNetworkError(msg string) bool {
	for _, needle := range []string{
		"connection refused",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"dial tcp",
		"context deadline exceeded",
	} {
		if len(msg) > 0 && contains(msg, needle) {
			return true
		}
	}
	return false
}

// contains is a simple substring check used by isNetworkError to avoid importing
// the strings package solely for Contains (strings is already used elsewhere in
// the package, but this helper keeps the logic self-contained).
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		len(substr) == 0 ||
		indexOfString(s, substr) >= 0)
}

func indexOfString(s, sub string) int {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
