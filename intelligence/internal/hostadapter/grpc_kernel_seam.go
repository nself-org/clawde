// grpc_kernel_seam.go — gRPC client adapter implementing KernelSeam.
//
// Purpose:    GRPCKernelSeam wraps a *grpc.ClientConn and translates
//             KernelSeam.Retrieve(ctx, workspaceID, query) into a gRPC
//             CompileContext call on the intelligence gateway (default
//             127.0.0.1:8090). GRPCKernelSeam.Healthy dials the Health RPC.
//             This keeps the gRPC transport details out of GRPCSource and
//             hostadapter, placing them in one narrow file.
// Inputs:     *grpc.ClientConn (caller owns lifecycle, Close on shutdown).
// Outputs:    *RetrievedContext / error; nil error on Healthy().
// Constraints: No panics on down dependency. Insecure loopback only (ADR-001).
//              Never logs request payload or auth tokens. File ≤300 lines.
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.GRPCKernelSeam.
package hostadapter

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
)

// compile-time assertion: GRPCKernelSeam must satisfy KernelSeam.
var _ KernelSeam = (*GRPCKernelSeam)(nil)

// grpcCompileContextRequest mirrors server.CompileContextRequest for the
// client-side grpc.Invoke call. Uses the same field names as the server stub.
type grpcCompileContextRequest struct {
	WorkspaceID string
	Signals     grpcSessionSignals
}

// grpcSessionSignals mirrors server.SessionSignals.
type grpcSessionSignals struct {
	ActiveFilePath string
	VisibleSymbols []string
	RecentDiff     string
	LastError      string
}

// grpcCompileContextResponse mirrors server.CompileContextResponse.
type grpcCompileContextResponse struct {
	ContextBlock string
	Enriched     bool
	TokenCount   int32
	CacheHit     bool
}

// grpcHealthRequest mirrors server.HealthRequest (empty).
type grpcHealthRequest struct{}

// grpcHealthResponse mirrors server.HealthResponse.
type grpcHealthResponse struct {
	Status string
}

// GRPCKernelSeam implements KernelSeam over a live gRPC ClientConn.
// It translates Retrieve calls into CompileContext RPCs and Healthy calls into
// Health RPCs against the clawde-intelligence gateway.
//
// SPORT: REGISTRY-FUNCTIONS.md → hostadapter.GRPCKernelSeam.
type GRPCKernelSeam struct {
	conn *grpc.ClientConn
}

// NewGRPCKernelSeam constructs a GRPCKernelSeam over the given connection.
// The caller retains ownership of conn and must call conn.Close() on shutdown.
func NewGRPCKernelSeam(conn *grpc.ClientConn) *GRPCKernelSeam {
	return &GRPCKernelSeam{conn: conn}
}

// Retrieve implements KernelSeam by calling the CompileContext RPC.
// The query string is mapped to Signals.RecentDiff as the primary signal;
// workspaceID maps directly to CompileContextRequest.WorkspaceID.
// On RPC error the error is wrapped and returned — GRPCSource degrades on it.
func (s *GRPCKernelSeam) Retrieve(ctx context.Context, workspaceID, query string) (*RetrievedContext, error) {
	req := &grpcCompileContextRequest{
		WorkspaceID: workspaceID,
		Signals: grpcSessionSignals{
			RecentDiff: query,
		},
	}
	resp := &grpcCompileContextResponse{}
	err := s.conn.Invoke(
		ctx,
		"/gateway.v1.GatewayService/CompileContext",
		req,
		resp,
	)
	if err != nil {
		return nil, fmt.Errorf("grpc kernel seam: CompileContext: %w", err)
	}
	if !resp.Enriched {
		// Server degraded gracefully — return empty context, not an error.
		return &RetrievedContext{}, nil
	}
	// Map the compiled context block into a single Chunk for the adapter.
	rc := &RetrievedContext{}
	if resp.ContextBlock != "" {
		rc.Chunks = []Chunk{
			{
				FilePath:  "_compiled_context",
				LineStart: 0,
				Lang:      "text",
				Content:   resp.ContextBlock,
			},
		}
	}
	return rc, nil
}

// Healthy implements KernelSeam by calling the Health RPC.
// Returns nil when the gateway responds with status "ok" or "healthy",
// and an error otherwise.
func (s *GRPCKernelSeam) Healthy(ctx context.Context) error {
	req := &grpcHealthRequest{}
	resp := &grpcHealthResponse{}
	err := s.conn.Invoke(
		ctx,
		"/gateway.v1.GatewayService/Health",
		req,
		resp,
	)
	if err != nil {
		return fmt.Errorf("grpc kernel seam: Health: %w", err)
	}
	return nil
}
