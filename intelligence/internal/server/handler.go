// Package server — GatewayServiceServer implementation.
//
// Purpose: Implement each RPC by delegating to internal/gateway Provider
//          methods. Maps gateway types to wire types and back.
// Inputs:  gRPC request messages; gateway.Provider implementations.
// Outputs: gRPC response messages; typed errors as gRPC Status.
// Constraints: No raw model strings exposed. No provider credentials logged.
//              All errors mapped to GatewayErrorMsg envelopes.
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

// gatewayHandler implements GatewayServiceServer, delegating to the first
// available provider. Future work: integrate the full router/failover from
// internal/gateway/router.go and internal/gateway/failover.go.
type gatewayHandler struct {
	providers []gw.Provider
	health    *healthHandler
	// compiler is the auto-context compiler. May be nil; when nil,
	// CompileContext returns an unenriched response (graceful degradation).
	compiler *compiler.Compiler
}

// primary returns the first provider or an error if the list is empty.
func (g *gatewayHandler) primary() (gw.Provider, error) {
	if len(g.providers) == 0 {
		return nil, status.Error(codes.Unavailable, "no providers configured")
	}
	return g.providers[0], nil
}

// Complete implements GatewayServiceServer.Complete.
func (g *gatewayHandler) Complete(ctx context.Context, req *CompleteRequest) (*CompleteResponse, error) {
	p, err := g.primary()
	if err != nil {
		return nil, err
	}
	msgs := make([]gw.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = gw.Message{Role: m.Role, Content: m.Content}
	}
	laneReq := gw.LaneRequest{
		Lane:          gw.Lane(req.Lane),
		Messages:      msgs,
		SystemPrompt:  req.SystemPrompt,
		MaxTokens:     int(req.MaxTokens),
		WorkspaceID:   req.WorkspaceID,
		RequestID:     req.RequestID,
		Images:        req.Images,
		ImageMimeType: req.ImageMimeType,
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
func (g *gatewayHandler) StreamComplete(req *StreamCompleteRequest, stream GatewayService_StreamCompleteServer) error {
	p, err := g.primary()
	if err != nil {
		return err
	}
	msgs := make([]gw.Message, len(req.Messages))
	for i, m := range req.Messages {
		msgs[i] = gw.Message{Role: m.Role, Content: m.Content}
	}
	laneReq := gw.LaneRequest{
		Lane:         gw.Lane(req.Lane),
		Messages:     msgs,
		SystemPrompt: req.SystemPrompt,
		MaxTokens:    int(req.MaxTokens),
		WorkspaceID:  req.WorkspaceID,
		RequestID:    req.RequestID,
	}
	ch, err := p.Stream(stream.Context(), laneReq)
	if err != nil {
		return mapGWError(err)
	}
	for chunk := range ch {
		msg := &StreamChunkMsg{Delta: chunk.Delta, Done: chunk.Done}
		if chunk.Err != nil {
			msg.Error = &GatewayErrorMsg{
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
func (g *gatewayHandler) Embed(ctx context.Context, req *EmbedRequest) (*EmbedResponse, error) {
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
	resp, err := g.compiler.CompileContext(ctx, compiler.CompileContextRequest{
		WorkspaceID: req.WorkspaceID,
		Signals: compiler.SessionSignals{
			ActiveFilePath: req.Signals.ActiveFilePath,
			VisibleSymbols: req.Signals.VisibleSymbols,
			RecentDiff:     req.Signals.RecentDiff,
			LastError:      req.Signals.LastError,
		},
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
