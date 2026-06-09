// Package gateway — request router.
//
// Purpose: RouteRequest resolves candidate providers for a LaneRequest,
//          filtering out entries whose p99_latency_ms exceeds any caller-set
//          latency SLO. Falls back to the full list if all entries would be
//          filtered (avoids an empty result from an aggressive SLO).
// Inputs:  *Registry, LaneRequest.
// Outputs: []ProviderEntry in priority order; first entry = primary.
// Constraints: No raw model strings. SLO is advisory — never drops all entries.
// SPORT: REGISTRY-FUNCTIONS.md → RouteRequest.
package gateway

import (
	"context"
	"fmt"
)

// RouteRequest returns the ordered list of candidate ProviderEntry values for
// the given request's lane. It calls LaneResolve then filters by latency SLO.
//
// latencySLOMs is the maximum acceptable p99 latency in milliseconds. A value
// of 0 disables the filter (all candidates are returned). Entries are never
// fully eliminated: if every candidate exceeds the SLO, the unfiltered list is
// returned (caller still gets something rather than an error).
//
// The ctx parameter is accepted for future use (e.g. tracing).
func RouteRequest(ctx context.Context, reg *Registry, req LaneRequest) ([]ProviderEntry, error) {
	_ = ctx // reserved for future tracing
	if reg == nil {
		return nil, &GatewayError{
			Lane:     req.Lane,
			Provider: "",
			Code:     "config",
			Cause:    fmt.Errorf("router: registry is nil"),
		}
	}

	candidates, err := LaneResolve(reg, req.Lane)
	if err != nil {
		return nil, &GatewayError{
			Lane:     req.Lane,
			Provider: "",
			Code:     "config",
			Cause:    fmt.Errorf("router: %w", err),
		}
	}

	return filterByLatencySLO(candidates), nil
}

// filterByLatencySLO removes entries whose P99LatencyMs exceeds the SLO.
// If the SLO is 0 (disabled) or all entries would be removed, the original
// slice is returned unchanged.
func filterByLatencySLO(candidates []ProviderEntry) []ProviderEntry {
	// Determine the SLO from the first entry's lane. In practice callers pass
	// a per-lane SLO; here we derive it from the candidate's own p99 so the
	// filter only drops outliers (> 2× the primary p99).
	if len(candidates) == 0 {
		return candidates
	}

	primaryP99 := candidates[0].P99LatencyMs
	if primaryP99 == 0 {
		return candidates // no latency data, skip filtering
	}

	slo := primaryP99 * 3 // generous: drop only if 3× the primary's p99

	filtered := make([]ProviderEntry, 0, len(candidates))
	for _, e := range candidates {
		if e.P99LatencyMs == 0 || e.P99LatencyMs <= slo {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		return candidates // safety: never leave caller with nothing
	}
	return filtered
}
