// Package gateway — OpenAI-compatible adapter.
//
// Purpose: Single adapter covering any endpoint that speaks the OpenAI API:
//   - Gemini via Vertex AI (openai-compat path)
//   - Ollama local models (base_url = http://127.0.0.1:11434/v1)
//   - vLLM inference lane (base_url = http://127.0.0.1:8093/v1) — MUST bind 127.0.0.1
//   - TEI embedding sidecar (:8080)
//   - TEI reranker sidecar (:8092)
//
// Inputs:  base_url + api_key_ref (resolved from vault.env) + model name from registry.
// Outputs: LaneResponse / StreamChunk / []float32 embedding / []int ranked indices.
// Constraints: No raw model strings; base_url comes from ProviderEntry config.
//              api_key_ref only — key pre-resolved by registry loader.
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence gateway.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultHTTPTimeout = 120 * time.Second

// OpenAICompatProvider implements Provider for any OpenAI-compatible REST API.
type OpenAICompatProvider struct {
	baseURL    string
	apiKey     string
	model      string
	providerID string // "openai" | "gemini" | "ollama" | "vllm" | "tei-embed" | "tei-rerank"
	httpClient *http.Client
}

// NewOpenAICompatProvider constructs a provider from config.
// baseURL example: "https://api.openai.com/v1" or "http://127.0.0.1:8080/v1".
// apiKey is pre-resolved from vault.env by the registry loader.
func NewOpenAICompatProvider(baseURL, apiKey, model, providerID string) (*OpenAICompatProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("openai-compat[%s]: base_url is required", providerID)
	}
	if model == "" {
		return nil, fmt.Errorf("openai-compat[%s]: model is required", providerID)
	}
	return &OpenAICompatProvider{
		baseURL:    baseURL,
		apiKey:     apiKey,
		model:      model,
		providerID: providerID,
		httpClient: &http.Client{Timeout: defaultHTTPTimeout},
	}, nil
}

// Name returns the canonical provider identifier.
func (p *OpenAICompatProvider) Name() string { return p.providerID }

// Complete calls POST /chat/completions and returns the first choice.
func (p *OpenAICompatProvider) Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error) {
	body, err := p.buildChatBody(req, false)
	if err != nil {
		return nil, err
	}

	respBytes, err := p.post(ctx, "/chat/completions", body)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream", Cause: err}
	}

	var resp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("decode: %w", err)}
	}
	if len(resp.Choices) == 0 {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("no choices in response")}
	}

	return &LaneResponse{
		Content:      resp.Choices[0].Message.Content,
		InputTokens:  resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		Provider:     p.Name(),
		Model:        p.model,
	}, nil
}

// Stream calls POST /chat/completions with stream=true and pushes deltas to a channel.
func (p *OpenAICompatProvider) Stream(ctx context.Context, req LaneRequest) (<-chan StreamChunk, error) {
	body, err := p.buildChatBody(req, true)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		if err := p.streamSSE(ctx, req.Lane, "/chat/completions", body, ch); err != nil {
			ch <- StreamChunk{Err: err}
		}
	}()
	return ch, nil
}

// Embed calls POST /embeddings and returns a float32 vector.
func (p *OpenAICompatProvider) Embed(ctx context.Context, text string, expectedDim int) ([]float32, error) {
	payload := map[string]any{"input": text, "model": p.model}
	b, _ := json.Marshal(payload)

	respBytes, err := p.post(ctx, "/embeddings", b)
	if err != nil {
		return nil, fmt.Errorf("embed[%s]: %w", p.Name(), err)
	}

	var resp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBytes, &resp); err != nil {
		return nil, fmt.Errorf("embed[%s] decode: %w", p.Name(), err)
	}
	if len(resp.Data) == 0 || len(resp.Data[0].Embedding) == 0 {
		return nil, fmt.Errorf("embed[%s]: empty embedding in response", p.Name())
	}

	vec := resp.Data[0].Embedding
	if expectedDim > 0 && len(vec) != expectedDim {
		return nil, fmt.Errorf("embed[%s]: dim mismatch: got %d, want %d", p.Name(), len(vec), expectedDim)
	}
	return vec, nil
}

// Rerank calls the TEI /rerank endpoint (cross-encoder scoring).
func (p *OpenAICompatProvider) Rerank(ctx context.Context, query string, documents []string, topN int) ([]int, error) {
	payload := map[string]any{
		"query":     query,
		"texts":     documents,
		"raw_scores": false,
	}
	b, _ := json.Marshal(payload)

	respBytes, err := p.post(ctx, "/rerank", b)
	if err != nil {
		return nil, fmt.Errorf("rerank[%s]: %w", p.Name(), err)
	}

	var scores []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	}
	if err := json.Unmarshal(respBytes, &scores); err != nil {
		return nil, fmt.Errorf("rerank[%s] decode: %w", p.Name(), err)
	}

	n := len(scores)
	if topN > 0 && topN < n {
		n = topN
		scores = scores[:n]
	}
	indices := make([]int, n)
	for i, s := range scores {
		indices[i] = s.Index
	}
	return indices, nil
}

// HealthCheck calls GET /models to verify the endpoint is reachable.
func (p *OpenAICompatProvider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return err
	}
	p.addAuthHeader(req)
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health[%s]: %w", p.Name(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("health[%s]: HTTP %d", p.Name(), resp.StatusCode)
	}
	return nil
}

// ---- internal helpers ----

func (p *OpenAICompatProvider) buildChatBody(req LaneRequest, stream bool) ([]byte, error) {
	msgs := make([]map[string]string, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		msgs = append(msgs, map[string]string{"role": "system", "content": req.SystemPrompt})
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue // already prepended
		}
		msgs = append(msgs, map[string]string{"role": m.Role, "content": m.Content})
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}

	payload := map[string]any{
		"model":      p.model,
		"messages":   msgs,
		"max_tokens": maxTok,
		"stream":     stream,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "config",
			Cause: fmt.Errorf("marshal: %w", err)}
	}
	return b, nil
}

func (p *OpenAICompatProvider) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.addAuthHeader(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return b, nil
}

// streamSSE reads server-sent events and pushes deltas to ch.
func (p *OpenAICompatProvider) streamSSE(ctx context.Context, lane Lane, path string, body []byte, ch chan<- StreamChunk) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return &GatewayError{Lane: lane, Provider: p.Name(), Code: "config", Cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	p.addAuthHeader(req)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return &GatewayError{Lane: lane, Provider: p.Name(), Code: "upstream", Cause: err}
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		var event map[string]any
		if err := dec.Decode(&event); err != nil {
			if err == io.EOF {
				break
			}
			return &GatewayError{Lane: lane, Provider: p.Name(), Code: "upstream", Cause: err}
		}

		choices, _ := event["choices"].([]any)
		if len(choices) == 0 {
			continue
		}
		choice, _ := choices[0].(map[string]any)
		delta, _ := choice["delta"].(map[string]any)
		content, _ := delta["content"].(string)
		if content != "" {
			ch <- StreamChunk{Delta: content}
		}
		if finishReason, _ := choice["finish_reason"].(string); finishReason == "stop" {
			ch <- StreamChunk{Done: true}
			return nil
		}
	}
	ch <- StreamChunk{Done: true}
	return nil
}

func (p *OpenAICompatProvider) addAuthHeader(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
}
