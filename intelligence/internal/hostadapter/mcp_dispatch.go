// Package hostadapter — ADR-003 deny-by-default dispatch chain primitives.
//
// Purpose:    The trust/policy/supply-chain interfaces and their stub
//             implementations that gate every MCP tool call before execution:
//             auth → trust_registry → PolicyEngine.evaluate → SupplyChainPolicy.
//             Split out of mcp_server.go to keep each file ≤500 lines.
// Inputs:     client_id, tool name, workspace id.
// Outputs:    trust/policy decisions (nil = allow, error = deny).
// Constraints: Deny-by-default — unknown client_id and unknown tool are denied.
//              Stdlib + context only. File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter dispatch chain.
package hostadapter

import (
	"context"
	"fmt"
)

// TrustRegistry resolves whether a client_id is a known/trusted MCP client.
// Unknown client_id MUST be denied (deny-by-default). The stub here trusts the
// configured local client id only; the production registry consults clawd state.
type TrustRegistry interface {
	IsTrusted(clientID string) bool
}

// PolicyEngine.Evaluate gates a tool call. The AllowAll stub (ADR-003) permits
// every call; the real engine evaluates per-tool/per-workspace policy.
type PolicyEngine interface {
	Evaluate(ctx context.Context, clientID, tool, workspaceID string) error
}

// SupplyChainPolicy is the final dispatch gate (ADR-003): verifies the tool is
// in the allowed supply-chain set. Unknown tools are denied.
type SupplyChainPolicy interface {
	Permit(tool string) error
}

// staticTrustRegistry trusts a fixed set of client ids; everything else is denied.
type staticTrustRegistry struct{ trusted map[string]struct{} }

func newStaticTrustRegistry(ids ...string) *staticTrustRegistry {
	m := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" {
			m[id] = struct{}{}
		}
	}
	return &staticTrustRegistry{trusted: m}
}

func (r *staticTrustRegistry) IsTrusted(clientID string) bool {
	_, ok := r.trusted[clientID]
	return ok
}

// allowAllPolicy is the ADR-003 PolicyEngine stub.
type allowAllPolicy struct{}

func (allowAllPolicy) Evaluate(_ context.Context, _, _, _ string) error {
	if !PolicyAllowAll() {
		return fmt.Errorf("policy denied")
	}
	return nil
}

// toolSetSupplyChain permits only the registered MCP tools.
type toolSetSupplyChain struct{ allowed map[string]struct{} }

func newToolSetSupplyChain(tools ...string) *toolSetSupplyChain {
	m := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		m[t] = struct{}{}
	}
	return &toolSetSupplyChain{allowed: m}
}

func (s *toolSetSupplyChain) Permit(tool string) error {
	if _, ok := s.allowed[tool]; !ok {
		return fmt.Errorf("tool %q not in supply-chain allowlist", tool)
	}
	return nil
}
