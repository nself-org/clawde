# ClawDE Retrieval Evaluation

This page covers the offline evaluation harness, golden dataset format, pass thresholds, and regression gate for nClaw semantic retrieval quality.

## Golden dataset format

Three JSONL files live in `evaluation/datasets/`:

| File | Retrieval type | Minimum fixtures |
|---|---|---|
| `semantic.jsonl` | Dense vector (pgvector cosine) | 20 |
| `keyword.jsonl` | BM25 full-text (tsvector GIN) | 20 |
| `hybrid.jsonl` | RRF fusion (dense + lexical + reranker) | 20 |

Each line is one JSON fixture:

```json
{
  "query_id": "hyb-001",
  "query": "how to configure nself plugin with authentication JWT token",
  "retrieval_type": "hybrid",
  "expected_top_k_ids": ["doc-nself-plugin-007", "doc-auth-jwt-003", "doc-nself-config-011"],
  "minimum_mrr": 0.75,
  "ground_truth_source": "human-annotated",
  "tags": ["nself", "plugin", "authentication", "jwt"]
}
```

**Field requirements:**
- `ground_truth_source`: at least 10 fixtures per file must be `human-annotated`; remainder may be `synthetic`
- `expected_top_k_ids`: the ordered list of relevant document IDs for this query

Validate datasets: `npx tsx evaluation/validate-datasets.ts`

## Metrics

Defined in `evaluation/metrics.ts`:

| Metric | Formula | Cutoff |
|---|---|---|
| MRR | `1 / rank_of_first_relevant_doc` | first relevant |
| NDCG@10 | `DCG@10 / IDCG@10` (binary relevance) | k=10 |
| P@5 | `relevant_in_top_5 / 5` | k=5 |

## Running the eval harness

```bash
./evaluation/run_eval.sh \
  --dataset evaluation/datasets/hybrid.jsonl \
  --model claude-haiku-4-5 \
  --top-k 10 \
  --output /tmp/eval-report.json
```

The script exits non-zero if any metric is below its threshold.

## Pass thresholds

| Metric | Threshold |
|---|---|
| MRR (hybrid) | ≥ 0.75 |
| NDCG@10 | ≥ 0.70 |
| P@5 | ≥ 0.65 |
| Latency p95 | ≤ 500ms |
| Cost per query | ≤ $0.002 |

## Regression gate

The CI workflow (`.github/workflows/eval.yml`) triggers on changes to `nclaw/core/retrieval/` or `plugins-pro/paid/nself-ai-mcp/`.

**LEDGER §A — Baseline-null rule:**
- First run: `evaluation/baseline.json` has `null` metric values. The harness populates baseline and exits 0.
- Subsequent runs: fail if any metric drops more than 5% from the baseline.

Check `evaluation/baseline.json` to see the current baseline metric values.

## Dataset validation

```bash
npx tsx evaluation/validate-datasets.ts
```

Exits non-zero if any fixture is missing required fields, has an invalid `retrieval_type`, or if a file has fewer than 20 fixtures.
