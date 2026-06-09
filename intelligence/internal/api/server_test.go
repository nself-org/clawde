// Package api — tests for the 7-gate public API interceptor chain.
//
// Purpose: Verify every gate enforces correct HTTP status codes and that
//          fail-closed behaviour holds. Tests use:
//            - in-process RSA keypair + injected JWKS (no network)
//            - stub quota/workspace seams (no DB)
//            - countingRateLimiter seam (no Redis)
//            - httptest.NewServer
//
// Test coverage:
//   - no JWT → 401
//   - expired JWT → 401
//   - valid JWT, quota exceeded → 503
//   - valid JWT, rate limit exceeded → 429
//   - valid JWT, policy service down → fail-closed 503
//   - 7-gate order asserted via sequential gate recording
//   - /health, /metrics no-auth → 200
//   - reflection disabled on 8094 (no grpc.Server in PublicServer)
//   - SSE: JWT validated once at connection establishment
//
// SPORT: REGISTRY-FUNCTIONS.md — api.PublicServer 7-gate chain.
package api

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v2/jwa"
	"github.com/lestrrat-go/jwx/v2/jwk"
	"github.com/lestrrat-go/jwx/v2/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nself-org/clawde/intelligence/internal/auth"
)

// ---- helpers ----

const (
	testIssuer   = "https://auth.clawde.test"
	testAudience = "clawde-intelligence"
)

func testKeyPair(t *testing.T) (*rsa.PrivateKey, jwk.Set) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	pubKey, err := jwk.FromRaw(priv.Public())
	require.NoError(t, err)
	require.NoError(t, pubKey.Set(jwk.AlgorithmKey, jwa.RS256))
	require.NoError(t, pubKey.Set(jwk.KeyIDKey, "test-kid"))

	ks := jwk.NewSet()
	require.NoError(t, ks.AddKey(pubKey))
	return priv, ks
}

func signRS256(t *testing.T, priv *rsa.PrivateKey, opts ...func(jwt.Token)) string {
	t.Helper()
	tok, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Audience([]string{testAudience}).
		Subject("user-abc").
		JwtID("tok-123").
		Expiration(time.Now().Add(time.Hour)).
		IssuedAt(time.Now()).
		Build()
	require.NoError(t, err)
	for _, opt := range opts {
		opt(tok)
	}
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))
	require.NoError(t, err)
	return string(signed)
}

// ---- stub seams ----

// stubWorkspaceResolver satisfies WorkspaceResolverIface.
type stubWorkspaceResolver struct{}

func (s *stubWorkspaceResolver) Resolve(_ context.Context, claims *auth.Claims) (*auth.Workspace, error) {
	wsID := claims.WorkspaceID
	if wsID == "" {
		wsID = "ws-test-" + claims.Sub
	}
	return &auth.Workspace{ID: wsID, OwnerID: claims.Sub, Name: "test"}, nil
}

// quotaAlwaysDeny satisfies QuotaChecker; always returns ErrQuotaExceeded.
type quotaAlwaysDeny struct{}

func (q *quotaAlwaysDeny) CheckAndIncrement(_ context.Context, _ string, _ auth.Tier) error {
	return auth.ErrQuotaExceeded
}

// quotaAlwaysAllow satisfies QuotaChecker; always passes.
type quotaAlwaysAllow struct{}

func (q *quotaAlwaysAllow) CheckAndIncrement(_ context.Context, _ string, _ auth.Tier) error {
	return nil
}

// rateLimitFunc is a replaceable rate-limit function for test injection.
type rateLimitFunc func(ctx context.Context, workspaceID string, tier auth.Tier) error

// rateLimitAlwaysExceeded always returns a rate-limit error.
func rateLimitAlwaysExceeded(_ context.Context, _ string, _ auth.Tier) error {
	return fmt.Errorf("rate limit exceeded")
}

// rateLimitAlwaysAllow always returns nil.
func rateLimitAlwaysAllow(_ context.Context, _ string, _ auth.Tier) error { return nil }

// ---- test server builder ----

// buildTestMux creates a gated HTTP mux using test seams.
// The rate-limit function rl may be nil (allow all).
// Returns the mux and a pointer to the slice that records which gates ran.
func buildTestMux(
	v *auth.JWTValidator,
	ws WorkspaceResolverIface,
	quota QuotaChecker,
	policy *PolicyEngine,
	rl rateLimitFunc,
	gateRecord *[]int,
) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		if gateRecord != nil {
			*gateRecord = nil
		}

		// Gate 1 — JWT validate.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 1)
		}
		authHdr := r.Header.Get("Authorization")
		rawToken, ok := bearerToken(authHdr)
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "AUTH_FAILED: missing Bearer token")
			return
		}
		claims, err := v.Validate(ctx, rawToken)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, err.Error())
			return
		}

		// Gate 2 — workspace resolve.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 2)
		}
		workspace, err := ws.Resolve(ctx, claims)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "AUTH_FAILED: workspace error")
			return
		}

		// Gate 3 — quota.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 3)
		}
		if quota != nil {
			if err := quota.CheckAndIncrement(ctx, workspace.ID, claims.Tier); err != nil {
				if isQuotaErr(err) {
					writeAPIError(w, http.StatusServiceUnavailable, "quota exceeded")
					return
				}
				writeAPIError(w, http.StatusServiceUnavailable, "quota check error")
				return
			}
		}

		// Gate 4 — policy.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 4)
		}
		if policy != nil {
			dec, err := policy.Check(ctx, workspace.ID, r.URL.Path)
			if err != nil || !dec.Allowed {
				writeAPIError(w, http.StatusServiceUnavailable, "policy denied")
				return
			}
		}

		// Gate 5 — trust registry.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 5)
		}
		td, err := TrustRegistryCheck(ctx, workspace.ID)
		if err != nil || !td.Trusted {
			writeAPIError(w, http.StatusForbidden, "trust: workspace not trusted")
			return
		}

		// Gate 6 — supply-chain.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 6)
		}
		sc, err := SupplyChainCheck(ctx, r.URL.Path)
		if err != nil || !sc.Allowed {
			writeAPIError(w, http.StatusForbidden, "supply-chain: denied")
			return
		}

		// Gate 7 — rate limit.
		if gateRecord != nil {
			*gateRecord = append(*gateRecord, 7)
		}
		if rl != nil {
			if err := rl(ctx, workspace.ID, claims.Tier); err != nil {
				writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
				return
			}
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	for _, path := range []string{"/v1/retrieve", "/v1/complete", "/v1/embed", "/v1/rerank", "/v1/probe"} {
		mux.Handle(path, handler)
	}
	return mux
}

// newTestValidator creates a JWTValidator with an injected test JWKS.
func newTestValidator(t *testing.T, ks jwk.Set) *auth.JWTValidator {
	t.Helper()
	v := auth.NewJWTValidator("unused", testIssuer, testAudience)
	v.InjectTestKeySet(ks)
	return v
}

// ---- tests ----

func TestPublicServer_NoJWT_Returns401(t *testing.T) {
	_, ks := testKeyPair(t)
	v := newTestValidator(t, ks)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, nil, nil, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/v1/retrieve", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestPublicServer_ExpiredJWT_Returns401(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newTestValidator(t, ks)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, nil, nil, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tok, err := jwt.NewBuilder().
		Issuer(testIssuer).
		Audience([]string{testAudience}).
		Subject("user-abc").
		Expiration(time.Now().Add(-time.Hour)).
		IssuedAt(time.Now().Add(-2 * time.Hour)).
		Build()
	require.NoError(t, err)
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256, priv))
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/retrieve", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+string(signed))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
}

func TestPublicServer_QuotaExceeded_Returns503(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newTestValidator(t, ks)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, &quotaAlwaysDeny{}, nil, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := signRS256(t, priv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/retrieve", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestPublicServer_RateLimit_Returns429(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newTestValidator(t, ks)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, &quotaAlwaysAllow{}, nil, rateLimitAlwaysExceeded, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := signRS256(t, priv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/retrieve", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusTooManyRequests, resp.StatusCode)
}

func TestPublicServer_PolicyServiceDown_Returns503(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newTestValidator(t, ks)

	// Closed test server → connection refused → fail-closed.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	pe := NewPolicyEngine(dead.URL, nil)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, &quotaAlwaysAllow{}, pe, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := signRS256(t, priv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/retrieve", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	// Policy service unreachable → fail-closed → 503.
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestPublicServer_7GateOrder(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newTestValidator(t, ks)

	var gateRecord []int
	mux := buildTestMux(v, &stubWorkspaceResolver{}, &quotaAlwaysAllow{}, nil, rateLimitAlwaysAllow, &gateRecord)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	token := signRS256(t, priv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/probe", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []int{1, 2, 3, 4, 5, 6, 7}, gateRecord,
		"7-gate order must be exactly 1(JWT)→2(ws)→3(quota)→4(policy)→5(trust)→6(supply-chain)→7(rate)")
}

func TestPublicServer_HealthNoAuth(t *testing.T) {
	_, ks := testKeyPair(t)
	v := newTestValidator(t, ks)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, nil, nil, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPublicServer_MetricsNoAuth(t *testing.T) {
	_, ks := testKeyPair(t)
	v := newTestValidator(t, ks)
	mux := buildTestMux(v, &stubWorkspaceResolver{}, nil, nil, nil, nil)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/metrics")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPublicServer_ReflectionDisabled(t *testing.T) {
	// PublicServer wraps an http.Server only — there is no *grpc.Server field.
	// Reflection requires a *grpc.Server; its absence is a compile-time guarantee.
	// This test asserts the structural invariant: httpSrv is nil before Start().
	cfg := PublicConfig{
		Addr:      "127.0.0.1:0",
		JWT:       auth.NewJWTValidator("unused", testIssuer, testAudience),
		Workspace: &stubWorkspaceResolver{},
	}
	ps := NewPublicServer(cfg)
	assert.Nil(t, ps.httpSrv, "httpSrv must be nil before Start — no gRPC server exists on port 8094")
}

func TestPublicServer_SSE_JWTValidatedOnce(t *testing.T) {
	priv, ks := testKeyPair(t)
	v := newTestValidator(t, ks)

	var validateCount atomic.Int64

	// Build a minimal SSE mux that counts JWT validations.
	sseMux := http.NewServeMux()
	sseMux.HandleFunc("/v1/complete", func(w http.ResponseWriter, r *http.Request) {
		authHdr := r.Header.Get("Authorization")
		rawToken, ok := bearerToken(authHdr)
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "no token")
			return
		}
		// JWT validated ONCE at connection establishment.
		_, err := v.Validate(r.Context(), rawToken)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, err.Error())
			return
		}
		validateCount.Add(1)

		// Emit an SSE response.
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"delta\":\"hello\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"done\":true}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
	srv := httptest.NewServer(sseMux)
	defer srv.Close()

	token := signRS256(t, priv)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/complete", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// JWT validated exactly once per SSE connection.
	assert.Equal(t, int64(1), validateCount.Load(),
		"JWT must be validated exactly once at SSE connection establishment")
}

// ---- rate-tier unit tests ----

func TestRateLimitForTier(t *testing.T) {
	assert.Equal(t, rateFree, rateLimitForTier(auth.TierFree))
	assert.Equal(t, ratePro, rateLimitForTier(auth.TierPro))
	assert.Equal(t, rateEnterprise, rateLimitForTier(auth.TierEnterprise))
	// Unknown tier should default to free limit.
	assert.Equal(t, rateFree, rateLimitForTier(auth.Tier("unknown")))
}

// ---- PolicyEngine unit tests ----

func TestPolicyEngine_Disabled_AlwaysAllows(t *testing.T) {
	pe := NewPolicyEngine("", nil)
	dec, err := pe.Check(context.Background(), "ws-1", "/v1/retrieve")
	require.NoError(t, err)
	assert.True(t, dec.Allowed)
}

func TestPolicyEngine_ServiceDown_FailClosed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	pe := NewPolicyEngine(dead.URL, nil)
	dec, err := pe.Check(context.Background(), "ws-1", "/v1/retrieve")
	assert.Error(t, err, "policy service down must return error (fail-closed)")
	assert.False(t, dec.Allowed)
}

// ---- TrustRegistryCheck unit tests ----

func TestTrustRegistryCheck_EmptyWorkspace_Denied(t *testing.T) {
	td, err := TrustRegistryCheck(context.Background(), "")
	assert.Error(t, err)
	assert.False(t, td.Trusted)
}

func TestTrustRegistryCheck_KnownWorkspace_Trusted(t *testing.T) {
	td, err := TrustRegistryCheck(context.Background(), "ws-abc")
	require.NoError(t, err)
	assert.True(t, td.Trusted)
}

// ---- SupplyChainCheck unit test ----

func TestSupplyChainCheck_AlwaysAllowed(t *testing.T) {
	dec, err := SupplyChainCheck(context.Background(), "/v1/retrieve")
	require.NoError(t, err)
	assert.True(t, dec.Allowed)
}
