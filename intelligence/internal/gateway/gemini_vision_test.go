// Package gateway — tests for GeminiVisionProvider (MULTIMODAL lane, W16-S16-T03).
//
// Covers: image count > 5 rejected, size > 10MB rejected, bad MIME rejected,
//         text-part-last ordering, MULTIMODAL lane registration, 1×1 PNG happy-path mock.
//         Live-Gemini test skips without GEMINI_API_KEY.
package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// minimal1x1PNG is a valid 1×1 transparent PNG (67 bytes).
var minimal1x1PNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x62, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

// TestGeminiVision_TooManyImages verifies that > 5 images returns InvalidArgument.
func TestGeminiVision_TooManyImages(t *testing.T) {
	t.Parallel()
	p, err := NewGeminiVisionProvider("https://example.com/v1beta", "fake-key", "gemini-1.5-pro-vision")
	if err != nil {
		t.Fatalf("NewGeminiVisionProvider: %v", err)
	}

	images := make([][]byte, 6)
	for i := range images {
		images[i] = minimal1x1PNG
	}
	req := LaneRequest{
		Lane:          LaneMultimodal,
		Messages:      []Message{{Role: "user", Content: "describe"}},
		Images:        images,
		ImageMimeType: "image/png",
	}
	_, err = p.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for > 5 images, got nil")
	}
	gwErr, ok := err.(*GatewayError)
	if !ok {
		t.Fatalf("expected *GatewayError, got %T", err)
	}
	if gwErr.Code != "invalid_argument" {
		t.Errorf("expected code=invalid_argument, got %q", gwErr.Code)
	}
	if !strings.Contains(gwErr.Cause.Error(), "too many images") {
		t.Errorf("expected 'too many images' in cause, got %q", gwErr.Cause.Error())
	}
}

// TestGeminiVision_ImageTooLarge verifies that > 10MB image returns InvalidArgument.
func TestGeminiVision_ImageTooLarge(t *testing.T) {
	t.Parallel()
	p, _ := NewGeminiVisionProvider("https://example.com/v1beta", "fake-key", "gemini-2.0-flash-vision")

	bigImage := make([]byte, 10*1024*1024+1) // 10MB + 1 byte
	req := LaneRequest{
		Lane:          LaneMultimodal,
		Messages:      []Message{{Role: "user", Content: "describe"}},
		Images:        [][]byte{bigImage},
		ImageMimeType: "image/jpeg",
	}
	_, err := p.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for oversized image, got nil")
	}
	gwErr, ok := err.(*GatewayError)
	if !ok {
		t.Fatalf("expected *GatewayError, got %T", err)
	}
	if gwErr.Code != "invalid_argument" {
		t.Errorf("expected code=invalid_argument, got %q", gwErr.Code)
	}
	if !strings.Contains(gwErr.Cause.Error(), "too large") {
		t.Errorf("expected 'too large' in cause, got %q", gwErr.Cause.Error())
	}
}

// TestGeminiVision_BadMIME verifies that an unsupported MIME type returns InvalidArgument.
func TestGeminiVision_BadMIME(t *testing.T) {
	t.Parallel()
	p, _ := NewGeminiVisionProvider("https://example.com/v1beta", "fake-key", "gemini-1.5-pro-vision")

	req := LaneRequest{
		Lane:          LaneMultimodal,
		Messages:      []Message{{Role: "user", Content: "describe"}},
		Images:        [][]byte{minimal1x1PNG},
		ImageMimeType: "image/bmp", // not in allowed set
	}
	_, err := p.Complete(context.Background(), req)
	if err == nil {
		t.Fatal("expected error for bad MIME, got nil")
	}
	gwErr, ok := err.(*GatewayError)
	if !ok {
		t.Fatalf("expected *GatewayError, got %T", err)
	}
	if gwErr.Code != "invalid_argument" {
		t.Errorf("expected code=invalid_argument, got %q", gwErr.Code)
	}
	if !strings.Contains(gwErr.Cause.Error(), "unsupported image MIME type") {
		t.Errorf("expected 'unsupported image MIME type' in cause, got %q", gwErr.Cause.Error())
	}
}

// TestGeminiVision_TextPartLast verifies that the text part is always last in the payload.
func TestGeminiVision_TextPartLast(t *testing.T) {
	t.Parallel()
	p, _ := NewGeminiVisionProvider("https://example.com/v1beta", "fake-key", "gemini-1.5-pro-vision")

	req := LaneRequest{
		Lane:          LaneMultimodal,
		Messages:      []Message{{Role: "user", Content: "what do you see?"}},
		Images:        [][]byte{minimal1x1PNG, minimal1x1PNG},
		ImageMimeType: "image/png",
	}
	body, err := p.buildRequest(req)
	if err != nil {
		t.Fatalf("buildRequest: %v", err)
	}

	var payload struct {
		Contents []struct {
			Parts []struct {
				Text       string `json:"text"`
				InlineData *struct {
					MimeType string `json:"mimeType"`
				} `json:"inlineData"`
			} `json:"parts"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if len(payload.Contents) == 0 {
		t.Fatal("no contents in payload")
	}
	parts := payload.Contents[0].Parts
	// Expect: 2 image parts + 1 text part, text is LAST.
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts (2 images + 1 text), got %d", len(parts))
	}
	last := parts[len(parts)-1]
	if last.InlineData != nil {
		t.Error("last part should be text, not inlineData")
	}
	if last.Text == "" {
		t.Error("last text part is empty")
	}
	// First two should be images.
	for i := 0; i < 2; i++ {
		if parts[i].InlineData == nil {
			t.Errorf("part[%d] should be inlineData, got text=%q", i, parts[i].Text)
		}
		if parts[i].InlineData != nil && parts[i].InlineData.MimeType != "image/png" {
			t.Errorf("part[%d] mimeType=%q, want image/png", i, parts[i].InlineData.MimeType)
		}
	}
}

// TestGeminiVision_MultimodalRegistration verifies the registry recognises gemini-vision entries.
func TestGeminiVision_MultimodalRegistration(t *testing.T) {
	t.Parallel()
	yaml := `
version: 1
lanes:
  - lane: multimodal
    entries:
      - provider: gemini-vision
        base_url: https://generativelanguage.googleapis.com/v1beta
        model: gemini-1.5-pro-vision
        api_key_ref: GEMINI_API_KEY
        project_id: test-project
        cost_per_1k_tokens: 0.007
        context_window_tokens: 1000000
        streaming_supported: false
        multimodal: true
        rate_limit:
          rpm: 360
          rpd: 0
          window_seconds: 60
        p99_latency_ms: 3500
      - provider: gemini-vision
        base_url: https://generativelanguage.googleapis.com/v1beta
        model: gemini-2.0-flash-vision
        api_key_ref: GEMINI_API_KEY
        project_id: test-project
        cost_per_1k_tokens: 0.001
        context_window_tokens: 1000000
        streaming_supported: false
        multimodal: true
        rate_limit:
          rpm: 1000
          rpd: 0
          window_seconds: 60
        p99_latency_ms: 1500
`
	reg, err := parseRegistry([]byte(yaml))
	if err != nil {
		t.Fatalf("parseRegistry: %v", err)
	}
	entries, err := LaneResolve(reg, LaneMultimodal)
	if err != nil {
		t.Fatalf("LaneResolve(multimodal): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.Provider != "gemini-vision" {
			t.Errorf("entry[%d].Provider=%q, want gemini-vision", i, e.Provider)
		}
		if e.Multimodal != true {
			t.Errorf("entry[%d].Multimodal=false, want true", i)
		}
	}
	// Verify BuildProvider returns a *GeminiVisionProvider.
	provider, err := BuildProvider(entries[0])
	if err != nil {
		t.Fatalf("BuildProvider: %v", err)
	}
	if provider.Name() != "gemini-vision" {
		t.Errorf("Name()=%q, want gemini-vision", provider.Name())
	}
}

// TestGeminiVision_HappyPath_Mock tests the full Complete path against a mock HTTP server.
func TestGeminiVision_HappyPath_Mock(t *testing.T) {
	t.Parallel()

	// Minimal Gemini generateContent response.
	mockResp := map[string]any{
		"candidates": []map[string]any{
			{
				"content": map[string]any{
					"parts": []map[string]any{
						{"text": "A 1×1 transparent pixel."},
					},
				},
			},
		},
		"usageMetadata": map[string]any{
			"promptTokenCount":     42,
			"candidatesTokenCount": 8,
		},
	}
	respBody, _ := json.Marshal(mockResp)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "gemini-1.5-pro-vision") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
	}))
	defer srv.Close()

	p, err := NewGeminiVisionProvider(srv.URL, "fake-key", "gemini-1.5-pro-vision")
	if err != nil {
		t.Fatalf("NewGeminiVisionProvider: %v", err)
	}

	req := LaneRequest{
		Lane:          LaneMultimodal,
		Messages:      []Message{{Role: "user", Content: "describe this image"}},
		Images:        [][]byte{minimal1x1PNG},
		ImageMimeType: "image/png",
	}
	resp, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "A 1×1 transparent pixel." {
		t.Errorf("Content=%q, want 'A 1×1 transparent pixel.'", resp.Content)
	}
	if resp.InputTokens != 42 {
		t.Errorf("InputTokens=%d, want 42", resp.InputTokens)
	}
	if resp.OutputTokens != 8 {
		t.Errorf("OutputTokens=%d, want 8", resp.OutputTokens)
	}
	if resp.Provider != "gemini-vision" {
		t.Errorf("Provider=%q, want gemini-vision", resp.Provider)
	}
	if resp.Model != "gemini-1.5-pro-vision" {
		t.Errorf("Model=%q, want gemini-1.5-pro-vision", resp.Model)
	}
}

// TestGeminiVision_Live_Skip skips if GEMINI_API_KEY is not set.
// Runs against the real Gemini API when key is available.
func TestGeminiVision_Live_Skip(t *testing.T) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		t.Skip("GEMINI_API_KEY not set — skipping live Gemini vision test")
	}

	p, err := NewGeminiVisionProvider(
		"https://generativelanguage.googleapis.com/v1beta",
		apiKey,
		"gemini-1.5-pro-vision",
	)
	if err != nil {
		t.Fatalf("NewGeminiVisionProvider: %v", err)
	}

	req := LaneRequest{
		Lane:          LaneMultimodal,
		Messages:      []Message{{Role: "user", Content: "What color is this pixel?"}},
		Images:        [][]byte{minimal1x1PNG},
		ImageMimeType: "image/png",
	}
	resp, err := p.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content == "" {
		t.Error("expected non-empty content from live Gemini vision")
	}
	t.Logf("live response: %s (in=%d out=%d)", resp.Content, resp.InputTokens, resp.OutputTokens)
}

// TestGeminiVision_StreamUnsupported verifies Stream returns the unsupported error.
func TestGeminiVision_StreamUnsupported(t *testing.T) {
	t.Parallel()
	p, _ := NewGeminiVisionProvider("https://example.com/v1beta", "k", "gemini-1.5-pro-vision")
	_, err := p.Stream(context.Background(), LaneRequest{Lane: LaneMultimodal})
	if err == nil {
		t.Fatal("expected error from Stream, got nil")
	}
	gwErr, ok := err.(*GatewayError)
	if !ok {
		t.Fatalf("expected *GatewayError, got %T", err)
	}
	if gwErr.Code != "unsupported" {
		t.Errorf("Code=%q, want unsupported", gwErr.Code)
	}
}
