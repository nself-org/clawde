// Package gateway — tests for the Ollama LOCAL inference lane provider.
//
// Covers: OpenAI-compat request shape to Ollama, model-pull-on-absent, graceful
// degradation on connection refused, and LOCAL-lane → ollama provider resolution.
// Tests that require a live Ollama daemon are skipped with reason.
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// newMockOllama builds an httptest server that emulates the Ollama daemon:
//   - GET  /api/tags             → installed model list (configurable)
//   - POST /api/pull             → streaming NDJSON progress, records call count
//   - POST /v1/chat/completions  → OpenAI-compat response, captures the request body
func newMockOllama(t *testing.T, installed []string, capturedBody *[]byte, pullCalls *int32) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()

	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, r *http.Request) {
		models := make([]map[string]string, 0, len(installed))
		for _, n := range installed {
			models = append(models, map[string]string{"name": n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	})

	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(pullCalls, 1)
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, `{"status":"pulling manifest"}`+"\n")
		_, _ = io.WriteString(w, `{"status":"downloading"}`+"\n")
		_, _ = io.WriteString(w, `{"status":"success"}`+"\n")
	})

	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if capturedBody != nil {
			*capturedBody = b
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"content": "ok"}},
			},
			"usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 1},
		})
	})

	return httptest.NewServer(mux)
}

// TestOllama_OpenAICompatRequestShape verifies inference uses /v1/chat/completions
// (per LEDGER §G) with the bare model name (prefix stripped).
func TestOllama_OpenAICompatRequestShape(t *testing.T) {
	var body []byte
	var pulls int32
	srv := newMockOllama(t, []string{"llama3.2:latest"}, &body, &pulls)
	defer srv.Close()

	p, err := NewOllamaProvider(srv.URL, "ollama/llama3.2")
	if err != nil {
		t.Fatalf("NewOllamaProvider: %v", err)
	}

	resp, err := p.Complete(context.Background(), LaneRequest{
		Lane:     LaneLocal,
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("content = %q, want ok", resp.Content)
	}

	var sent map[string]any
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("request body not JSON: %v", err)
	}
	if sent["model"] != "llama3.2" {
		t.Errorf("model = %v, want bare 'llama3.2' (prefix must be stripped)", sent["model"])
	}
	if _, ok := sent["messages"]; !ok {
		t.Error("OpenAI-compat body missing 'messages'")
	}
	// Model already installed → no pull.
	if pulls != 0 {
		t.Errorf("pull called %d times for an installed model, want 0", pulls)
	}
}

// TestOllama_PullOnAbsent verifies that an absent model triggers GET /api/tags
// then POST /api/pull before the completion proceeds.
func TestOllama_PullOnAbsent(t *testing.T) {
	var pulls int32
	srv := newMockOllama(t, []string{}, nil, &pulls) // empty tags → model absent
	defer srv.Close()

	p, err := NewOllamaProvider(srv.URL, "ollama/llama3.2")
	if err != nil {
		t.Fatalf("NewOllamaProvider: %v", err)
	}

	if _, err := p.Complete(context.Background(), LaneRequest{
		Lane:     LaneLocal,
		Messages: []Message{{Role: "user", Content: "hi"}},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if pulls != 1 {
		t.Fatalf("pull called %d times, want exactly 1 for an absent model", pulls)
	}

	// Second call must NOT pull again (sync.Once).
	if _, err := p.Complete(context.Background(), LaneRequest{
		Lane:     LaneLocal,
		Messages: []Message{{Role: "user", Content: "again"}},
	}); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if pulls != 1 {
		t.Errorf("pull called %d times after two requests, want 1 (sync.Once)", pulls)
	}
}

// TestOllama_GracefulDegrade verifies a daemon that is down (connection refused)
// surfaces as ErrUnavailable rather than a panic or opaque error.
func TestOllama_GracefulDegrade(t *testing.T) {
	// Point at a closed port: start then immediately close a server.
	srv := newMockOllama(t, []string{}, nil, new(int32))
	addr := srv.URL
	srv.Close() // now connections are refused

	p, err := NewOllamaProvider(addr, "ollama/llama3.2")
	if err != nil {
		t.Fatalf("NewOllamaProvider: %v", err)
	}

	_, err = p.Complete(context.Background(), LaneRequest{
		Lane:     LaneLocal,
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error when daemon is down, got nil")
	}
	if !strings.Contains(err.Error(), ErrUnavailable.Error()) {
		t.Errorf("error = %v, want wrapped ErrUnavailable", err)
	}

	// HealthCheck should likewise report unavailable, not panic.
	if hcErr := p.HealthCheck(context.Background()); hcErr == nil {
		t.Error("HealthCheck on down daemon returned nil, want ErrUnavailable")
	}
}

// TestOllama_LocalLaneResolvesToOllama verifies the LOCAL lane in the registry
// can build an *OllamaProvider via the resolver.
func TestOllama_LocalLaneResolvesToOllama(t *testing.T) {
	reg, err := LoadRegistry("model_registry.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	entries, err := LaneResolve(reg, LaneLocal)
	if err != nil {
		t.Fatalf("LaneResolve(local): %v", err)
	}

	var ollamaEntry *ProviderEntry
	for i := range entries {
		if entries[i].Provider == "ollama" {
			ollamaEntry = &entries[i]
			break
		}
	}
	if ollamaEntry == nil {
		t.Fatal("no ollama entry in LOCAL lane")
	}
	if !strings.HasPrefix(ollamaEntry.Model, "ollama/") {
		t.Errorf("registry model = %q, want 'ollama/' prefix", ollamaEntry.Model)
	}

	prov, err := BuildProvider(*ollamaEntry)
	if err != nil {
		t.Fatalf("BuildProvider(ollama): %v", err)
	}
	if _, ok := prov.(*OllamaProvider); !ok {
		t.Errorf("BuildProvider returned %T, want *OllamaProvider", prov)
	}
	if prov.Name() != "ollama" {
		t.Errorf("Name() = %q, want ollama", prov.Name())
	}
}

// TestOllama_LiveDaemon is a live integration test, skipped unless an Ollama
// daemon is actually running. It is opt-in only.
func TestOllama_LiveDaemon(t *testing.T) {
	p, err := NewOllamaProvider("", "ollama/llama3.2")
	if err != nil {
		t.Fatalf("NewOllamaProvider: %v", err)
	}
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Skipf("skipping: live Ollama daemon not reachable (%v)", err)
	}
	// If we got here a daemon is up; a real completion would pull a large model,
	// so we only assert reachability here.
}
