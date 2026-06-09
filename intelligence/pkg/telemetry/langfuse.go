// Package telemetry — Langfuse LLM-call tracing + Ragas score export.
//
// Purpose:    Emit one Langfuse trace per LLM call (name {lane}/{provider}/{model},
//             input+output truncated to MaxAttrLen) and push Ragas evaluation
//             scores to the Langfuse Score API (POST /api/public/scores).
// Inputs:     config.TelemetryConfig (host + Basic-auth keys), LLMTrace, RagasScore.
// Outputs:    error from the HTTP call; a disabled client is a no-op (nil error).
// Constraints: PII guard — input/output truncated before the request body is
//             built. Graceful degradation: when Langfuse is not configured the
//             client returns immediately with no error.
// SPORT:      REGISTRY-FUNCTIONS.md → Langfuse (NewLangfuseClient, TraceLLMCall, ScoreTrace).
package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/config"
)

// LangfuseClient posts traces and scores to a Langfuse ingestion endpoint.
type LangfuseClient struct {
	enabled    bool
	host       string
	publicKey  string
	secretKey  string
	httpClient *http.Client
}

// LLMTrace is the metadata for a single LLM call exported to Langfuse.
type LLMTrace struct {
	Lane     string
	Provider string
	Model    string
	Input    string
	Output   string
	TraceID  string // optional; links to the Score API.
}

// RagasScore is a single Ragas-style evaluation metric pushed via the Score API.
type RagasScore struct {
	TraceID string  `json:"traceId"`
	Name    string  `json:"name"`  // e.g. "faithfulness", "answer_relevance".
	Value   float64 `json:"value"` // typically 0..1.
	Comment string  `json:"comment,omitempty"`
}

// NewLangfuseClient builds a client from config. When Langfuse is not fully
// configured the returned client is disabled and all methods are no-ops.
func NewLangfuseClient(cfg config.TelemetryConfig) *LangfuseClient {
	return &LangfuseClient{
		enabled:    cfg.LangfuseEnabled,
		host:       cfg.LangfuseHost,
		publicKey:  cfg.LangfusePublicKey,
		secretKey:  cfg.LangfuseSecretKey,
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// Enabled reports whether the client will actually emit to Langfuse.
func (c *LangfuseClient) Enabled() bool { return c != nil && c.enabled }

// TraceLLMCall posts a single LLM-call trace. The trace name is
// {lane}/{provider}/{model}; input/output are truncated to MaxAttrLen first.
// No-op (nil error) when the client is disabled.
func (c *LangfuseClient) TraceLLMCall(ctx context.Context, t LLMTrace) error {
	if !c.Enabled() {
		return nil
	}
	body := map[string]any{
		"name":   fmt.Sprintf("%s/%s/%s", t.Lane, t.Provider, t.Model),
		"input":  Truncate(t.Input),
		"output": Truncate(t.Output),
		"metadata": map[string]string{
			"lane":     t.Lane,
			"provider": t.Provider,
			"model":    t.Model,
		},
	}
	if t.TraceID != "" {
		body["id"] = t.TraceID
	}
	return c.post(ctx, "/api/public/traces", body)
}

// ScoreTrace pushes a Ragas score to the Langfuse Score API. No-op when disabled.
func (c *LangfuseClient) ScoreTrace(ctx context.Context, s RagasScore) error {
	if !c.Enabled() {
		return nil
	}
	if s.Comment != "" {
		s.Comment = Truncate(s.Comment)
	}
	return c.post(ctx, "/api/public/scores", s)
}

// post sends a JSON body to the Langfuse endpoint with HTTP Basic auth
// (public key as username, secret key as password).
func (c *LangfuseClient) post(ctx context.Context, path string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.host+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.publicKey, c.secretKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("langfuse %s: status %d", path, resp.StatusCode)
	}
	return nil
}
