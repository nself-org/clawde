// policy_engine.go — deny-by-default PolicyEngine for the ADR-003 dispatch chain.
//
// Purpose:    Replace the AllowAll stub (mcp-server-spec §4 step 3). Implements
//             the spec algorithm: (a) unknown caller → deny, (b) a tool absent
//             from the tier table → deny at every level, even with an explicit
//             override (fail closed), (c) explicit per-tool permission for the
//             caller → allow or deny, (d) otherwise the trust-level default.
// Inputs:     PolicyEntry list (caller id, trust level 0..3, per-tool overrides).
// Outputs:    PolicyEngine whose Evaluate returns nil (allow) or a typed error.
// Constraints: Stdlib only. No env switch can turn this into allow-all.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter dispatch chain (PolicyEngine).
package hostadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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

// policyFile is the on-disk caller policy (CLAWDE_MCP_POLICY_FILE). Level
// defaults to TrustElevated (local Claude Code session) when omitted.
// Fields are raw so an explicit null can be told apart from an omitted key.
type policyFile struct {
	Level     json.RawMessage `json:"level"`
	ToolPerms json.RawMessage `json:"tool_perms"`
}

// parsePolicyFile decodes strictly: the body must be one JSON object, unknown
// fields and null values are errors, and tool_perms may only name known tools
// (a misspelled tool would otherwise silently drop the operator's denial).
func parsePolicyFile(raw []byte) (level *int, perms map[string]bool, err error) {
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		return nil, nil, errors.New("policy must be a JSON object")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var f policyFile
	if err := dec.Decode(&f); err != nil {
		return nil, nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nil, errors.New("trailing data after policy object")
	}
	if f.Level != nil {
		var l int
		if err := json.Unmarshal(f.Level, &l); err != nil || string(f.Level) == "null" {
			return nil, nil, errors.New("level must be an integer")
		}
		level = &l
	}
	if f.ToolPerms != nil {
		if string(f.ToolPerms) == "null" {
			return nil, nil, errors.New("tool_perms must be an object")
		}
		if err := json.Unmarshal(f.ToolPerms, &perms); err != nil {
			return nil, nil, fmt.Errorf("tool_perms: %w", err)
		}
		for tool := range perms {
			if _, known := toolMinTrust[tool]; !known {
				return nil, nil, fmt.Errorf("tool_perms: unknown tool %q", tool)
			}
		}
	}
	return level, perms, nil
}

// LoadPolicyEntry builds the caller's PolicyEntry for production wiring. An
// empty path keeps the local default (elevated, no per-tool overrides). A set
// path that cannot be read or parsed is an error so the server fails closed
// instead of silently dropping the operator's denials.
func LoadPolicyEntry(clientID, path string) (PolicyEntry, error) {
	e := PolicyEntry{ClientID: clientID, Level: TrustElevated}
	if path == "" {
		return e, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return PolicyEntry{}, fmt.Errorf("policy file: %w", err)
	}
	level, perms, err := parsePolicyFile(raw)
	if err != nil {
		return PolicyEntry{}, fmt.Errorf("policy file %s: %w", path, err)
	}
	if level != nil {
		if *level < TrustNone || *level > TrustElevated {
			return PolicyEntry{}, fmt.Errorf("policy file %s: level %d out of range 0..3", path, *level)
		}
		e.Level = *level
	}
	e.ToolPerms = perms
	return e, nil
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
	// Unknown tools are denied before overrides so no permission can invent one.
	min, known := toolMinTrust[tool]
	if !known {
		return fmt.Errorf("%w: tool %q not configured", ErrPolicyDenied, tool)
	}
	if allow, explicit := e.ToolPerms[tool]; explicit {
		if allow {
			return nil
		}
		return fmt.Errorf("%w: tool %q explicitly denied", ErrPolicyDenied, tool)
	}
	if e.Level < min {
		return fmt.Errorf("%w: tool %q needs trust level %d, caller has %d", ErrPolicyDenied, tool, min, e.Level)
	}
	return nil
}
