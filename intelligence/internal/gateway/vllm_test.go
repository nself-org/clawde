// Package gateway — tests for vLLM provider and M6 binding guard.
package gateway

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── ValidateVLLMHost table tests ─────────────────────────────────────────────

func TestValidateVLLMHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		host    string
		wantErr bool
		errSnip string // substring expected in error when wantErr=true
	}{
		// pass cases
		{
			name:    "bare loopback IPv4",
			host:    "127.0.0.1",
			wantErr: false,
		},
		{
			name:    "loopback with port",
			host:    "127.0.0.1:8093",
			wantErr: false,
		},
		{
			name:    "full URL loopback",
			host:    "http://127.0.0.1:8093/v1",
			wantErr: false,
		},
		{
			name:    "IPv6 loopback ::1",
			host:    "::1",
			wantErr: false,
		},
		{
			name:    "IPv6 loopback with port",
			host:    "[::1]:8093",
			wantErr: false,
		},
		{
			name:    "localhost resolves to loopback",
			host:    "localhost",
			wantErr: false,
		},
		// fail cases
		{
			name:    "all-zeros 0.0.0.0 is not loopback",
			host:    "0.0.0.0",
			wantErr: true,
			errSnip: "M6 violation",
		},
		{
			name:    "private LAN address is not loopback",
			host:    "192.168.1.100",
			wantErr: true,
			errSnip: "M6 violation",
		},
		{
			name:    "public IP is not loopback",
			host:    "8.8.8.8",
			wantErr: true,
			errSnip: "M6 violation",
		},
		{
			name:    "0.0.0.0 with port is not loopback",
			host:    "0.0.0.0:8093",
			wantErr: true,
			errSnip: "M6 violation",
		},
		{
			name:    "full URL with non-loopback host",
			host:    "http://192.168.1.100:8093/v1",
			wantErr: true,
			errSnip: "M6 violation",
		},
		{
			name:    "empty host",
			host:    "",
			wantErr: true,
			errSnip: "M6 violation",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateVLLMHost(tc.host)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ValidateVLLMHost(%q): expected error containing %q, got nil", tc.host, tc.errSnip)
				}
				if tc.errSnip != "" && !strings.Contains(err.Error(), tc.errSnip) {
					t.Fatalf("ValidateVLLMHost(%q): error %q does not contain %q", tc.host, err.Error(), tc.errSnip)
				}
			} else {
				if err != nil {
					t.Fatalf("ValidateVLLMHost(%q): unexpected error: %v", tc.host, err)
				}
			}
		})
	}
}

// ── VLLMProvider construction and embedding tests ────────────────────────────

func TestNewVLLMProvider_EmbeddedCompat(t *testing.T) {
	t.Parallel()
	// Construct with a valid loopback host and dummy model — must not panic or error.
	p, err := NewVLLMProvider("http://127.0.0.1:8093", "", "test-model")
	if err != nil {
		t.Fatalf("NewVLLMProvider: unexpected error: %v", err)
	}
	if p == nil {
		t.Fatal("NewVLLMProvider: returned nil provider")
	}
	if p.Name() != "vllm" {
		t.Errorf("Name() = %q; want %q", p.Name(), "vllm")
	}
	if p.compat == nil {
		t.Error("compat provider is nil — delegation will fail")
	}
}

func TestNewVLLMProvider_EmptyModel(t *testing.T) {
	t.Parallel()
	_, err := NewVLLMProvider("http://127.0.0.1:8093", "", "")
	if err == nil {
		t.Fatal("expected error for empty model, got nil")
	}
}

// TestNewVLLMProvider_PanicOnNonLoopback verifies that constructing a VLLMProvider
// with a non-loopback host panics (startup misconfiguration).
func TestNewVLLMProvider_PanicOnNonLoopback(t *testing.T) {
	t.Parallel()
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic for non-loopback host, got none")
		}
	}()
	// This must panic — 0.0.0.0 is a non-loopback address.
	_, _ = NewVLLMProvider("http://0.0.0.0:8093", "", "some-model") //nolint:errcheck
}

// ── Provider interface delegation tests ──────────────────────────────────────

// startFakeVLLM starts a local HTTP test server that mimics a vLLM /v1 endpoint.
// Returns (base_url, cleanup_fn).
func startFakeVLLM(t *testing.T) (string, func()) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"object":"list","data":[{"id":"test-model","object":"model"}]}`)
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":5,"completion_tokens":3}}`)
	})
	srv := httptest.NewServer(mux)
	return srv.URL, srv.Close
}

func TestVLLMProvider_CompleteAndDelegate(t *testing.T) {
	t.Parallel()
	base, cleanup := startFakeVLLM(t)
	defer cleanup()

	// httptest.NewServer binds to 127.0.0.1 — passes M6 guard.
	p, err := NewVLLMProvider(base, "", "test-model")
	if err != nil {
		t.Fatalf("NewVLLMProvider: %v", err)
	}
	resp, err := p.Complete(context.Background(), LaneRequest{
		Lane:     LaneDeep,
		Messages: []Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello" {
		t.Errorf("Content = %q; want %q", resp.Content, "hello")
	}
	if resp.Provider != "vllm" {
		t.Errorf("Provider = %q; want %q", resp.Provider, "vllm")
	}
}

func TestVLLMProvider_HealthCheck_Available(t *testing.T) {
	t.Parallel()
	base, cleanup := startFakeVLLM(t)
	defer cleanup()

	p, err := NewVLLMProvider(base, "", "test-model")
	if err != nil {
		t.Fatalf("NewVLLMProvider: %v", err)
	}
	if err := p.HealthCheck(context.Background()); err != nil {
		t.Errorf("HealthCheck: unexpected error: %v", err)
	}
}

func TestVLLMProvider_HealthCheck_Unavailable(t *testing.T) {
	t.Parallel()
	// Use a closed server — connection refused should map to ErrUnavailable.
	p, err := NewVLLMProvider("http://127.0.0.1:19993", "", "test-model")
	if err != nil {
		t.Fatalf("NewVLLMProvider: %v", err)
	}
	err = p.HealthCheck(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
}

// ── DEEP/FAST lane registration tests ────────────────────────────────────────

// TestRegistryDeepLaneVLLMPrimary verifies vLLM is registered as the primary (position 0)
// provider in the DEEP lane by loading the real model_registry.yaml.
func TestRegistryDeepLaneVLLMPrimary(t *testing.T) {
	t.Parallel()
	reg, err := LoadRegistry("model_registry.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	entries, err := LaneResolve(reg, LaneDeep)
	if err != nil {
		t.Fatalf("LaneResolve(deep): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("DEEP lane has no entries")
	}
	if entries[0].Provider != "vllm" {
		t.Errorf("DEEP lane primary provider = %q; want %q", entries[0].Provider, "vllm")
	}
}

// TestRegistryFastLaneVLLMFallback verifies vLLM is at fallback position 3 (index 2)
// in the FAST lane by loading the real model_registry.yaml.
func TestRegistryFastLaneVLLMFallback(t *testing.T) {
	t.Parallel()
	reg, err := LoadRegistry("model_registry.yaml")
	if err != nil {
		t.Fatalf("LoadRegistry: %v", err)
	}
	entries, err := LaneResolve(reg, LaneFast)
	if err != nil {
		t.Fatalf("LaneResolve(fast): %v", err)
	}
	if len(entries) < 3 {
		t.Fatalf("FAST lane has only %d entries; want at least 3", len(entries))
	}
	if entries[2].Provider != "vllm" {
		t.Errorf("FAST lane fallback position 3 (index 2) = %q; want %q", entries[2].Provider, "vllm")
	}
}

// ── Integration test (skipped when VLLM_HOST is not set) ─────────────────────

func TestVLLMProvider_IntegrationSkipsWhenUnset(t *testing.T) {
	host := "http://127.0.0.1:8093"
	// Attempt a real health check; skip cleanly if the server isn't running.
	p, err := NewVLLMProvider(host, "", "test-model")
	if err != nil {
		t.Skipf("skipping integration test — NewVLLMProvider error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), vllmHealthTimeout)
	defer cancel()
	if err := p.HealthCheck(ctx); err != nil {
		t.Skipf("skipping integration test — vLLM not available at %s: %v", host, err)
	}
	// If we reach here the server is live — do a real completion call.
	resp, err := p.Complete(ctx, LaneRequest{
		Lane:     LaneDeep,
		Messages: []Message{{Role: "user", Content: "say hello"}},
	})
	if err != nil {
		t.Errorf("Complete on live vLLM: %v", err)
	} else if resp.Content == "" {
		t.Error("Complete on live vLLM returned empty content")
	}
}
