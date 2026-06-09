// Package staticanalysis — pgmq-triggered analysis runner + FindingsStore interface.
//
// Purpose: Handle jobs from pgmq clawde_analyze_queue. Payload schema:
//
//	{workspace_id, repo_path, tools: ["semgrep", "codeql"], ruleset?, query_suite?, lang?}
//
// Each tool runs independently; failure of one does not abort the other.
// DLQ archival occurs after maxRetries consecutive failures (delegated to the worker Pool).
//
// Also defines: Finding (shared value type), FindingsStore (DB seam), GetFindings.
//
// Constraints: File ≤500 lines. No panic on missing binary.
//
// SPORT: REGISTRY-FUNCTIONS.md → staticanalysis.Runner, staticanalysis.GetFindings.
package staticanalysis

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
)

// ── Finding — shared value type ───────────────────────────────────────────────

// Finding is a single static analysis result ready for clawde_findings upsert.
// source must be "semgrep" or "codeql". severity must be one of
// critical / high / medium / low / info.
type Finding struct {
	RuleID    string
	Source    string // "semgrep" | "codeql"
	Severity  string // "critical" | "high" | "medium" | "low" | "info"
	Message   string
	FilePath  string
	LineStart int
	LineEnd   int
	ColStart  int
	ColEnd    int
}

// ── FindingsStore — DB seam ───────────────────────────────────────────────────

// FindingsStore is the storage interface the runner and retrieval helpers depend on.
// The real implementation executes INSERT … ON CONFLICT DO NOTHING via pgx;
// tests inject a stub.
type FindingsStore interface {
	// UpsertFindings inserts findings for workspaceID, ignoring duplicates.
	// Returns the number of rows actually inserted.
	UpsertFindings(ctx context.Context, workspaceID string, findings []Finding) (int, error)

	// GetFindings returns findings for workspaceID whose file_path is in filePaths.
	// Used by RetrieveContext to surface relevant findings alongside chunks.
	GetFindings(ctx context.Context, workspaceID string, filePaths []string) ([]Finding, error)
}

// ── AnalyzePayload — pgmq message schema ─────────────────────────────────────

// AnalyzePayload is the JSON body of a clawde_analyze_queue message.
// Tools is a list of tool names to run; valid values: "semgrep", "codeql".
// Ruleset defaults to "p/default" for semgrep when empty.
// QuerySuite defaults to "codeql/go-queries" when empty.
// Lang defaults to "go" when empty.
type AnalyzePayload struct {
	WorkspaceID string   `json:"workspace_id"`
	RepoPath    string   `json:"repo_path"`
	Tools       []string `json:"tools"`
	Ruleset     string   `json:"ruleset,omitempty"`
	QuerySuite  string   `json:"query_suite,omitempty"`
	Lang        string   `json:"lang,omitempty"`
}

// ── Runner ────────────────────────────────────────────────────────────────────

// Runner handles analyze-queue jobs dispatched by the pgmq worker Pool.
// It is designed to be registered as a worker.Handler for QueueAnalyze.
type Runner struct {
	store  FindingsStore
	logger *slog.Logger
}

// NewRunner creates a Runner.
// If logger is nil the default slog logger is used.
func NewRunner(store FindingsStore, logger *slog.Logger) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{store: store, logger: logger}
}

// Handle processes a single pgmq analyze-queue message.
// It is safe to call multiple times for the same message (idempotent via
// ON CONFLICT DO NOTHING in UpsertFindings).
//
// Inputs:  raw pgmq message JSON. Expects AnalyzePayload.
// Outputs: error triggers retry in the worker Pool; nil acknowledges success.
func (r *Runner) Handle(ctx context.Context, raw []byte) error {
	var payload AnalyzePayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("staticanalysis: unmarshal analyze payload: %w", err)
	}
	if payload.WorkspaceID == "" || payload.RepoPath == "" {
		return fmt.Errorf("staticanalysis: analyze payload missing workspace_id or repo_path")
	}
	if len(payload.Tools) == 0 {
		// Default: run both tools when caller does not specify.
		payload.Tools = []string{"semgrep", "codeql"}
	}

	// Defaults.
	ruleset := payload.Ruleset
	if ruleset == "" {
		ruleset = "p/default"
	}
	querySuite := payload.QuerySuite
	if querySuite == "" {
		querySuite = "codeql/go-queries"
	}

	var firstErr error
	for _, tool := range payload.Tools {
		switch tool {
		case "semgrep":
			n, err := RunSemgrep(ctx, r.store, payload.WorkspaceID, payload.RepoPath, ruleset, r.logger)
			if err != nil {
				r.logger.Error("staticanalysis: semgrep run failed",
					"workspace_id", payload.WorkspaceID,
					"error", err)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				r.logger.Info("staticanalysis: semgrep upserted", "count", n)
			}
		case "codeql":
			n, err := RunCodeQL(ctx, r.store, payload.WorkspaceID, payload.RepoPath, querySuite, payload.Lang, r.logger)
			if err != nil {
				r.logger.Error("staticanalysis: codeql run failed",
					"workspace_id", payload.WorkspaceID,
					"error", err)
				if firstErr == nil {
					firstErr = err
				}
			} else {
				r.logger.Info("staticanalysis: codeql upserted", "count", n)
			}
		default:
			r.logger.Warn("staticanalysis: unknown tool in payload; skipping", "tool", tool)
		}
	}
	return firstErr
}

// ── GetFindings ───────────────────────────────────────────────────────────────

// GetFindings retrieves findings for workspaceID filtered to filePaths.
// Delegates to FindingsStore.GetFindings. Used by the RetrieveContext layer
// to surface relevant static-analysis results alongside retrieved chunks.
//
// Inputs:  ctx, store, workspaceID (UUID string), filePaths (from chunk metadata).
// Outputs: []Finding, error.
func GetFindings(
	ctx context.Context,
	store FindingsStore,
	workspaceID string,
	filePaths []string,
) ([]Finding, error) {
	if len(filePaths) == 0 {
		return nil, nil
	}
	return store.GetFindings(ctx, workspaceID, filePaths)
}
