// Package config — telemetry configuration loaded from environment.
//
// Purpose:    Build a TelemetryConfig from CLAWDE_OTEL_*, CLAWDE_LANGFUSE_*, and
//             CLAWDE_COST_RATES_JSON environment variables for the observability
//             pipeline (pkg/telemetry).
// Inputs:     process environment.
// Outputs:    TelemetryConfig value; never errors — missing/invalid values fall
//             back to safe defaults so the pipeline degrades gracefully.
// Constraints: An unset OTLP endpoint means the exporter is a no-op (no error to
//             caller). Cost rates default to empty (RecordCost falls back to 0).
// SPORT:      REGISTRY-PACKAGES.md → internal/config.
package config

import (
	"encoding/json"
	"os"
	"strconv"
)

// CostRate holds per-1k-token USD prices for a single provider/model key.
type CostRate struct {
	InputPer1k  float64 `json:"input_per_1k"`
	OutputPer1k float64 `json:"output_per_1k"`
}

// TelemetryConfig is the resolved observability configuration. A zero value is
// valid and produces a fully no-op pipeline (graceful degradation).
type TelemetryConfig struct {
	// OTel.
	OTLPEndpoint string // CLAWDE_OTEL_ENDPOINT — empty disables export.
	OTLPProtocol string // CLAWDE_OTEL_PROTOCOL — "grpc" (default) or "http".
	OTLPInsecure bool   // CLAWDE_OTEL_INSECURE — true skips TLS (default true for grpc).
	ServiceName  string // service.name resource attr (canonical: "clawde").
	Version      string // service.version resource attr.
	Environment  string // deployment.environment resource attr.
	HostName     string // host.name resource attr.

	// Langfuse.
	LangfuseEnabled   bool   // derived: true when host + keys are present.
	LangfuseHost      string // CLAWDE_LANGFUSE_HOST.
	LangfusePublicKey string // CLAWDE_LANGFUSE_PUBLIC_KEY.
	LangfuseSecretKey string // CLAWDE_LANGFUSE_SECRET_KEY.

	// Cost rates keyed by "provider/model" (e.g. "anthropic/claude-sonnet-4-6").
	CostRates map[string]CostRate
}

// LoadTelemetryConfig reads the telemetry environment and returns a resolved
// config. It never returns an error: invalid JSON in CLAWDE_COST_RATES_JSON is
// ignored (empty rate table), and an unset OTLP endpoint yields a no-op pipeline.
func LoadTelemetryConfig() TelemetryConfig {
	c := TelemetryConfig{
		OTLPEndpoint:      os.Getenv("CLAWDE_OTEL_ENDPOINT"),
		OTLPProtocol:      getenvDefault("CLAWDE_OTEL_PROTOCOL", "grpc"),
		OTLPInsecure:      getenvBool("CLAWDE_OTEL_INSECURE", true),
		ServiceName:       getenvDefault("CLAWDE_OTEL_SERVICE_NAME", "clawde"),
		Version:           getenvDefault("CLAWDE_OTEL_SERVICE_VERSION", "dev"),
		Environment:       getenvDefault("CLAWDE_OTEL_ENVIRONMENT", "development"),
		HostName:          resolveHostName(),
		LangfuseHost:      os.Getenv("CLAWDE_LANGFUSE_HOST"),
		LangfusePublicKey: os.Getenv("CLAWDE_LANGFUSE_PUBLIC_KEY"),
		LangfuseSecretKey: os.Getenv("CLAWDE_LANGFUSE_SECRET_KEY"),
		CostRates:         parseCostRates(os.Getenv("CLAWDE_COST_RATES_JSON")),
	}
	c.LangfuseEnabled = c.LangfuseHost != "" && c.LangfusePublicKey != "" && c.LangfuseSecretKey != ""
	return c
}

// OTLPEnabled reports whether an OTLP endpoint is configured. When false the
// telemetry pipeline installs no-op providers.
func (c TelemetryConfig) OTLPEnabled() bool { return c.OTLPEndpoint != "" }

// parseCostRates decodes CLAWDE_COST_RATES_JSON into a rate table. Invalid or
// empty JSON yields an empty (non-nil) map so callers never panic.
func parseCostRates(raw string) map[string]CostRate {
	out := map[string]CostRate{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func resolveHostName() string {
	if h := os.Getenv("CLAWDE_OTEL_HOST_NAME"); h != "" {
		return h
	}
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getenvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
