// gate_test.go — unit tests for Ragas composite, regression gate, tie-break,
// baseline-null skip, and the 200-item cascade dataset loader (50/tier).
//
// Purpose: Exhaustively verify the deterministic eval-gate math without any
//          live DB, HTTP, or Python. Python/DB integration paths skip-with-reason.
// Constraints: All math is pure; no randomness; no network.
package eval

import (
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const floatEps = 1e-9

// ── Ragas composite ────────────────────────────────────────────────────────────

func TestDefaultCompositeWeights_sumToOne(t *testing.T) {
	if err := DefaultCompositeWeights().Validate(); err != nil {
		t.Fatalf("default weights invalid: %v", err)
	}
}

func TestCompositeScore_default(t *testing.T) {
	m := RagasMetrics{Faithfulness: 0.8, AnswerRelevancy: 0.6, ContextPrecision: 0.5, ContextRecall: 1.0}
	got, err := CompositeScore(m, DefaultCompositeWeights())
	if err != nil {
		t.Fatal(err)
	}
	// 0.30*0.8 + 0.30*0.6 + 0.20*0.5 + 0.20*1.0 = 0.24+0.18+0.10+0.20 = 0.72
	want := 0.72
	if math.Abs(got-want) > floatEps {
		t.Fatalf("composite = %v, want %v", got, want)
	}
}

func TestCompositeScore_clamps(t *testing.T) {
	m := RagasMetrics{Faithfulness: 1.5, AnswerRelevancy: -0.5, ContextPrecision: 0.5, ContextRecall: 0.5}
	got, err := CompositeScore(m, DefaultCompositeWeights())
	if err != nil {
		t.Fatal(err)
	}
	// clamped: 1.0, 0.0, 0.5, 0.5 -> 0.30+0+0.10+0.10 = 0.50
	if math.Abs(got-0.50) > floatEps {
		t.Fatalf("clamped composite = %v, want 0.50", got)
	}
}

func TestCompositeScore_badWeights(t *testing.T) {
	bad := CompositeWeights{Faithfulness: 0.5, AnswerRelevancy: 0.5, ContextPrecision: 0.5, ContextRecall: 0.5}
	if _, err := CompositeScore(RagasMetrics{}, bad); err == nil {
		t.Fatal("expected error for weights summing to 2.0")
	}
}

// ── Regression gate ─────────────────────────────────────────────────────────────

func TestRegressionGate_baselineNullSkips(t *testing.T) {
	cur := ProviderResult{Provider: providerBGE, CompositeScore: 0.10}
	res := RegressionGate(cur, Baseline{}, DefaultRegressionTolerance())
	if !res.Skipped || !res.Passed {
		t.Fatalf("baseline-null must skip+pass, got %+v", res)
	}
}

func TestRegressionGate_passWithinTolerance(t *testing.T) {
	base := 0.70
	cur := ProviderResult{Provider: providerBGE, CompositeScore: 0.69} // -0.01 within 0.02
	res := RegressionGate(cur, Baseline{CompositeScore: &base}, DefaultRegressionTolerance())
	if res.Skipped || !res.Passed {
		t.Fatalf("expected pass within tolerance, got %+v", res)
	}
}

func TestRegressionGate_failOnRegression(t *testing.T) {
	base := 0.70
	cur := ProviderResult{Provider: providerBGE, CompositeScore: 0.60} // -0.10 exceeds 0.02
	res := RegressionGate(cur, Baseline{CompositeScore: &base}, DefaultRegressionTolerance())
	if res.Passed {
		t.Fatalf("expected fail on regression, got %+v", res)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	// Missing file -> baseline-null.
	b, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.HasMetrics() {
		t.Fatal("missing baseline must have no metrics")
	}
	// Write then reload.
	if err := WriteBaseline(path, ProviderResult{Provider: providerBGE, RecallAt10: 0.9, P95Ms: 120, CompositeScore: 0.77}); err != nil {
		t.Fatal(err)
	}
	b2, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if !b2.HasMetrics() || math.Abs(*b2.CompositeScore-0.77) > floatEps {
		t.Fatalf("reloaded baseline wrong: %+v", b2)
	}
}

// ── Tie-break: both branches ─────────────────────────────────────────────────────

func TestTieBreak_geminiWins(t *testing.T) {
	// recall 0.95 > 0.80*1.05 (0.84), p95 150 < 200, composite clearly apart.
	bge := ProviderResult{Provider: providerBGE, RecallAt10: 0.80, P95Ms: 100, CompositeScore: 0.70}
	gem := ProviderResult{Provider: providerGemini, RecallAt10: 0.95, P95Ms: 150, CompositeScore: 0.78}
	d := TieBreak(bge, gem)
	if d.Winner != providerGemini {
		t.Fatalf("expected gemini win, got %s (%s)", d.Winner, d.Reason)
	}
}

func TestTieBreak_bgeWins_nearTieEpsilon(t *testing.T) {
	// composite delta 0.005 < 0.01 -> BGE wins unconditionally, even if recall/p95 favor gemini.
	bge := ProviderResult{Provider: providerBGE, RecallAt10: 0.80, P95Ms: 100, CompositeScore: 0.700}
	gem := ProviderResult{Provider: providerGemini, RecallAt10: 0.99, P95Ms: 50, CompositeScore: 0.705}
	d := TieBreak(bge, gem)
	if d.Winner != providerBGE {
		t.Fatalf("expected bge win on near-tie, got %s (%s)", d.Winner, d.Reason)
	}
}

func TestTieBreak_bgeWins_recallTooLow(t *testing.T) {
	// composite apart (0.05) but recall margin not cleared -> BGE wins (default).
	bge := ProviderResult{Provider: providerBGE, RecallAt10: 0.90, P95Ms: 100, CompositeScore: 0.70}
	gem := ProviderResult{Provider: providerGemini, RecallAt10: 0.92, P95Ms: 100, CompositeScore: 0.75} // 0.92 < 0.90*1.05=0.945
	d := TieBreak(bge, gem)
	if d.Winner != providerBGE {
		t.Fatalf("expected bge win (recall margin), got %s (%s)", d.Winner, d.Reason)
	}
}

func TestTieBreak_bgeWins_p95TooHigh(t *testing.T) {
	bge := ProviderResult{Provider: providerBGE, RecallAt10: 0.80, P95Ms: 100, CompositeScore: 0.70}
	gem := ProviderResult{Provider: providerGemini, RecallAt10: 0.95, P95Ms: 250, CompositeScore: 0.80} // p95 250 >= 200
	d := TieBreak(bge, gem)
	if d.Winner != providerBGE {
		t.Fatalf("expected bge win (p95 ceiling), got %s (%s)", d.Winner, d.Reason)
	}
}

// ── Cascade dataset: 200 items, 50 per tier ──────────────────────────────────────

func cascadePath(t *testing.T) string {
	t.Helper()
	// internal/eval -> repo root is two levels up.
	p, err := filepath.Abs(filepath.Join("..", "..", "eval", "testdata", "golden_cascade.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadCascadeDataset_200items_50perTier(t *testing.T) {
	ds, err := LoadCascadeDataset(cascadePath(t))
	if err != nil {
		t.Fatalf("load cascade dataset: %v", err)
	}
	if len(ds.Queries) < 200 {
		t.Fatalf("expected ≥200 queries, got %d", len(ds.Queries))
	}
	counts := ds.CascadeCounts()
	for _, tier := range AllCascadeTiers {
		if counts[tier] < 50 {
			t.Errorf("tier %s has %d, want ≥50", tier, counts[tier])
		}
	}
	if err := ds.AssertBalanced(50); err != nil {
		t.Fatalf("AssertBalanced(50): %v", err)
	}
}

func TestAssertBalanced_failsWhenShort(t *testing.T) {
	ds := &CascadeDataset{Queries: []CascadeQuery{
		{GoldenQuery: GoldenQuery{Query: "q", RelevantChunkIDs: []string{"c"}}, CascadeTier: TierGCI},
	}}
	if err := ds.AssertBalanced(50); err == nil {
		t.Fatal("expected AssertBalanced to fail for under-populated dataset")
	}
}

func TestLoadCascadeDataset_invalidTier(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "bad.jsonl")
	if err := os.WriteFile(p, []byte(`{"query":"q","relevant_chunk_ids":["c"],"cascade_tier":"XXX"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCascadeDataset(p); err == nil {
		t.Fatal("expected error for invalid cascade_tier")
	}
}

// ── Python/Ragas integration: skip-with-reason ──────────────────────────────────

func TestRagasHarness_integration(t *testing.T) {
	if os.Getenv("CLAWDE_RUN_RAGAS") == "" {
		t.Skip("skip-with-reason: Ragas integration requires CLAWDE_RUN_RAGAS=1 + pip install -r eval/requirements.txt (ragas==0.1.21); not run in default CI")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("skip-with-reason: python3 not on PATH")
	}
	// Real invocation lives behind the env gate; default CI never reaches here.
	t.Log("ragas harness gate enabled; invoke eval/ragas_eval.py externally")
}
