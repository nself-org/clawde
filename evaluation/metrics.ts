/**
 * Purpose: Information retrieval metric functions for offline evaluation of nClaw semantic retrieval.
 * Inputs: ranked_result_ids (retrieved document IDs in rank order), ground_truth_ids (relevant docs), k (cutoff)
 * Outputs: MRR (float 0-1), NDCG@k (float 0-1), Precision@k (float 0-1)
 * Constraints: Pure functions — no I/O, no DB calls. Safe to run in CI without any live services.
 * SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals (evaluation harness component)
 */

/**
 * Computes Mean Reciprocal Rank.
 * MRR = 1 / rank_of_first_relevant_document
 * Returns 0 if no relevant document is found in the ranked list.
 *
 * @param ranked_result_ids - Retrieved document IDs in rank order (rank 1 = index 0)
 * @param ground_truth_ids - Set of relevant document IDs for this query
 * @returns MRR score in [0, 1]
 */
export function computeMRR(
  ranked_result_ids: string[],
  ground_truth_ids: string[]
): number {
  const relevantSet = new Set(ground_truth_ids);
  for (let i = 0; i < ranked_result_ids.length; i++) {
    if (relevantSet.has(ranked_result_ids[i])) {
      // rank is 1-indexed
      return 1 / (i + 1);
    }
  }
  return 0;
}

/**
 * Computes Normalized Discounted Cumulative Gain at cutoff k.
 * DCG@k = sum_{i=1}^{k} rel_i / log2(i + 1)
 * NDCG@k = DCG@k / IDCG@k  where IDCG is the ideal (all relevant docs at top).
 * Binary relevance: rel_i = 1 if doc is in ground_truth_ids, 0 otherwise.
 * Returns 0 if ground_truth_ids is empty.
 *
 * @param ranked_result_ids - Retrieved document IDs in rank order
 * @param ground_truth_ids - Set of relevant document IDs
 * @param k - Rank cutoff (default: 10)
 * @returns NDCG@k score in [0, 1]
 */
export function computeNDCG(
  ranked_result_ids: string[],
  ground_truth_ids: string[],
  k: number = 10
): number {
  if (ground_truth_ids.length === 0) return 0;

  const relevantSet = new Set(ground_truth_ids);
  const topK = ranked_result_ids.slice(0, k);

  // Compute DCG@k
  let dcg = 0;
  for (let i = 0; i < topK.length; i++) {
    if (relevantSet.has(topK[i])) {
      // rank is i+1; log2(rank + 1) = log2(i + 2)
      dcg += 1 / Math.log2(i + 2);
    }
  }

  // Compute IDCG@k — ideal: all relevant docs at top positions
  const idealCount = Math.min(ground_truth_ids.length, k);
  let idcg = 0;
  for (let i = 0; i < idealCount; i++) {
    idcg += 1 / Math.log2(i + 2);
  }

  if (idcg === 0) return 0;
  return dcg / idcg;
}

/**
 * Computes Precision at cutoff k.
 * P@k = (number of relevant docs in top-k) / k
 * Returns 0 if k <= 0.
 *
 * @param ranked_result_ids - Retrieved document IDs in rank order
 * @param ground_truth_ids - Set of relevant document IDs
 * @param k - Rank cutoff (default: 5)
 * @returns Precision@k score in [0, 1]
 */
export function computePrecision(
  ranked_result_ids: string[],
  ground_truth_ids: string[],
  k: number = 5
): number {
  if (k <= 0) return 0;

  const relevantSet = new Set(ground_truth_ids);
  const topK = ranked_result_ids.slice(0, k);
  const hits = topK.filter((id) => relevantSet.has(id)).length;
  return hits / k;
}

/**
 * Aggregates per-query metrics into dataset-level averages.
 *
 * @param queries - Array of {mrr, ndcg, precision} per query
 * @returns Averaged metrics object
 */
export function aggregateMetrics(
  queries: Array<{ mrr: number; ndcg: number; precision: number }>
): { mrr: number; ndcg: number; precision: number } {
  if (queries.length === 0) return { mrr: 0, ndcg: 0, precision: 0 };
  const sum = queries.reduce(
    (acc, q) => ({
      mrr: acc.mrr + q.mrr,
      ndcg: acc.ndcg + q.ndcg,
      precision: acc.precision + q.precision,
    }),
    { mrr: 0, ndcg: 0, precision: 0 }
  );
  const n = queries.length;
  return {
    mrr: sum.mrr / n,
    ndcg: sum.ndcg / n,
    precision: sum.precision / n,
  };
}
