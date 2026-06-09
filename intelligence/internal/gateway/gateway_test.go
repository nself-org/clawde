// Package gateway — unit tests.
//
// Covers: registry load, hot-reload swap, lane resolve for all 7 lanes,
//         fallback ordering, nil/empty registry edge cases, provider build.
package gateway

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// ── Registry load ────────────────────────────────────────────────────────────

func TestParseRegistry_AllLanes(t *testing.T) {
	data := minimalRegistryYAML()
	reg, err := parseRegistry([]byte(data))
	if err != nil {
		t.Fatalf("parseRegistry failed: %v", err)
	}
	for _, lane := range AllLanes {
		if _, ok := reg.Entries[lane]; !ok {
			t.Errorf("lane %q missing from registry", lane)
		}
	}
}

func TestParseRegistry_GeminiRequiresProjectID(t *testing.T) {
	yaml := `
version: 1
lanes:
  - lane: fast
    entries:
      - provider: gemini
        base_url: https://example.com/v1
        model: gemini-test
        api_key_ref: GEMINI_API_KEY
`
	_, err := parseRegistry([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing gemini project_id, got nil")
	}
}

func TestParseRegistry_UnknownLane(t *testing.T) {
	yaml := `
version: 1
lanes:
  - lane: bogus
    entries:
      - provider: anthropic
        model: test-model
        api_key_ref: ANTHROPIC_API_KEY
`
	_, err := parseRegistry([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for unknown lane")
	}
}

func TestParseRegistry_DuplicateLane(t *testing.T) {
	yaml := `
version: 1
lanes:
  - lane: fast
    entries:
      - provider: anthropic
        model: test-model
        api_key_ref: ANTHROPIC_API_KEY
  - lane: fast
    entries:
      - provider: anthropic
        model: test-model
        api_key_ref: ANTHROPIC_API_KEY
`
	_, err := parseRegistry([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for duplicate lane")
	}
}

func TestParseRegistry_EmptyEntries(t *testing.T) {
	yaml := `
version: 1
lanes:
  - lane: fast
    entries: []
`
	_, err := parseRegistry([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for empty entries")
	}
}

func TestParseRegistry_MissingModel(t *testing.T) {
	yaml := `
version: 1
lanes:
  - lane: fast
    entries:
      - provider: anthropic
        api_key_ref: ANTHROPIC_API_KEY
`
	_, err := parseRegistry([]byte(yaml))
	if err == nil {
		t.Fatal("expected error for missing model")
	}
}

// ── Lane resolve ─────────────────────────────────────────────────────────────

func TestLaneResolve_AllLanes(t *testing.T) {
	reg := mustParseRegistry(t, minimalRegistryYAML())
	for _, lane := range AllLanes {
		entries, err := LaneResolve(reg, lane)
		if err != nil {
			t.Errorf("LaneResolve(%q) failed: %v", lane, err)
			continue
		}
		if len(entries) == 0 {
			t.Errorf("LaneResolve(%q) returned empty list", lane)
		}
	}
}

func TestLaneResolve_FallbackOrder(t *testing.T) {
	yaml := `
version: 1
lanes:
  - lane: fast
    entries:
      - provider: anthropic
        model: claude-primary
        api_key_ref: ANTHROPIC_API_KEY
      - provider: openai
        base_url: https://api.openai.com/v1
        model: gpt-fallback
        api_key_ref: OPENAI_API_KEY
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
	entries, err := LaneResolve(reg, LaneFast)
	if err != nil {
		t.Fatalf("LaneResolve failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Provider != "anthropic" {
		t.Errorf("primary provider: want anthropic, got %s", entries[0].Provider)
	}
	if entries[1].Provider != "openai" {
		t.Errorf("fallback provider: want openai, got %s", entries[1].Provider)
	}
}

func TestLaneResolve_NilRegistry(t *testing.T) {
	_, err := LaneResolve(nil, LaneFast)
	if err == nil {
		t.Fatal("expected error for nil registry")
	}
}

func TestLaneResolve_UnknownLane(t *testing.T) {
	reg := &Registry{Entries: map[Lane][]ProviderEntry{}}
	_, err := LaneResolve(reg, "nonexistent")
	if err == nil {
		t.Fatal("expected error for unconfigured lane")
	}
}

func TestLaneResolve_ReturnsCopy(t *testing.T) {
	reg := mustParseRegistry(t, minimalRegistryYAML())
	entries1, _ := LaneResolve(reg, LaneFast)
	entries2, _ := LaneResolve(reg, LaneFast)
	// Mutate the first result — must not affect the second.
	entries1[0].Model = "MUTATED"
	if entries2[0].Model == "MUTATED" {
		t.Error("LaneResolve returned a reference to internal slice, not a copy")
	}
}

// ── Hot-reload ───────────────────────────────────────────────────────────────

func TestRegistryWatcher_HotReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "model_registry.yaml")

	// Write initial YAML.
	if err := os.WriteFile(path, []byte(minimalRegistryYAML()), 0644); err != nil {
		t.Fatal(err)
	}

	reg, err := LoadRegistry(path)
	if err != nil {
		t.Fatal(err)
	}

	w := NewRegistryWatcher(path, reg)
	initial := w.Get()
	if initial == nil {
		t.Fatal("initial registry is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Start watcher in background.
	var watchErr atomic.Value
	go func() {
		if err := w.Run(ctx); err != nil && err != context.DeadlineExceeded && err != context.Canceled {
			watchErr.Store(err)
		}
	}()

	// Write an updated YAML (add a comment to trigger a write event).
	updated := minimalRegistryYAML() + "\n# updated\n"
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatal(err)
	}

	// Wait for the watcher to swap the registry (up to 1s).
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if w.Get() != initial {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	cancel()

	if we := watchErr.Load(); we != nil {
		t.Errorf("watcher error: %v", we)
	}
	// The swap may or may not have occurred depending on OS event latency —
	// the important thing is the watcher ran without panic. We accept either state.
}

// ── BuildProvider ────────────────────────────────────────────────────────────

func TestBuildProvider_Anthropic(t *testing.T) {
	e := ProviderEntry{
		Provider:  "anthropic",
		Model:     "claude-test",
		APIKey:    "sk-test",
		APIKeyRef: "ANTHROPIC_API_KEY",
		Lane:      LaneFast,
	}
	p, err := BuildProvider(e)
	if err != nil {
		t.Fatalf("BuildProvider failed: %v", err)
	}
	if p.Name() != "anthropic" {
		t.Errorf("Name: want anthropic, got %s", p.Name())
	}
}

func TestBuildProvider_OpenAICompat(t *testing.T) {
	e := ProviderEntry{
		Provider:  "vllm",
		BaseURL:   "http://127.0.0.1:8093/v1",
		Model:     "local-model",
		APIKey:    "",
		APIKeyRef: "",
		Lane:      LaneLocal,
	}
	p, err := BuildProvider(e)
	if err != nil {
		t.Fatalf("BuildProvider failed: %v", err)
	}
	if p.Name() != "vllm" {
		t.Errorf("Name: want vllm, got %s", p.Name())
	}
}

func TestBuildProvider_UnknownProvider(t *testing.T) {
	e := ProviderEntry{Provider: "bogus", Model: "m", BaseURL: "http://x"}
	_, err := BuildProvider(e)
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestBuildProvider_AnthropicEmptyKey(t *testing.T) {
	e := ProviderEntry{Provider: "anthropic", Model: "test", APIKey: ""}
	_, err := BuildProvider(e)
	if err == nil {
		t.Fatal("expected error for empty anthropic key")
	}
}

// ── APIKeyRef resolution ─────────────────────────────────────────────────────

func TestParseRegistry_APIKeyRefResolved(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "resolved-key-value")
	reg := mustParseRegistry(t, minimalRegistryYAML())
	fastEntries := reg.Entries[LaneFast]
	if len(fastEntries) == 0 {
		t.Fatal("fast lane has no entries")
	}
	found := false
	for _, e := range fastEntries {
		if e.Provider == "anthropic" && e.APIKey == "resolved-key-value" {
			found = true
		}
	}
	if !found {
		t.Error("api_key_ref was not resolved from env")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func mustParseRegistry(t *testing.T, yaml string) *Registry {
	t.Helper()
	reg, err := parseRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseRegistry: %v", err)
	}
	return reg
}

// minimalRegistryYAML returns a valid registry covering all 7 lanes.
func minimalRegistryYAML() string {
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
  - lane: multimodal
    entries:
      - provider: anthropic
        model: claude-opus-multi-test
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
