// Package api — public API surface for clawde-intelligence on port 8094.
//
// Purpose: PolicyEngine performs policy, trust-registry, and supply-chain
//          checks as gates 4–6 in the 7-gate interceptor chain. All checks
//          are FAIL-CLOSED: an unreachable service or unexpected error → DENY.
//
// Inputs:  HTTP policy-service URL (optional). WorkspaceID string.
// Outputs: PolicyDecision{Allowed bool}. Non-nil error → deny.
// Constraints:
//   - PolicyEngine.Check: if enabled + policy-service unreachable → DENY (fail-closed).
//   - TrustRegistryCheck: stub → Trusted=true; error → DENY fail-closed.
//   - SupplyChainCheck: stub → Allowed=true.
//   - File ≤ 500 lines.
//
// SPORT: REGISTRY-FUNCTIONS.md — PolicyEngine, TrustRegistryCheck, SupplyChainCheck.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
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

// TrustRegistryCheck verifies whether a workspace is registered in the trust
// registry. This is a stub implementation: it returns Trusted=true for all
// workspaces. Errors (which would arise in the production registry implementation)
// are FAIL-CLOSED: the caller must deny on non-nil error.
//
// SPORT: REGISTRY-FUNCTIONS.md — TrustRegistryCheck.
func TrustRegistryCheck(ctx context.Context, workspaceID string) (TrustDecision, error) {
	if workspaceID == "" {
		// Empty workspace ID is always untrusted (fail-closed).
		return TrustDecision{Trusted: false}, fmt.Errorf("trust: empty workspace_id")
	}
	// Stub: production would query the trust registry service.
	// Error from registry → fail-closed (return error; caller denies).
	_ = ctx
	return TrustDecision{Trusted: true}, nil
}

// SupplyChainCheck validates that the request path is in the approved supply
// chain. This stub always permits. In production it would verify the gRPC method
// or HTTP path against a signed allowlist.
//
// SPORT: REGISTRY-FUNCTIONS.md — SupplyChainCheck.
func SupplyChainCheck(_ context.Context, _ string) (PolicyDecision, error) {
	return PolicyDecision{Allowed: true}, nil
}
