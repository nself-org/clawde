// metrics.go — Recall@k, MRR@10, and latency percentile computation.
//
// Purpose: Pure, stateless functions over retrieved result lists.
//          No I/O, no concurrency — safe to test exhaustively.
// Inputs:  retrieved chunk ID lists; relevant chunk ID sets; latency samples.
// Outputs: Recall@5, Recall@10, MRR@10 in [0,1]; p50_ms, p95_ms as integers.
// Constraints: File ≤500 lines. All math is deterministic; no randomness.
// SPORT: REGISTRY-FUNCTIONS.md → eval.RecallAtK, eval.MRRAtK, eval.LatencyPercentile.
package eval

import (
	"math"
	"sort"
	"time"
)

// EvalResult holds the aggregate metrics for one provider run over a dataset.
//
// Purpose: Returned by RunEval and written to clawde_eval_runs by Recorder.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.EvalResult.
type EvalResult struct {
	Provider    string
	Dataset     string
	RecallAt5   float64
	RecallAt10  float64
	MRRAt10     float64
	P50Ms       int
	P95Ms       int
	SampleCount int
}

// RecallAtK computes mean Recall@k over all queries.
//
// Purpose: For each query, check whether any relevant chunk appears in the top-k
//          retrieved IDs. Recall is binary per query (hit/miss), then averaged.
// Inputs:  retrieved — ordered list of chunk IDs returned by the provider (len ≥ k).
//          relevant  — set of relevant chunk IDs for this query.
//          k         — cutoff (5 or 10).
// Outputs: mean recall in [0,1].
// SPORT:   REGISTRY-FUNCTIONS.md → eval.RecallAtK.
func RecallAtK(retrieved [][]string, relevant [][]string, k int) float64 {
	if len(retrieved) == 0 {
		return 0
	}
	var sum float64
	for i, ret := range retrieved {
		if i >= len(relevant) {
			break
		}
		relSet := toSet(relevant[i])
		topK := ret
		if len(topK) > k {
			topK = topK[:k]
		}
		for _, id := range topK {
			if relSet[id] {
				sum++
				break
			}
		}
	}
	return sum / float64(len(retrieved))
}

// MRRAtK computes Mean Reciprocal Rank at k over all queries.
//
// Purpose: For each query, find the rank of the first relevant result in the
//          top-k list.  MRR = mean of 1/rank over all queries (0 if no hit).
// Inputs:  retrieved — ordered list of retrieved chunk ID lists.
//          relevant  — relevant chunk ID sets (parallel to retrieved).
//          k         — cutoff (typically 10).
// Outputs: MRR in [0,1].
// SPORT:   REGISTRY-FUNCTIONS.md → eval.MRRAtK.
func MRRAtK(retrieved [][]string, relevant [][]string, k int) float64 {
	if len(retrieved) == 0 {
		return 0
	}
	var sum float64
	for i, ret := range retrieved {
		if i >= len(relevant) {
			break
		}
		relSet := toSet(relevant[i])
		topK := ret
		if len(topK) > k {
			topK = topK[:k]
		}
		for rank, id := range topK {
			if relSet[id] {
				sum += 1.0 / float64(rank+1)
				break
			}
		}
	}
	return sum / float64(len(retrieved))
}

// LatencyPercentile computes a percentile (0–100) from a slice of durations.
//
// Purpose: Derive p50 and p95 latency values from raw embed timings.
// Inputs:  samples — raw durations (unsorted); p — percentile 0–100.
// Outputs: duration at the requested percentile; 0 if samples is empty.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.LatencyPercentile.
func LatencyPercentile(samples []time.Duration, p float64) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := make([]time.Duration, len(samples))
	copy(sorted, samples)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })

	idx := int(math.Ceil(p/100.0*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// toSet converts a slice of strings to a presence map.
func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
