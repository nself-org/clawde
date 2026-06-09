#!/usr/bin/env python3
"""ragas_eval.py — canonical Ragas 4-metric producer (pinned ragas==0.1.21).

Purpose:    Compute faithfulness, answer_relevancy, context_precision and
            context_recall for an eval run, then emit a JSON document the Go
            gate consumes (internal/eval/ragas.go computes the composite).
Inputs:     --samples PATH  JSONL of {question, answer, contexts, ground_truth}.
            --out PATH      where to write the metrics JSON.
Outputs:    {"faithfulness":x,"answer_relevancy":x,
             "context_precision":x,"context_recall":x} all in [0,1].
Constraints: ragas==0.1.21 EXACT (see requirements.txt). The Go integration
             test skips with a reason when this harness / its deps are absent,
             so `go build && go test` stays green without Python.
SPORT:      REGISTRY-FUNCTIONS.md -> eval ragas harness (Python reference).

Run:
    pip install -r eval/requirements.txt
    python eval/ragas_eval.py --samples eval/testdata/ragas_samples.jsonl \
        --out eval/testdata/ragas_metrics.json
"""
import argparse
import json
import sys

# The four metric names MUST match RagasMetrics json tags in ragas.go.
METRIC_NAMES = ["faithfulness", "answer_relevancy", "context_precision", "context_recall"]


def load_samples(path):
    rows = []
    with open(path, "r", encoding="utf-8") as fh:
        for line in fh:
            line = line.strip()
            if not line:
                continue
            rows.append(json.loads(line))
    return rows


def compute(samples):
    """Compute the four Ragas metrics over the samples.

    Uses ragas==0.1.21's evaluate() over a HuggingFace Dataset. Each sample
    needs: question, answer, contexts (list[str]), ground_truth.
    """
    from datasets import Dataset  # noqa: WPS433  (lazy import for skip-safety)
    from ragas import evaluate
    from ragas.metrics import (
        faithfulness,
        answer_relevancy,
        context_precision,
        context_recall,
    )

    ds = Dataset.from_list(
        [
            {
                "question": s["question"],
                "answer": s["answer"],
                "contexts": s["contexts"],
                "ground_truth": s["ground_truth"],
            }
            for s in samples
        ]
    )
    result = evaluate(
        ds,
        metrics=[faithfulness, answer_relevancy, context_precision, context_recall],
    )
    scores = result.to_pandas()[METRIC_NAMES].mean().to_dict()
    return {k: float(scores[k]) for k in METRIC_NAMES}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--samples", required=True)
    ap.add_argument("--out", required=True)
    args = ap.parse_args()

    samples = load_samples(args.samples)
    if not samples:
        print("ragas_eval: no samples", file=sys.stderr)
        sys.exit(2)

    metrics = compute(samples)
    with open(args.out, "w", encoding="utf-8") as fh:
        json.dump(metrics, fh, indent=2)
        fh.write("\n")
    print(json.dumps(metrics))


if __name__ == "__main__":
    main()
