// Package gateway — model registry loader.
//
// Purpose: Parse model_registry.yaml at startup, validate all entries, resolve
//          api_key_ref values from environment (vault.env), and build the
//          lane→[]ProviderEntry mapping used by the resolver.
// Inputs:  YAML file path; environment (vault.env already sourced by caller).
// Outputs: *Registry with ValidEntries map ready for LaneResolve.
// Constraints: api_key_ref resolved via os.Getenv; raw keys never stored in YAML.
//              project_id required for gemini provider entries.
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence gateway.
package gateway

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// ProviderEntry is a single model+provider record from the registry.
// All string fields are populated from YAML; api_key is resolved at load time
// from os.Getenv(APIKeyRef).
type ProviderEntry struct {
	Lane      Lane   `yaml:"-"`      // populated by registry loader
	Provider  string `yaml:"provider"`
	BaseURL   string `yaml:"base_url"`  // empty for anthropic (SDK manages it)
	Model     string `yaml:"model"`
	APIKeyRef string `yaml:"api_key_ref"` // vault.env variable name, e.g. "ANTHROPIC_API_KEY"
	APIKey    string `yaml:"-"`           // resolved at load from env; never in YAML
	ProjectID string `yaml:"project_id"`  // required for gemini (quota is per GCP project)

	CostPer1kTokens     float64 `yaml:"cost_per_1k_tokens"`
	ContextWindowTokens int     `yaml:"context_window_tokens"`
	StreamingSupported  bool    `yaml:"streaming_supported"`
	Multimodal          bool    `yaml:"multimodal"`

	RateLimit struct {
		RPM           int `yaml:"rpm"`
		RPD           int `yaml:"rpd"`
		WindowSeconds int `yaml:"window_seconds"`
	} `yaml:"rate_limit"`

	P99LatencyMs int `yaml:"p99_latency_ms"`
}

// laneConfig is the raw YAML structure for a single lane block.
type laneConfig struct {
	Lane    Lane            `yaml:"lane"`
	Entries []ProviderEntry `yaml:"entries"`
}

// registryFile is the top-level YAML document.
type registryFile struct {
	Version int          `yaml:"version"`
	Lanes   []laneConfig `yaml:"lanes"`
}

// Registry holds the parsed and validated model registry.
type Registry struct {
	// Entries maps each Lane to its ordered list of ProviderEntry values.
	// Order in the YAML defines fallback order: first entry = primary.
	Entries map[Lane][]ProviderEntry
}

// LoadRegistry parses the YAML file at path, resolves api_key_ref values from
// the environment, and validates that each entry is structurally complete.
// Gemini entries without project_id return a validation error.
func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("registry: read %s: %w", path, err)
	}
	return parseRegistry(data)
}

// parseRegistry parses raw YAML bytes. Split from LoadRegistry for testability.
func parseRegistry(data []byte) (*Registry, error) {
	var doc registryFile
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("registry: parse YAML: %w", err)
	}
	if doc.Version != 1 {
		return nil, fmt.Errorf("registry: unsupported version %d", doc.Version)
	}

	entries := make(map[Lane][]ProviderEntry, len(doc.Lanes))
	seenLanes := make(map[Lane]bool)

	for _, lc := range doc.Lanes {
		if !validLane(lc.Lane) {
			return nil, fmt.Errorf("registry: unknown lane %q", lc.Lane)
		}
		if seenLanes[lc.Lane] {
			return nil, fmt.Errorf("registry: duplicate lane %q", lc.Lane)
		}
		seenLanes[lc.Lane] = true

		if len(lc.Entries) == 0 {
			return nil, fmt.Errorf("registry: lane %q has no entries", lc.Lane)
		}

		resolved := make([]ProviderEntry, 0, len(lc.Entries))
		for i, e := range lc.Entries {
			e.Lane = lc.Lane
			if err := validateEntry(e, i); err != nil {
				return nil, fmt.Errorf("registry: lane %q entry %d: %w", lc.Lane, i, err)
			}
			// Resolve api_key_ref → actual key from environment (vault.env sourced by main).
			if e.APIKeyRef != "" {
				e.APIKey = os.Getenv(e.APIKeyRef)
				// Missing keys are allowed at load time (key may not be installed on every machine).
				// HealthCheck will fail if the key is needed and missing.
			}
			resolved = append(resolved, e)
		}
		entries[lc.Lane] = resolved
	}

	return &Registry{Entries: entries}, nil
}

// validateEntry checks that required fields are present.
func validateEntry(e ProviderEntry, idx int) error {
	if e.Provider == "" {
		return fmt.Errorf("entry[%d]: provider is required", idx)
	}
	if e.Model == "" {
		return fmt.Errorf("entry[%d]: model is required", idx)
	}
	// Gemini (openai-compat path) requires project_id for correct quota attribution.
	if e.Provider == "gemini" && e.ProjectID == "" {
		return fmt.Errorf("entry[%d] provider=gemini: project_id is required (quota is per GCP project)", idx)
	}
	// gemini-vision (direct REST path) also uses project_id for gcp_project_pool.
	if e.Provider == "gemini-vision" && e.ProjectID == "" {
		return fmt.Errorf("entry[%d] provider=gemini-vision: project_id is required (quota is per GCP project)", idx)
	}
	// Non-anthropic providers need a base_url.
	if e.Provider != "anthropic" && e.BaseURL == "" {
		return fmt.Errorf("entry[%d] provider=%s: base_url is required", idx, e.Provider)
	}
	return nil
}

// validLane returns true if l is one of the 7 canonical lanes.
func validLane(l Lane) bool {
	for _, known := range AllLanes {
		if l == known {
			return true
		}
	}
	return false
}
