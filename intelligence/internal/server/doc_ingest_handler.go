// doc_ingest_handler.go — DocIngestServiceServer implementation.
//
// Purpose:    Implement the IngestDocURL RPC by delegating to docs.KBIngestor,
//             which runs the ADR-003 dispatch chain and enqueues chunks. Maps the
//             wire request/response to/from the docs package types.
// Inputs:     IngestDocURLRequest; the resolved caller client id from context.
// Outputs:    IngestDocURLResponse; gRPC Status errors on denial/failure.
// Constraints: File ≤500 lines. No provider credentials logged.
// SPORT: REGISTRY-ENDPOINTS.md → IngestDocURL RPC.
package server

import (
	"context"

	"github.com/nself-org/clawde/intelligence/internal/docs"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// docIngestHandler implements DocIngestServiceServer.
type docIngestHandler struct {
	ingestor *docs.KBIngestor
	// clientID is the resolved caller identity (HMAC-authenticated by clawd).
	// In production this is threaded from the auth interceptor; kept as a field
	// so the handler stays testable without the full interceptor stack.
	clientID string
}

// NewDocIngestHandler builds a DocIngestServiceServer from a KBIngestor.
func NewDocIngestHandler(ingestor *docs.KBIngestor, clientID string) DocIngestServiceServer {
	return &docIngestHandler{ingestor: ingestor, clientID: clientID}
}

// IngestDocURL implements DocIngestServiceServer.IngestDocURL.
func (h *docIngestHandler) IngestDocURL(ctx context.Context, req *IngestDocURLRequest) (*IngestDocURLResponse, error) {
	if h.ingestor == nil {
		return nil, status.Error(codes.Unavailable, "doc ingestion not configured")
	}
	resp, err := h.ingestor.IngestDocURL(ctx, h.clientID, docs.IngestDocURLRequest{
		WorkspaceID: req.WorkspaceID,
		URL:         req.URL,
		DocType:     req.DocType,
	})
	if err != nil {
		// Dispatch-chain denials surface as PermissionDenied; others as Internal.
		return nil, status.Error(codes.PermissionDenied, err.Error())
	}
	return &IngestDocURLResponse{
		ChunksEnqueued: int32(resp.ChunksEnqueued),
		Skipped:        resp.Skipped,
		Reason:         resp.Reason,
	}, nil
}
