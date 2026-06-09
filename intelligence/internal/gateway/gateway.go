// Package gateway provides the unified LLM provider abstraction and lane-based
// routing layer for clawde-intelligence.
//
// Purpose: Single entry point for all LLM calls (completion, streaming, embedding,
//          reranking). Callers request a Lane; the gateway resolves the best available
//          provider from the registry and executes the call.
// Inputs:  Lane enum + LaneRequest containing prompt/text/documents.
// Outputs: LaneResponse or stream channel; all errors are typed GatewayError.
// Constraints: No raw model-name strings here — names live in model_registry.yaml only.
//              No raw API keys — only api_key_ref strings resolved from vault.env at load time.
// SPORT: REGISTRY-SERVICES.md — clawde-intelligence, gRPC 8090 / REST 8091.
package gateway

import (
	"context"
	"fmt"
	"io"
)

// Lane identifies a routing lane in the model registry.
// Each lane maps to one or more provider+model entries.
type Lane string

const (
	// LaneFast is for low-latency completions (small context, quick tasks).
	LaneFast Lane = "fast"
	// LaneDeep is for extended reasoning, large-context completions.
	LaneDeep Lane = "deep"
	// LaneMultimodal is for vision + text tasks.
	LaneMultimodal Lane = "multimodal"
	// LaneEmbedding is for text → vector embedding calls.
	LaneEmbedding Lane = "embedding"
	// LaneRerank is for cross-encoder re-ranking of candidate passages.
	LaneRerank Lane = "rerank"
	// LaneLive is for streaming real-time completions.
	LaneLive Lane = "live"
	// LaneLocal is for locally-hosted models (Ollama, vLLM on 127.0.0.1).
	LaneLocal Lane = "local"
)

// AllLanes is the exhaustive list of valid lanes, used for validation.
var AllLanes = []Lane{LaneFast, LaneDeep, LaneMultimodal, LaneEmbedding, LaneRerank, LaneLive, LaneLocal}

// Message is a single chat turn.
type Message struct {
	Role    string // "user" | "assistant" | "system"
	Content string
}

// LaneRequest carries all parameters for a single LLM call.
// Only the fields relevant to the target lane are required.
type LaneRequest struct {
	Lane Lane

	// Completion / streaming fields (fast, deep, multimodal, live)
	Messages    []Message
	SystemPrompt string
	MaxTokens   int

	// Embedding fields (embedding lane)
	Text        string
	ExpectedDim int // expected embedding dimension; 0 = any

	// Rerank fields (rerank lane)
	Query       string
	Documents   []string
	TopN        int // 0 = return all ranked

	// Multimodal fields (multimodal lane — proto fields 10/11)
	// Max 5 images per request; max 10 MB per image; MIME in {png,jpeg,gif,webp}.
	Images        [][]byte // raw image bytes; each element is one image
	ImageMimeType string   // shared MIME type for all images in this request

	// Metadata
	WorkspaceID string
	RequestID   string
}

// LaneResponse carries the result of a completed LaneRequest.
type LaneResponse struct {
	// Completion output (non-streaming)
	Content     string
	InputTokens  int
	OutputTokens int

	// Embedding output
	Embedding []float32

	// Rerank output: indices into original Documents, highest-score first.
	RankedIndices []int

	// Enriched indicates the response was post-processed (e.g., citations added).
	// Mandatory per P1-BUILD-DECISIONS.md § H canonical maps.
	Enriched bool

	// Provider and model that served the request (for audit / cost ledger).
	Provider string
	Model    string
}

// StreamChunk is a single delta in a streaming completion.
type StreamChunk struct {
	Delta string
	Done  bool
	Err   error
}

// Provider is the interface all LLM adapters must satisfy.
// Concrete implementations: AnthropicProvider, OpenAICompatProvider.
//
// Embed signature: (ctx, text, expectedDim int) per P1-CANONICAL-MAPS.md
// §Shared Interface Registry: Provider.Embed(ctx, text, expectedDim).
type Provider interface {
	// Complete executes a non-streaming chat completion.
	Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error)

	// Stream returns a channel of StreamChunk for streaming completions.
	// The channel is closed (with Done=true) when the stream ends normally.
	// The caller must drain the channel; cancelling ctx stops the stream.
	Stream(ctx context.Context, req LaneRequest) (<-chan StreamChunk, error)

	// Embed converts text to a float32 vector of expectedDim dimensions.
	// If expectedDim == 0, the provider returns its native dimension.
	// Canonical signature: Embed(ctx, text string, expectedDim int).
	Embed(ctx context.Context, text string, expectedDim int) ([]float32, error)

	// Rerank scores documents against a query and returns ranked indices.
	Rerank(ctx context.Context, query string, documents []string, topN int) ([]int, error)

	// HealthCheck returns nil when the provider endpoint is reachable.
	HealthCheck(ctx context.Context) error

	// Name returns the canonical provider name (e.g., "anthropic", "openai").
	Name() string
}

// GatewayError wraps errors from provider calls with lane/provider context.
type GatewayError struct {
	Lane     Lane
	Provider string
	Code     string // "rate_limit" | "auth" | "timeout" | "upstream" | "config"
	Cause    error
}

func (e *GatewayError) Error() string {
	return fmt.Sprintf("gateway[%s/%s]: %s: %v", e.Lane, e.Provider, e.Code, e.Cause)
}

func (e *GatewayError) Unwrap() error { return e.Cause }

// nopReranker is a no-op implementation so providers that don't support
// reranking (e.g., plain chat providers) can satisfy the interface.
type nopReranker struct{}

func (nopReranker) Rerank(_ context.Context, _ string, docs []string, topN int) ([]int, error) {
	n := len(docs)
	if topN > 0 && topN < n {
		n = topN
	}
	indices := make([]int, n)
	for i := range indices {
		indices[i] = i
	}
	return indices, nil
}

// nopEmbedder is a no-op so providers that don't embed can satisfy the interface.
type nopEmbedder struct{}

func (nopEmbedder) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return nil, fmt.Errorf("embedding not supported by this provider")
}

// ReadAll drains an io.Reader to a string (used by adapters for HTTP bodies).
func readAll(r io.Reader) (string, error) {
	b, err := io.ReadAll(r)
	return string(b), err
}
