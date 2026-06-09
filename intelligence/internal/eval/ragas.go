// ragas.go — Ragas 4-metric composite score computation (Go reference impl).
//
// Purpose:    Compute the Ragas-style answer-quality composite from the four
//             metrics faithfulness, answer_relevancy, context_precision and
//             context_recall, each in [0,1]. The Python harness at
//             eval/ragas_eval.py (pinned ragas==0.1.21) is the canonical
//             metric producer; this Go side computes the deterministic
//             composite + clamping so the gate can run without Python.
// Inputs:     RagasMetrics (four floats in [0,1]); optional CompositeWeights.
// Outputs:    composite score in [0,1]; weight set used.
// Constraints: File ≤500 lines. Pure, deterministic, no I/O. Weights sum to 1.0.
//              Default weights per LEDGER: 0.30 faithfulness, 0.30 answer_relevancy,
//              0.20 context_precision, 0.20 context_recall.
// SPORT: REGISTRY-FUNCTIONS.md → eval.RagasMetrics, eval.CompositeWeights,
//        eval.CompositeScore, eval.DefaultCompositeWeights.
package eval

import (
	"fmt"
	"math"
)

// RagasMetrics holds the four Ragas answer-quality metrics, each in [0,1].
//
// Purpose: Produced by the Python harness (ragas==0.1.21) per eval run and
//          consumed by CompositeScore + the eval gate.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.RagasMetrics.
type RagasMetrics struct {
	Faithfulness     float64 `json:"faithfulness"`
	AnswerRelevancy  float64 `json:"answer_relevancy"`
	ContextPrecision float64 `json:"context_precision"`
	ContextRecall    float64 `json:"context_recall"`
}

// CompositeWeights are the per-metric weights for the composite score.
// They MUST sum to 1.0 (validated by Validate).
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.CompositeWeights.
type CompositeWeights struct {
	Faithfulness     float64 `json:"faithfulness"`
	AnswerRelevancy  float64 `json:"answer_relevancy"`
	ContextPrecision float64 `json:"context_precision"`
	ContextRecall    float64 `json:"context_recall"`
}

// DefaultCompositeWeights returns the LEDGER-locked default weights:
// 0.30 faithfulness + 0.30 answer_relevancy + 0.20 context_precision
// + 0.20 context_recall.
//
// SPORT: REGISTRY-FUNCTIONS.md → eval.DefaultCompositeWeights.
func DefaultCompositeWeights() CompositeWeights {
	return CompositeWeights{
		Faithfulness:     0.30,
		AnswerRelevancy:  0.30,
		ContextPrecision: 0.20,
		ContextRecall:    0.20,
	}
}

// Validate checks the weights sum to 1.0 (within a small epsilon) and are
// all non-negative.
func (w CompositeWeights) Validate() error {
	if w.Faithfulness < 0 || w.AnswerRelevancy < 0 ||
		w.ContextPrecision < 0 || w.ContextRecall < 0 {
		return fmt.Errorf("ragas: composite weights must be non-negative")
	}
	sum := w.Faithfulness + w.AnswerRelevancy + w.ContextPrecision + w.ContextRecall
	if math.Abs(sum-1.0) > 1e-9 {
		return fmt.Errorf("ragas: composite weights must sum to 1.0, got %g", sum)
	}
	return nil
}

// clamp01 bounds a value to [0,1] to defend against driver noise.
func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// Clamp returns a copy of the metrics with each field bounded to [0,1].
func (m RagasMetrics) Clamp() RagasMetrics {
	return RagasMetrics{
		Faithfulness:     clamp01(m.Faithfulness),
		AnswerRelevancy:  clamp01(m.AnswerRelevancy),
		ContextPrecision: clamp01(m.ContextPrecision),
		ContextRecall:    clamp01(m.ContextRecall),
	}
}

// CompositeScore computes the weighted composite of the four Ragas metrics.
//
// Purpose: Single scalar in [0,1] used by the regression gate and tie-break.
// Inputs:  m — the four metrics (clamped to [0,1] internally).
//          w — weights (must Validate; pass DefaultCompositeWeights() for default).
// Outputs: composite in [0,1]; error if weights are invalid.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.CompositeScore.
func CompositeScore(m RagasMetrics, w CompositeWeights) (float64, error) {
	if err := w.Validate(); err != nil {
		return 0, err
	}
	c := m.Clamp()
	score := w.Faithfulness*c.Faithfulness +
		w.AnswerRelevancy*c.AnswerRelevancy +
		w.ContextPrecision*c.ContextPrecision +
		w.ContextRecall*c.ContextRecall
	return clamp01(score), nil
}
