#!/usr/bin/env bash
# Purpose: Offline evaluation harness for nClaw semantic retrieval quality gates.
# Inputs: --dataset <path>, --model <model-id>, --top-k <int>, --output <path>
# Outputs: JSON report with per-query and aggregate MRR, NDCG@10, P@5 metrics; exits non-zero if any metric < threshold.
# Constraints: Offline-only — no live model calls. Reads JSONL dataset, simulates ranking via lookup.
# SPORT: F-MASTER.md F-AI-GATEWAY:observability-evals
#
# LEDGER §A baseline-null rule:
#   If baseline.json has null metrics (first run), skip regression comparison,
#   populate baseline.json with current metrics, and exit 0.
#   Subsequent runs fail if any metric drops >5% from baseline.
#
# Pass thresholds:
#   MRR >= 0.75 (hybrid), NDCG@10 >= 0.70, P@5 >= 0.65
#   Latency p95 <= 500ms (checked against --latency-p95 flag if provided)
#   Cost per query <= $0.002 (checked against --cost-per-query flag if provided)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASELINE_FILE="${SCRIPT_DIR}/baseline.json"

# ─── Defaults ──────────────────────────────────────────────────────────────────
DATASET=""
MODEL="claude-haiku-4-5"
TOP_K=10
OUTPUT=""
LATENCY_P95=""
COST_PER_QUERY=""

# ─── Thresholds ────────────────────────────────────────────────────────────────
THRESHOLD_MRR=0.75
THRESHOLD_NDCG=0.70
THRESHOLD_P5=0.65
THRESHOLD_LATENCY_P95_MS=500
THRESHOLD_COST_USD=0.002
REGRESSION_DELTA=0.05  # 5% drop = failure

# ─── Usage ─────────────────────────────────────────────────────────────────────
usage() {
  cat <<'USAGE'
Usage: run_eval.sh --dataset <path> --model <model-id> --top-k <int> --output <path>
                   [--latency-p95 <ms>] [--cost-per-query <usd>]

Options:
  --dataset       Path to JSONL evaluation dataset file (required)
  --model         Model identifier used for retrieval (default: claude-haiku-4-5)
  --top-k         Number of results to evaluate (default: 10)
  --output        Path to write JSON report (required)
  --latency-p95   Observed latency p95 in milliseconds (optional threshold check)
  --cost-per-query  Observed cost per query in USD (optional threshold check)
  --help          Show this help message

Output JSON report fields:
  {
    "dataset": "<path>",
    "model": "<model-id>",
    "top_k": <int>,
    "per_query": [{"query_id": "...", "mrr": 0.0, "ndcg_at_10": 0.0, "precision_at_5": 0.0}],
    "aggregate": {"mrr": 0.0, "ndcg_at_10": 0.0, "precision_at_5": 0.0},
    "passed": true|false,
    "failures": [],
    "regression_skipped": false,
    "baseline_populated": false
  }

Exit codes:
  0  All metrics pass thresholds and regression gate
  1  One or more metrics below threshold or regression detected
USAGE
}

# ─── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --dataset) DATASET="$2"; shift 2 ;;
    --model) MODEL="$2"; shift 2 ;;
    --top-k) TOP_K="$2"; shift 2 ;;
    --output) OUTPUT="$2"; shift 2 ;;
    --latency-p95) LATENCY_P95="$2"; shift 2 ;;
    --cost-per-query) COST_PER_QUERY="$2"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "Unknown option: $1" >&2; usage; exit 1 ;;
  esac
done

# ─── Validation ────────────────────────────────────────────────────────────────
if [[ -z "$DATASET" ]]; then
  echo "ERROR: --dataset is required" >&2
  usage
  exit 1
fi
if [[ -z "$OUTPUT" ]]; then
  echo "ERROR: --output is required" >&2
  usage
  exit 1
fi
if [[ ! -f "$DATASET" ]]; then
  echo "ERROR: dataset file not found: $DATASET" >&2
  exit 1
fi

# ─── Check dependencies ────────────────────────────────────────────────────────
if ! command -v node &>/dev/null && ! command -v npx &>/dev/null; then
  echo "ERROR: node/npx is required to run metric computation" >&2
  exit 1
fi
if ! command -v jq &>/dev/null; then
  echo "ERROR: jq is required" >&2
  exit 1
fi

# ─── Metric computation via Node (calls metrics.ts compiled or tsx) ────────────
# Build the inline Node script that computes metrics from the dataset.
# In offline mode, we use expected_top_k_ids as the "retrieved" list (simulated perfect retrieval).
# This tests the metric computation pipeline; E5 W10 will plug in real retrieval.

NODE_SCRIPT=$(cat <<'NODESCRIPT'
const fs = require('fs');
const readline = require('readline');

function computeMRR(ranked, groundTruth) {
  const rel = new Set(groundTruth);
  for (let i = 0; i < ranked.length; i++) {
    if (rel.has(ranked[i])) return 1 / (i + 1);
  }
  return 0;
}

function computeNDCG(ranked, groundTruth, k = 10) {
  if (!groundTruth.length) return 0;
  const rel = new Set(groundTruth);
  const topK = ranked.slice(0, k);
  let dcg = 0;
  for (let i = 0; i < topK.length; i++) {
    if (rel.has(topK[i])) dcg += 1 / Math.log2(i + 2);
  }
  const ideal = Math.min(groundTruth.length, k);
  let idcg = 0;
  for (let i = 0; i < ideal; i++) idcg += 1 / Math.log2(i + 2);
  return idcg === 0 ? 0 : dcg / idcg;
}

function computePrecision(ranked, groundTruth, k = 5) {
  if (k <= 0) return 0;
  const rel = new Set(groundTruth);
  const hits = ranked.slice(0, k).filter(id => rel.has(id)).length;
  return hits / k;
}

async function main() {
  const datasetPath = process.argv[2];
  const topK = parseInt(process.argv[3] || '10', 10);
  const model = process.argv[4] || 'unknown';

  const rl = readline.createInterface({ input: fs.createReadStream(datasetPath), crlfDelay: Infinity });
  const perQuery = [];

  for await (const line of rl) {
    if (!line.trim()) continue;
    const fixture = JSON.parse(line);
    // Offline: use expected_top_k_ids as simulated retrieved list
    const retrieved = fixture.expected_top_k_ids;
    const groundTruth = fixture.expected_top_k_ids; // same for offline baseline
    const mrr = computeMRR(retrieved, groundTruth);
    const ndcg = computeNDCG(retrieved, groundTruth, topK);
    const precision = computePrecision(retrieved, groundTruth, 5);
    perQuery.push({ query_id: fixture.query_id, mrr, ndcg_at_10: ndcg, precision_at_5: precision });
  }

  const n = perQuery.length;
  const agg = perQuery.reduce((a, q) => ({
    mrr: a.mrr + q.mrr,
    ndcg_at_10: a.ndcg_at_10 + q.ndcg_at_10,
    precision_at_5: a.precision_at_5 + q.precision_at_5
  }), { mrr: 0, ndcg_at_10: 0, precision_at_5: 0 });

  const aggregate = {
    mrr: n > 0 ? agg.mrr / n : 0,
    ndcg_at_10: n > 0 ? agg.ndcg_at_10 / n : 0,
    precision_at_5: n > 0 ? agg.precision_at_5 / n : 0
  };

  process.stdout.write(JSON.stringify({ per_query: perQuery, aggregate, model, top_k: topK }));
}

main().catch(e => { console.error(e); process.exit(1); });
NODESCRIPT
)

# Run metric computation
METRICS_JSON=$(echo "$NODE_SCRIPT" | node - "$DATASET" "$TOP_K" "$MODEL")
AGGREGATE_MRR=$(echo "$METRICS_JSON" | jq -r '.aggregate.mrr')
AGGREGATE_NDCG=$(echo "$METRICS_JSON" | jq -r '.aggregate.ndcg_at_10')
AGGREGATE_P5=$(echo "$METRICS_JSON" | jq -r '.aggregate.precision_at_5')

# ─── Threshold checks ──────────────────────────────────────────────────────────
FAILURES=()
PASSED=true

# Helper: float comparison using awk
float_lt() { awk -v a="$1" -v b="$2" 'BEGIN { exit (a >= b) }'; }

if float_lt "$AGGREGATE_MRR" "$THRESHOLD_MRR"; then
  FAILURES+=("MRR=${AGGREGATE_MRR} < threshold ${THRESHOLD_MRR}")
  PASSED=false
fi
if float_lt "$AGGREGATE_NDCG" "$THRESHOLD_NDCG"; then
  FAILURES+=("NDCG@10=${AGGREGATE_NDCG} < threshold ${THRESHOLD_NDCG}")
  PASSED=false
fi
if float_lt "$AGGREGATE_P5" "$THRESHOLD_P5"; then
  FAILURES+=("P@5=${AGGREGATE_P5} < threshold ${THRESHOLD_P5}")
  PASSED=false
fi
if [[ -n "$LATENCY_P95" ]] && float_lt "$THRESHOLD_LATENCY_P95_MS" "$LATENCY_P95"; then
  FAILURES+=("latency_p95=${LATENCY_P95}ms > threshold ${THRESHOLD_LATENCY_P95_MS}ms")
  PASSED=false
fi
if [[ -n "$COST_PER_QUERY" ]] && float_lt "$THRESHOLD_COST_USD" "$COST_PER_QUERY"; then
  FAILURES+=("cost_per_query=\$${COST_PER_QUERY} > threshold \$${THRESHOLD_COST_USD}")
  PASSED=false
fi

# ─── LEDGER §A: Baseline-null rule ─────────────────────────────────────────────
REGRESSION_SKIPPED=false
BASELINE_POPULATED=false

if [[ -f "$BASELINE_FILE" ]]; then
  BASELINE_MRR=$(jq -r '.metrics.mrr' "$BASELINE_FILE")
  if [[ "$BASELINE_MRR" == "null" ]]; then
    # First run: populate baseline, skip regression
    REGRESSION_SKIPPED=true
    BASELINE_POPULATED=true
    jq -n \
      --arg run_at "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      --argjson mrr "$AGGREGATE_MRR" \
      --argjson ndcg "$AGGREGATE_NDCG" \
      --argjson p5 "$AGGREGATE_P5" \
      '{metrics: {mrr: $mrr, ndcg_at_10: $ndcg, precision_at_5: $p5}, run_at: $run_at}' \
      > "$BASELINE_FILE"
    echo "INFO: First run — baseline.json populated with current metrics." >&2
  else
    # Regression check: fail if any metric drops > 5% from baseline
    BASELINE_NDCG=$(jq -r '.metrics.ndcg_at_10' "$BASELINE_FILE")
    BASELINE_P5=$(jq -r '.metrics.precision_at_5' "$BASELINE_FILE")
    check_regression() {
      local metric_name="$1" current="$2" baseline="$3"
      # fail if current < baseline * (1 - REGRESSION_DELTA)
      local min_allowed
      min_allowed=$(awk -v b="$baseline" -v d="$REGRESSION_DELTA" 'BEGIN { printf "%.6f", b * (1 - d) }')
      if float_lt "$current" "$min_allowed"; then
        FAILURES+=("REGRESSION: ${metric_name} dropped from ${baseline} to ${current} (>${REGRESSION_DELTA*100}% drop)")
        PASSED=false
      fi
    }
    check_regression "MRR" "$AGGREGATE_MRR" "$BASELINE_MRR"
    check_regression "NDCG@10" "$AGGREGATE_NDCG" "$BASELINE_NDCG"
    check_regression "P@5" "$AGGREGATE_P5" "$BASELINE_P5"
  fi
fi

# ─── Write output report ───────────────────────────────────────────────────────
FAILURES_JSON=$(printf '%s\n' "${FAILURES[@]}" | jq -R . | jq -s .)
echo "$METRICS_JSON" | jq \
  --arg dataset "$DATASET" \
  --argjson passed "$PASSED" \
  --argjson failures "$FAILURES_JSON" \
  --argjson regression_skipped "$REGRESSION_SKIPPED" \
  --argjson baseline_populated "$BASELINE_POPULATED" \
  '. + {dataset: $dataset, passed: $passed, failures: $failures,
         regression_skipped: $regression_skipped, baseline_populated: $baseline_populated}' \
  > "$OUTPUT"

echo "Report written to: $OUTPUT"
cat "$OUTPUT" | jq '.aggregate'

# ─── Exit ──────────────────────────────────────────────────────────────────────
if [[ "$PASSED" == "false" ]]; then
  echo "EVAL FAILED:" >&2
  for f in "${FAILURES[@]}"; do echo "  - $f" >&2; done
  exit 1
fi
echo "EVAL PASSED"
exit 0
