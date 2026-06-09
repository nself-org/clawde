// cmd/eval-gate/main.go — eval regression gate + BGE-vs-Gemini tie-break CLI.
//
// Purpose: Read a baseline.json + per-provider result JSONs, run the
//          deterministic regression gate (baseline-null aware) and the LEDGER
//          tie-break, print decisions, and exit non-zero on regression.
//          Pure decision logic — no live DB/HTTP — so it runs in CI.
// Usage:   go run ./cmd/eval-gate --baseline b.json --bge bge.json --gemini gem.json
//          Optional: --write-baseline to persist the winner as the new baseline.
// SPORT:   REGISTRY-FUNCTIONS.md → eval.RegressionGate, eval.TieBreak (CLI driver).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/nself-org/clawde/intelligence/internal/eval"
)

func loadResult(path string) (eval.ProviderResult, error) {
	var r eval.ProviderResult
	data, err := os.ReadFile(path)
	if err != nil {
		return r, fmt.Errorf("read result %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, fmt.Errorf("parse result %q: %w", path, err)
	}
	return r, nil
}

func main() {
	baselinePath := flag.String("baseline", "eval/testdata/baseline.json", "baseline.json path")
	bgePath := flag.String("bge", "eval/testdata/result_bge.json", "BGE-M3 ProviderResult JSON")
	geminiPath := flag.String("gemini", "eval/testdata/result_gemini.json", "Gemini ProviderResult JSON")
	writeBaseline := flag.Bool("write-baseline", false, "persist the winner as the new baseline")
	flag.Parse()

	bge, err := loadResult(*bgePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval-gate:", err)
		os.Exit(2)
	}
	gemini, err := loadResult(*geminiPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval-gate:", err)
		os.Exit(2)
	}
	baseline, err := eval.LoadBaseline(*baselinePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "eval-gate:", err)
		os.Exit(2)
	}

	// 1) Deterministic tie-break (no build-time judgment).
	decision := eval.TieBreak(bge, gemini)
	fmt.Printf("tie-break: winner=%s delta=%+.4f reason=%s\n", decision.Winner, decision.Delta, decision.Reason)

	// 2) Regression gate against the winning provider's composite.
	winner := bge
	if decision.Winner == gemini.Provider {
		winner = gemini
	}
	gate := eval.RegressionGate(winner, baseline, eval.DefaultRegressionTolerance())
	fmt.Printf("gate: passed=%v skipped=%v delta=%+.4f reason=%s\n", gate.Passed, gate.Skipped, gate.Delta, gate.Reason)

	if !gate.Passed {
		fmt.Fprintln(os.Stderr, "eval-gate: REGRESSION — blocking")
		os.Exit(1)
	}

	if *writeBaseline {
		if err := eval.WriteBaseline(*baselinePath, winner); err != nil {
			fmt.Fprintln(os.Stderr, "eval-gate:", err)
			os.Exit(2)
		}
		fmt.Printf("baseline updated: %s (composite %.4f)\n", *baselinePath, winner.CompositeScore)
	}
}
