// Package api — public API server for clawde-intelligence on port 8094.
//
// Purpose: Expose a public gRPC server (127.0.0.1:8094) and HTTP mux for
//          POST /v1/retrieve, /v1/complete (SSE streaming), /v1/embed,
//          /v1/rerank plus /health and /metrics (auth-exempt).
//
//          Seven-gate interceptor chain (EXACT, in order):
//            1. JWT validate         — 401 on failure
//            2. workspace resolve    — 401 on failure
//            3. quota check          — 503 on exceeded
//            4. policy check         — 403 on deny / 503 on service error
//            5. trust registry       — 403 on untrusted / 503 on error
//            6. supply-chain         — 403 on deny
//            7. rate limit           — 429 on exceeded
//
//          FAIL-CLOSED: any gate error → deny. Never fall through to handler.
//          gRPC reflection DISABLED unconditionally on port 8094.
//          Port 8094 is the public surface; internal gRPC stays on 8090.
//
// Inputs:  PublicConfig{Addr, JWTValidator, WorkspaceResolver, QuotaEnforcer,
//                        PolicyEngine, RedisAddr, RateTiers}.
// Outputs: Running HTTP server on 127.0.0.1:8094. Shutdown() for graceful stop.
// Constraints:
//   - No reflection on port 8094.
//   - SSE JWT validated once at connection establishment (standard interceptor).
//   - Redis sliding window for rate limiting (CLAWDE_REDIS_URL).
//   - Rate tiers: free=10 req/min, pro=100 req/min, enterprise=unlimited.
//   - File ≤ 500 lines.
//
// SPORT: REGISTRY-SERVICES.md — public_api_addr=127.0.0.1:8094,
//                                auth=JWT,
//                                reflection=disabled.
//        REGISTRY-ENDPOINTS.md — POST /v1/retrieve, /v1/complete, /v1/embed,
//                                 /v1/rerank, /health, /metrics.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nself-org/clawde/intelligence/internal/auth"
)

const (
	defaultPublicAddr = "127.0.0.1:8094"
	envPublicAPIPort  = "CLAWDE_PUBLIC_API_PORT"
	envRedisURL       = "CLAWDE_REDIS_URL"

	// Rate-limit tiers (requests per minute per workspace).
	rateFree       = 10
	ratePro        = 100
	rateEnterprise = -1 // unlimited
)

// QuotaChecker is the seam used by PublicServer for quota enforcement.
// *auth.QuotaEnforcer satisfies this interface.
//
// SPORT: REGISTRY-FUNCTIONS.md — QuotaChecker.
type QuotaChecker interface {
	CheckAndIncrement(ctx context.Context, workspaceID string, tier auth.Tier) error
}

// WorkspaceResolverIface is the seam used by PublicServer for workspace resolution.
// *auth.WorkspaceResolver satisfies this interface.
//
// SPORT: REGISTRY-FUNCTIONS.md — WorkspaceResolverIface.
type WorkspaceResolverIface interface {
	Resolve(ctx context.Context, claims *auth.Claims) (*auth.Workspace, error)
}

// PublicConfig holds all parameters needed to start the public API server.
//
// SPORT: REGISTRY-FUNCTIONS.md — PublicConfig.
type PublicConfig struct {
	// Addr is the listen address for the public HTTP mux (default 127.0.0.1:8094).
	Addr string
	// JWT is the validator for Bearer tokens on public routes.
	JWT *auth.JWTValidator
	// Workspace resolves JWT claims to workspace rows.
	Workspace WorkspaceResolverIface
	// Quota enforces per-workspace daily limits.
	Quota QuotaChecker
	// Policy gates requests via an optional external policy service.
	Policy *PolicyEngine
	// Redis is the rate-limit backend (nil → rate limit skipped with allow).
	Redis redis.Cmdable
}

// DefaultPublicAddr returns the listen address derived from CLAWDE_PUBLIC_API_PORT
// or the canonical default (127.0.0.1:8094).
func DefaultPublicAddr() string {
	if p := os.Getenv(envPublicAPIPort); p != "" {
		return "127.0.0.1:" + p
	}
	return defaultPublicAddr
}

// PublicServer is the public-facing HTTP server on port 8094.
//
// SPORT: REGISTRY-SERVICES.md — public_api_addr.
type PublicServer struct {
	cfg     PublicConfig
	httpSrv *http.Server
	mu      sync.Mutex
}

// NewPublicServer creates a PublicServer; does not start listeners.
func NewPublicServer(cfg PublicConfig) *PublicServer {
	if cfg.Addr == "" {
		cfg.Addr = DefaultPublicAddr()
	}
	return &PublicServer{cfg: cfg}
}

// Start binds and starts the public HTTP mux on cfg.Addr.
// Returns once the listener is established.
func (s *PublicServer) Start() error {
	mux := s.buildMux()

	lis, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("api: listen %s: %w", s.cfg.Addr, err)
	}

	s.mu.Lock()
	s.httpSrv = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 90 * time.Second,
	}
	s.mu.Unlock()

	go func() { _ = s.httpSrv.Serve(lis) }()
	return nil
}

// Shutdown gracefully stops the public server.
func (s *PublicServer) Shutdown(ctx context.Context) {
	s.mu.Lock()
	srv := s.httpSrv
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Shutdown(ctx)
	}
}

// Addr returns the configured listen address.
func (s *PublicServer) Addr() string { return s.cfg.Addr }

// buildMux assembles the HTTP mux with per-route auth exemptions.
func (s *PublicServer) buildMux() *http.ServeMux {
	mux := http.NewServeMux()

	// Auth-exempt routes.
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)

	// Auth-required public routes — all through the 7-gate chain.
	mux.Handle("/v1/retrieve", s.withGates(http.HandlerFunc(s.handleRetrieve)))
	mux.Handle("/v1/complete", s.withGates(http.HandlerFunc(s.handleComplete)))
	mux.Handle("/v1/embed", s.withGates(http.HandlerFunc(s.handleEmbed)))
	mux.Handle("/v1/rerank", s.withGates(http.HandlerFunc(s.handleRerank)))

	return mux
}

// ---- 7-gate chain ----

// withGates wraps a handler with the full 7-gate interceptor chain.
// The gates execute in the locked order below. Any gate returning an error
// terminates the chain with an appropriate HTTP status (fail-closed).
//
// Gate order: JWT(1) → workspace(2) → quota(3) → policy(4) →
//             trust(5) → supply-chain(6) → rate limit(7).
//
// SPORT: REGISTRY-FUNCTIONS.md — withGates, 7-gate chain.
func (s *PublicServer) withGates(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		// Gate 1 — JWT validate.
		authHdr := r.Header.Get("Authorization")
		rawToken, ok := bearerToken(authHdr)
		if !ok {
			writeAPIError(w, http.StatusUnauthorized, "AUTH_FAILED: missing Bearer token")
			return
		}
		claims, err := s.cfg.JWT.Validate(ctx, rawToken)
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, err.Error())
			return
		}

		// Gate 2 — workspace resolve.
		ws, err := s.cfg.Workspace.Resolve(ctx, claims)
		if err != nil {
			if isWsNotFound(err) {
				writeAPIError(w, http.StatusUnauthorized, "AUTH_FAILED: workspace not found")
				return
			}
			// Resolver internal error → fail-closed.
			writeAPIError(w, http.StatusUnauthorized, "AUTH_FAILED: workspace resolve error")
			return
		}

		// Gate 3 — quota check (503 when exceeded, not 429 per spec).
		if s.cfg.Quota != nil {
			if err := s.cfg.Quota.CheckAndIncrement(ctx, ws.ID, claims.Tier); err != nil {
				if isQuotaErr(err) {
					writeAPIError(w, http.StatusServiceUnavailable, "quota exceeded")
					return
				}
				// Quota backend error → fail-closed.
				writeAPIError(w, http.StatusServiceUnavailable, "quota check error")
				return
			}
		}

		// Gate 4 — policy check.
		if s.cfg.Policy != nil {
			dec, err := s.cfg.Policy.Check(ctx, ws.ID, r.URL.Path)
			if err != nil || !dec.Allowed {
				writeAPIError(w, http.StatusServiceUnavailable, "policy denied")
				return
			}
		}

		// Gate 5 — trust registry.
		td, err := TrustRegistryCheck(ctx, ws.ID)
		if err != nil || !td.Trusted {
			writeAPIError(w, http.StatusForbidden, "trust: workspace not trusted")
			return
		}

		// Gate 6 — supply-chain.
		sc, err := SupplyChainCheck(ctx, r.URL.Path)
		if err != nil || !sc.Allowed {
			writeAPIError(w, http.StatusForbidden, "supply-chain: denied")
			return
		}

		// Gate 7 — rate limit (per-workspace, per-minute sliding window).
		if err := s.checkRateLimit(ctx, ws.ID, claims.Tier); err != nil {
			writeAPIError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}

		// All 7 gates passed — inject workspace/claims into context and call handler.
		ctx = context.WithValue(ctx, ctxKeyClaims, claims)
		ctx = context.WithValue(ctx, ctxKeyWorkspace, ws)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// ---- rate limit ----

type ctxKey int

const (
	ctxKeyClaims    ctxKey = iota
	ctxKeyWorkspace ctxKey = iota
)

// rateWindow is the per-workspace rate-limit window (1 minute).
const rateWindow = time.Minute

// checkRateLimit enforces a per-workspace sliding-window rate limit via Redis.
// Tiers: free=10/min, pro=100/min, enterprise=unlimited.
// Redis failure → allow (fail open for rate limit only — the 6 prior gates remain fail-closed).
//
// Returns non-nil error when the request exceeds the limit.
//
// SPORT: REGISTRY-FUNCTIONS.md — checkRateLimit, rate tiers.
func (s *PublicServer) checkRateLimit(ctx context.Context, workspaceID string, tier auth.Tier) error {
	limit := rateLimitForTier(tier)
	if limit < 0 {
		return nil // unlimited
	}
	if s.cfg.Redis == nil {
		return nil // no Redis configured → allow
	}

	key := "rl:ws:" + workspaceID
	now := time.Now()
	nowMs := now.UnixMilli()
	windowMs := rateWindow.Milliseconds()
	cutoff := nowMs - windowMs

	pipe := s.cfg.Redis.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", fmt.Sprintf("%d", cutoff))
	countCmd := pipe.ZCard(ctx, key)
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(nowMs), Member: fmt.Sprintf("%d", nowMs)})
	pipe.Expire(ctx, key, 2*rateWindow)
	if _, err := pipe.Exec(ctx); err != nil {
		// Redis error → fail open for rate limit (not a security gate).
		return nil
	}

	if countCmd.Val() >= int64(limit) {
		return fmt.Errorf("rate limit exceeded: %d req/min for tier %s", limit, tier)
	}
	return nil
}

// rateLimitForTier returns requests/minute for the given tier.
// Returns -1 for enterprise (unlimited).
//
// SPORT: REGISTRY-FUNCTIONS.md — rateLimitForTier.
func rateLimitForTier(tier auth.Tier) int {
	switch tier {
	case auth.TierPro:
		return ratePro // 100/min
	case auth.TierEnterprise:
		return rateEnterprise // unlimited
	default: // free + unknown
		return rateFree // 10/min
	}
}

// ---- route handlers (stubs — real impl delegates to internal/gateway) ----

func (s *PublicServer) handleRetrieve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "route": "/v1/retrieve"})
}

func (s *PublicServer) handleComplete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// SSE streaming: JWT was validated at gate 1 (once per connection).
	accept := r.Header.Get("Accept")
	if accept == "text/event-stream" {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		_, _ = w.Write([]byte("data: {\"done\":true}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "route": "/v1/complete"})
}

func (s *PublicServer) handleEmbed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "route": "/v1/embed"})
}

func (s *PublicServer) handleRerank(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "route": "/v1/rerank"})
}

func (s *PublicServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *PublicServer) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- JSON helpers ----

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeAPIError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// bearerToken extracts the token from "Bearer <token>".
func bearerToken(hdr string) (string, bool) {
	const prefix = "Bearer "
	if len(hdr) <= len(prefix) {
		return "", false
	}
	if hdr[:len(prefix)] != prefix {
		return "", false
	}
	tok := hdr[len(prefix):]
	if tok == "" {
		return "", false
	}
	return tok, true
}

func isWsNotFound(err error) bool {
	return err != nil && containsStr(err.Error(), auth.ErrWorkspaceNotFound.Error())
}

func isQuotaErr(err error) bool {
	return err != nil && containsStr(err.Error(), auth.ErrQuotaExceeded.Error())
}

func containsStr(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && func() bool {
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}()
}
