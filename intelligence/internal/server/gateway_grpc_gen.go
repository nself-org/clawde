// Package server — hand-written gRPC stubs for GatewayService.
//
// Purpose: Provide compilable gRPC service types mirroring proto/gateway.proto
//          without requiring protoc-gen-go / protoc-gen-go-grpc / grpc-gateway
//          plugins in the build environment. Replace this file with generated
//          output once `make proto` is runnable (see Makefile).
// Inputs:  gRPC request messages from clients.
// Outputs: gRPC response messages; streaming via grpc.ServerStream.
// Constraints: Binds only to 127.0.0.1. HMAC secret never logged.
// SPORT: REGISTRY-ENDPOINTS.md — gRPC 8090 RPCs; REGISTRY-SERVICES.md.
package server

import (
	"context"

	"google.golang.org/grpc"
)

// ---- Message types (mirrors gateway.proto) ----

// Message is a single chat turn.
type Message struct {
	Role    string
	Content string
}

// CompleteRequest carries completion call parameters.
// Fields 1-9 unchanged. Fields 10-11 added for the MULTIMODAL lane (W16-S16-T03).
type CompleteRequest struct {
	Lane          string
	Messages      []Message
	SystemPrompt  string
	MaxTokens     int32
	WorkspaceID   string
	RequestID     string
	// Fields 7-9 reserved.
	// Multimodal fields (proto fields 10-11):
	Images        [][]byte // raw image bytes; max 5 images, max 10 MB each
	ImageMimeType string   // shared MIME for all images: image/png|jpeg|gif|webp
}

// CompleteResponse carries completion results.
type CompleteResponse struct {
	Content      string
	InputTokens  int32
	OutputTokens int32
	Enriched     bool
	Provider     string
	Model        string
}

// StreamCompleteRequest carries streaming completion parameters.
type StreamCompleteRequest struct {
	Lane        string
	Messages    []Message
	SystemPrompt string
	MaxTokens   int32
	WorkspaceID string
	RequestID   string
}

// StreamChunkMsg is a single delta in a streaming response.
type StreamChunkMsg struct {
	Delta string
	Done  bool
	Error *GatewayErrorMsg
}

// GatewayErrorMsg is the wire-level error envelope.
type GatewayErrorMsg struct {
	Code          string
	Message       string
	ProviderError string
	RetryAfter    int64
}

// EmbedRequest carries embedding call parameters.
type EmbedRequest struct {
	Text        string
	ExpectedDim int32
	WorkspaceID string
	RequestID   string
}

// EmbedResponse carries embedding results.
type EmbedResponse struct {
	Embedding []float32
	Provider  string
	Model     string
}

// RerankRequest carries rerank call parameters.
type RerankRequest struct {
	Query       string
	Documents   []string
	TopN        int32
	WorkspaceID string
	RequestID   string
}

// RerankResponse carries ranked indices.
type RerankResponse struct {
	RankedIndices []int32
	Provider      string
	Model         string
}

// HealthRequest is the health check request (empty).
type HealthRequest struct{}

// HealthResponse carries aggregated provider health.
type HealthResponse struct {
	Status    string
	Providers []ProviderHealth
}

// ProviderHealth reports one provider's health status.
type ProviderHealth struct {
	Name      string
	Healthy   bool
	LatencyMs int64
}

// SessionSignals are raw editor signals for auto-context compilation.
type SessionSignals struct {
	ActiveFilePath string
	VisibleSymbols []string
	RecentDiff     string
	LastError      string
}

// CompileContextRequest carries the workspace and signals to enrich from.
type CompileContextRequest struct {
	WorkspaceID string
	Signals     SessionSignals
}

// CompileContextResponse carries the compiled context block + metadata.
type CompileContextResponse struct {
	ContextBlock string
	Enriched     bool
	TokenCount   int32
	CacheHit     bool
}

// ---- gRPC service interface ----

// GatewayServiceServer is the server-side interface for GatewayService.
type GatewayServiceServer interface {
	Complete(context.Context, *CompleteRequest) (*CompleteResponse, error)
	StreamComplete(*StreamCompleteRequest, GatewayService_StreamCompleteServer) error
	Embed(context.Context, *EmbedRequest) (*EmbedResponse, error)
	Rerank(context.Context, *RerankRequest) (*RerankResponse, error)
	Health(context.Context, *HealthRequest) (*HealthResponse, error)
	CompileContext(context.Context, *CompileContextRequest) (*CompileContextResponse, error)
}

// GatewayService_StreamCompleteServer is the server-stream interface for StreamComplete.
type GatewayService_StreamCompleteServer interface {
	Send(*StreamChunkMsg) error
	grpc.ServerStream
}

// gatewayServiceStreamCompleteServer wraps the raw grpc.ServerStream.
type gatewayServiceStreamCompleteServer struct {
	grpc.ServerStream
}

func (s *gatewayServiceStreamCompleteServer) Send(chunk *StreamChunkMsg) error {
	return s.ServerStream.SendMsg(chunk)
}

// ---- gRPC service registration ----

// GatewayServiceDesc is the grpc.ServiceDesc for GatewayService.
var GatewayServiceDesc = grpc.ServiceDesc{
	ServiceName: "gateway.v1.GatewayService",
	HandlerType: (*GatewayServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Complete",
			Handler:    _GatewayService_Complete_Handler,
		},
		{
			MethodName: "Embed",
			Handler:    _GatewayService_Embed_Handler,
		},
		{
			MethodName: "Rerank",
			Handler:    _GatewayService_Rerank_Handler,
		},
		{
			MethodName: "Health",
			Handler:    _GatewayService_Health_Handler,
		},
		{
			MethodName: "CompileContext",
			Handler:    _GatewayService_CompileContext_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "StreamComplete",
			Handler:       _GatewayService_StreamComplete_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "gateway.proto",
}

// RegisterGatewayServiceServer registers the service implementation.
func RegisterGatewayServiceServer(s grpc.ServiceRegistrar, srv GatewayServiceServer) {
	s.RegisterService(&GatewayServiceDesc, srv)
}

func _GatewayService_Complete_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CompleteRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServiceServer).Complete(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/gateway.v1.GatewayService/Complete"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServiceServer).Complete(ctx, req.(*CompleteRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _GatewayService_StreamComplete_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(StreamCompleteRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(GatewayServiceServer).StreamComplete(m, &gatewayServiceStreamCompleteServer{stream})
}

func _GatewayService_Embed_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(EmbedRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServiceServer).Embed(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/gateway.v1.GatewayService/Embed"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServiceServer).Embed(ctx, req.(*EmbedRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _GatewayService_Rerank_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(RerankRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServiceServer).Rerank(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/gateway.v1.GatewayService/Rerank"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServiceServer).Rerank(ctx, req.(*RerankRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _GatewayService_Health_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(HealthRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServiceServer).Health(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/gateway.v1.GatewayService/Health"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServiceServer).Health(ctx, req.(*HealthRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _GatewayService_CompileContext_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(CompileContextRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(GatewayServiceServer).CompileContext(ctx, in)
	}
	info := &grpc.UnaryServerInfo{Server: srv, FullMethod: "/gateway.v1.GatewayService/CompileContext"}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(GatewayServiceServer).CompileContext(ctx, req.(*CompileContextRequest))
	}
	return interceptor(ctx, in, info, handler)
}
