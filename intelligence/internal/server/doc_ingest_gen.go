// doc_ingest_gen.go — hand-written gRPC stub for the IngestDocURL RPC.
//
// Purpose:    Mirror the gateway_grpc_gen.go pattern for the W14-T04 IngestDocURL
//             RPC without modifying the existing GatewayServiceServer interface
//             (which would force every implementer to add a method). IngestDocURL
//             is registered as its own optional DocIngestService.
// Inputs:     IngestDocURLRequest wire message.
// Outputs:    IngestDocURLResponse wire message.
// Constraints: No protoc available — hand-written equivalent. File ≤500 lines.
//             Run `make proto` once codegen tools are installed to regenerate.
// SPORT: REGISTRY-ENDPOINTS.md → IngestDocURL RPC (gRPC 8090 / REST 8091).
package server

import (
	"context"

	"google.golang.org/grpc"
)

// ---- Wire messages ----

// IngestDocURLRequest carries the workspace, URL, and doc_type to ingest.
type IngestDocURLRequest struct {
	WorkspaceID string
	URL         string
	DocType     string
}

// IngestDocURLResponse carries the enqueue count + skip metadata.
type IngestDocURLResponse struct {
	ChunksEnqueued int32
	Skipped        bool
	Reason         string
}

// ---- gRPC service interface ----

// DocIngestServiceServer is the server-side interface for the IngestDocURL RPC.
type DocIngestServiceServer interface {
	IngestDocURL(context.Context, *IngestDocURLRequest) (*IngestDocURLResponse, error)
}

// DocIngestServiceDesc is the grpc.ServiceDesc for DocIngestService.
var DocIngestServiceDesc = grpc.ServiceDesc{
	ServiceName: "gateway.v1.DocIngestService",
	HandlerType: (*DocIngestServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "IngestDocURL",
			Handler:    _DocIngestService_IngestDocURL_Handler,
		},
	},
	Streams:  []grpc.StreamDesc{},
	Metadata: "gateway.proto",
}

// RegisterDocIngestServiceServer registers the IngestDocURL implementation.
func RegisterDocIngestServiceServer(s grpc.ServiceRegistrar, srv DocIngestServiceServer) {
	s.RegisterService(&DocIngestServiceDesc, srv)
}

func _DocIngestService_IngestDocURL_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(IngestDocURLRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(DocIngestServiceServer).IngestDocURL(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/gateway.v1.DocIngestService/IngestDocURL"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(DocIngestServiceServer).IngestDocURL(ctx, req.(*IngestDocURLRequest))
	}
	return interceptor(ctx, in, info, handler)
}
