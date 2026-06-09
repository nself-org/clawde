// rrf.go — Reciprocal Rank Fusion for merging dense and lexical result sets.
//
// Purpose: Fuse two independently-ranked ScoredChunk lists (dense + lexical)
//          using the RRF algorithm. RRF is rank-based (not score-based) so it is
//          robust to score magnitude differences between cosine similarity and
//          ts_rank_cd.
// Inputs:  dense []ScoredChunk, lexical []ScoredChunk, k float64 (60 per ADR-005).
// Outputs: []ScoredChunk ordered by RRF score descending; deduplicated by chunk ID.
// Constraints: File ≤500 lines. Pure in-memory — no DB calls. k=60 is the
//              CLAWDE_RRF_K env default per ADR-005.
// SPORT: REGISTRY-FUNCTIONS.md → retrieval.RRFMerge, retrieval.LoadRRFConfig.
package retrieval

import (
	"os"
	"sort"
	"strconv"

	"github.com/google/uuid"
)

// defaultRRFK is the Borda-constant used in RRF.
// Cormack et al. (2009) recommend k=60 as a robust default.
// Lower values increase the dominance of top-ranked results.
const defaultRRFK = 60.0

// LoadRRFConfig reads the RRF k parameter from CLAWDE_RRF_K env.
// Returns defaultRRFK (60) if the variable is unset or invalid.
func LoadRRFConfig() float64 {
	if v := os.Getenv("CLAWDE_RRF_K"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			return f
		}
	}
	return defaultRRFK
}

// rrfEntry accumulates per-chunk data during the merge.
type rrfEntry struct {
	chunk    ScoredChunk
	rrfScore float64
}

// RRFMerge fuses two ordered result lists using Reciprocal Rank Fusion.
//
// Algorithm:
//  1. For each list, assign ranks 1, 2, 3, … to entries in order.
//  2. For each chunk ID that appears in any list, accumulate:
//     rrfScore += 1 / (k + rank)
//  3. Sort by rrfScore descending.
//  4. Return deduplicated ScoredChunk slice with Source="rrf" and Score=rrfScore.
//
// Chunks that appear in both lists receive contributions from both; chunks
// that appear in only one list still receive a partial score.
func RRFMerge(dense, lexical []ScoredChunk, k float64) []ScoredChunk {
	if k <= 0 {
		k = defaultRRFK
	}

	// Map from chunk ID to accumulated entry.
	acc := make(map[uuid.UUID]*rrfEntry)

	// Helper: apply rank contributions from one list.
	applyList := func(list []ScoredChunk) {
		for rank, chunk := range list {
			contrib := 1.0 / (k + float64(rank+1))
			if e, ok := acc[chunk.ID]; ok {
				e.rrfScore += contrib
			} else {
				// First encounter — copy chunk metadata.
				cp := chunk
				cp.Source = "rrf"
				acc[chunk.ID] = &rrfEntry{chunk: cp, rrfScore: contrib}
			}
		}
	}

	applyList(dense)
	applyList(lexical)

	// Collect and sort.
	merged := make([]ScoredChunk, 0, len(acc))
	for _, e := range acc {
		e.chunk.Score = e.rrfScore
		merged = append(merged, e.chunk)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score > merged[j].Score
	})

	return merged
}
