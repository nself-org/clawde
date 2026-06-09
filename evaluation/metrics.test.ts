/**
 * Purpose: Unit tests for information retrieval metric functions.
 * Tests computeMRR, computeNDCG, computePrecision with known inputs and expected outputs.
 * All tests are pure — no I/O, no DB, no external calls.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals
 */

import { computeMRR, computeNDCG, computePrecision, aggregateMetrics } from "./metrics";

// ─── computeMRR ───────────────────────────────────────────────────────────────

describe("computeMRR", () => {
  it("returns 1.0 when first result is relevant", () => {
    const result = computeMRR(["a", "b", "c"], ["a", "d"]);
    expect(result).toBe(1.0);
  });

  it("returns 0.5 when second result is first relevant", () => {
    const result = computeMRR(["x", "a", "b"], ["a"]);
    expect(result).toBe(0.5);
  });

  it("returns 1/3 when third result is first relevant", () => {
    const result = computeMRR(["x", "y", "a", "b"], ["a"]);
    expect(result).toBeCloseTo(1 / 3, 6);
  });

  it("returns 0 when no result is relevant", () => {
    const result = computeMRR(["x", "y", "z"], ["a", "b"]);
    expect(result).toBe(0);
  });

  it("returns 0 for empty ranked list", () => {
    const result = computeMRR([], ["a"]);
    expect(result).toBe(0);
  });

  it("handles multiple relevant docs — only first rank matters", () => {
    const result = computeMRR(["x", "a", "b"], ["a", "b"]);
    expect(result).toBe(0.5); // first relevant at rank 2
  });
});

// ─── computeNDCG ──────────────────────────────────────────────────────────────

describe("computeNDCG", () => {
  it("returns 1.0 for perfect ranking (all relevant docs at top)", () => {
    // Ideal: relevant docs [a,b,c] all in top-3 → NDCG = 1
    const result = computeNDCG(["a", "b", "c", "d"], ["a", "b", "c"], 3);
    expect(result).toBeCloseTo(1.0, 6);
  });

  it("returns 0 for empty ground truth", () => {
    const result = computeNDCG(["a", "b", "c"], [], 3);
    expect(result).toBe(0);
  });

  it("returns 0 when no retrieved result is relevant", () => {
    const result = computeNDCG(["x", "y", "z"], ["a", "b"], 3);
    expect(result).toBe(0);
  });

  it("computes correct NDCG@3 for partial relevance", () => {
    // Retrieved: [a, x, b] — a (relevant, rank 1), x (not), b (relevant, rank 3)
    // Ground truth: [a, b, c]
    // DCG = 1/log2(2) + 0 + 1/log2(4) = 1 + 0.5 = 1.5
    // IDCG = 1/log2(2) + 1/log2(3) + 1/log2(4) ≈ 1 + 0.631 + 0.5 = 2.131
    const retrieved = ["a", "x", "b"];
    const groundTruth = ["a", "b", "c"];
    const dcg = 1 / Math.log2(2) + 1 / Math.log2(4); // 1 + 0.5
    const idcg =
      1 / Math.log2(2) + 1 / Math.log2(3) + 1 / Math.log2(4);
    const expected = dcg / idcg;
    const result = computeNDCG(retrieved, groundTruth, 3);
    expect(result).toBeCloseTo(expected, 6);
  });

  it("uses k=10 as default cutoff", () => {
    const retrieved = ["a", "b", "c"];
    const groundTruth = ["a", "b", "c"];
    const result = computeNDCG(retrieved, groundTruth); // no k provided
    expect(result).toBeCloseTo(1.0, 6);
  });
});

// ─── computePrecision ─────────────────────────────────────────────────────────

describe("computePrecision", () => {
  it("returns 1.0 when all top-k results are relevant", () => {
    const result = computePrecision(["a", "b", "c", "d"], ["a", "b", "c"], 3);
    expect(result).toBe(1.0);
  });

  it("returns 0 when no top-k result is relevant", () => {
    const result = computePrecision(["x", "y", "z"], ["a", "b"], 3);
    expect(result).toBe(0);
  });

  it("returns 2/5 with 2 relevant docs in top-5", () => {
    const result = computePrecision(
      ["a", "x", "y", "b", "z"],
      ["a", "b"],
      5
    );
    expect(result).toBeCloseTo(2 / 5, 6);
  });

  it("returns 0 for k <= 0", () => {
    const result = computePrecision(["a", "b"], ["a"], 0);
    expect(result).toBe(0);
  });

  it("uses k=5 as default cutoff", () => {
    const result = computePrecision(["a", "b", "c", "d", "e"], ["a", "b"], 5);
    expect(result).toBe(2 / 5);
  });
});

// ─── aggregateMetrics ─────────────────────────────────────────────────────────

describe("aggregateMetrics", () => {
  it("averages metrics correctly over multiple queries", () => {
    const queries = [
      { mrr: 1.0, ndcg: 0.8, precision: 0.6 },
      { mrr: 0.5, ndcg: 0.6, precision: 0.4 },
    ];
    const result = aggregateMetrics(queries);
    expect(result.mrr).toBeCloseTo(0.75, 6);
    expect(result.ndcg).toBeCloseTo(0.7, 6);
    expect(result.precision).toBeCloseTo(0.5, 6);
  });

  it("returns zeros for empty input", () => {
    const result = aggregateMetrics([]);
    expect(result).toEqual({ mrr: 0, ndcg: 0, precision: 0 });
  });
});
