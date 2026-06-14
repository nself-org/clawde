// grpc_client.go — gRPC client adapter for GatewayService.Complete.
//
// Purpose: Provide a GRPCGatewayClient that wraps a *grpc.ClientConn and satisfies
//          the orchestration.GatewayClient interface (Complete only). The worker uses
//          this to call the clawde-intelligence gRPC server for LLM completions from
//          within Temporal activities, keeping the activity layer decoupled from the
//          full gateway.Provider interface (embed/rerank/stream not needed there).
//
// Inputs:  *grpc.ClientConn (pre-dialled by cmd/worker/main.go from CLAWDE_GRPC_ADDR).
// Outputs: *LaneResponse mapped from the wire CompleteResponse.
// Constraints: Complete-only — embed/stream/rerank go through a different path.
//              TLS is not enforced here; cmd/worker uses insecure.NewCredentials()
//              for localhost-to-localhost communication. Add TLS wrapper when
//              the gRPC server moves to a remote host.
//
// SPORT: REGISTRY-FUNCTIONS.md → gateway.GRPCGatewayClient.
package gateway

import (
	"context"
	"encoding/json"
	"fmt"

	"google.golang.org/grpc"
)

// wireCompleteRequest is the on-wire shape for /gateway.v1.GatewayService/Complete.
// Mirrors server.CompleteRequest without importing the server package.
type wireCompleteRequest struct {
	Lane         string         `json:"lane"`
	Messages     []wireMessage  `json:"messages"`
	SystemPrompt string         `json:"system_prompt,omitempty"`
	MaxTokens    int32          `json:"max_tokens,omitempty"`
	WorkspaceID  string         `json:"workspace_id,omitempty"`
	RequestID    string         `json:"request_id,omitempty"`
}

// wireMessage is a single chat turn on the wire.
type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// wireCompleteResponse is the on-wire shape returned by Complete.
type wireCompleteResponse struct {
	Content      string `json:"content"`
	InputTokens  int32  `json:"input_tokens,omitempty"`
	OutputTokens int32  `json:"output_tokens,omitempty"`
	Enriched     bool   `json:"enriched,omitempty"`
	Provider     string `json:"provider,omitempty"`
	Model        string `json:"model,omitempty"`
}

// GRPCGatewayClient calls GatewayService.Complete over an existing gRPC connection.
//
// Purpose: Seam that lets the Temporal worker call the clawde-intelligence gRPC
//          server for LLM completions without importing the server package or
//          requiring protoc-generated stubs. Uses grpc.ClientConn.Invoke directly.
type GRPCGatewayClient struct {
	conn *grpc.ClientConn
}

// NewGRPCGatewayClient wraps a pre-dialled *grpc.ClientConn.
// The caller is responsible for closing the connection.
func NewGRPCGatewayClient(conn *grpc.ClientConn) *GRPCGatewayClient {
	return &GRPCGatewayClient{conn: conn}
}

// Complete calls /gateway.v1.GatewayService/Complete and maps the result to
// a *LaneResponse. Request and response are marshalled/unmarshalled as JSON
// (codec="json") — the gRPC server must use the JSON codec or grpc-gateway.
//
// Inputs:  ctx (carries deadline/cancel), LaneRequest with lane + messages.
// Outputs: *LaneResponse; error on network, codec, or upstream failure.
func (c *GRPCGatewayClient) Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error) {
	msgs := make([]wireMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msgs = append(msgs, wireMessage{Role: m.Role, Content: m.Content})
	}

	wireReq := wireCompleteRequest{
		Lane:         string(req.Lane),
		Messages:     msgs,
		SystemPrompt: req.SystemPrompt,
		MaxTokens:    int32(req.MaxTokens), //nolint:gosec // bounded by caller
		WorkspaceID:  req.WorkspaceID,
		RequestID:    req.RequestID,
	}

	reqBytes, err := json.Marshal(wireReq)
	if err != nil {
		return nil, fmt.Errorf("grpc_gateway_client: marshal request: %w", err)
	}

	var respBytes []byte
	if err := c.conn.Invoke(ctx, "/gateway.v1.GatewayService/Complete", reqBytes, &respBytes); err != nil {
		return nil, fmt.Errorf("grpc_gateway_client: invoke Complete: %w", err)
	}

	var wireResp wireCompleteResponse
	if err := json.Unmarshal(respBytes, &wireResp); err != nil {
		return nil, fmt.Errorf("grpc_gateway_client: unmarshal response: %w", err)
	}

	return &LaneResponse{
		Content:      wireResp.Content,
		InputTokens:  int(wireResp.InputTokens),
		OutputTokens: int(wireResp.OutputTokens),
		Enriched:     wireResp.Enriched,
		Provider:     wireResp.Provider,
		Model:        wireResp.Model,
	}, nil
}
