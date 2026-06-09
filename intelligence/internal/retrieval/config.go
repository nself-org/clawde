// Package retrieval provides lexical and semantic retrieval over clawde_chunks.
//
// Purpose: Abstract the BM25 retrieval surface behind a lane interface so callers
//          can swap tsvector baseline ↔ ParadeDB pg_bm25 at runtime via env flags.
//          Includes an A/B harness that logs comparative top-10 results to
//          clawde_lane_ab_log for offline recall analysis.
// Inputs:  env CLAWDE_BM25_ENABLED (bool, default false), CLAWDE_BM25_AB_MODE (bool, default false).
// Outputs: BM25Lane implementations; A/B log rows; OTel events on fallback.
// Constraints: File ≤500 lines. No ParadeDB hard dependency — TSVectorBM25Lane
//              compiles and passes CI without pg_bm25 installed.
// SPORT: REGISTRY-FUNCTIONS.md → BM25Lane, TSVectorBM25Lane, ParadeDBBM25Lane.
package retrieval

import (
	"os"
	"strings"
)

// Config holds retrieval-specific feature flags loaded from environment variables.
// Extend this struct if new retrieval flags are added in future tickets.
//
// Inputs:  Environment variables (read once at program start).
// Outputs: Config struct consumed by NewBM25Kernel.
// SPORT:   REGISTRY-FUNCTIONS.md → retrieval.Config.
type Config struct {
	// BM25Enabled selects the BM25 retrieval path.
	// false (default) → TSVectorBM25Lane (always available, no extension required).
	// true            → ParadeDBBM25Lane with automatic fallback to tsvector on error.
	// Env: CLAWDE_BM25_ENABLED
	BM25Enabled bool

	// BM25ABMode enables comparative A/B logging.
	// When true, both TSVectorBM25Lane and ParadeDBBM25Lane are queried per request;
	// top-10 results from each are logged to clawde_lane_ab_log for offline analysis.
	// Implies BM25Enabled=true semantically but the kernel checks both flags independently.
	// Env: CLAWDE_BM25_AB_MODE
	BM25ABMode bool
}

// LoadConfig reads the retrieval Config from environment variables.
// Call once at program start; the result is safe for concurrent reads.
func LoadConfig() Config {
	return Config{
		BM25Enabled: parseBool(os.Getenv("CLAWDE_BM25_ENABLED")),
		BM25ABMode:  parseBool(os.Getenv("CLAWDE_BM25_AB_MODE")),
	}
}

// parseBool returns true for "1", "true", "yes" (case-insensitive).
func parseBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes":
		return true
	}
	return false
}
