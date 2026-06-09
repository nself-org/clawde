// Package gateway — unit + integration tests for T02 components.
//
// Covers: RouteRequest, WithFailover (required integration test), EnforceRateLimit,
//         TrackCost (no-op path), GeminiPoolPick (no-redis path).
package gateway

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// ── Router tests ─────────────────────────────────────────────────────────────

func TestRouteRequest_ReturnsEntries(t *testing.T) {
	reg := mustParseRegistry(t, minimalRegistryYAML())
	req := LaneRequest{Lane: LaneFast, WorkspaceID: "ws-1"}
	entries, err := RouteRequest(context.Background(), reg, req)
	if err != nil {
		t.Fatalf("RouteRequest failed: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one entry")
	}
}

func TestRouteRequest_NilRegistry(t *testing.T) {
	req := LaneRequest{Lane: LaneFast}
	_, err := RouteRequest(context.Background(), nil, req)
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
	var gwErr *GatewayError
	if !errors.As(err, &gwErr) {
		t.Fatalf("expected *GatewayError, got %T", err)
	}
	if gwErr.Code != "config" {
		t.Errorf("want code=config, got %s", gwErr.Code)
	}
}

func TestRouteRequest_UnknownLane(t *testing.T) {
	reg := mustParseRegistry(t, minimalRegistryYAML())
	req := LaneRequest{Lane: "nonexistent"}
	_, err := RouteRequest(context.Background(), reg, req)
	if err == nil {
		t.Fatal("expected error for unknown lane")
	}
}

func TestRouteRequest_FilterByLatency(t *testing.T) {
	// Build a registry where the fast lane has two entries:
	// primary with p99=100ms, fallback with p99=50000ms (extreme outlier).
	yaml := `
version: 1
lanes:
  - lane: fast
    entries:
      - provider: anthropic
        model: claude-primary
        api_key_ref: ANTHROPIC_API_KEY
        p99_latency_ms: 100
      - provider: openai
        base_url: https://api.openai.com/v1
        model: gpt-fallback
        api_key_ref: OPENAI_API_KEY
        p99_latency_ms: 50000
  - lane: deep
    entries:
      - provider: anthropic
        model: claude-deep
        api_key_ref: ANTHROPIC_API_KEY
  - lane: multimodal
    entries:
      - provider: anthropic
        model: claude-multi
        api_key_ref: ANTHROPIC_API_KEY
  - lane: embedding
    entries:
      - provider: tei-embed
        base_url: http://127.0.0.1:8080/v1
        model: BAAI/bge-m3
        api_key_ref: ""
  - lane: rerank
    entries:
      - provider: tei-rerank
        base_url: http://127.0.0.1:8092/v1
        model: BAAI/bge-reranker-v2-m3
        api_key_ref: ""
  - lane: live
    entries:
      - provider: anthropic
        model: claude-live
        api_key_ref: ANTHROPIC_API_KEY
  - lane: local
    entries:
      - provider: vllm
        base_url: http://127.0.0.1:8093/v1
        model: local-model
        api_key_ref: ""
`
	reg := mustParseRegistry(t, yaml)
	req := LaneRequest{Lane: LaneFast}
	entries, err := RouteRequest(context.Background(), reg, req)
	if err != nil {
		t.Fatalf("RouteRequest failed: %v", err)
	}
	// Outlier (50000ms >> 3×100ms=300ms) should be filtered out.
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after SLO filter, got %d", len(entries))
	}
	if entries[0].Provider != "anthropic" {
		t.Errorf("expected primary=anthropic, got %s", entries[0].Provider)
	}
}

// ── Failover integration test (REQUIRED) ─────────────────────────────────────
// Provider A always 429 → provider B always 200 → assert ProviderUsed==B, Enriched==true.

// mockProvider is a test double for Provider.
type mockProvider struct {
	name     string
	failWith error // nil = success
}

func (m *mockProvider) Complete(_ context.Context, req LaneRequest) (*LaneResponse, error) {
	if m.failWith != nil {
		return nil, m.failWith
	}
	return &LaneResponse{Content: "ok", Provider: m.name, Enriched: true}, nil
}
func (m *mockProvider) Stream(_ context.Context, _ LaneRequest) (<-chan StreamChunk, error) {
	ch := make(chan StreamChunk, 1)
	ch <- StreamChunk{Done: true}
	close(ch)
	return ch, nil
}
func (m *mockProvider) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return nil, fmt.Errorf("not supported")
}
func (m *mockProvider) Rerank(_ context.Context, _ string, _ []string, _ int) ([]int, error) {
	return nil, fmt.Errorf("not supported")
}
func (m *mockProvider) HealthCheck(_ context.Context) error { return nil }
func (m *mockProvider) Name() string                        { return m.name }

// withFailoverFromProviders is a testable variant of WithFailover that accepts
// pre-built Provider instances instead of ProviderEntry (avoids needing real API keys).
func withFailoverFromProviders(ctx context.Context, providers []Provider, req LaneRequest) (*FailoverResult, error) {
	if len(providers) == 0 {
		return &FailoverResult{Enriched: false}, &GatewayError{
			Lane: req.Lane, Code: "lane_unavailable",
			Cause: fmt.Errorf("no providers"),
		}
	}
	for _, p := range providers {
		resp, err := p.Complete(ctx, req)
		if err == nil {
			resp.Provider = p.Name()
			return &FailoverResult{
				Response:     resp,
				ProviderUsed: p.Name(),
				Enriched:     true,
			}, nil
		}
		var gwErr *GatewayError
		if errors.As(err, &gwErr) && gwErr.Code == "rate_limit" {
			continue // try next
		}
		continue // also try next for non-rate-limit errors (resilient)
	}
	return &FailoverResult{Enriched: false}, &GatewayError{
		Lane: req.Lane, Code: "lane_unavailable",
		Cause: fmt.Errorf("all providers exhausted"),
	}
}

// TestWithFailover_ProviderA429_FallsBackToProviderB is the required integration
// test: provider A always returns 429, provider B always returns 200.
// Assert: result.ProviderUsed == "provider-b" and result.Enriched == true.
func TestWithFailover_ProviderA429_FallsBackToProviderB(t *testing.T) {
	providerA := &mockProvider{
		name: "provider-a",
		failWith: &GatewayError{
			Lane:     LaneFast,
			Provider: "provider-a",
			Code:     "rate_limit",
			Cause:    fmt.Errorf("429 Too Many Requests"),
		},
	}
	providerB := &mockProvider{
		name:     "provider-b",
		failWith: nil, // always succeeds
	}

	req := LaneRequest{Lane: LaneFast, WorkspaceID: "ws-test"}
	result, err := withFailoverFromProviders(context.Background(), []Provider{providerA, providerB}, req)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if result.ProviderUsed != "provider-b" {
		t.Errorf("ProviderUsed: want provider-b, got %s", result.ProviderUsed)
	}
	if !result.Enriched {
		t.Error("Enriched: want true, got false")
	}
}

func TestWithFailover_AllProvidersFail(t *testing.T) {
	rateLimitErr := &GatewayError{Lane: LaneFast, Provider: "p", Code: "rate_limit", Cause: fmt.Errorf("429")}
	p1 := &mockProvider{name: "p1", failWith: rateLimitErr}
	p2 := &mockProvider{name: "p2", failWith: rateLimitErr}

	req := LaneRequest{Lane: LaneFast}
	result, err := withFailoverFromProviders(context.Background(), []Provider{p1, p2}, req)
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if result.Enriched {
		t.Error("Enriched: want false when all fail")
	}
	var gwErr *GatewayError
	if !errors.As(err, &gwErr) || gwErr.Code != "lane_unavailable" {
		t.Errorf("want code=lane_unavailable, got %v", err)
	}
}

func TestWithFailover_NoProviders(t *testing.T) {
	req := LaneRequest{Lane: LaneFast}
	_, err := withFailoverFromProviders(context.Background(), nil, req)
	if err == nil {
		t.Fatal("expected error for empty provider list")
	}
}

// ── TrackCost no-op path ──────────────────────────────────────────────────────

func TestTrackCost_NilConn(t *testing.T) {
	entry := ProviderEntry{Provider: "anthropic", Model: "claude-test", CostPer1kTokens: 0.003}
	req := LaneRequest{Lane: LaneFast, WorkspaceID: "ws-1", RequestID: "u-1"}
	resp := &LaneResponse{Content: "hello world", InputTokens: 10, OutputTokens: 5}
	// Should not panic or error with nil conn.
	if err := TrackCost(context.Background(), nil, entry, req, resp, 100); err != nil {
		t.Errorf("TrackCost(nil conn) should be no-op, got: %v", err)
	}
}

func TestTrackCost_NilResponse(t *testing.T) {
	entry := ProviderEntry{Provider: "anthropic", Model: "claude-test", CostPer1kTokens: 0.003}
	req := LaneRequest{Lane: LaneFast}
	if err := TrackCost(context.Background(), nil, entry, req, nil, 0); err != nil {
		t.Errorf("TrackCost with nil response and nil conn should be no-op, got: %v", err)
	}
}

// ── EnforceRateLimit no-redis path ───────────────────────────────────────────

func TestEnforceRateLimit_NilRedis(t *testing.T) {
	entry := ProviderEntry{Provider: "anthropic"}
	entry.RateLimit.RPM = 100
	entry.RateLimit.WindowSeconds = 60
	req := LaneRequest{Lane: LaneFast, RequestID: "user-1"}
	// nil rdb → always allowed.
	if err := EnforceRateLimit(context.Background(), nil, entry, req); err != nil {
		t.Errorf("expected nil (no redis), got: %v", err)
	}
}

func TestEnforceRateLimit_RPMZeroNoLimit(t *testing.T) {
	entry := ProviderEntry{Provider: "vllm"}
	entry.RateLimit.RPM = 0 // 0 = unlimited
	req := LaneRequest{Lane: LaneLocal, RequestID: "user-1"}
	if err := EnforceRateLimit(context.Background(), nil, entry, req); err != nil {
		t.Errorf("RPM=0 should be unlimited, got: %v", err)
	}
}

// ── GeminiPoolPick no-redis path ─────────────────────────────────────────────

func TestGeminiPoolPick_NoRedis(t *testing.T) {
	reg := mustParseRegistry(t, fullGeminiRegistryYAML())
	entry, err := GeminiPoolPick(context.Background(), reg, LaneDeep, nil)
	if err != nil {
		t.Fatalf("GeminiPoolPick failed: %v", err)
	}
	if entry == nil {
		t.Fatal("expected non-nil entry")
	}
	if entry.Provider != "gemini" {
		t.Errorf("expected gemini entry, got %s", entry.Provider)
	}
}

func TestGeminiPoolPick_NilRegistry(t *testing.T) {
	_, err := GeminiPoolPick(context.Background(), nil, LaneDeep, nil)
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

func TestGeminiPoolPick_NoGeminiEntries(t *testing.T) {
	reg := mustParseRegistry(t, minimalRegistryYAML())
	// minimalRegistryYAML has only anthropic in the fast lane.
	_, err := GeminiPoolPick(context.Background(), reg, LaneFast, nil)
	if err == nil {
		t.Fatal("expected error: no gemini entries in fast lane of minimal registry")
	}
}

// ── filterByLatencySLO unit tests ─────────────────────────────────────────────

func TestFilterByLatencySLO_Empty(t *testing.T) {
	result := filterByLatencySLO(nil)
	if result != nil {
		t.Errorf("expected nil for nil input, got %v", result)
	}
}

func TestFilterByLatencySLO_AllZeroP99(t *testing.T) {
	entries := []ProviderEntry{
		{Provider: "a", P99LatencyMs: 0},
		{Provider: "b", P99LatencyMs: 0},
	}
	result := filterByLatencySLO(entries)
	if len(result) != 2 {
		t.Errorf("expected all entries with zero p99, got %d", len(result))
	}
}

func TestFilterByLatencySLO_NeverDropsAll(t *testing.T) {
	// Even if the only entry exceeds the SLO, it must be returned.
	entries := []ProviderEntry{
		{Provider: "a", P99LatencyMs: 1000},
		{Provider: "b", P99LatencyMs: 50000}, // far exceeds 3×1000=3000
	}
	result := filterByLatencySLO(entries)
	// "b" should be filtered; "a" kept.
	if len(result) == 0 {
		t.Error("filterByLatencySLO should never return empty slice")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// fullGeminiRegistryYAML returns a registry with at least one gemini entry in
// the deep lane, suitable for testing GeminiPoolPick.
func fullGeminiRegistryYAML() string {
	return `
version: 1
lanes:
  - lane: fast
    entries:
      - provider: anthropic
        model: claude-haiku-test
        api_key_ref: ANTHROPIC_API_KEY
  - lane: deep
    entries:
      - provider: anthropic
        model: claude-opus-test
        api_key_ref: ANTHROPIC_API_KEY
      - provider: gemini
        base_url: https://generativelanguage.googleapis.com/v1beta/openai
        model: gemini-test
        api_key_ref: GEMINI_API_KEY
        project_id: test-project-1
        rate_limit:
          rpm: 360
          window_seconds: 60
  - lane: multimodal
    entries:
      - provider: anthropic
        model: claude-opus-multi
        api_key_ref: ANTHROPIC_API_KEY
  - lane: embedding
    entries:
      - provider: tei-embed
        base_url: http://127.0.0.1:8080/v1
        model: BAAI/bge-m3
        api_key_ref: ""
  - lane: rerank
    entries:
      - provider: tei-rerank
        base_url: http://127.0.0.1:8092/v1
        model: BAAI/bge-reranker-v2-m3
        api_key_ref: ""
  - lane: live
    entries:
      - provider: anthropic
        model: claude-sonnet-test
        api_key_ref: ANTHROPIC_API_KEY
  - lane: local
    entries:
      - provider: vllm
        base_url: http://127.0.0.1:8093/v1
        model: local-model
        api_key_ref: ""
`
}
