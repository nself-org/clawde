// Package eval implements the BGE-M3 vs Gemini text-embedding-004 A/B eval harness.
//
// Purpose: Load golden query datasets, run embeddings via pluggable drivers, compute
//          Recall@5, Recall@10, MRR@10, latency p50/p95, and persist results to
//          clawde_eval_runs via Recorder.
// Inputs:  JSONL golden dataset file; EmbeddingDriver implementations.
// Outputs: EvalResult with recall/MRR/latency metrics; rows in clawde_eval_runs.
// Constraints: File ≤500 lines. No live DB or HTTP in unit tests (skip-with-reason).
// SPORT: REGISTRY-FUNCTIONS.md → eval.LoadDataset, eval.EmbeddingDriver.
package eval

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

// QueryCategory classifies the intent of a golden query.
type QueryCategory string

const (
	CategorySymbolLookup  QueryCategory = "symbol_lookup"
	CategoryUsageExample  QueryCategory = "usage_example"
	CategoryBugContext    QueryCategory = "bug_context"
)

// QueryLanguage is the primary programming language for the query context.
type QueryLanguage string

const (
	LangGo         QueryLanguage = "go"
	LangTypeScript QueryLanguage = "typescript"
)

// GoldenQuery is one labelled evaluation example.
//
// Purpose: Carries a natural-language query and the set of chunk IDs that are
//          considered relevant (ground truth). Used by metrics.go to compute recall.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.GoldenQuery.
type GoldenQuery struct {
	// Query is the natural-language question the user might ask.
	Query string `json:"query"`

	// RelevantChunkIDs is the ordered list of chunk IDs that satisfy the query.
	// At least one must appear in the top-k results for a hit to be counted.
	RelevantChunkIDs []string `json:"relevant_chunk_ids"`

	// Language is the programming language context (go | typescript).
	Language QueryLanguage `json:"language"`

	// Category classifies the query intent (symbol_lookup | usage_example | bug_context).
	Category QueryCategory `json:"category"`
}

// Dataset is a loaded collection of golden queries.
type Dataset struct {
	Name    string
	Queries []GoldenQuery
}

// LoadDataset reads a JSONL file where each line is a GoldenQuery JSON object.
//
// Purpose: Deserialise the golden query file for use in eval runs.
// Inputs:  path — absolute or relative path to a .jsonl file.
// Outputs: *Dataset; error on I/O or JSON parse failure.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.LoadDataset.
func LoadDataset(path string) (*Dataset, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("eval: open dataset %q: %w", path, err)
	}
	defer f.Close()

	ds := &Dataset{Name: path}
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var q GoldenQuery
		if err := json.Unmarshal(line, &q); err != nil {
			return nil, fmt.Errorf("eval: parse line %d of %q: %w", lineNo, path, err)
		}
		if q.Query == "" {
			return nil, fmt.Errorf("eval: line %d of %q: query must not be empty", lineNo, path)
		}
		if len(q.RelevantChunkIDs) == 0 {
			return nil, fmt.Errorf("eval: line %d of %q: relevant_chunk_ids must not be empty", lineNo, path)
		}
		ds.Queries = append(ds.Queries, q)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("eval: scan %q: %w", path, err)
	}
	if len(ds.Queries) == 0 {
		return nil, fmt.Errorf("eval: dataset %q contains no queries", path)
	}
	return ds, nil
}
