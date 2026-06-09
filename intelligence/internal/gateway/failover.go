// Package gateway — provider failover executor.
//
// Purpose: Try providers in fallback order. On 429 (rate-limited) from the
//          primary, attempt the next provider. The total wall-clock budget is
//          2 seconds. On success, sets LaneResponse.Provider and Enriched=true.
//          Exhausted / timed-out → LaneResponse{Enriched:false} + LANE_UNAVAILABLE.
// Inputs:  []ProviderEntry (in priority order), LaneRequest, 2s deadline.
// Outputs: *LaneResponse (Enriched=true on success) or error.
// Constraints: LANE_UNAVAILABLE code signals all providers exhausted. Timeout
//              uses context.WithTimeout(parent, 2s) per P1-CANONICAL-MAPS.
// SPORT: REGISTRY-FUNCTIONS.md → WithFailover.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const failoverTotalTimeout = 2 * time.Second

// FailoverResult is returned by WithFailover.
type FailoverResult struct {
	Response     *LaneResponse
	ProviderUsed string // name of the provider that succeeded
	Enriched     bool   // true if a provider returned a successful response
}

// WithFailover tries each provider in entries order within a 2-second wall-clock
// budget. On HTTP 429 (rate_limit GatewayError code) it moves to the next
// provider immediately. Other errors are treated as fatal for that provider.
//
// On success: result.Enriched=true, result.ProviderUsed=winner.
// On total failure: result.Enriched=false, error has Code="lane_unavailable".
func WithFailover(ctx context.Context, entries []ProviderEntry, req LaneRequest) (*FailoverResult, error) {
	if len(entries) == 0 {
		return &FailoverResult{Enriched: false}, &GatewayError{
			Lane: req.Lane, Provider: "", Code: "lane_unavailable",
			Cause: fmt.Errorf("failover: no providers configured"),
		}
	}

	deadline := time.Now().Add(failoverTotalTimeout)
	fCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	var lastErr error
	for _, entry := range entries {
		if time.Now().After(deadline) {
			lastErr = fmt.Errorf("failover: 2s wall-clock budget exhausted")
			break
		}

		p, err := BuildProvider(entry)
		if err != nil {
			lastErr = err
			continue
		}

		resp, err := p.Complete(fCtx, req)
		if err == nil {
			// Success — annotate response.
			resp.Provider = entry.Provider
			resp.Model = entry.Model
			return &FailoverResult{
				Response:     resp,
				ProviderUsed: entry.Provider,
				Enriched:     true,
			}, nil
		}

		// Only retry on rate-limit; treat other errors as provider-fatal.
		var gwErr *GatewayError
		if errors.As(err, &gwErr) && gwErr.Code == "rate_limit" {
			lastErr = err
			continue
		}
		lastErr = err
		// Non-rate-limit error: still try fallbacks for resilience.
		continue
	}

	return &FailoverResult{Enriched: false}, &GatewayError{
		Lane:     req.Lane,
		Provider: "",
		Code:     "lane_unavailable",
		Cause:    fmt.Errorf("failover: all providers exhausted or timed out: %w", lastErr),
	}
}
