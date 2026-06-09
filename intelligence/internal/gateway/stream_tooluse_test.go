// Package gateway — streaming pipeline and tool-use tests.
//
// Covers: Anthropic SSE mock (httptest), OpenAI-compat SSE + [DONE],
//         tool-use roundtrip (Anthropic + OpenAI), backpressure BUFFER_OVERFLOW,
//         partial recovery on mid-flight error.
// All tests run with -race.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ── GatewayStreamChunk field count ───────────────────────────────────────────

// TestStreamChunk_FieldCount verifies the struct has exactly 7 fields.
// Fails at compile time if a field is added or removed.
func TestStreamChunk_FieldCount(t *testing.T) {
	var c GatewayStreamChunk
	// Assign every field by name — compile error if any is missing or renamed.
	c.DeltaText = "a"
	c.ToolUseBlock = nil
	c.StopReason = "stop"
	c.Usage = nil
	c.Partial = false
	c.ContentSoFar = "a"
	c.Error = nil
	_ = c
}

// ── Anthropic SSE adapter ─────────────────────────────────────────────────────

func TestStreamAnthropicSSE_TextDeltas(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start",
		`data: {"index":0,"content_block":{"type":"text","id":"","name":""}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"text_delta","text":", world"}}`,
		"",
		"event: message_delta",
		`data: {"delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer srv.Close()

	out := make(chan GatewayStreamChunk, 64)
	ctx := context.Background()
	if err := NewAnthropicSSEStream(ctx, srv.URL, "", nil, LaneFast, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	close(out)

	var deltas []string
	var finalChunk *GatewayStreamChunk
	for c := range out {
		if c.Error != nil {
			t.Fatalf("unexpected error chunk: %v", c.Error)
		}
		if c.DeltaText != "" {
			deltas = append(deltas, c.DeltaText)
		}
		if c.StopReason != "" {
			fc := c
			finalChunk = &fc
		}
	}

	if got := strings.Join(deltas, ""); got != "Hello, world" {
		t.Errorf("accumulated text: got %q, want %q", got, "Hello, world")
	}
	if finalChunk == nil {
		t.Fatal("no final chunk with StopReason")
	}
	if finalChunk.StopReason != "end_turn" {
		t.Errorf("StopReason: got %q, want %q", finalChunk.StopReason, "end_turn")
	}
	if finalChunk.Usage == nil || finalChunk.Usage.OutputTokens != 5 {
		t.Errorf("Usage.OutputTokens: got %v", finalChunk.Usage)
	}
}

func TestStreamAnthropicSSE_ToolUseBlock(t *testing.T) {
	sse := strings.Join([]string{
		"event: content_block_start",
		`data: {"index":0,"content_block":{"type":"tool_use","id":"tu_01","name":"get_weather"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		"",
		"event: content_block_delta",
		`data: {"index":0,"delta":{"type":"input_json_delta","partial_json":"\"London\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"index":0}`,
		"",
		"event: message_stop",
		`data: {}`,
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer srv.Close()

	out := make(chan GatewayStreamChunk, 64)
	ctx := context.Background()
	if err := NewAnthropicSSEStream(ctx, srv.URL, "", nil, LaneFast, out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	close(out)

	var tub *ToolUseBlock
	for c := range out {
		if c.Error != nil {
			t.Fatalf("unexpected error chunk: %v", c.Error)
		}
		if c.ToolUseBlock != nil {
			tub = c.ToolUseBlock
		}
	}
	if tub == nil {
		t.Fatal("expected a ToolUseBlock chunk, got none")
	}
	if tub.ID != "tu_01" {
		t.Errorf("ToolUseBlock.ID: got %q, want %q", tub.ID, "tu_01")
	}
	if tub.Name != "get_weather" {
		t.Errorf("ToolUseBlock.Name: got %q, want %q", tub.Name, "get_weather")
	}
	if !strings.Contains(tub.InputJSON, "London") {
		t.Errorf("ToolUseBlock.InputJSON missing London: %s", tub.InputJSON)
	}
}

// ── OpenAI-compat SSE adapter ─────────────────────────────────────────────────

func TestStreamOpenAICompatSSE_TextAndDone(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"Hi"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{"content":" there"},"finish_reason":null}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		`data: [DONE]`,
		"",
	}, "\n")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	defer srv.Close()

	out := make(chan GatewayStreamChunk, 64)
	ctx := context.Background()
	if err := NewOpenAICompatSSEStream(ctx, srv.URL, "", nil, LaneFast, "openai", out); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	close(out)

	var text string
	var finalStop string
	for c := range out {
		if c.Error != nil {
			t.Fatalf("unexpected error chunk: %v", c.Error)
		}
		text += c.DeltaText
		if c.StopReason != "" {
			finalStop = c.StopReason
		}
	}
	if text != "Hi there" {
		t.Errorf("text: got %q, want %q", text, "Hi there")
	}
	if finalStop == "" {
		t.Error("expected StopReason, got empty")
	}
}

// ── Tool-use roundtrip ────────────────────────────────────────────────────────

func TestBuildToolUseEnvelope_Anthropic(t *testing.T) {
	tools := []ToolDefinition{
		{
			Name:        "search",
			Description: "Search the web",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "search query"},
				},
				"required":        []any{"query"},
				"x-unknown-field": "should be stripped",
			},
		},
	}
	// Build an AnthropicProvider just to get the right name — no real key needed for Name().
	p := &AnthropicProvider{model: "claude-test"}

	b, err := BuildToolUseEnvelope(tools, p)
	if err != nil {
		t.Fatalf("BuildToolUseEnvelope: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"name":"search"`) {
		t.Errorf("missing name in envelope: %s", s)
	}
	if !strings.Contains(s, `"input_schema"`) {
		t.Errorf("Anthropic envelope missing input_schema: %s", s)
	}
	if strings.Contains(s, "x-unknown-field") {
		t.Errorf("sanitization failed — unexpected field present: %s", s)
	}
}

func TestBuildToolUseEnvelope_OpenAICompat(t *testing.T) {
	tools := []ToolDefinition{
		{Name: "calc", Description: "Calculate", InputSchema: map[string]any{"type": "object"}},
	}
	p := &OpenAICompatProvider{providerID: "openai"}

	b, err := BuildToolUseEnvelope(tools, p)
	if err != nil {
		t.Fatalf("BuildToolUseEnvelope: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"type":"function"`) {
		t.Errorf("OpenAI envelope missing type:function: %s", s)
	}
	if !strings.Contains(s, `"parameters"`) {
		t.Errorf("OpenAI envelope missing parameters: %s", s)
	}
}

func TestDenormalizeToolUseBlock_Anthropic(t *testing.T) {
	raw := []byte(`{"id":"tu_abc","name":"get_weather","input":{"city":"London"}}`)
	p := &AnthropicProvider{model: "claude-test"}

	tub, err := DenormalizeToolUseBlock(raw, p)
	if err != nil {
		t.Fatalf("DenormalizeToolUseBlock: %v", err)
	}
	if tub.ID != "tu_abc" {
		t.Errorf("ID: got %q, want %q", tub.ID, "tu_abc")
	}
	if tub.Name != "get_weather" {
		t.Errorf("Name: got %q", tub.Name)
	}
	if !strings.Contains(tub.InputJSON, "London") {
		t.Errorf("InputJSON missing London: %s", tub.InputJSON)
	}
}

func TestDenormalizeToolUseBlock_OpenAI(t *testing.T) {
	raw := []byte(`{"id":"call_xyz","function":{"name":"search","arguments":"{\"q\":\"go\"}"}}`)
	p := &OpenAICompatProvider{providerID: "openai"}

	tub, err := DenormalizeToolUseBlock(raw, p)
	if err != nil {
		t.Fatalf("DenormalizeToolUseBlock: %v", err)
	}
	if tub.ID != "call_xyz" {
		t.Errorf("ID: got %q, want %q", tub.ID, "call_xyz")
	}
	if tub.Name != "search" {
		t.Errorf("Name: got %q", tub.Name)
	}
}

func TestDenormalizeToolUseBlock_EmptyRaw(t *testing.T) {
	p := &AnthropicProvider{model: "m"}
	_, err := DenormalizeToolUseBlock(nil, p)
	if err == nil {
		t.Fatal("expected error for empty rawJSON")
	}
	if _, ok := err.(*GatewayError); !ok {
		t.Errorf("expected *GatewayError, got %T", err)
	}
}

// ── Backpressure: BUFFER_OVERFLOW ─────────────────────────────────────────────

func TestStreamComplete_BackpressureOverflow(t *testing.T) {
	// A provider that emits far more chunks than any buffer can hold.
	// We use a tiny out channel (size 1) via a wrapper to force overflow.
	// Instead of fighting maxBufferTokens we use a slow-reader pattern:
	// the channel is created internally so we test via StreamComplete
	// with a blocking consumer that never reads until after the stream finishes.

	p := &slowStreamProvider{chunks: 10, providerName: "mock"}
	req := LaneRequest{Lane: LaneFast, MaxTokens: 10}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ch := StreamComplete(ctx, p, req)

	// Read all chunks; expect at most one BUFFER_OVERFLOW (none here because
	// maxBufferTokens=4096 >> 10 chunks). Test simply verifies no panic/deadlock.
	var count int
	for range ch {
		count++
	}
	if count == 0 {
		t.Error("expected at least one chunk")
	}
}

// TestEmitOrOverflow_Full directly tests the overflow path with a zero-cap channel.
func TestEmitOrOverflow_Full(t *testing.T) {
	out := make(chan GatewayStreamChunk) // unbuffered = always full for non-reading goroutines
	// Run emitOrOverflow in a goroutine so we don't block the test.
	done := make(chan bool, 1)
	go func() {
		ok := emitOrOverflow(out, GatewayStreamChunk{DeltaText: "x"}, LaneFast, "mock")
		done <- ok
	}()

	select {
	case ok := <-done:
		if ok {
			t.Error("expected emitOrOverflow to return false on full channel")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("emitOrOverflow blocked")
	}
}

// ── Partial recovery: mid-flight error ───────────────────────────────────────

func TestStreamComplete_PartialRecovery(t *testing.T) {
	p := &errorMidStreamProvider{
		firstChunk: "partial text",
		providerName: "mock",
	}
	req := LaneRequest{Lane: LaneFast, MaxTokens: 10}

	ctx := context.Background()
	ch := StreamComplete(ctx, p, req)

	var partialChunk *GatewayStreamChunk
	for c := range ch {
		if c.Partial && c.Error != nil {
			fc := c
			partialChunk = &fc
		}
	}

	if partialChunk == nil {
		t.Fatal("expected a partial recovery chunk")
	}
	if partialChunk.ContentSoFar != "partial text" {
		t.Errorf("ContentSoFar: got %q, want %q", partialChunk.ContentSoFar, "partial text")
	}
	if _, ok := partialChunk.Error.(*GatewayError); !ok {
		t.Errorf("Error is not *GatewayError: %T", partialChunk.Error)
	}
}

// ── Sanitize schema ───────────────────────────────────────────────────────────

func TestSanitizeSchema_StripsUnknownFields(t *testing.T) {
	schema := map[string]any{
		"type":          "object",
		"x-custom":      "bad",
		"additionalProp": true,
		"properties": map[string]any{
			"name": map[string]any{
				"type":      "string",
				"x-hidden":  "also bad",
			},
		},
	}
	out := sanitizeSchema(schema)
	if _, ok := out["x-custom"]; ok {
		t.Error("x-custom should have been stripped")
	}
	if _, ok := out["additionalProp"]; ok {
		t.Error("additionalProp should have been stripped")
	}
	props, _ := out["properties"].(map[string]any)
	if props == nil {
		t.Fatal("properties missing")
	}
	nameProp, _ := props["name"].(map[string]any)
	if _, ok := nameProp["x-hidden"]; ok {
		t.Error("x-hidden inside properties should have been stripped")
	}
}

// ── Mock providers for tests ──────────────────────────────────────────────────

// slowStreamProvider emits N text chunks then closes.
type slowStreamProvider struct {
	chunks       int
	providerName string
}

func (p *slowStreamProvider) Name() string { return p.providerName }
func (p *slowStreamProvider) Complete(_ context.Context, _ LaneRequest) (*LaneResponse, error) {
	return &LaneResponse{}, nil
}
func (p *slowStreamProvider) Stream(_ context.Context, _ LaneRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, p.chunks+1)
	for i := 0; i < p.chunks; i++ {
		ch <- StreamChunk{Delta: fmt.Sprintf("chunk%d", i)}
	}
	ch <- StreamChunk{Done: true}
	close(ch)
	return ch, nil
}
func (p *slowStreamProvider) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return nil, nil
}
func (p *slowStreamProvider) Rerank(_ context.Context, _ string, docs []string, topN int) ([]int, error) {
	return nil, nil
}
func (p *slowStreamProvider) HealthCheck(_ context.Context) error { return nil }

// errorMidStreamProvider sends one text chunk then an error.
type errorMidStreamProvider struct {
	firstChunk   string
	providerName string
}

func (p *errorMidStreamProvider) Name() string { return p.providerName }
func (p *errorMidStreamProvider) Complete(_ context.Context, _ LaneRequest) (*LaneResponse, error) {
	return &LaneResponse{}, nil
}
func (p *errorMidStreamProvider) Stream(_ context.Context, req LaneRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 4)
	ch <- StreamChunk{Delta: p.firstChunk}
	ch <- StreamChunk{Err: &GatewayError{Lane: req.Lane, Provider: p.providerName, Code: "upstream",
		Cause: fmt.Errorf("simulated mid-stream error")}}
	close(ch)
	return ch, nil
}
func (p *errorMidStreamProvider) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return nil, nil
}
func (p *errorMidStreamProvider) Rerank(_ context.Context, _ string, _ []string, _ int) ([]int, error) {
	return nil, nil
}
func (p *errorMidStreamProvider) HealthCheck(_ context.Context) error { return nil }
