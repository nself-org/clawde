// Package gateway — Anthropic adapter.
//
// Purpose: Implement the Provider interface for the Anthropic API using the
//          official anthropic-sdk-go library (v1.46.0+).
// Inputs:  LaneRequest; api_key_ref resolved from vault.env at registry load.
// Outputs: LaneResponse / StreamChunk channel.
// Constraints: No raw model strings here; model name comes from ProviderEntry.Model.
//              No raw API keys; key is pre-resolved and passed as a string.
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence gateway.
package gateway

import (
	"context"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// AnthropicProvider wraps the anthropic-sdk-go client.
// Client is a value type in v1.46.0 — stored by value here.
type AnthropicProvider struct {
	client anthropic.Client
	model  string // resolved from registry; never hardcoded here
}

// NewAnthropicProvider constructs a provider from a pre-resolved API key and model name.
// The apiKey is loaded from vault.env by the registry loader — never passed raw in config.
func NewAnthropicProvider(apiKey, model string) (*AnthropicProvider, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic: api key is empty (check api_key_ref in registry)")
	}
	if model == "" {
		return nil, fmt.Errorf("anthropic: model is required")
	}
	client := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &AnthropicProvider{client: client, model: model}, nil
}

// Name returns the canonical provider name.
func (p *AnthropicProvider) Name() string { return "anthropic" }

// Complete executes a non-streaming chat completion.
func (p *AnthropicProvider) Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error) {
	msgs, err := convertMessages(req.Messages)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "config", Cause: err}
	}

	maxTok := int64(req.MaxTokens)
	if maxTok <= 0 {
		maxTok = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: maxTok,
		Messages:  msgs,
	}
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.SystemPrompt}}
	}

	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream", Cause: err}
	}

	var sb strings.Builder
	for _, block := range resp.Content {
		if v, ok := block.AsAny().(anthropic.TextBlock); ok {
			sb.WriteString(v.Text)
		}
	}

	return &LaneResponse{
		Content:      sb.String(),
		InputTokens:  int(resp.Usage.InputTokens),
		OutputTokens: int(resp.Usage.OutputTokens),
		Provider:     p.Name(),
		Model:        p.model,
	}, nil
}

// Stream returns a channel of StreamChunk for streaming completions.
func (p *AnthropicProvider) Stream(ctx context.Context, req LaneRequest) (<-chan StreamChunk, error) {
	msgs, err := convertMessages(req.Messages)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "config", Cause: err}
	}

	maxTok := int64(req.MaxTokens)
	if maxTok <= 0 {
		maxTok = 4096
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: maxTok,
		Messages:  msgs,
	}
	if req.SystemPrompt != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.SystemPrompt}}
	}

	ch := make(chan StreamChunk, 64)
	go func() {
		defer close(ch)
		stream := p.client.Messages.NewStreaming(ctx, params)
		for stream.Next() {
			event := stream.Current()
			// Switch on the typed union — AsAny() returns the concrete event type.
			switch v := event.AsAny().(type) {
			case anthropic.ContentBlockDeltaEvent:
				if td, ok := v.Delta.AsAny().(anthropic.TextDelta); ok {
					ch <- StreamChunk{Delta: td.Text}
				}
			}
		}
		if err := stream.Err(); err != nil {
			ch <- StreamChunk{Err: &GatewayError{
				Lane: req.Lane, Provider: p.Name(), Code: "upstream", Cause: err,
			}}
			return
		}
		ch <- StreamChunk{Done: true}
	}()
	return ch, nil
}

// Embed is not natively supported by Anthropic chat models.
// The embedding lane uses the TEI sidecar (OpenAICompatProvider pointing to :8080).
func (p *AnthropicProvider) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return nil, fmt.Errorf("anthropic: embedding not supported; use the embedding lane (TEI sidecar)")
}

// Rerank returns pass-through indices; Anthropic does not natively rerank.
// The rerank lane uses the TEI reranker sidecar (:8092).
func (p *AnthropicProvider) Rerank(_ context.Context, _ string, docs []string, topN int) ([]int, error) {
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

// HealthCheck pings the Anthropic API with a minimal request.
func (p *AnthropicProvider) HealthCheck(ctx context.Context) error {
	_, err := p.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 1,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock("ping")),
		},
	})
	if err != nil {
		return fmt.Errorf("anthropic health: %w", err)
	}
	return nil
}

// convertMessages translates gateway Messages to anthropic SDK params.
// System messages are handled separately via params.System.
func convertMessages(msgs []Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case "user":
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
		case "assistant":
			out = append(out, anthropic.NewAssistantMessage(anthropic.NewTextBlock(m.Content)))
		case "system":
			// system content is handled via params.System; skip here
		default:
			return nil, fmt.Errorf("unknown message role %q", m.Role)
		}
	}
	return out, nil
}
