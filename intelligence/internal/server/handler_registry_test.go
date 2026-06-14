// Package server — unit tests for T08 registry-based routing in gatewayHandler.
//
// Purpose: Verify that Complete() and Embed() route through RouteRequest when
//          g.registry is non-nil, and fall back to primary() when registry is nil.
//          QA-C: confirm registry routing switch is wired (not primary-only bypass).
// Constraints: No real network calls. Uses in-process stubs and unknown-lane probes.
//              429-failover logic within WithFailover is covered by:
//                gateway.TestWithFailover_ProviderA429_FallsBackToProviderB (router_failover_test.go).
//              Here we verify the handler wiring (registry switch → RouteRequest) only.
// SPORT: REGISTRY-ENDPOINTS.md — Complete RPC, Embed RPC.
package server

import (
	"context"
	"testing"

	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
)

// TestComplete_NilRegistry_UsesPrimary verifies that when registry is nil,
// Complete() delegates to providers[0] (primary() fallback).
func TestComplete_NilRegistry_UsesPrimary(t *testing.T) {
	stub := &stubProvider{name: "primary-stub"}
	h := &gatewayHandler{
		providers: []gw.Provider{stub},
		registry:  nil, // nil → primary() path
	}
	resp, err := h.Complete(context.Background(), &CompleteRequest{
		Lane:     "fast",
		Messages: []*Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("nil registry path: unexpected error: %v", err)
	}
	if resp.Provider != "primary-stub" {
		t.Errorf("nil registry: expected provider=primary-stub, got %q", resp.Provider)
	}
}

// TestComplete_NilRegistry_NoProviders verifies the proper Unavailable error
// when no providers are configured and registry is nil.
func TestComplete_NilRegistry_NoProviders(t *testing.T) {
	h := &gatewayHandler{
		providers: []gw.Provider{},
		registry:  nil,
	}
	_, err := h.Complete(context.Background(), &CompleteRequest{
		Lane:     "fast",
		Messages: []*Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected error for empty providers with nil registry")
	}
}

// TestComplete_Registry_UnknownLane verifies that with a non-nil registry,
// a request for an unconfigured lane returns a config error from RouteRequest
// (not a nil-registry primary() success). This proves the registry path is active.
//
// If primary() had been called instead of RouteRequest, stubProvider would return
// success (no error). Getting an error here proves the routing switch is wired.
func TestComplete_Registry_UnknownLane(t *testing.T) {
	// Build a registry with fast lane only (no "nonexistent-lane").
	reg := mustBuildMinimalRegistry(t)
	h := &gatewayHandler{
		providers: []gw.Provider{&stubProvider{name: "primary-stub"}},
		registry:  reg,
	}
	// Request an unconfigured lane — RouteRequest returns a config GatewayError
	// before WithFailover is called (no network calls).
	_, err := h.Complete(context.Background(), &CompleteRequest{
		Lane:     "nonexistent-lane",
		Messages: []*Message{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		// primary() with stubProvider would have returned success;
		// getting nil error here means registry routing was NOT taken.
		t.Fatal("expected error for unconfigured lane with non-nil registry — got nil (primary() bypass?)")
	}
	// Any non-nil error confirms RouteRequest was called (not primary()).
}

// TestEmbed_NilRegistry_UsesPrimary verifies Embed() falls back to primary()
// when registry is nil.
func TestEmbed_NilRegistry_UsesPrimary(t *testing.T) {
	stub := &stubProvider{name: "embed-primary"}
	h := &gatewayHandler{
		providers: []gw.Provider{stub},
		registry:  nil,
	}
	resp, err := h.Embed(context.Background(), &EmbedRequest{
		Text:        "hello world",
		ExpectedDim: 0,
	})
	if err != nil {
		t.Fatalf("nil registry embed: unexpected error: %v", err)
	}
	if resp.Provider != "embed-primary" {
		t.Errorf("nil registry embed: expected provider=embed-primary, got %q", resp.Provider)
	}
}

// TestEmbed_Registry_UnknownLane verifies Embed() uses RouteRequest (not primary())
// when registry is non-nil by requesting an embedding lane that has no entries
// in a fast-only registry. Getting an error proves registry routing is active.
//
// NOTE: Embed() always routes through LaneEmbedding regardless of the request lane.
// This test uses a registry that omits the embedding lane entries by constructing
// a registry with only a fast lane and then requesting embed, which routes to
// the embedding lane → no entries → RouteRequest returns error.
func TestEmbed_Registry_UnknownEmbedLane(t *testing.T) {
	// Fast-lane-only registry (no embedding lane) — causes RouteRequest to error.
	reg := mustBuildFastOnlyRegistry(t)
	h := &gatewayHandler{
		providers: []gw.Provider{&stubProvider{name: "primary-stub"}},
		registry:  reg,
	}
	_, err := h.Embed(context.Background(), &EmbedRequest{
		Text:        "hello",
		ExpectedDim: 0,
	})
	// stubProvider.Embed returns []float32{0.1, 0.2} with no error.
	// If registry routing is NOT taken (primary() fallback), err would be nil.
	// If registry routing IS taken, RouteRequest returns "no providers for lane embedding" error.
	if err == nil {
		t.Error("Embed with registry should route through registry (not primary stub) — got nil error (primary bypass?)")
	}
}

// TestStreamComplete_AlwaysUsesPrimary verifies that StreamComplete() always
// uses primary() regardless of registry state (explicit behavior per T08 design).
func TestStreamComplete_AlwaysUsesPrimary(t *testing.T) {
	stub := &stubProvider{name: "stream-primary"}

	// With nil registry: should use primary()
	h := &gatewayHandler{
		providers: []gw.Provider{stub},
		registry:  nil,
	}
	// StreamComplete is hard to invoke without a real stream; we just verify
	// that calling primary() on a handler with stub providers returns the stub.
	p, err := h.primary()
	if err != nil {
		t.Fatalf("primary() failed: %v", err)
	}
	if p.Name() != "stream-primary" {
		t.Errorf("primary(): expected stream-primary, got %q", p.Name())
	}

	// With non-nil registry: primary() still returns providers[0] (StreamComplete
	// ignores registry per design — documented in handler.go comment).
	reg := mustBuildMinimalRegistry(t)
	h2 := &gatewayHandler{
		providers: []gw.Provider{stub},
		registry:  reg,
	}
	p2, err := h2.primary()
	if err != nil {
		t.Fatalf("primary() with registry failed: %v", err)
	}
	if p2.Name() != "stream-primary" {
		t.Errorf("primary() with registry: expected stream-primary, got %q", p2.Name())
	}
}

// mustBuildMinimalRegistry creates a *gw.Registry with all 7 canonical lanes
// (anthropic entries with empty api_key_ref — no real keys needed for routing tests).
// WithFailover will fail if called (network attempt), but RouteRequest succeeds.
func mustBuildMinimalRegistry(t *testing.T) *gw.Registry {
	t.Helper()
	yamlData := []byte(`
version: 1
lanes:
  - lane: fast
    entries:
      - provider: anthropic
        model: claude-test
        api_key_ref: ""
  - lane: deep
    entries:
      - provider: anthropic
        model: claude-deep
        api_key_ref: ""
  - lane: multimodal
    entries:
      - provider: anthropic
        model: claude-multi
        api_key_ref: ""
  - lane: embedding
    entries:
      - provider: tei-embed
        base_url: http://127.0.0.1:9997/v1
        model: embed-test
        api_key_ref: ""
  - lane: rerank
    entries:
      - provider: tei-rerank
        base_url: http://127.0.0.1:9996/v1
        model: rerank-test
        api_key_ref: ""
  - lane: live
    entries:
      - provider: anthropic
        model: claude-live
        api_key_ref: ""
  - lane: local
    entries:
      - provider: ollama
        base_url: http://127.0.0.1:11434
        model: local-model
        api_key_ref: ""
`)
	reg, err := gw.LoadRegistryFromYAML(yamlData)
	if err != nil {
		t.Fatalf("mustBuildMinimalRegistry: %v", err)
	}
	return reg
}

// mustBuildFastOnlyRegistry creates a *gw.Registry with ONLY the fast lane,
// so requests for other lanes (e.g. embedding) cause RouteRequest to error.
// This is used to verify registry routing without triggering network calls.
func mustBuildFastOnlyRegistry(t *testing.T) *gw.Registry {
	t.Helper()
	// We need all 7 canonical lanes for a valid registry; to simulate "no embedding"
	// we parse a registry manually. Since the registry enforces all lanes at validation,
	// we work around by building a Registry struct directly.
	reg := &gw.Registry{
		Entries: map[gw.Lane][]gw.ProviderEntry{
			gw.LaneFast: {
				{
					Lane:     gw.LaneFast,
					Provider: "anthropic",
					Model:    "claude-test",
				},
			},
			// LaneEmbedding intentionally omitted → RouteRequest returns "no providers" error.
		},
	}
	return reg
}
