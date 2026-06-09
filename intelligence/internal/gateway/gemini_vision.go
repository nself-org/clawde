// Package gateway — Gemini Vision provider for the MULTIMODAL lane.
//
// Purpose: Handle vision+text completions using the Gemini generateContent REST API
//          (not the OpenAI-compat path, which cannot carry inline image data).
//          Validates image constraints, builds a multipart content payload, and
//          ensures text is the LAST part so model context ends with the question.
// Inputs:  LaneRequest with len(Images)>0; Images[i] = raw bytes; ImageMimeType shared.
// Outputs: LaneResponse with Content/InputTokens/OutputTokens.
// Constraints:
//   - Max 5 images per request → InvalidArgument GatewayError.
//   - Max 10 MB per image → InvalidArgument GatewayError.
//   - MIME must be in {image/png,image/jpeg,image/gif,image/webp} → InvalidArgument.
//   - Text part is ALWAYS last in the parts slice (model reads image context first).
//   - Reuses gcp_project_pool (ADR-006) — no new pool.
//   - Embed/Rerank delegate to nopEmbedder/nopReranker (not supported by vision models).
//   - Stream is not implemented (returns a typed GatewayError with Code="unsupported").
//
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence gateway / MULTIMODAL lane.
//        REGISTRY-FUNCTIONS.md → GeminiVisionProvider.
package gateway

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	maxImagesPerRequest = 5
	maxImageBytes       = 10 * 1024 * 1024 // 10 MB
)

// allowedMIMETypes is the set of MIME types accepted for inline images.
var allowedMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// GeminiVisionProvider calls the Gemini generateContent REST API directly.
// It is selected when the MULTIMODAL lane entry has provider="gemini-vision".
type GeminiVisionProvider struct {
	baseURL    string // e.g. "https://generativelanguage.googleapis.com/v1beta"
	apiKey     string
	model      string
	httpClient *http.Client
	nopEmbedder
	nopReranker
}

// NewGeminiVisionProvider constructs the vision provider from a ProviderEntry.
// baseURL is the Gemini REST base (without /models/...).
func NewGeminiVisionProvider(baseURL, apiKey, model string) (*GeminiVisionProvider, error) {
	if baseURL == "" {
		return nil, fmt.Errorf("gemini-vision: base_url is required")
	}
	if model == "" {
		return nil, fmt.Errorf("gemini-vision: model is required")
	}
	return &GeminiVisionProvider{
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 180 * time.Second},
	}, nil
}

// Name returns the canonical provider identifier.
func (p *GeminiVisionProvider) Name() string { return "gemini-vision" }

// Complete executes a vision+text completion via the Gemini generateContent API.
func (p *GeminiVisionProvider) Complete(ctx context.Context, req LaneRequest) (*LaneResponse, error) {
	if err := p.validateImages(req); err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "invalid_argument", Cause: err}
	}

	body, err := p.buildRequest(req)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "config", Cause: err}
	}

	url := fmt.Sprintf("%s/models/%s:generateContent?key=%s", p.baseURL, p.model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "config", Cause: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream", Cause: err}
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("read body: %w", err)}
	}
	if resp.StatusCode >= 400 {
		return nil, &GatewayError{Lane: req.Lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))}
	}

	return p.parseResponse(respBytes, req.Lane)
}

// Stream is not supported for the vision lane; callers should use Complete.
func (p *GeminiVisionProvider) Stream(_ context.Context, req LaneRequest) (<-chan StreamChunk, error) {
	return nil, &GatewayError{
		Lane:     req.Lane,
		Provider: p.Name(),
		Code:     "unsupported",
		Cause:    fmt.Errorf("streaming not supported for gemini-vision provider; use Complete"),
	}
}

// HealthCheck calls the model metadata endpoint to verify reachability.
func (p *GeminiVisionProvider) HealthCheck(ctx context.Context) error {
	url := fmt.Sprintf("%s/models/%s?key=%s", p.baseURL, p.model, p.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health[gemini-vision]: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("health[gemini-vision]: HTTP %d", resp.StatusCode)
	}
	return nil
}

// ---- internal helpers ----

// validateImages enforces the three image constraints.
func (p *GeminiVisionProvider) validateImages(req LaneRequest) error {
	if len(req.Images) > maxImagesPerRequest {
		return fmt.Errorf("too many images: got %d, max %d", len(req.Images), maxImagesPerRequest)
	}
	if len(req.Images) > 0 {
		if !allowedMIMETypes[req.ImageMimeType] {
			return fmt.Errorf("unsupported image MIME type %q; allowed: image/png, image/jpeg, image/gif, image/webp",
				req.ImageMimeType)
		}
		for i, img := range req.Images {
			if len(img) > maxImageBytes {
				return fmt.Errorf("image[%d] too large: %d bytes, max %d", i, len(img), maxImageBytes)
			}
		}
	}
	return nil
}

// buildRequest marshals the Gemini generateContent payload.
// Text part is ALWAYS last to ensure the model reads images first.
func (p *GeminiVisionProvider) buildRequest(req LaneRequest) ([]byte, error) {
	// Collect text from messages into a single prompt string.
	var textBuf strings.Builder
	if req.SystemPrompt != "" {
		textBuf.WriteString(req.SystemPrompt)
		textBuf.WriteString("\n\n")
	}
	for _, m := range req.Messages {
		if m.Role == "system" {
			continue // already prepended
		}
		textBuf.WriteString(m.Content)
		textBuf.WriteString("\n")
	}
	textContent := strings.TrimSpace(textBuf.String())

	// Build parts: images first, text LAST.
	type inlineData struct {
		MimeType string `json:"mimeType"`
		Data     string `json:"data"` // base64
	}
	type part struct {
		Text       string      `json:"text,omitempty"`
		InlineData *inlineData `json:"inlineData,omitempty"`
	}

	parts := make([]part, 0, len(req.Images)+1)
	for _, img := range req.Images {
		parts = append(parts, part{
			InlineData: &inlineData{
				MimeType: req.ImageMimeType,
				Data:     base64.StdEncoding.EncodeToString(img),
			},
		})
	}
	// Text part is LAST.
	parts = append(parts, part{Text: textContent})

	type content struct {
		Parts []part `json:"parts"`
	}
	type genConfig struct {
		MaxOutputTokens int `json:"maxOutputTokens,omitempty"`
	}
	type payload struct {
		Contents         []content `json:"contents"`
		GenerationConfig genConfig `json:"generationConfig,omitempty"`
	}

	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}

	p2 := payload{
		Contents:         []content{{Parts: parts}},
		GenerationConfig: genConfig{MaxOutputTokens: maxTok},
	}
	return json.Marshal(p2)
}

// geminiGenerateResponse is a minimal decode target for generateContent responses.
type geminiGenerateResponse struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
}

func (p *GeminiVisionProvider) parseResponse(body []byte, lane Lane) (*LaneResponse, error) {
	var gr geminiGenerateResponse
	if err := json.Unmarshal(body, &gr); err != nil {
		return nil, &GatewayError{Lane: lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("decode: %w", err)}
	}
	if len(gr.Candidates) == 0 {
		return nil, &GatewayError{Lane: lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("no candidates in response")}
	}
	parts := gr.Candidates[0].Content.Parts
	if len(parts) == 0 {
		return nil, &GatewayError{Lane: lane, Provider: p.Name(), Code: "upstream",
			Cause: fmt.Errorf("empty parts in candidate")}
	}
	return &LaneResponse{
		Content:      parts[0].Text,
		InputTokens:  gr.UsageMetadata.PromptTokenCount,
		OutputTokens: gr.UsageMetadata.CandidatesTokenCount,
		Provider:     p.Name(),
		Model:        p.model,
	}, nil
}
