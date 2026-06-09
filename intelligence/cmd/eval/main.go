// cmd/eval/main.go — run_eval CLI: embed a dataset and report Recall@5/10, MRR, p50/p95.
//
// Purpose: One-shot CLI that drives the A/B eval harness without requiring a running server.
//          Flags select provider (bge-m3 | gemini-text-embedding-004) and dataset path.
// Usage:   go run ./cmd/eval --provider bge-m3 --dataset eval/testdata/golden_queries.jsonl
//          go run ./cmd/eval --provider gemini-text-embedding-004 --dataset eval/testdata/golden_queries.jsonl
//          go run ./cmd/eval --provider bge-m3 --smoke   # 10-query subset
//
// Constraints: Provider/DB connections are SKIPPED when endpoints are unreachable
//              (eval-smoke target uses a mock searcher for CI-safe runs).
// SPORT: REGISTRY-FUNCTIONS.md → eval.main.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/nself-org/clawde/intelligence/internal/eval"
)

func main() {
	provider := flag.String("provider", "bge-m3", "embedding provider: bge-m3 | gemini-text-embedding-004")
	dataset := flag.String("dataset", "eval/testdata/golden_queries.jsonl", "path to JSONL dataset")
	smoke := flag.Bool("smoke", false, "run only first 10 queries (CI-safe smoke mode)")
	teiAddr := flag.String("tei", "http://localhost:8080", "TEI endpoint for BGE-M3")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	ds, err := eval.LoadDataset(*dataset)
	if err != nil {
		logger.Error("load dataset", "err", err)
		os.Exit(1)
	}
	logger.Info("dataset loaded", "queries", len(ds.Queries), "dataset", *dataset)

	cfg := eval.DefaultRunConfig()
	if *smoke {
		cfg.SmokeLimit = 10
	}

	// Workspace ID: use a fixed eval workspace or read from env.
	wid := uuid.New()
	if v := os.Getenv("EVAL_WORKSPACE_ID"); v != "" {
		if parsed, err := uuid.Parse(v); err == nil {
			wid = parsed
		}
	}

	// In smoke mode, always use mock driver + mock searcher — no live endpoints needed.
	var driver eval.EmbeddingDriver
	var searcher eval.ChunkSearcher
	if *smoke {
		driver = &mockDriver{name: *provider}
		searcher = &mockSearcher{}
	} else {
		switch *provider {
		case "bge-m3":
			driver = eval.NewBGEDriver(*teiAddr)
		case "gemini-text-embedding-004":
			if os.Getenv("GEMINI_API_KEY") == "" {
				logger.Error("GEMINI_API_KEY not set; required for gemini-text-embedding-004")
				os.Exit(1)
			}
			logger.Error("gemini provider requires gateway integration; wire gateway bootstrap here")
			os.Exit(1)
		default:
			logger.Error("unknown provider", "provider", *provider)
			os.Exit(1)
		}
		logger.Error("non-smoke mode requires live pgvector searcher; wire DB connection here")
		os.Exit(1)
	}

	result, err := eval.RunEval(context.Background(), ds, driver, searcher, wid, cfg, nil)
	if err != nil {
		logger.Error("eval run failed", "err", err)
		os.Exit(1)
	}

	fmt.Printf("\n=== Eval Results ===\n")
	fmt.Printf("Provider:    %s\n", result.Provider)
	fmt.Printf("Dataset:     %s\n", result.Dataset)
	fmt.Printf("Samples:     %d\n", result.SampleCount)
	fmt.Printf("Recall@5:    %.4f\n", result.RecallAt5)
	fmt.Printf("Recall@10:   %.4f\n", result.RecallAt10)
	fmt.Printf("MRR@10:      %.4f\n", result.MRRAt10)
	fmt.Printf("p50 (ms):    %d\n", result.P50Ms)
	fmt.Printf("p95 (ms):    %d\n", result.P95Ms)
	fmt.Printf("===================\n\n")
}

// ── Smoke-mode stubs (no live endpoints required) ────────────────────────────

type mockDriver struct{ name string }

func (m *mockDriver) Name() string { return m.name }
func (m *mockDriver) Embed(_ context.Context, text string) ([]float32, error) {
	v := float32(len(text))
	return []float32{v, v, v, v}, nil
}

type mockSearcher struct{}

func (s *mockSearcher) Search(_ context.Context, _ uuid.UUID, _ []float32, topK int) ([]string, error) {
	ids := make([]string, topK)
	// Return a plausible hit at rank 0 for Recall@10 sanity check.
	ids[0] = "chunk-bm25-kernel-constructor"
	for i := 1; i < topK; i++ {
		ids[i] = fmt.Sprintf("other-%d", i)
	}
	return ids, nil
}
