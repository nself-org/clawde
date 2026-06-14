// Package server — GatewayServiceServer implementation.
//
// Purpose: Implement each RPC by delegating to the gateway Router + Failover
//          chain (internal/gateway/router.go + failover.go). RouteRequest
//          resolves ordered candidates from the Registry; WithFailover tries
//          providers in priority order with a 2s budget, moving to the next on
//          HTTP 429. Maps gateway types to wire types and back.
// Inputs:  gRPC request messages; *gateway.Registry; []gateway.Provider (health).
// Outputs: gRPC response messages; typed errors as gRPC Status.
// Constraints: No raw model strings exposed. No provider credentials logged.
//              All errors mapped to GatewayError envelopes.
//              registry may be nil (graceful degradation via primary() fallback).
// SPORT: REGISTRY-ENDPOINTS.md — gRPC 8090 RPCs.
package server

import (
	"context"
	"fmt"

	"github.com/nself-org/clawde/intelligence/internal/compiler"
	gw "github.com/nself-org/clawde/intelligence/internal/gateway"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// gatewayHandler implements GatewayServiceServer, routing requests through the
// full gateway.Registry → RouteRequest → WithFailover chain when a registry is
// available. Falls back to primary() (providers[0]) when registry is nil, so
// the server can start without a registry yaml (health-check-only mode).
type gatewayHandler struct {
	// UnimplementedGatewayServiceServer provides forward compatibility for
	// GatewayServiceServer; required by protoc-gen-go-grpc generated interface.
	UnimplementedGatewayServiceServer
	providers []gw.Provider
	registry  *gw.Registry // may be nil; nil → primary()-only fallback
	health    *healthHandler
	// compiler is the auto-context compiler. May be nil; when nil,
	// CompileContext returns an unenriched response (graceful degradation).
	compiler *compiler.Compiler
	// ingestor handles IngestDocURL (part of GatewayServiceServer since codegen).
	ingestor *docIngestHandler
}

// primary returns the first provider or an error if the list is empty.
func (g *gatewayHandler) primary() (gw.Provider, error) {
	if len(g.providers) == 0 {
		return nil, status.Error(codes.Unavailable, "no providers configured")
	}
	return g.providers[0], nil
}

// Complete implements GatewayServiceServer.Complete.
//
// Routing: when g.registry is non-nil, RouteRequest resolves ordered
// candidates and WithFailover tries them in priority order (2s wall-clock
// budget; skips to next on rate_limit). When registry is nil, falls back to
// primary() (providers[0]) for backward compatibility.
func (g *gatewayHandler) Complete(ctx context.Context, req *CompleteRequest) (*CompleteResponse, error) {
	msgs := make([]gw.Message, len(req.Messages))
	for i, m := range req.Messages {
		if m != nil {
			msgs[i] = gw.Message{Role: m.Role, Content: m.Content}
		}
	}
	laneReq := gw.LaneRequest{
		Lane:          gw.Lane(req.Lane),
		Messages:      msgs,
		SystemPrompt:  req.SystemPrompt,
		MaxTokens:     int(req.MaxTokens),
		WorkspaceID:   req.WorkspaceId,
		RequestID:     req.RequestId,
		Images:        req.Images,
		ImageMimeType: req.ImageMimeType,
	}

	if g.registry != nil {
		entries, err := gw.RouteRequest(ctx, g.registry, laneReq)
		if err != nil {
			return nil, mapGWError(err)
		}
		result, err := gw.WithFailover(ctx, entries, laneReq)
		if err != nil {
			return nil, mapGWError(err)
		}
		resp := result.Response
		return &CompleteResponse{
			Content:      resp.Content,
			InputTokens:  int32(resp.InputTokens),
			OutputTokens: int32(resp.OutputTokens),
			Enriched:     result.Enriched,
			Provider:     result.ProviderUsed,
			Model:        resp.Model,
		}, nil
	}

	// Registry-nil fallback: delegate to primary provider (health-check-only mode).
	p, err := g.primary()
	if err != nil {
		return nil, err
	}
	resp, err := p.Complete(ctx, laneReq)
	if err != nil {
		return nil, mapGWError(err)
	}
	return &CompleteResponse{
		Content:      resp.Content,
		InputTokens:  int32(resp.InputTokens),
		OutputTokens: int32(resp.OutputTokens),
		Enriched:     resp.Enriched,
		Provider:     resp.Provider,
		Model:        resp.Model,
	}, nil
}

// StreamComplete implements GatewayServiceServer.StreamComplete.
//
// Streaming path — primary()-only (no registry failover for streaming):
// WithFailover is designed for unary calls (Complete/Embed) where the entire
// response can be retried atomically. Streaming chunks are written incrementally
// to the client; once the first chunk is sent, the stream cannot be rewound for
// a retry on a different provider without breaking the wire protocol.
//
// TODO(T-E1-W3-S8-T13): Implement streaming failover — options include
//   (a) pre-flight unary probe via Complete before opening the stream, or
//   (b) buffering the first chunk before committing the stream to the client.
// Until then, StreamComplete falls back to primary() (providers[0]) or the
// first registry-resolved entry. This is an explicit design boundary, not a bug.
func (g *gatewayHandler) StreamComplete(req *StreamCompleteRequest, stream GatewayService_StreamCompleteServer) error {
	// Streaming uses primary() regardless of registry state; see doc above.
	p, err := g.primary()
	if err != nil {
		return err
	}
	msgs := make([]gw.Message, len(req.Messages))
	for i, m := range req.Messages {
		if m != nil {
			msgs[i] = gw.Message{Role: m.Role, Content: m.Content}
		}
	}
	laneReq := gw.LaneRequest{
		Lane:         gw.Lane(req.Lane),
		Messages:     msgs,
		SystemPrompt: req.SystemPrompt,
		MaxTokens:    int(req.MaxTokens),
		WorkspaceID:  req.WorkspaceId,
		RequestID:    req.RequestId,
	}
	ch, err := p.Stream(stream.Context(), laneReq)
	if err != nil {
		return mapGWError(err)
	}
	for chunk := range ch {
		msg := &StreamChunk{Delta: chunk.Delta, Done: chunk.Done}
		if chunk.Err != nil {
			msg.Error = &GatewayError{
				Code:    "upstream",
				Message: chunk.Err.Error(),
			}
		}
		if err := stream.Send(msg); err != nil {
			return err
		}
		if chunk.Done {
			break
		}
	}
	return nil
}

// Embed implements GatewayServiceServer.Embed.
//
// Routing: same RouteRequest → WithFailover chain as Complete() when registry
// is non-nil. WithFailover calls p.Complete() internally; embedding requests
// are routed via the embedding lane which maps to providers that support Embed.
// When registry is nil, falls back to primary() for backward compatibility.
//
// Note: WithFailover invokes p.Complete() on each candidate. For embedding, the
// caller must use a lane (e.g. LaneEmbedding) whose registry entries resolve to
// providers that implement Embed. The Embed call below is used in the nil-registry
// fallback path and in the direct-primary path; the registry path lets WithFailover
// select the provider and we call Embed on it directly after resolution.
func (g *gatewayHandler) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
	if g.registry != nil {
		laneReq := gw.LaneRequest{
			Lane:        gw.LaneEmbedding,
			Text:        req.Text,
			ExpectedDim: int(req.ExpectedDim),
			WorkspaceID: req.WorkspaceId,
		}
		entries, err := gw.RouteRequest(ctx, g.registry, laneReq)
		if err != nil {
			return nil, mapGWError(err)
		}
		// WithFailover uses p.Complete() internally; for embedding we need p.Embed().
		// Build provider from the first routed entry and call Embed directly,
		// iterating fallbacks manually on failure (mirrors WithFailover's retry logic).
		var lastErr error
		for _, entry := range entries {
			p, buildErr := gw.BuildProvider(entry)
			if buildErr != nil {
				lastErr = buildErr
				continue
			}
			vec, embedErr := p.Embed(ctx, req.Text, int(req.ExpectedDim))
			if embedErr != nil {
				lastErr = embedErr
				continue
			}
			return &EmbedResponse{
				Embedding: vec,
				Provider:  entry.Provider,
			}, nil
		}
		if lastErr != nil {
			return nil, mapGWError(lastErr)
		}
		return nil, mapGWError(&gw.GatewayError{
			Lane: gw.LaneEmbedding, Code: "lane_unavailable",
		})
	}

	// Registry-nil fallback: delegate to primary provider.
	p, err := g.primary()
	if err != nil {
		return nil, err
	}
	vec, err := p.Embed(ctx, req.Text, int(req.ExpectedDim))
	if err != nil {
		return nil, mapGWError(err)
	}
	return &EmbedResponse{
		Embedding: vec,
		Provider:  p.Name(),
	}, nil
}

// Rerank implements GatewayServiceServer.Rerank.
func (g *gatewayHandler) Rerank(ctx context.Context, req *RerankRequest) (*RerankResponse, error) {
	p, err := g.primary()
	if err != nil {
		return nil, err
	}
	indices, err := p.Rerank(ctx, req.Query, req.Documents, int(req.TopN))
	if err != nil {
		return nil, mapGWError(err)
	}
	ranked := make([]int32, len(indices))
	for i, idx := range indices {
		ranked[i] = int32(idx)
	}
	return &RerankResponse{
		RankedIndices: ranked,
		Provider:      p.Name(),
	}, nil
}

// Health implements GatewayServiceServer.Health.
func (g *gatewayHandler) Health(ctx context.Context, _ *HealthRequest) (*HealthResponse, error) {
	return g.health.check(ctx), nil
}

// CompileContext implements GatewayServiceServer.CompileContext. It delegates to
// the auto-context compiler, which routes through the ADR-003 dispatch chain
// (PolicyEngine) before retrieval. A nil compiler yields an unenriched response.
func (g *gatewayHandler) CompileContext(ctx context.Context, req *CompileContextRequest) (*CompileContextResponse, error) {
	if g.compiler == nil {
		return &CompileContextResponse{Enriched: false}, nil
	}
	var sigs compiler.SessionSignals
	if req.Signals != nil {
		sigs = compiler.SessionSignals{
			ActiveFilePath: req.Signals.ActiveFilePath,
			VisibleSymbols: req.Signals.VisibleSymbols,
			RecentDiff:     req.Signals.RecentDiff,
			LastError:      req.Signals.LastError,
		}
	}
	resp, err := g.compiler.CompileContext(ctx, compiler.CompileContextRequest{
		WorkspaceID: req.WorkspaceId,
		Signals:     sigs,
	})
	if err != nil {
		// Graceful degradation: report unenriched, never abort the host turn.
		return &CompileContextResponse{Enriched: false}, nil
	}
	return &CompileContextResponse{
		ContextBlock: resp.ContextBlock,
		Enriched:     resp.Enriched,
		TokenCount:   int32(resp.TokenCount),
		CacheHit:     resp.CacheHit,
	}, nil
}

// IngestDocURL implements GatewayServiceServer.IngestDocURL. Delegates to the
// docIngestHandler which runs the ADR-003 dispatch chain and enqueues chunks.
// A nil ingestor returns codes.Unavailable (graceful degradation per ADR-001).
func (g *gatewayHandler) IngestDocURL(ctx context.Context, req *IngestDocURLRequest) (*IngestDocURLResponse, error) {
	if g.ingestor == nil {
		return nil, status.Error(codes.Unavailable, "doc ingestion not configured")
	}
	return g.ingestor.IngestDocURL(ctx, req)
}

// mapGWError converts a gateway.GatewayError (or any error) to a gRPC Status.
func mapGWError(err error) error {
	if err == nil {
		return nil
	}
	var gwErr *gw.GatewayError
	if e, ok := err.(*gw.GatewayError); ok {
		gwErr = e
	}
	if gwErr == nil {
		return status.Errorf(codes.Internal, "upstream error: %v", err)
	}
	var code codes.Code
	var envelope string
	switch gwErr.Code {
	case "rate_limit":
		code = codes.ResourceExhausted
		envelope = fmt.Sprintf(`{"code":"RATE_LIMIT","message":"%s","provider_error":"","retry_after":0}`, gwErr.Error())
	case "auth":
		code = codes.Unauthenticated
		envelope = fmt.Sprintf(`{"code":"AUTH_FAILED","message":"%s"}`, gwErr.Error())
	case "timeout":
		code = codes.DeadlineExceeded
		envelope = fmt.Sprintf(`{"code":"TIMEOUT","message":"%s"}`, gwErr.Error())
	case "config":
		code = codes.FailedPrecondition
		envelope = fmt.Sprintf(`{"code":"CONFIG_ERROR","message":"%s"}`, gwErr.Error())
	default:
		code = codes.Internal
		envelope = fmt.Sprintf(`{"code":"UPSTREAM","message":"%s"}`, gwErr.Error())
	}
	return status.Errorf(code, "%s", envelope)
}
