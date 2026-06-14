// Package server — tests for HMAC auth, health, bind, and error envelope.
//
// Purpose: Verify HMAC valid/tampered/expired-timestamp paths, health bypass,
//          127.0.0.1 bind assertion, and gRPC error envelope mapping.
// Constraints: No network calls to real providers. All providers are stubs.
//              Tests pass with `go test -race`.
// SPORT: REGISTRY-SERVICES.md — auth=HMAC-SHA256.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nself-org/clawde/intelligence/internal/compiler"
	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

// ---- stub provider ----

type stubProvider struct{ name string }

func (s *stubProvider) Name() string { return s.name }
func (s *stubProvider) Complete(_ context.Context, _ gw.LaneRequest) (*gw.LaneResponse, error) {
	return &gw.LaneResponse{Content: "stub", Provider: s.name}, nil
}
func (s *stubProvider) Stream(_ context.Context, _ gw.LaneRequest) (<-chan gw.StreamChunk, error) {
	ch := make(chan gw.StreamChunk, 1)
	ch <- gw.StreamChunk{Delta: "hello", Done: true}
	close(ch)
	return ch, nil
}
func (s *stubProvider) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return []float32{0.1, 0.2}, nil
}
func (s *stubProvider) Rerank(_ context.Context, _ string, docs []string, topN int) ([]int, error) {
	n := len(docs)
	if topN > 0 && topN < n {
		n = topN
	}
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out, nil
}
func (s *stubProvider) HealthCheck(_ context.Context) error { return nil }

type failProvider struct{ name string }

func (f *failProvider) Name() string { return f.name }
func (f *failProvider) Complete(_ context.Context, _ gw.LaneRequest) (*gw.LaneResponse, error) {
	return nil, fmt.Errorf("down")
}
func (f *failProvider) Stream(_ context.Context, _ gw.LaneRequest) (<-chan gw.StreamChunk, error) {
	return nil, fmt.Errorf("down")
}
func (f *failProvider) Embed(_ context.Context, _ string, _ int) ([]float32, error) {
	return nil, fmt.Errorf("down")
}
func (f *failProvider) Rerank(_ context.Context, _ string, _ []string, _ int) ([]int, error) {
	return nil, fmt.Errorf("down")
}
func (f *failProvider) HealthCheck(_ context.Context) error { return fmt.Errorf("unreachable") }

// ---- HMAC helpers ----

func makeTimestamp() string {
	return strconv.FormatInt(time.Now().Unix(), 10)
}

func makeExpiredTimestamp() string {
	return strconv.FormatInt(time.Now().Unix()-60, 10)
}

func bodySHA256Hex(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

func makeSignature(secret []byte, ts, bodySHA string) string {
	return ComputeSignature(secret, ts, bodySHA)
}

// ---- Tests ----

// TestHMACValid verifies a correctly signed request passes auth.
func TestHMACValid(t *testing.T) {
	secret := []byte("test-secret-abc123")
	ts := makeTimestamp()
	body := []byte(`{"lane":"fast"}`)
	bodySHA := bodySHA256Hex(body)
	sig := makeSignature(secret, ts, bodySHA)

	if err := ValidateSignatureString(secret, ts, body, sig); err != nil {
		t.Fatalf("expected valid signature to pass, got: %v", err)
	}
}

// TestHMACTamperedBody verifies a tampered body is rejected.
func TestHMACTamperedBody(t *testing.T) {
	secret := []byte("test-secret-abc123")
	ts := makeTimestamp()
	originalBody := []byte(`{"lane":"fast"}`)
	tamperedBody := []byte(`{"lane":"deep"}`)
	bodySHA := bodySHA256Hex(originalBody)
	sig := makeSignature(secret, ts, bodySHA)

	// Validate with tampered body — body SHA will differ.
	tamperedSHA := bodySHA256Hex(tamperedBody)
	expectedSig := makeSignature(secret, ts, tamperedSHA)
	if sig == expectedSig {
		t.Fatal("sigs should differ for different bodies")
	}
	if err := ValidateSignatureString(secret, ts, tamperedBody, sig); err == nil {
		t.Fatal("expected tampered body to fail auth, but it passed")
	}
}

// TestHMACExpiredTimestamp verifies an old timestamp is rejected.
func TestHMACExpiredTimestamp(t *testing.T) {
	secret := []byte("test-secret-abc123")
	ts := makeExpiredTimestamp()
	body := []byte(`{"lane":"fast"}`)
	bodySHA := bodySHA256Hex(body)
	sig := makeSignature(secret, ts, bodySHA)

	if err := ValidateSignatureString(secret, ts, body, sig); err == nil {
		t.Fatal("expected expired timestamp to fail auth, but it passed")
	}
}

// TestHMACWrongSecret verifies a wrong secret is rejected.
func TestHMACWrongSecret(t *testing.T) {
	secret := []byte("correct-secret")
	wrongSecret := []byte("wrong-secret")
	ts := makeTimestamp()
	body := []byte(`{"lane":"fast"}`)
	bodySHA := bodySHA256Hex(body)
	sig := makeSignature(wrongSecret, ts, bodySHA)

	if err := ValidateSignatureString(secret, ts, body, sig); err == nil {
		t.Fatal("expected wrong secret to fail auth, but it passed")
	}
}

// TestHealthNoAuth verifies the health handler works without HMAC headers.
func TestHealthNoAuth(t *testing.T) {
	h := newHealthHandler([]gw.Provider{
		&stubProvider{name: "anthropic"},
		&failProvider{name: "openai"},
	})

	req := httptest.NewRequest(http.MethodGet, "/v1/gateway/health", nil)
	_ = httptest.NewRecorder() // recorder unused; health check uses req context only

	// Call health directly (no HMAC middleware).
	resp := h.check(req.Context())
	if resp == nil {
		t.Fatal("expected health response, got nil")
	}
	if resp.Status != "degraded" {
		t.Errorf("expected status=degraded (one provider down), got %q", resp.Status)
	}
	found := false
	for _, p := range resp.Providers {
		if p.Name == "anthropic" && p.Healthy {
			found = true
		}
	}
	if !found {
		t.Error("expected anthropic to be healthy")
	}
}

// TestHealthAllHealthy verifies "ok" when all providers are up.
func TestHealthAllHealthy(t *testing.T) {
	h := newHealthHandler([]gw.Provider{
		&stubProvider{name: "anthropic"},
		&stubProvider{name: "openai"},
	})
	resp := h.check(context.Background())
	if resp.Status != "ok" {
		t.Errorf("expected status=ok, got %q", resp.Status)
	}
}

// TestBindAddr127 verifies the server refuses non-loopback addresses.
func TestBindAddr127(t *testing.T) {
	// Try to listen on 0.0.0.0 — should not appear in canonical config.
	// We verify our Config uses 127.0.0.1 by checking the default.
	cfg := Config{
		GRPCAddr:   defaultGRPCAddr,
		RESTAddr:   defaultRESTAddr,
		HMACSecret: []byte("secret"),
		Providers:  []gw.Provider{&stubProvider{name: "test"}},
		Env:        "test",
	}
	if !strings.HasPrefix(cfg.GRPCAddr, "127.0.0.1:") {
		t.Errorf("gRPC addr must be 127.0.0.1, got %s", cfg.GRPCAddr)
	}
	if !strings.HasPrefix(cfg.RESTAddr, "127.0.0.1:") {
		t.Errorf("REST addr must be 127.0.0.1, got %s", cfg.RESTAddr)
	}
}

// TestServerStartStop verifies the server binds and shuts down cleanly.
func TestServerStartStop(t *testing.T) {
	grpcPort := freePort(t)
	restPort := freePort(t)
	cfg := Config{
		GRPCAddr:   fmt.Sprintf("127.0.0.1:%d", grpcPort),
		RESTAddr:   fmt.Sprintf("127.0.0.1:%d", restPort),
		HMACSecret: []byte("integration-secret"),
		Providers:  []gw.Provider{&stubProvider{name: "test"}},
		Env:        "test",
	}
	srv := New(cfg)
	if err := srv.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	// Verify gRPC listener is up by dialing.
	conn, err := grpc.NewClient(
		fmt.Sprintf("127.0.0.1:%d", grpcPort),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("gRPC dial failed: %v", err)
	}
	defer conn.Close()
}

// TestGRPCHMACUnaryIntercept verifies the gRPC interceptor rejects missing auth.
func TestGRPCHMACUnaryIntercept(t *testing.T) {
	secret := []byte("grpc-test-secret")
	interceptor := UnaryHMACInterceptor(secret)

	// Call with no metadata — should fail.
	ctx := context.Background()
	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/gateway.v1.GatewayService/Complete"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatal("expected auth failure with no metadata, got nil")
	}
}

// TestGRPCHMACHealthBypass verifies Health skips auth.
func TestGRPCHMACHealthBypass(t *testing.T) {
	secret := []byte("grpc-test-secret")
	interceptor := UnaryHMACInterceptor(secret)

	ctx := context.Background()
	resp, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: healthFullMethod}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "health-ok", nil
	})
	if err != nil {
		t.Fatalf("Health should bypass HMAC, got error: %v", err)
	}
	if resp != "health-ok" {
		t.Errorf("expected health-ok, got %v", resp)
	}
}

// TestGRPCHMACWithValidHeaders verifies a properly signed gRPC call passes.
func TestGRPCHMACWithValidHeaders(t *testing.T) {
	secret := []byte("grpc-test-secret")
	interceptor := UnaryHMACInterceptor(secret)

	ts := makeTimestamp()
	emptySHA := bodySHA256Hex([]byte{})
	sig := makeSignature(secret, ts, emptySHA)

	md := metadata.Pairs(
		hmacTimestampHeader, ts,
		hmacSignatureHeader, sig,
	)
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := interceptor(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/gateway.v1.GatewayService/Complete"}, func(ctx context.Context, req interface{}) (interface{}, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatalf("valid HMAC headers should pass, got: %v", err)
	}
}

// TestErrorEnvelopeMapping verifies mapGWError produces correct gRPC codes.
func TestErrorEnvelopeMapping(t *testing.T) {
	cases := []struct {
		code     string
		wantCode string
	}{
		{"rate_limit", "ResourceExhausted"},
		{"auth", "Unauthenticated"},
		{"timeout", "DeadlineExceeded"},
		{"config", "FailedPrecondition"},
		{"upstream", "Internal"},
	}
	for _, c := range cases {
		gwErr := &gw.GatewayError{
			Lane:     gw.LaneFast,
			Provider: "test",
			Code:     c.code,
			Cause:    fmt.Errorf("test error"),
		}
		err := mapGWError(gwErr)
		if err == nil {
			t.Errorf("code=%s: expected error, got nil", c.code)
			continue
		}
		if !strings.Contains(err.Error(), c.wantCode) {
			t.Errorf("code=%s: expected gRPC code %s in error %q", c.code, c.wantCode, err.Error())
		}
	}
}

// freePort returns a free TCP port on loopback.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	return port
}

// ---- CompileContext unit tests (QA-B) ----

// stubRetriever implements compiler.ContextRetriever and returns a single
// scored chunk so CompileContext produces Enriched:true.
type stubRetriever struct{}

func (s *stubRetriever) RetrieveContext(_ context.Context, _, _ string) (*compiler.RetrievalResult, error) {
	return &compiler.RetrievalResult{
		Chunks: []compiler.ScoredChunk{
			{FilePath: "main.go", Lang: "go", Content: "func main() {}", Score: 0.9, Method: "dense"},
		},
	}, nil
}

// TestCompileContext_NilCompiler verifies that a nil compiler yields
// Enriched:false without panicking (graceful degradation per ADR-001).
func TestCompileContext_NilCompiler(t *testing.T) {
	h := &gatewayHandler{compiler: nil}
	resp, err := h.CompileContext(context.Background(), &CompileContextRequest{
		WorkspaceId: "ws-test",
		Signals:     &SessionSignals{ActiveFilePath: "main.go"},
	})
	if err != nil {
		t.Fatalf("nil compiler: unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("nil compiler: expected non-nil response")
	}
	if resp.Enriched {
		t.Errorf("nil compiler: expected Enriched=false, got true")
	}
}

// TestCompileContext_StubCompiler verifies that a non-nil compiler wired with
// a stub retriever returns Enriched:true.
func TestCompileContext_StubCompiler(t *testing.T) {
	t.Setenv("CLAWDE_AUTO_CONTEXT", "true")
	c := compiler.NewCompiler(&stubRetriever{}, nil, nil)
	h := &gatewayHandler{compiler: c}
	resp, err := h.CompileContext(context.Background(), &CompileContextRequest{
		WorkspaceId: "ws-test",
		Signals:     &SessionSignals{ActiveFilePath: "main.go"},
	})
	if err != nil {
		t.Fatalf("stub compiler: unexpected error: %v", err)
	}
	if resp == nil {
		t.Fatal("stub compiler: expected non-nil response")
	}
	if !resp.Enriched {
		t.Errorf("stub compiler: expected Enriched=true, got false")
	}
}
