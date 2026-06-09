// Package gateway — lane resolver.
//
// Purpose: Given a Lane, return the ordered list of ProviderEntry values from
//          the active registry. First entry = primary; subsequent = fallback order.
//          Build a concrete Provider instance from a ProviderEntry.
// Inputs:  *RegistryWatcher (for the current registry) + Lane enum.
// Outputs: []ProviderEntry ordered by priority; error if lane is not configured.
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence gateway.
package gateway

import (
	"fmt"
)

// LaneResolve returns the ordered list of ProviderEntry for lane from the
// current registry. Returns an error if the lane has no entries configured.
// The caller iterates the list and tries providers in order (primary → fallbacks).
func LaneResolve(reg *Registry, lane Lane) ([]ProviderEntry, error) {
	if reg == nil {
		return nil, fmt.Errorf("resolver: registry is nil")
	}
	entries, ok := reg.Entries[lane]
	if !ok || len(entries) == 0 {
		return nil, fmt.Errorf("resolver: no providers configured for lane %q", lane)
	}
	// Return a shallow copy so callers cannot mutate the registry slice.
	out := make([]ProviderEntry, len(entries))
	copy(out, entries)
	return out, nil
}

// BuildProvider constructs a concrete Provider from a ProviderEntry.
// api_key_ref has already been resolved to APIKey by the registry loader.
func BuildProvider(e ProviderEntry) (Provider, error) {
	switch e.Provider {
	case "anthropic":
		return NewAnthropicProvider(e.APIKey, e.Model)
	case "ollama":
		// Ollama gets a dedicated provider: OpenAI-compat inference (per LEDGER §G)
		// plus model-pull bootstrap and connection-refused → ErrUnavailable mapping.
		// base_url carries the host (with /v1); NewOllamaProvider normalizes it.
		return NewOllamaProvider(e.BaseURL, e.Model)
	case "vllm":
		// vLLM gets its own provider: loopback guard (M6/ADR-001) + ErrUnavailable
		// mapping for DEEP/FAST lane fallback. base_url carries the host only
		// (no /v1 suffix); NewVLLMProvider appends /v1 internally.
		// VLLM_HOST / VLLM_API_KEY env vars used when BaseURL/APIKey are empty.
		return NewVLLMProvider(e.BaseURL, e.APIKey, e.Model)
	case "openai", "gemini", "tei-embed", "tei-rerank":
		return NewOpenAICompatProvider(e.BaseURL, e.APIKey, e.Model, e.Provider)
	case "gemini-vision":
		// MULTIMODAL lane — Gemini generateContent REST API with inline image support.
		// Reuses gcp_project_pool quota tracking (ADR-006).
		return NewGeminiVisionProvider(e.BaseURL, e.APIKey, e.Model)
	default:
		return nil, fmt.Errorf("resolver: unknown provider %q", e.Provider)
	}
}

// ResolveAndBuild is a convenience helper that resolves the lane and builds
// the first (primary) provider. It returns the full entry list alongside
// the provider so callers can build fallbacks on demand.
func ResolveAndBuild(reg *Registry, lane Lane) (Provider, []ProviderEntry, error) {
	entries, err := LaneResolve(reg, lane)
	if err != nil {
		return nil, nil, err
	}
	p, err := BuildProvider(entries[0])
	if err != nil {
		return nil, nil, fmt.Errorf("resolver: build primary provider for lane %q: %w", lane, err)
	}
	return p, entries, nil
}
