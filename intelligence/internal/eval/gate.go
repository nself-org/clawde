// gate.go — eval regression gate + deterministic BGE-M3 vs Gemini tie-break.
//
// Purpose:    Decide pass/fail of an eval run against a stored baseline, and
//             pick the winning embedding provider with NO build-time judgment.
//             All thresholds come from the LEDGER and are encoded as constants.
// Inputs:     Baseline JSON (baseline.json); current ProviderResult(s).
// Outputs:    GateResult (pass/fail + reason); TieBreakDecision (winner + reason).
// Constraints: File ≤500 lines. Pure + deterministic. Baseline-null rule:
//              if baseline.CompositeScore is null/absent, the regression gate is
//              SKIPPED (first run has nothing to compare against).
// SPORT: REGISTRY-FUNCTIONS.md → eval.LoadBaseline, eval.Baseline,
//        eval.RegressionGate, eval.TieBreak, eval.GateResult, eval.TieBreakDecision.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
)

// LEDGER-locked tie-break constants. Changing these requires an ADR.
const (
	// tieBreakRecallMargin: Gemini must beat BGE recall@10 by >5%.
	tieBreakRecallMargin = 1.05
	// tieBreakP95CeilingMs: Gemini's p95 latency must be under this to win.
	tieBreakP95CeilingMs = 200
	// tieBreakCompositeEpsilon: if the composite delta is below this, BGE-M3
	// wins unconditionally (a near-tie resolves to the default provider).
	tieBreakCompositeEpsilon = 0.01

	providerBGE    = "bge-m3"
	providerGemini = "gemini-text-embedding-004"

	// defaultRegressionTolerance: current composite may dip below baseline by
	// at most this much before the gate fails.
	defaultRegressionTolerance = 0.02
)

// ProviderResult is the per-provider summary the gate operates on.
//
// Purpose: Decouple the gate from EvalResult so callers can also feed Ragas
//          composites that are not part of the retrieval EvalResult.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.ProviderResult.
type ProviderResult struct {
	Provider       string  `json:"provider"`
	RecallAt10     float64 `json:"recall_at_10"`
	P95Ms          int     `json:"p95_ms"`
	CompositeScore float64 `json:"composite_score"`
}

// Baseline is the persisted prior run that a new run is measured against.
//
// Purpose: Stored as baseline.json next to the dataset; updated after a passing
//          run is accepted. A pointer CompositeScore distinguishes "no baseline
//          yet" (nil → skip regression gate) from a real zero score.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.Baseline.
type Baseline struct {
	Provider       string   `json:"provider"`
	RecallAt10     float64  `json:"recall_at_10"`
	P95Ms          int      `json:"p95_ms"`
	CompositeScore *float64 `json:"composite_score"`
}

// HasMetrics reports whether the baseline carries a composite score to compare.
// When false, the regression gate is skipped (baseline-null rule).
func (b Baseline) HasMetrics() bool { return b.CompositeScore != nil }

// LoadBaseline reads a baseline.json file. A missing file is NOT an error —
// it returns an empty Baseline (HasMetrics()==false) so the first run skips
// the regression gate per the baseline-null rule.
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.LoadBaseline.
func LoadBaseline(path string) (Baseline, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Baseline{}, nil // baseline-null: nothing to compare
		}
		return Baseline{}, fmt.Errorf("eval: read baseline %q: %w", path, err)
	}
	var b Baseline
	if err := json.Unmarshal(data, &b); err != nil {
		return Baseline{}, fmt.Errorf("eval: parse baseline %q: %w", path, err)
	}
	return b, nil
}

// WriteBaseline persists a baseline.json from a passing ProviderResult.
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.WriteBaseline.
func WriteBaseline(path string, r ProviderResult) error {
	score := r.CompositeScore
	b := Baseline{
		Provider:       r.Provider,
		RecallAt10:     r.RecallAt10,
		P95Ms:          r.P95Ms,
		CompositeScore: &score,
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: marshal baseline: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("eval: write baseline %q: %w", path, err)
	}
	return nil
}

// GateResult is the outcome of a regression gate evaluation.
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.GateResult.
type GateResult struct {
	Passed  bool    // false → regression detected, block the run
	Skipped bool    // true → baseline-null, gate not applicable
	Delta   float64 // current composite − baseline composite (0 when skipped)
	Reason  string  // human-readable explanation
}

// RegressionGate compares a current ProviderResult to a Baseline.
//
// Purpose: Block runs whose composite score regressed beyond tolerance.
//          Baseline-null rule: when baseline has no metrics, returns
//          Skipped=true, Passed=true (nothing to compare against).
// Inputs:  current result; baseline; tolerance (use defaultRegressionTolerance
//          via DefaultRegressionTolerance() — pass 0 to mean "any drop fails").
// Outputs: GateResult.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.RegressionGate.
func RegressionGate(current ProviderResult, baseline Baseline, tolerance float64) GateResult {
	if !baseline.HasMetrics() {
		return GateResult{
			Passed:  true,
			Skipped: true,
			Reason:  "baseline-null: first eval run, regression gate skipped",
		}
	}
	base := *baseline.CompositeScore
	delta := current.CompositeScore - base
	if delta >= -tolerance {
		return GateResult{
			Passed: true,
			Delta:  delta,
			Reason: fmt.Sprintf("composite %.4f vs baseline %.4f (delta %+.4f, tol %.4f) — pass",
				current.CompositeScore, base, delta, tolerance),
		}
	}
	return GateResult{
		Passed: false,
		Delta:  delta,
		Reason: fmt.Sprintf("regression: composite %.4f vs baseline %.4f (delta %+.4f exceeds tol %.4f)",
			current.CompositeScore, base, delta, tolerance),
	}
}

// DefaultRegressionTolerance returns the LEDGER default regression tolerance.
func DefaultRegressionTolerance() float64 { return defaultRegressionTolerance }

// TieBreakDecision records the deterministic provider choice + rationale.
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.TieBreakDecision.
type TieBreakDecision struct {
	Winner string  // providerBGE or providerGemini
	Reason string  // why this provider won, citing the LEDGER rule
	Delta  float64 // composite delta gemini − bge (signed)
}

// TieBreak applies the LEDGER deterministic tie-break between BGE-M3 and Gemini.
//
// Rule (verbatim from LEDGER, no build-time judgment):
//  1. If |composite(gemini) − composite(bge)| < 0.01 → BGE-M3 wins unconditionally.
//  2. Else Gemini wins ONLY IF recall@10(gemini) > recall@10(bge)×1.05
//     AND p95(gemini) < 200ms.
//  3. Otherwise BGE-M3 wins (default provider).
//
// Inputs:  bge, gemini — ProviderResult for each provider.
// Outputs: TieBreakDecision.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.TieBreak.
func TieBreak(bge, gemini ProviderResult) TieBreakDecision {
	delta := gemini.CompositeScore - bge.CompositeScore

	// Rule 1: near-tie on composite → BGE-M3 wins unconditionally.
	if abs(delta) < tieBreakCompositeEpsilon {
		return TieBreakDecision{
			Winner: providerBGE,
			Delta:  delta,
			Reason: fmt.Sprintf("composite delta %.4f < epsilon %.2f — BGE-M3 wins unconditionally (near-tie)",
				delta, tieBreakCompositeEpsilon),
		}
	}

	// Rule 2: Gemini wins only if it clears BOTH the recall margin and p95 ceiling.
	recallClear := gemini.RecallAt10 > bge.RecallAt10*tieBreakRecallMargin
	p95Clear := gemini.P95Ms < tieBreakP95CeilingMs
	if recallClear && p95Clear {
		return TieBreakDecision{
			Winner: providerGemini,
			Delta:  delta,
			Reason: fmt.Sprintf("Gemini wins: recall@10 %.4f > BGE %.4f×%.2f AND p95 %dms < %dms",
				gemini.RecallAt10, bge.RecallAt10, tieBreakRecallMargin, gemini.P95Ms, tieBreakP95CeilingMs),
		}
	}

	// Rule 3: default to BGE-M3 with the specific failing condition.
	reason := "BGE-M3 wins (default): Gemini failed tie-break — "
	switch {
	case !recallClear && !p95Clear:
		reason += fmt.Sprintf("recall@10 %.4f ≤ BGE %.4f×%.2f AND p95 %dms ≥ %dms",
			gemini.RecallAt10, bge.RecallAt10, tieBreakRecallMargin, gemini.P95Ms, tieBreakP95CeilingMs)
	case !recallClear:
		reason += fmt.Sprintf("recall@10 %.4f ≤ BGE %.4f×%.2f", gemini.RecallAt10, bge.RecallAt10, tieBreakRecallMargin)
	default:
		reason += fmt.Sprintf("p95 %dms ≥ %dms", gemini.P95Ms, tieBreakP95CeilingMs)
	}
	return TieBreakDecision{Winner: providerBGE, Delta: delta, Reason: reason}
}

// abs is a tiny float helper (avoids importing math just for Abs in hot paths).
func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
