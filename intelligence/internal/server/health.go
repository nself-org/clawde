// Package server — Health handler for GatewayService.
//
// Purpose: Poll each registered Provider with a HealthCheck (1-token ping,
//          3s timeout) and return aggregated status. /health bypasses HMAC.
// Inputs:  HealthRequest (empty), list of gateway.Provider implementations.
// Outputs: HealthResponse{status, providers[{name, healthy, latency_ms}]}.
// Constraints: Timeout is 3s per provider (configurable via healthTimeout const).
//              Never logs provider credentials or model strings.
// SPORT: REGISTRY-ENDPOINTS.md — GET /v1/gateway/health.
package server

import (
	"context"
	"sync"
	"time"

	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
)

const healthTimeout = 3 * time.Second

// healthHandler wraps the Health RPC logic, shared between gRPC and HTTP.
type healthHandler struct {
	providers []gw.Provider
}

// newHealthHandler creates a healthHandler for the given providers.
func newHealthHandler(providers []gw.Provider) *healthHandler {
	return &healthHandler{providers: providers}
}

// check polls all providers in parallel and returns a HealthResponse.
func (h *healthHandler) check(ctx context.Context) *HealthResponse {
	type result struct {
		name      string
		healthy   bool
		latencyMs int64
	}

	results := make([]result, len(h.providers))
	var wg sync.WaitGroup

	for i, p := range h.providers {
		wg.Add(1)
		go func(idx int, provider gw.Provider) {
			defer wg.Done()
			tCtx, cancel := context.WithTimeout(ctx, healthTimeout)
			defer cancel()

			start := time.Now()
			err := provider.HealthCheck(tCtx)
			latency := time.Since(start).Milliseconds()

			results[idx] = result{
				name:      provider.Name(),
				healthy:   err == nil,
				latencyMs: latency,
			}
		}(i, p)
	}

	wg.Wait()

	resp := &HealthResponse{
		Status:    "ok",
		Providers: make([]ProviderHealth, len(results)),
	}
	for i, r := range results {
		resp.Providers[i] = ProviderHealth{
			Name:      r.name,
			Healthy:   r.healthy,
			LatencyMs: r.latencyMs,
		}
		if !r.healthy {
			resp.Status = "degraded"
		}
	}
	return resp
}
