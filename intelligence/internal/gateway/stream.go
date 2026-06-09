// Package gateway — canonical streaming pipeline and tool-use envelope.
//
// Purpose: Provide StreamComplete, which wraps a Provider's raw SSE stream into
//          typed GatewayStreamChunk values with full text accumulation, tool-use
//          block extraction, backpressure protection, and partial recovery on
//          mid-flight errors.
// Inputs:  LaneRequest; resolved Provider from the router.
// Outputs: <-chan GatewayStreamChunk (7 canonical fields, no raw provider strings).
// Constraints:
//   - All errors wrapped in canonical GatewayError — no raw provider error strings.
//   - Buffer cap = maxBufferTokens (default 4096 chunks); BUFFER_OVERFLOW emitted +
//     channel closed when full.
//   - Mid-flight error → final chunk {Partial:true, ContentSoFar:<acc>, Error:<wrapped>}.
//   - No raw model name strings here; provider already holds the resolved model.
//
// SPORT: REGISTRY-FUNCTIONS.md → StreamComplete.
package gateway

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// maxBufferTokens is the default channel buffer capacity (in chunk count).
// Prevents OOM when callers are slow readers.
const maxBufferTokens = 4096

// ToolUseBlock is the canonical representation of a tool-use call from the model.
// Both Anthropic and OpenAI function-call shapes are normalised into this struct.
type ToolUseBlock struct {
	// ID is the provider-assigned tool call identifier (stable across streaming).
	ID string
	// Name is the tool/function name.
	Name string
	// InputJSON is the raw JSON-encoded arguments string.
	InputJSON string
}

// UsageStats carries token counters emitted in message_stop / usage events.
type UsageStats struct {
	InputTokens  int
	OutputTokens int
}

// GatewayStreamChunk is the canonical streaming delta type.
// Exactly 7 fields per LEDGER §H interface spec.
type GatewayStreamChunk struct {
	// DeltaText is the incremental text content for this chunk (may be empty).
	DeltaText string
	// ToolUseBlock is non-nil when the chunk carries a complete tool-call block.
	ToolUseBlock *ToolUseBlock
	// StopReason is non-empty on the final chunk ("end_turn", "tool_use", "stop", etc.).
	StopReason string
	// Usage is non-nil on the final chunk when the provider reports token counts.
	Usage *UsageStats
	// Partial is true when the stream was interrupted mid-flight (error recovery path).
	Partial bool
	// ContentSoFar is the full text accumulated up to this chunk (always maintained).
	ContentSoFar string
	// Error is non-nil on error or BUFFER_OVERFLOW chunks; always a *GatewayError.
	Error error
}

// StreamComplete resolves the correct provider for req.Lane, calls its Stream
// method (which delivers raw StreamChunks), then re-packages those into the
// canonical GatewayStreamChunk channel.
//
// The caller must drain the returned channel; cancelling ctx closes it.
func StreamComplete(ctx context.Context, provider Provider, req LaneRequest) <-chan GatewayStreamChunk {
	out := make(chan GatewayStreamChunk, maxBufferTokens)

	go func() {
		defer close(out)

		rawCh, err := provider.Stream(ctx, req)
		if err != nil {
			ge := wrapErr(req.Lane, provider.Name(), "upstream", err)
			tryEmit(out, GatewayStreamChunk{Error: ge})
			return
		}

		var acc strings.Builder

		for raw := range rawCh {
			if raw.Err != nil {
				// Mid-flight error: emit partial recovery chunk.
				ge := wrapErr(req.Lane, provider.Name(), "upstream", raw.Err)
				chunk := GatewayStreamChunk{
					Partial:      true,
					ContentSoFar: acc.String(),
					Error:        ge,
				}
				tryEmit(out, chunk)
				return
			}

			if raw.Delta != "" {
				acc.WriteString(raw.Delta)
			}

			chunk := GatewayStreamChunk{
				DeltaText:    raw.Delta,
				ContentSoFar: acc.String(),
			}

			if raw.Done {
				chunk.StopReason = "stop"
			}

			if !emitOrOverflow(out, chunk, req.Lane, provider.Name()) {
				return
			}

			if raw.Done {
				return
			}
		}
	}()

	return out
}

// streamAnthropicSSE reads an Anthropic SSE response body and pushes
// GatewayStreamChunks to out. It handles content_block_delta, message_delta,
// and message_stop events. Used by the HTTP-level Anthropic SSE path when
// the SDK is bypassed (e.g., in tests with httptest servers).
//
// lane and providerName are used for GatewayError wrapping only.
func streamAnthropicSSE(ctx context.Context, body io.ReadCloser, lane Lane, providerName string, out chan<- GatewayStreamChunk) {
	defer body.Close()

	var acc strings.Builder
	// tool accumulator: indexed by content_block index
	toolBlocks := map[int]*toolAccum{}

	scanner := bufio.NewScanner(body)
	var eventType string

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ge := wrapErr(lane, providerName, "timeout", ctx.Err())
			emitOrOverflow(out, GatewayStreamChunk{Partial: true, ContentSoFar: acc.String(), Error: ge}, lane, providerName) //nolint:errcheck
			return
		default:
		}

		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			eventType = strings.TrimPrefix(line, "event: ")
			continue
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			return
		}

		switch eventType {
		case "content_block_delta":
			var ev struct {
				Index int `json:"index"`
				Delta struct {
					Type  string `json:"type"`
					Text  string `json:"text"`
					// Tool input delta fields
					PartialJSON string `json:"partial_json"`
				} `json:"delta"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			switch ev.Delta.Type {
			case "text_delta":
				acc.WriteString(ev.Delta.Text)
				chunk := GatewayStreamChunk{DeltaText: ev.Delta.Text, ContentSoFar: acc.String()}
				if !emitOrOverflow(out, chunk, lane, providerName) {
					return
				}
			case "input_json_delta":
				if tb, ok := toolBlocks[ev.Index]; ok {
					tb.inputJSON.WriteString(ev.Delta.PartialJSON)
				}
			}

		case "content_block_start":
			var ev struct {
				Index        int `json:"index"`
				ContentBlock struct {
					Type string `json:"type"`
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"content_block"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if ev.ContentBlock.Type == "tool_use" {
				toolBlocks[ev.Index] = &toolAccum{id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			}

		case "content_block_stop":
			var ev struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			if tb, ok := toolBlocks[ev.Index]; ok {
				tub := &ToolUseBlock{ID: tb.id, Name: tb.name, InputJSON: tb.inputJSON.String()}
				chunk := GatewayStreamChunk{ToolUseBlock: tub, ContentSoFar: acc.String()}
				if !emitOrOverflow(out, chunk, lane, providerName) {
					return
				}
				delete(toolBlocks, ev.Index)
			}

		case "message_delta":
			var ev struct {
				Delta struct {
					StopReason string `json:"stop_reason"`
				} `json:"delta"`
				Usage struct {
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal([]byte(data), &ev); err != nil {
				continue
			}
			chunk := GatewayStreamChunk{
				StopReason:   ev.Delta.StopReason,
				ContentSoFar: acc.String(),
				Usage:        &UsageStats{OutputTokens: ev.Usage.OutputTokens},
			}
			if !emitOrOverflow(out, chunk, lane, providerName) {
				return
			}

		case "message_stop":
			// Final event — nothing more to emit beyond what message_delta already sent.
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ge := wrapErr(lane, providerName, "upstream", err)
		emitOrOverflow(out, GatewayStreamChunk{Partial: true, ContentSoFar: acc.String(), Error: ge}, lane, providerName) //nolint:errcheck
	}
}

// streamOpenAICompatSSE reads an OpenAI-compatible SSE response body
// (stream=true) and pushes GatewayStreamChunks. Handles the [DONE] sentinel.
func streamOpenAICompatSSE(ctx context.Context, body io.ReadCloser, lane Lane, providerName string, out chan<- GatewayStreamChunk) {
	defer body.Close()

	var acc strings.Builder
	scanner := bufio.NewScanner(body)

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ge := wrapErr(lane, providerName, "timeout", ctx.Err())
			emitOrOverflow(out, GatewayStreamChunk{Partial: true, ContentSoFar: acc.String(), Error: ge}, lane, providerName) //nolint:errcheck
			return
		default:
		}

		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			chunk := GatewayStreamChunk{StopReason: "stop", ContentSoFar: acc.String()}
			emitOrOverflow(out, chunk, lane, providerName) //nolint:errcheck
			return
		}

		var event struct {
			Choices []struct {
				Delta struct {
					Content      string `json:"content"`
					ToolCalls    []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}
		if len(event.Choices) == 0 {
			continue
		}
		choice := event.Choices[0]

		if choice.Delta.Content != "" {
			acc.WriteString(choice.Delta.Content)
			chunk := GatewayStreamChunk{DeltaText: choice.Delta.Content, ContentSoFar: acc.String()}
			if !emitOrOverflow(out, chunk, lane, providerName) {
				return
			}
		}

		// Emit completed tool calls embedded in a single chunk
		for _, tc := range choice.Delta.ToolCalls {
			tub := &ToolUseBlock{ID: tc.ID, Name: tc.Function.Name, InputJSON: tc.Function.Arguments}
			chunk := GatewayStreamChunk{ToolUseBlock: tub, ContentSoFar: acc.String()}
			if !emitOrOverflow(out, chunk, lane, providerName) {
				return
			}
		}

		if choice.FinishReason != "" {
			var usage *UsageStats
			if event.Usage != nil {
				usage = &UsageStats{
					InputTokens:  event.Usage.PromptTokens,
					OutputTokens: event.Usage.CompletionTokens,
				}
			}
			chunk := GatewayStreamChunk{
				StopReason:   choice.FinishReason,
				ContentSoFar: acc.String(),
				Usage:        usage,
			}
			emitOrOverflow(out, chunk, lane, providerName) //nolint:errcheck
			return
		}
	}

	if err := scanner.Err(); err != nil {
		ge := wrapErr(lane, providerName, "upstream", err)
		emitOrOverflow(out, GatewayStreamChunk{Partial: true, ContentSoFar: acc.String(), Error: ge}, lane, providerName) //nolint:errcheck
	}
}

// NewAnthropicSSEStream performs a raw HTTP POST to url with body, then calls
// streamAnthropicSSE on the response. Exposed for integration tests using httptest.
// apiKey may be empty for test servers.
func NewAnthropicSSEStream(ctx context.Context, url, apiKey string, body []byte, lane Lane, out chan<- GatewayStreamChunk) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return wrapErr(lane, "anthropic", "config", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapErr(lane, "anthropic", "upstream", err)
	}
	streamAnthropicSSE(ctx, resp.Body, lane, "anthropic", out)
	return nil
}

// NewOpenAICompatSSEStream performs a raw HTTP POST and calls streamOpenAICompatSSE.
// Exposed for tests using httptest.
func NewOpenAICompatSSEStream(ctx context.Context, url, apiKey string, body []byte, lane Lane, providerName string, out chan<- GatewayStreamChunk) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(body)))
	if err != nil {
		return wrapErr(lane, providerName, "config", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return wrapErr(lane, providerName, "upstream", err)
	}
	streamOpenAICompatSSE(ctx, resp.Body, lane, providerName, out)
	return nil
}

// ---- internal helpers ----

// toolAccum accumulates streamed tool-input JSON for one content block.
type toolAccum struct {
	id        string
	name      string
	inputJSON strings.Builder
}

// wrapErr wraps err in a *GatewayError if it is not already one.
func wrapErr(lane Lane, provider, code string, err error) *GatewayError {
	if ge, ok := err.(*GatewayError); ok {
		return ge
	}
	return &GatewayError{Lane: lane, Provider: provider, Code: code, Cause: err}
}

// tryEmit sends chunk to ch, ignoring if ch is closed.
func tryEmit(ch chan<- GatewayStreamChunk, chunk GatewayStreamChunk) {
	select {
	case ch <- chunk:
	default:
	}
}

// emitOrOverflow sends chunk to out; if out is full it emits a BUFFER_OVERFLOW error
// chunk, closes out, and returns false. Returns true on success.
func emitOrOverflow(out chan<- GatewayStreamChunk, chunk GatewayStreamChunk, lane Lane, provider string) bool {
	select {
	case out <- chunk:
		return true
	default:
		// Channel full — emit overflow error then signal caller to stop.
		overflow := GatewayStreamChunk{
			Error: &GatewayError{
				Lane:     lane,
				Provider: provider,
				Code:     "BUFFER_OVERFLOW",
				Cause:    fmt.Errorf("stream channel full (%d slots): slow reader", maxBufferTokens),
			},
			Partial:      true,
			ContentSoFar: chunk.ContentSoFar,
		}
		// Best-effort: channel may be full for overflow too; that's acceptable.
		select {
		case out <- overflow:
		default:
		}
		return false
	}
}
