// Package api — public API surface for clawde-intelligence on port 8094.
//
// Purpose: PolicyEngine performs policy, trust-registry, and supply-chain
//          checks as gates 4–6 in the 7-gate interceptor chain. All checks
//          are FAIL-CLOSED: an unreachable service or unexpected error → DENY.
//
// Inputs:  HTTP policy-service URL (optional). Pgx pool for trust-registry.
//          WorkspaceID string. gRPC FullMethod or HTTP path string.
// Outputs: PolicyDecision{Allowed bool}. TrustDecision{Trusted bool}.
//          Non-nil error → deny.
// Constraints:
//   - PolicyEngine.Check: if enabled + policy-service unreachable → DENY (fail-closed).
//   - TrustRegistry.Check: pgx SELECT against np_clawde_trusted_workspaces; 60s TTL cache.
//     When pool is nil (dev mode): log warning, return Trusted:true (preserve dev UX).
//   - SupplyChainCheck: compile-time allowlist of known HTTP paths; unknown → Allowed:false.
//   - Both TrustRegistry.Check and SupplyChainCheck are FAIL-CLOSED on error.
//   - File ≤ 500 lines.
//
// SPORT: REGISTRY-FUNCTIONS.md — PolicyEngine, TrustRegistry, SupplyChainCheck.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PolicyDecision is the outcome of a policy evaluation.
//
// SPORT: REGISTRY-FUNCTIONS.md — PolicyDecision.
type PolicyDecision struct {
	// Allowed is true when the request passes the policy gate.
	Allowed bool
	// Reason is a human-readable explanation (populated on deny for logging).
	Reason string
}

// TrustDecision is the outcome of a trust-registry lookup.
//
// SPORT: REGISTRY-FUNCTIONS.md — TrustDecision.
type TrustDecision struct {
	// Trusted is true when the workspace is in the trust registry.
	Trusted bool
}

// policyRequest is the JSON body sent to the external policy service.
type policyRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Method      string `json:"method"`
}

// policyResponse is the JSON body expected from the external policy service.
type policyResponse struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// PolicyEngine evaluates per-request policy via an optional external HTTP service.
// When disabled (policyURL == ""), every request is allowed (fast path).
// When enabled, any error from the remote service causes a DENY (fail-closed).
//
// SPORT: REGISTRY-FUNCTIONS.md — PolicyEngine.
type PolicyEngine struct {
	enabled    bool
	policyURL  string
	httpClient *http.Client
}

// NewPolicyEngine creates a PolicyEngine.
//
// policyURL may be empty ("") to disable external policy checks (allow all).
// httpClient may be nil; a default 5-second-timeout client is used.
//
// SPORT: REGISTRY-FUNCTIONS.md — NewPolicyEngine.
func NewPolicyEngine(policyURL string, httpClient *http.Client) *PolicyEngine {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 5 * time.Second}
	}
	return &PolicyEngine{
		enabled:    policyURL != "",
		policyURL:  policyURL,
		httpClient: httpClient,
	}
}

// Check evaluates the policy for the given context and gRPC method.
//
// Fast path (disabled): returns PolicyDecision{Allowed: true}, nil.
// When enabled: POSTs to policyURL; any HTTP error, non-200 status, or
// parse failure → returns PolicyDecision{Allowed: false} + non-nil error
// (fail-closed: the caller must deny the request).
//
// SPORT: REGISTRY-FUNCTIONS.md — PolicyEngine.Check.
func (e *PolicyEngine) Check(ctx context.Context, workspaceID, method string) (PolicyDecision, error) {
	if !e.enabled {
		return PolicyDecision{Allowed: true}, nil
	}

	body, err := json.Marshal(policyRequest{WorkspaceID: workspaceID, Method: method})
	if err != nil {
		// JSON marshal failure is an internal error → fail-closed.
		return PolicyDecision{Allowed: false, Reason: "policy: marshal error"}, fmt.Errorf("policy: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.policyURL, bytes.NewReader(body))
	if err != nil {
		return PolicyDecision{Allowed: false, Reason: "policy: build request error"}, fmt.Errorf("policy: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		// Network or timeout error → fail-closed.
		return PolicyDecision{Allowed: false, Reason: "policy: service unreachable"}, fmt.Errorf("policy: request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return PolicyDecision{Allowed: false, Reason: fmt.Sprintf("policy: service returned %d", resp.StatusCode)},
			fmt.Errorf("policy: service returned HTTP %d", resp.StatusCode)
	}

	var pr policyResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return PolicyDecision{Allowed: false, Reason: "policy: decode error"}, fmt.Errorf("policy: decode response: %w", err)
	}

	if !pr.Allowed {
		reason := pr.Reason
		if reason == "" {
			reason = "policy denied"
		}
		return PolicyDecision{Allowed: false, Reason: reason}, fmt.Errorf("policy: denied: %s", reason)
	}
	return PolicyDecision{Allowed: true}, nil
}

// ---- TrustRegistry ----

// trustCacheEntry is a single cached trust-registry result with wall-clock expiry.
type trustCacheEntry struct {
	trusted   bool
	expiresAt time.Time
}

// trustCacheTTL is the positive-result cache TTL for trust-registry lookups.
const trustCacheTTL = 60 * time.Second

// trustDBFunc is the seam for injecting a fake DB query in tests.
// In production, TrustRegistry.pool is used directly; dbFn is nil.
type trustDBFunc func(ctx context.Context, workspaceID string) (bool, error)

// TrustRegistry performs real workspace trust lookups against
// np_clawde_trusted_workspaces via pgx, with a 60-second TTL cache for positive
// results. Negative results (not found) are not cached so new registrations take
// effect immediately. Fail-closed: any DB error → Trusted:false + non-nil error.
//
// When pool is nil (dev/test mode): logs a warning and returns Trusted:true to
// preserve developer UX — production must always supply a pool.
//
// SPORT: REGISTRY-FUNCTIONS.md — TrustRegistry, TrustRegistryCheck (real DB lookup).
type TrustRegistry struct {
	pool  *pgxpool.Pool
	cache sync.Map    // key: string workspaceID → trustCacheEntry
	dbFn  trustDBFunc // non-nil in tests: overrides pgx query; nil in production
}

// NewTrustRegistry creates a TrustRegistry. pool may be nil for dev mode.
//
// SPORT: REGISTRY-FUNCTIONS.md — NewTrustRegistry.
func NewTrustRegistry(pool *pgxpool.Pool) *TrustRegistry {
	return &TrustRegistry{pool: pool}
}

// Check verifies whether the workspace is in the np_clawde_trusted_workspaces
// table. Returns (TrustDecision{Trusted:false}, non-nil error) on any DB error
// (fail-closed). Returns (TrustDecision{Trusted:false}, nil) when the workspace
// is genuinely absent (not an error — caller still denies via !Trusted).
//
// SPORT: REGISTRY-FUNCTIONS.md — TrustRegistry.Check.
func (tr *TrustRegistry) Check(ctx context.Context, workspaceID string) (TrustDecision, error) {
	if workspaceID == "" {
		return TrustDecision{Trusted: false}, fmt.Errorf("trust: empty workspace_id")
	}

	// Dev mode: pool not configured and no test seam injected.
	if tr.pool == nil && tr.dbFn == nil {
		log.Printf("[trust] WARNING: no pgx pool configured — returning Trusted:true for workspace %q (dev mode)", workspaceID)
		return TrustDecision{Trusted: true}, nil
	}

	// Consult wall-clock TTL cache for positive results.
	if raw, ok := tr.cache.Load(workspaceID); ok {
		entry := raw.(trustCacheEntry)
		if entry.trusted && time.Now().Before(entry.expiresAt) {
			return TrustDecision{Trusted: true}, nil
		}
		// Expired or negative — evict and re-query.
		tr.cache.Delete(workspaceID)
	}

	// Execute DB query: prefer test seam (dbFn) over real pgx pool.
	var found bool
	var queryErr error
	if tr.dbFn != nil {
		found, queryErr = tr.dbFn(ctx, workspaceID)
	} else {
		var scanErr error
		scanErr = tr.pool.QueryRow(ctx,
			"SELECT EXISTS(SELECT 1 FROM np_clawde_trusted_workspaces WHERE workspace_id=$1)",
			workspaceID,
		).Scan(&found)
		queryErr = scanErr
	}
	if queryErr != nil {
		// DB error → fail-closed (deny + surface error to caller).
		return TrustDecision{Trusted: false}, fmt.Errorf("trust: registry query: %w", queryErr)
	}

	if found {
		// Cache positive result with wall-clock expiry.
		tr.cache.Store(workspaceID, trustCacheEntry{
			trusted:   true,
			expiresAt: time.Now().Add(trustCacheTTL),
		})
		return TrustDecision{Trusted: true}, nil
	}

	// Workspace not in registry — deny, no cache.
	return TrustDecision{Trusted: false}, nil
}

// TrustRegistryCheck is the package-level adapter called by the interceptor chain.
// It delegates to the global default trust registry (gDefaultTrustRegistry).
// The global registry is set via SetDefaultTrustRegistry; in tests it may be
// overridden directly.
//
// Fail-closed: empty workspaceID → error; DB error → error.
// Dev mode (nil pool): Trusted:true with a log warning.
//
// SPORT: REGISTRY-FUNCTIONS.md — TrustRegistryCheck (real DB lookup, was: allow-all stub).
func TrustRegistryCheck(ctx context.Context, workspaceID string) (TrustDecision, error) {
	return gDefaultTrustRegistry.Check(ctx, workspaceID)
}

// gDefaultTrustRegistry is the process-wide TrustRegistry instance.
// Initialised with a nil pool (dev mode) and replaced via SetDefaultTrustRegistry
// when a pgx pool is available at server startup.
var gDefaultTrustRegistry = NewTrustRegistry(nil)

// SetDefaultTrustRegistry replaces the process-wide TrustRegistry.
// Must be called before the public server begins serving requests.
//
// SPORT: REGISTRY-FUNCTIONS.md — SetDefaultTrustRegistry.
func SetDefaultTrustRegistry(tr *TrustRegistry) {
	gDefaultTrustRegistry = tr
}

// ---- SupplyChain ----

// allowedHTTPPaths is the compile-time allowlist of HTTP paths that the public
// API server (port 8094) is permitted to serve. Any request whose URL.Path is
// not in this map is denied (fail-closed).
//
// Mapping reflects buildMux registrations in server.go. Auth-exempt routes
// (/health, /metrics) bypass the 7-gate chain entirely and are NOT listed here.
//
// SPORT: REGISTRY-FUNCTIONS.md — SupplyChainCheck allowlist (method allowlist, was: allow-all stub).
var allowedHTTPPaths = map[string]bool{
	"/v1/retrieve": true,
	"/v1/complete": true,
	"/v1/embed":    true,
	"/v1/rerank":   true,
}

// SupplyChainCheck validates that the HTTP path (or gRPC FullMethod) is in the
// compile-time allowlist. Unknown paths → Allowed:false (fail-closed).
// Returns a non-nil error only for empty path — unknown paths return
// PolicyDecision{Allowed:false} with nil error (the caller still denies via !Allowed).
//
// SPORT: REGISTRY-FUNCTIONS.md — SupplyChainCheck (method allowlist, was: allow-all stub).
func SupplyChainCheck(_ context.Context, path string) (PolicyDecision, error) {
	if path == "" {
		return PolicyDecision{Allowed: false, Reason: "supply-chain: empty path"}, fmt.Errorf("supply-chain: empty path")
	}
	if allowedHTTPPaths[path] {
		return PolicyDecision{Allowed: true}, nil
	}
	return PolicyDecision{Allowed: false, Reason: fmt.Sprintf("supply-chain: denied path %q", path)}, nil
}
