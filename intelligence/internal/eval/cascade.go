// cascade.go — cascade-tier labeling + per-tier dataset validation.
//
// Purpose:    The benchmark golden set is balanced across the four instruction
//             cascade tiers (GCI, ASI, PPI, MCI). This file defines the tier
//             enum, extends loading to carry a tier label, and asserts the
//             50-per-tier balance the ticket requires.
// Inputs:     JSONL golden dataset with a "cascade_tier" field per row.
// Outputs:    CascadeDataset; per-tier counts; balance validation error.
// Constraints: File ≤500 lines. Pure parse + count; no I/O beyond LoadDataset.
// SPORT: REGISTRY-FUNCTIONS.md → eval.CascadeTier, eval.LoadCascadeDataset,
//        eval.CascadeCounts, eval.AssertBalanced.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// CascadeTier identifies which instruction-cascade layer a query targets.
//
// GCI = global Claude instructions; ASI = all-sites instructions;
// PPI = per-project instructions; MCI = module/code-level instructions.
type CascadeTier string

const (
	TierGCI CascadeTier = "GCI"
	TierASI CascadeTier = "ASI"
	TierPPI CascadeTier = "PPI"
	TierMCI CascadeTier = "MCI"
)

// AllCascadeTiers is the canonical ordered list used for balance checks.
var AllCascadeTiers = []CascadeTier{TierGCI, TierASI, TierPPI, TierMCI}

// CascadeQuery extends GoldenQuery with a cascade-tier label.
//
// Purpose: One labelled benchmark example tagged with its cascade tier.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.CascadeQuery.
type CascadeQuery struct {
	GoldenQuery
	CascadeTier CascadeTier `json:"cascade_tier"`
}

// CascadeDataset is a loaded collection of cascade-labelled golden queries.
type CascadeDataset struct {
	Name    string
	Queries []CascadeQuery
}

// LoadCascadeDataset reads a JSONL file where each line is a CascadeQuery.
//
// Purpose: Deserialise the cascade-balanced benchmark golden set.
// Inputs:  path to a .jsonl file with cascade_tier on every row.
// Outputs: *CascadeDataset; error on I/O, JSON, or missing/invalid tier.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.LoadCascadeDataset.
func LoadCascadeDataset(path string) (*CascadeDataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("eval: open cascade dataset %q: %w", path, err)
	}
	defer f.Close()

	ds := &CascadeDataset{Name: path}
	scanner := bufio.NewScanner(f)
	// Lines can be long (relevant_chunk_ids lists); raise the buffer ceiling.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var q CascadeQuery
		if err := json.Unmarshal(line, &q); err != nil {
			return nil, fmt.Errorf("eval: parse line %d of %q: %w", lineNo, path, err)
		}
		if q.Query == "" {
			return nil, fmt.Errorf("eval: line %d of %q: query must not be empty", lineNo, path)
		}
		if len(q.RelevantChunkIDs) == 0 {
			return nil, fmt.Errorf("eval: line %d of %q: relevant_chunk_ids must not be empty", lineNo, path)
		}
		if !isValidTier(q.CascadeTier) {
			return nil, fmt.Errorf("eval: line %d of %q: invalid cascade_tier %q", lineNo, path, q.CascadeTier)
		}
		ds.Queries = append(ds.Queries, q)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("eval: scan %q: %w", path, err)
	}
	if len(ds.Queries) == 0 {
		return nil, fmt.Errorf("eval: cascade dataset %q contains no queries", path)
	}
	return ds, nil
}

// CascadeCounts returns the number of queries per tier.
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.CascadeCounts.
func (ds *CascadeDataset) CascadeCounts() map[CascadeTier]int {
	counts := make(map[CascadeTier]int, len(AllCascadeTiers))
	for _, t := range AllCascadeTiers {
		counts[t] = 0
	}
	for _, q := range ds.Queries {
		counts[q.CascadeTier]++
	}
	return counts
}

// AssertBalanced verifies the dataset has at least minPerTier queries in every
// cascade tier (and at least 4×minPerTier total).
//
// Purpose: Enforce the "≥50 per cascade tier, ≥200 total" benchmark contract.
// Inputs:  minPerTier — required minimum per tier (50 for the benchmark set).
// Outputs: error listing any under-populated tier; nil when balanced.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.AssertBalanced.
func (ds *CascadeDataset) AssertBalanced(minPerTier int) error {
	counts := ds.CascadeCounts()
	for _, t := range AllCascadeTiers {
		if counts[t] < minPerTier {
			return fmt.Errorf("eval: cascade tier %s has %d queries, need ≥%d", t, counts[t], minPerTier)
		}
	}
	return nil
}

func isValidTier(t CascadeTier) bool {
	switch t {
	case TierGCI, TierASI, TierPPI, TierMCI:
		return true
	}
	return false
}
