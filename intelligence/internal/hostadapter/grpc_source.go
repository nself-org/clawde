// Package hostadapter — gRPC-backed ContextSource.
//
// Purpose:    GRPCSource adapts the in-process retrieval kernel seam into a
//             ContextSource. In production this is a thin wrapper over a gRPC
//             client dialing 127.0.0.1:8090; here it accepts any KernelSeam so
//             both the live kernel and a mock gRPC server satisfy it. ADR-001
//             graceful degradation is preserved: a nil/erroring seam surfaces an
//             error that OpenCodeAdapter degrades on (Enriched:false).
// Inputs:     KernelSeam.
// Outputs:    *RetrievedContext / error.
// Constraints: No panics on a down dependency. File ≤500 lines.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.GRPCSource.
package hostadapter

import (
	"context"
	"fmt"
)

// KernelSeam is the minimal retrieval surface GRPCSource depends on. The live
// HybridKernel.RetrieveContext (chunks+symbols) plus a findings provider, or a
// mock gRPC server, both satisfy this.
type KernelSeam interface {
	// Retrieve returns context for a workspace+query. Errors mean the
	// dependency is unreachable (the adapter degrades).
	Retrieve(ctx context.Context, workspaceID, query string) (*RetrievedContext, error)
	// Healthy reports dependency reachability.
	Healthy(ctx context.Context) error
}

// GRPCSource implements ContextSource over a KernelSeam.
type GRPCSource struct {
	seam KernelSeam
}

// NewGRPCSource builds a ContextSource over the given seam.
func NewGRPCSource(seam KernelSeam) *GRPCSource {
	return &GRPCSource{seam: seam}
}

// Retrieve implements ContextSource.
func (g *GRPCSource) Retrieve(ctx context.Context, workspaceID, query string) (*RetrievedContext, error) {
	if g.seam == nil {
		return nil, fmt.Errorf("grpc source: no kernel seam configured")
	}
	rc, err := g.seam.Retrieve(ctx, workspaceID, query)
	if err != nil {
		return nil, fmt.Errorf("grpc source: retrieve: %w", err)
	}
	return rc, nil
}

// Ping implements ContextSource.
func (g *GRPCSource) Ping(ctx context.Context) error {
	if g.seam == nil {
		return fmt.Errorf("grpc source: no kernel seam configured")
	}
	return g.seam.Healthy(ctx)
}

// compile-time assertion.
var _ ContextSource = (*GRPCSource)(nil)
