// dispatch_export.go — exported constructors for the ADR-003 dispatch chain.
//
// Purpose:    Expose the deny-by-default dispatch primitives (TrustRegistry,
//             PolicyEngine, SupplyChainPolicy) to sibling packages (e.g.
//             internal/docs KB ingestion) without duplicating the gating logic.
//             The unexported newStaticTrustRegistry/newToolSetSupplyChain remain
//             for in-package MCP wiring; these are thin exported wrappers.
// Inputs:     trusted client ids; allowlisted tool names.
// Outputs:    interface implementations identical to the MCP server's.
// Constraints: Stdlib only. File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter dispatch chain (exported ctors).
package hostadapter

// NewTrustRegistry returns a TrustRegistry trusting exactly the given client ids.
// Unknown ids are denied (deny-by-default).
func NewTrustRegistry(ids ...string) TrustRegistry {
	return newStaticTrustRegistry(ids...)
}

// NewAllowAllPolicy returns the ADR-003 PolicyEngine stub (gated by PolicyAllowAll).
func NewAllowAllPolicy() PolicyEngine {
	return allowAllPolicy{}
}

// NewSupplyChainPolicy returns a SupplyChainPolicy permitting only the given tools.
// Unknown tools are denied.
func NewSupplyChainPolicy(tools ...string) SupplyChainPolicy {
	return newToolSetSupplyChain(tools...)
}
