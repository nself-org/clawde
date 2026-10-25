// policy_engine.go — deny-by-default PolicyEngine for the ADR-003 dispatch chain.
//
// Purpose:    Replace the AllowAll stub (mcp-server-spec §4 step 3). Implements
//             the spec algorithm: (a) unknown caller → deny, (b) explicit
//             per-tool permission for the caller → allow or deny, (c) otherwise
//             the trust-level default table. A tool absent from the tier table
//             is denied at every level (fail closed).
// Inputs:     PolicyEntry list (caller id, trust level 0..3, per-tool overrides).
// Outputs:    PolicyEngine whose Evaluate returns nil (allow) or a typed error.
// Constraints: Stdlib only. No env switch can turn this into allow-all.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter dispatch chain (PolicyEngine).
package hostadapter

import (
	"context"
	"errors"
	"fmt"
)

// Trust levels from cd_trust_registry (mcp-server-spec §3).
const (
	TrustNone     = 0 // registered but nothing permitted by default
	TrustLimited  = 1 // tier-1 read-only tools (nClaw mobile)
	TrustStandard = 2 // tier-2 tools that change state
	TrustElevated = 3 // Claude Code / OpenCode local sessions
)

// ErrPolicyDenied wraps every policy denial so callers can match with errors.Is.
var ErrPolicyDenied = errors.New("policy denied")

// toolMinTrust is the trust-level default table: the lowest level at which a
// tool is allowed when the caller has no explicit per-tool permission. Tools not
// listed here are denied for everyone.
var toolMinTrust = map[string]int{
	toolRetrieveContext: TrustLimited,
	toolRunAnalysis:     TrustLimited,
	toolListSymbols:     TrustLimited,
	"ingest_doc_url":    TrustStandard, // writes to the KB embed queue
}

// PolicyEntry registers one caller with its trust level and optional explicit
// per-tool permissions (true = allow, false = deny; wins over the default table).
type PolicyEntry struct {
	ClientID  string
	Level     int
	ToolPerms map[string]bool
}

type trustLevelPolicy struct{ callers map[string]PolicyEntry }

// NewPolicyEngine returns the deny-by-default PolicyEngine for the given callers.
func NewPolicyEngine(entries ...PolicyEntry) PolicyEngine {
	m := make(map[string]PolicyEntry, len(entries))
	for _, e := range entries {
		if e.ClientID != "" {
			m[e.ClientID] = e
		}
	}
	return &trustLevelPolicy{callers: m}
}

func (p *trustLevelPolicy) Evaluate(_ context.Context, clientID, tool, _ string) error {
	e, ok := p.callers[clientID]
	if !ok {
		return fmt.Errorf("%w: unknown_entity", ErrPolicyDenied)
	}
	if allow, explicit := e.ToolPerms[tool]; explicit {
		if allow {
			return nil
		}
		return fmt.Errorf("%w: tool %q explicitly denied", ErrPolicyDenied, tool)
	}
	min, known := toolMinTrust[tool]
	if !known {
		return fmt.Errorf("%w: tool %q not configured", ErrPolicyDenied, tool)
	}
	if e.Level < min {
		return fmt.Errorf("%w: tool %q needs trust level %d, caller has %d", ErrPolicyDenied, tool, min, e.Level)
	}
	return nil
}
