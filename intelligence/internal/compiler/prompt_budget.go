// prompt_budget.go — token budgeting for the compiled context block.
//
// Purpose: Enforce the canonical 60% chunks / 25% symbols / 15% findings token
//          split under CLAWDE_CONTEXT_TOKEN_LIMIT (default 8192). Items are
//          token-counted (tiktoken-go, with a deterministic offline fallback)
//          and the lowest-scored items per section are trimmed until each
//          section fits its sub-budget.
// Inputs:  total token budget, RetrievalResult.
// Outputs: BudgetedContext{Chunks, Symbols, Findings} that fits the split.
// Constraints: File ≤500 lines. tiktoken is best-effort; never panics offline.
// SPORT: REGISTRY-FUNCTIONS.md → compiler.Allocate, compiler.BudgetedContext,
//        compiler.CountTokens.
package compiler

import (
	"os"
	"sort"
	"strconv"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

const (
	// DefaultTokenLimit is the canonical CLAWDE_CONTEXT_TOKEN_LIMIT default.
	DefaultTokenLimit = 8192
	// tokenLimitEnv is the override env var.
	tokenLimitEnv = "CLAWDE_CONTEXT_TOKEN_LIMIT"

	// Canonical budget split. Sums to 1.0.
	chunkShare   = 0.60
	symbolShare  = 0.25
	findingShare = 0.15
)

// BudgetedContext is the trimmed, budget-fitting result of Allocate.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.BudgetedContext.
type BudgetedContext struct {
	Chunks   []ScoredChunk
	Symbols  []ScoredSymbol
	Findings []ScoredFinding
}

// tokenLimitFromEnv reads CLAWDE_CONTEXT_TOKEN_LIMIT or returns the default.
func tokenLimitFromEnv() int {
	if v := os.Getenv(tokenLimitEnv); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return DefaultTokenLimit
}

// Allocate trims rr to fit the 60/25/15 split under totalTokens. When
// totalTokens <= 0 the env-configured limit is used.
//
// Lowest-scored items in each section are dropped first until the section's
// token sum is within its sub-budget.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.Allocate.
func Allocate(totalTokens int, rr RetrievalResult) BudgetedContext {
	if totalTokens <= 0 {
		totalTokens = tokenLimitFromEnv()
	}
	chunkBudget := int(float64(totalTokens) * chunkShare)
	symbolBudget := int(float64(totalTokens) * symbolShare)
	findingBudget := int(float64(totalTokens) * findingShare)

	return BudgetedContext{
		Chunks:   trimChunks(rr.Chunks, chunkBudget),
		Symbols:  trimSymbols(rr.Symbols, symbolBudget),
		Findings: trimFindings(rr.Findings, findingBudget),
	}
}

// trimChunks keeps highest-scored chunks whose cumulative tokens fit budget.
func trimChunks(in []ScoredChunk, budget int) []ScoredChunk {
	if len(in) == 0 || budget <= 0 {
		return nil
	}
	items := append([]ScoredChunk(nil), in...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	var out []ScoredChunk
	used := 0
	for _, c := range items {
		t := CountTokens(c.Content) + CountTokens(c.FilePath) + 8 // fences/labels
		if used+t > budget {
			continue
		}
		used += t
		out = append(out, c)
	}
	return out
}

// trimSymbols keeps highest-scored symbols within budget.
func trimSymbols(in []ScoredSymbol, budget int) []ScoredSymbol {
	if len(in) == 0 || budget <= 0 {
		return nil
	}
	items := append([]ScoredSymbol(nil), in...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	var out []ScoredSymbol
	used := 0
	for _, s := range items {
		t := CountTokens(s.Name) + CountTokens(s.Signature) + CountTokens(s.FilePath) + 4
		if used+t > budget {
			continue
		}
		used += t
		out = append(out, s)
	}
	return out
}

// trimFindings keeps highest-scored findings within budget.
func trimFindings(in []ScoredFinding, budget int) []ScoredFinding {
	if len(in) == 0 || budget <= 0 {
		return nil
	}
	items := append([]ScoredFinding(nil), in...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	var out []ScoredFinding
	used := 0
	for _, f := range items {
		t := CountTokens(f.Message) + CountTokens(f.Rule) + CountTokens(f.FilePath) + 6
		if used+t > budget {
			continue
		}
		used += t
		out = append(out, f)
	}
	return out
}

// tiktoken encoder is cached; nil after a failed (offline) init so we fall back.
var (
	encOnce sync.Once
	encoder *tiktoken.Tiktoken
)

// CountTokens returns the token count of s. It uses tiktoken (cl100k_base) when
// the encoder loads; otherwise it falls back to a deterministic 4-chars/token
// heuristic so unit tests run offline without network access.
//
// SPORT: REGISTRY-FUNCTIONS.md → compiler.CountTokens.
func CountTokens(s string) int {
	if s == "" {
		return 0
	}
	encOnce.Do(func() {
		// GetEncoding may fetch the BPE file; ignore errors and fall back.
		enc, err := tiktoken.GetEncoding("cl100k_base")
		if err == nil {
			encoder = enc
		}
	})
	if encoder != nil {
		return len(encoder.Encode(s, nil, nil))
	}
	return (len(s) + 3) / 4
}
