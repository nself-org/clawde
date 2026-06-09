// Package staticanalysis — Semgrep + CodeQL static analysis runners.
//
// Purpose: Execute semgrep and codeql against a workspace repo path, parse
//          their output (JSON / SARIF 2.1.0), and upsert findings into
//          clawde_findings. Triggered via pgmq clawde_analyze_queue.
//
// Inputs:  AnalyzePayload from pgmq ({workspace_id, repo_path, tools}).
//          DB querier implementing FindingsStore.
// Outputs: Rows upserted into clawde_findings (ON CONFLICT DO NOTHING).
//          Structured log warnings when binaries are absent — NO panic.
//
// Constraints: File ≤500 lines. Graceful degradation when binaries missing.
//
// SPORT: REGISTRY-FUNCTIONS.md → staticanalysis.RunSemgrep, staticanalysis.ParseSemgrepJSON.
package staticanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"time"
)

// ── Semgrep JSON output types ─────────────────────────────────────────────────

// semgrepRoot is the top-level structure of `semgrep --json` output.
type semgrepRoot struct {
	Results []semgrepResult `json:"results"`
	Errors  []semgrepError  `json:"errors"`
}

// semgrepResult represents one finding returned by semgrep.
type semgrepResult struct {
	CheckID string          `json:"check_id"`
	Path    string          `json:"path"`
	Start   semgrepPosition `json:"start"`
	End     semgrepPosition `json:"end"`
	Extra   semgrepExtra    `json:"extra"`
}

// semgrepPosition holds a line/column pair in semgrep output.
type semgrepPosition struct {
	Line   int `json:"line"`
	Col    int `json:"col"`
}

// semgrepExtra carries the human-readable message and severity.
type semgrepExtra struct {
	Message  string `json:"message"`
	Severity string `json:"severity"` // ERROR, WARNING, INFO
}

// semgrepError represents a semgrep parse/scan error (logged, not fatal).
type semgrepError struct {
	Message string `json:"message"`
}

// ── Severity mapping ──────────────────────────────────────────────────────────

// semgrepSeverity maps semgrep severity strings to clawde_findings severity enum.
// Unknown values fall back to "info" to avoid CHECK constraint violations.
func semgrepSeverity(raw string) string {
	switch raw {
	case "ERROR":
		return "high"
	case "WARNING":
		return "medium"
	case "INFO":
		return "info"
	case "CRITICAL":
		return "critical"
	case "LOW":
		return "low"
	default:
		return "info"
	}
}

// ── ParseSemgrepJSON ──────────────────────────────────────────────────────────

// ParseSemgrepJSON parses the raw output of `semgrep --json` and returns a
// slice of Finding values ready for upsert.
//
// Inputs:  raw JSON bytes from semgrep stdout.
// Outputs: []Finding (may be empty if no results), error on malformed JSON.
func ParseSemgrepJSON(data []byte) ([]Finding, error) {
	var root semgrepRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("staticanalysis: unmarshal semgrep output: %w", err)
	}
	findings := make([]Finding, 0, len(root.Results))
	for _, r := range root.Results {
		findings = append(findings, Finding{
			RuleID:    r.CheckID,
			Source:    "semgrep",
			Severity:  semgrepSeverity(r.Extra.Severity),
			Message:   r.Extra.Message,
			FilePath:  r.Path,
			LineStart: r.Start.Line,
			LineEnd:   r.End.Line,
			ColStart:  r.Start.Col,
			ColEnd:    r.End.Col,
		})
	}
	return findings, nil
}

// ── RunSemgrep ────────────────────────────────────────────────────────────────

// RunSemgrep executes semgrep against repoPath using the given ruleset config,
// parses the JSON output, and batch-upserts findings for workspaceID.
//
// Graceful degradation: if the semgrep binary is absent, logs a warning and
// returns nil — never panics. Callers must not treat absence as an error.
//
// Inputs:  ctx, store (FindingsStore), workspaceID, repoPath, ruleset (e.g. "p/default").
// Outputs: number of findings upserted, error.
func RunSemgrep(
	ctx context.Context,
	store FindingsStore,
	workspaceID string,
	repoPath string,
	ruleset string,
	logger *slog.Logger,
) (int, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Graceful degradation: binary absent → warn + skip.
	path, err := exec.LookPath("semgrep")
	if err != nil {
		logger.Warn("staticanalysis: semgrep binary not found; skipping",
			"repo_path", repoPath,
			"reason", err.Error(),
		)
		return 0, nil
	}

	args := []string{"--json", "--config", ruleset, repoPath}
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// semgrep exits non-zero when findings exist (exit code 1) — treat as
	// "findings found", not as an error. Only exit ≥2 indicates a real failure.
	if runErr := cmd.Run(); runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok && exitErr.ExitCode() >= 2 {
			return 0, fmt.Errorf("staticanalysis: semgrep failed (exit %d): %s",
				exitErr.ExitCode(), stderr.String())
		}
	}

	findings, parseErr := ParseSemgrepJSON(stdout.Bytes())
	if parseErr != nil {
		return 0, parseErr
	}
	if len(findings) == 0 {
		return 0, nil
	}

	n, upsertErr := store.UpsertFindings(ctx, workspaceID, findings)
	if upsertErr != nil {
		return 0, fmt.Errorf("staticanalysis: upsert semgrep findings: %w", upsertErr)
	}
	logger.Info("staticanalysis: semgrep complete",
		"workspace_id", workspaceID,
		"findings", n,
		"elapsed_ms", time.Since(time.Now()).Milliseconds(),
	)
	return n, nil
}
