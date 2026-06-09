// Package gateway — Ollama LOCAL inference lane provider.
//
// Purpose: Drive a local Ollama daemon as a LOCAL-lane Provider. Delegates all
//          inference (Complete/Stream/Embed/Rerank) to OpenAICompatProvider
//          against Ollama's OpenAI-compatible endpoint (/v1/chat/completions),
//          and adds two Ollama-native behaviors on top:
//            1. Model puller — on first use, GET /api/tags; if the model is
//               absent, POST /api/pull (streaming progress) before proceeding.
//            2. Graceful degradation — a connection-refused (daemon down) is
//               mapped to ErrUnavailable so the gateway falls back per lane order
//               instead of panicking.
// Inputs:  OLLAMA_HOST (default http://127.0.0.1:11434) + model name from registry
//          (registry models are prefixed "ollama/", e.g. "ollama/llama3.2").
// Outputs: LaneResponse / StreamChunk / []float32 / []int — same as the Provider
//          interface; ErrUnavailable when the daemon is unreachable.
// Constraints: Ollama MUST use the OpenAI-compat path per LEDGER §G — raw /api/chat
//              is NOT used. /api/tags and /api/pull are the only Ollama-native
//              endpoints touched (model management has no OpenAI-compat equivalent).
//              vLLM (port 8093) is a separate provider — never hardcode 11434 there.
// SPORT: REGISTRY-FUNCTIONS.md → OllamaProvider; REGISTRY-SERVICES.md → Ollama LOCAL lane.
package gateway

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// defaultOllamaHost is the canonical local Ollama endpoint base (no /v1 suffix).
// Overridable via the OLLAMA_HOST environment variable.
const defaultOllamaHost = "http://127.0.0.1:11434"

// ollamaModelPrefix is the registry-side prefix that marks a model as Ollama-served.
// It is stripped before talking to the daemon (Ollama itself uses bare names like "llama3.2").
const ollamaModelPrefix = "ollama/"

// ErrUnavailable signals that a provider's backing service is not reachable
// (e.g., the Ollama daemon is not running). The gateway/failover layer treats
// this as a recoverable, fall-throughable condition rather than a fatal error.
var ErrUnavailable = errors.New("provider unavailable")

// OllamaProvider implements Provider for a local Ollama daemon.
// Inference is delegated to an embedded OpenAICompatProvider; this type only
// adds the model-pull bootstrap and connection-refused → ErrUnavailable mapping.
type OllamaProvider struct {
	host       string // base host, no /v1 suffix, e.g. "http://127.0.0.1:11434"
	bareModel  string // Ollama-native model name (prefix stripped), e.g. "llama3.2"
	compat     *OpenAICompatProvider
	httpClient *http.Client

	pullOnce sync.Once // ensures the model is pulled at most once per provider instance
	pullErr  error     // result of the one-shot pull attempt
}

// NewOllamaProvider builds an Ollama provider for the given registry model.
// host may be empty (defaults to OLLAMA_HOST env, then defaultOllamaHost).
// model is the registry model string (with or without the "ollama/" prefix).
func NewOllamaProvider(host, model string) (*OllamaProvider, error) {
	if host == "" {
		host = os.Getenv("OLLAMA_HOST")
	}
	if host == "" {
		host = defaultOllamaHost
	}
	host = strings.TrimRight(host, "/")
	host = strings.TrimSuffix(host, "/v1") // tolerate a /v1 already on OLLAMA_HOST

	if model == "" {
		return nil, fmt.Errorf("ollama: model is required")
	}
	bare := strings.TrimPrefix(model, ollamaModelPrefix)

	// Delegate inference to the shared OpenAI-compat adapter. Ollama serves the
	// OpenAI API under /v1; the bare model name is what Ollama expects in the body.
	compat, err := NewOpenAICompatProvider(host+"/v1", "", bare, "ollama")
	if err != nil {
		return nil, fmt.Errorf("ollama: build compat adapter: %w", err)
	}

	return &OllamaProvider{
		host:       host,
		bareModel:  bare,
		compat:     compat,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}, nil
}

// Name returns the canonical provider identifier.
func (p *OllamaProvider) Name() string { return "ollama" }

// Complete ensures the model is present, then delegates to the compat adapter.
func (p *OllamaProvider) Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error) {
	if err := p.ensureModel(ctx); err != nil {
		return nil, err
	}
	resp, err := p.compat.Complete(ctx, req)
	return resp, p.mapErr(err)
}

// Stream ensures the model is present, then delegates streaming to the adapter.
func (p *OllamaProvider) Stream(ctx context.Context, req LaneRequest) (<-chan StreamChunk, error) {
	if err := p.ensureModel(ctx); err != nil {
		return nil, err
	}
	ch, err := p.compat.Stream(ctx, req)
	return ch, p.mapErr(err)
}

// Embed ensures the model is present, then delegates embedding to the adapter.
func (p *OllamaProvider) Embed(ctx context.Context, text string, expectedDim int) ([]float32, error) {
	if err := p.ensureModel(ctx); err != nil {
		return nil, err
	}
	vec, err := p.compat.Embed(ctx, text, expectedDim)
	return vec, p.mapErr(err)
}

// Rerank delegates to the no-op reranker (Ollama has no native rerank endpoint).
func (p *OllamaProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]int, error) {
	return nopReranker{}.Rerank(ctx, query, documents, topN)
}

// HealthCheck verifies the Ollama daemon is reachable via GET /api/tags.
// Connection-refused is reported as ErrUnavailable so callers can fall back.
func (p *OllamaProvider) HealthCheck(ctx context.Context) error {
	_, err := p.listTags(ctx)
	return err
}

// ---- model puller ----

// ensureModel pulls the model on first use if it is not already installed.
// It runs at most once per provider instance (sync.Once). A daemon that is
// down surfaces as ErrUnavailable.
func (p *OllamaProvider) ensureModel(ctx context.Context) error {
	p.pullOnce.Do(func() {
		present, err := p.modelPresent(ctx)
		if err != nil {
			p.pullErr = err
			return
		}
		if present {
			return
		}
		p.pullErr = p.pullModel(ctx)
	})
	return p.pullErr
}

// modelPresent returns true if bareModel appears in GET /api/tags.
func (p *OllamaProvider) modelPresent(ctx context.Context) (bool, error) {
	names, err := p.listTags(ctx)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		// Ollama tags are like "llama3.2:latest"; match the base name too.
		if n == p.bareModel || strings.HasPrefix(n, p.bareModel+":") {
			return true, nil
		}
	}
	return false, nil
}

// listTags calls GET /api/tags and returns the installed model names.
func (p *OllamaProvider) listTags(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.host+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, p.mapErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama: /api/tags HTTP %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("ollama: decode /api/tags: %w", err)
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// pullModel calls POST /api/pull and drains the streaming progress to completion.
// Each NDJSON line carries a "status" field; a line with {"status":"success"} or
// EOF without error indicates the pull finished.
func (p *OllamaProvider) pullModel(ctx context.Context) error {
	body, _ := json.Marshal(map[string]any{"name": p.bareModel, "stream": true})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.host+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return p.mapErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ollama: /api/pull HTTP %d: %s", resp.StatusCode, string(b))
	}

	// Drain streaming NDJSON progress. We don't surface progress to the caller
	// here (the gateway is request-scoped), but we must consume to completion so
	// the pull actually finishes and any terminal error is observed.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var prog struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(line, &prog); err != nil {
			continue // tolerate any non-JSON keepalive line
		}
		if prog.Error != "" {
			return fmt.Errorf("ollama: pull %q failed: %s", p.bareModel, prog.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("ollama: read pull stream: %w", err)
	}
	return nil
}

// mapErr converts a connection-level failure (daemon down) into ErrUnavailable.
// Other errors pass through unchanged. nil passes through as nil.
func (p *OllamaProvider) mapErr(err error) error {
	if err == nil {
		return nil
	}
	if isConnRefused(err) {
		return fmt.Errorf("ollama at %s: %w", p.host, ErrUnavailable)
	}
	return err
}

// isConnRefused reports whether err looks like a refused / unreachable connection.
func isConnRefused(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrUnavailable) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connect: ") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp")
}
