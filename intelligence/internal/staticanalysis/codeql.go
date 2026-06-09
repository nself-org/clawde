// Package staticanalysis — CodeQL runner: SARIF 2.1.0 parse + upsert.
//
// Purpose: Run `codeql database create` + `codeql database analyze` (SARIF output),
//          parse the SARIF 2.1.0 result, map severity, and upsert into clawde_findings.
//
// Inputs:  ctx, FindingsStore, workspaceID, repoPath, codeql query suite path.
// Outputs: number of findings upserted, error.
// Constraints: File ≤500 lines. Graceful degradation when codeql binary absent.
//
// SPORT: REGISTRY-FUNCTIONS.md → staticanalysis.RunCodeQL, staticanalysis.ParseSARIF.
package staticanalysis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// ── SARIF 2.1.0 types (minimal subset needed) ─────────────────────────────────

// sarifRoot is the top-level SARIF 2.1.0 log object.
type sarifRoot struct {
	Runs []sarifRun `json:"runs"`
}

// sarifRun holds one tool's results within the SARIF log.
type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}

// sarifTool carries the driver name (e.g. "CodeQL").
type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}

// sarifDriver is the tool component descriptor.
type sarifDriver struct {
	Name  string      `json:"name"`
	Rules []sarifRule `json:"rules"`
}

// sarifRule holds per-rule metadata including default severity.
type sarifRule struct {
	ID                   string                    `json:"id"`
	DefaultConfiguration sarifRuleConfiguration    `json:"defaultConfiguration"`
}

// sarifRuleConfiguration carries the rule's configured severity level.
type sarifRuleConfiguration struct {
	Level string `json:"level"` // "error", "warning", "note", "none"
}

// sarifResult is a single finding entry in the SARIF log.
type sarifResult struct {
	RuleID  string           `json:"ruleId"`
	Level   string           `json:"level"` // may override rule default
	Message sarifMessage     `json:"message"`
	Locations []sarifLocation `json:"locations"`
}

// sarifMessage holds the human-readable finding description.
type sarifMessage struct {
	Text string `json:"text"`
}

// sarifLocation is a physical/logical location of the finding.
type sarifLocation struct {
	PhysicalLocation sarifPhysicalLocation `json:"physicalLocation"`
}

// sarifPhysicalLocation refers to a file and region.
type sarifPhysicalLocation struct {
	ArtifactLocation sarifArtifactLocation `json:"artifactLocation"`
	Region           sarifRegion           `json:"region"`
}

// sarifArtifactLocation holds the file URI.
type sarifArtifactLocation struct {
	URI string `json:"uri"`
}

// sarifRegion holds line/column span of the finding.
type sarifRegion struct {
	StartLine   int `json:"startLine"`
	EndLine     int `json:"endLine"`
	StartColumn int `json:"startColumn"`
	EndColumn   int `json:"endColumn"`
}

// ── Severity mapping ──────────────────────────────────────────────────────────

// sarifLevelToSeverity maps SARIF level strings to clawde_findings severity enum.
// SARIF defines: error, warning, note, none. We add a "critical" extension guard.
func sarifLevelToSeverity(level string) string {
	switch level {
	case "error":
		return "high"
	case "warning":
		return "medium"
	case "note":
		return "low"
	case "none":
		return "info"
	case "critical":
		return "critical"
	default:
		return "info"
	}
}

// ── ParseSARIF ────────────────────────────────────────────────────────────────

// ParseSARIF parses a SARIF 2.1.0 JSON byte slice and returns []Finding.
//
// Inputs:  raw SARIF JSON bytes (from codeql output file or stdout).
// Outputs: []Finding (may be empty), error on malformed JSON.
func ParseSARIF(data []byte) ([]Finding, error) {
	var root sarifRoot
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("staticanalysis: unmarshal SARIF: %w", err)
	}

	// Build rule-id → default level map for fallback when result.Level is empty.
	ruleLevel := make(map[string]string)
	for _, run := range root.Runs {
		for _, rule := range run.Tool.Driver.Rules {
			ruleLevel[rule.ID] = rule.DefaultConfiguration.Level
		}
	}

	var findings []Finding
	for _, run := range root.Runs {
		for _, res := range run.Results {
			level := res.Level
			if level == "" {
				level = ruleLevel[res.RuleID]
			}
			sev := sarifLevelToSeverity(level)

			// Use first location if present.
			var (
				filePath                        string
				lineStart, lineEnd, colS, colE  int
			)
			if len(res.Locations) > 0 {
				loc := res.Locations[0].PhysicalLocation
				filePath = loc.ArtifactLocation.URI
				r := loc.Region
				lineStart = r.StartLine
				lineEnd = r.EndLine
				colS = r.StartColumn
				colE = r.EndColumn
			}

			findings = append(findings, Finding{
				RuleID:    res.RuleID,
				Source:    "codeql",
				Severity:  sev,
				Message:   res.Message.Text,
				FilePath:  filePath,
				LineStart: lineStart,
				LineEnd:   lineEnd,
				ColStart:  colS,
				ColEnd:    colE,
			})
		}
	}
	return findings, nil
}

// ── RunCodeQL ─────────────────────────────────────────────────────────────────

// RunCodeQL executes a two-step CodeQL analysis:
//  1. codeql database create --language=<lang> --source-root=<repoPath> <dbPath>
//  2. codeql database analyze <dbPath> <querySuite> --format=sarifv2.1.0 --output=<sarifFile>
//
// The SARIF output is then parsed and upserted via FindingsStore.
//
// Graceful degradation: if the codeql binary is absent, logs a warning and
// returns nil — never panics. lang defaults to "go" when empty.
//
// Inputs:  ctx, store, workspaceID, repoPath, querySuite (e.g. "codeql/go-queries").
// Outputs: number of findings upserted, error.
func RunCodeQL(
	ctx context.Context,
	store FindingsStore,
	workspaceID string,
	repoPath string,
	querySuite string,
	lang string,
	logger *slog.Logger,
) (int, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if lang == "" {
		lang = "go"
	}

	// Graceful degradation: binary absent → warn + skip.
	codeqlPath, err := exec.LookPath("codeql")
	if err != nil {
		logger.Warn("staticanalysis: codeql binary not found; skipping",
			"repo_path", repoPath,
			"reason", err.Error(),
		)
		return 0, nil
	}

	// Use a temp dir for the CodeQL database and SARIF output.
	tmpDir, err := os.MkdirTemp("", "clawde-codeql-*")
	if err != nil {
		return 0, fmt.Errorf("staticanalysis: create codeql tmpdir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "db")
	sarifFile := filepath.Join(tmpDir, "results.sarif")

	// Step 1: create database.
	createCmd := exec.CommandContext(ctx, codeqlPath,
		"database", "create",
		"--language="+lang,
		"--source-root="+repoPath,
		dbPath,
	)
	var createStderr bytes.Buffer
	createCmd.Stderr = &createStderr
	if runErr := createCmd.Run(); runErr != nil {
		return 0, fmt.Errorf("staticanalysis: codeql database create: %w (stderr: %s)",
			runErr, createStderr.String())
	}

	// Step 2: run analysis → SARIF output.
	analyzeCmd := exec.CommandContext(ctx, codeqlPath,
		"database", "analyze",
		dbPath,
		querySuite,
		"--format=sarifv2.1.0",
		"--output="+sarifFile,
	)
	var analyzeStderr bytes.Buffer
	analyzeCmd.Stderr = &analyzeStderr
	if runErr := analyzeCmd.Run(); runErr != nil {
		return 0, fmt.Errorf("staticanalysis: codeql analyze: %w (stderr: %s)",
			runErr, analyzeStderr.String())
	}

	sarifData, err := os.ReadFile(sarifFile)
	if err != nil {
		return 0, fmt.Errorf("staticanalysis: read codeql sarif output: %w", err)
	}

	findings, parseErr := ParseSARIF(sarifData)
	if parseErr != nil {
		return 0, parseErr
	}
	if len(findings) == 0 {
		return 0, nil
	}

	n, upsertErr := store.UpsertFindings(ctx, workspaceID, findings)
	if upsertErr != nil {
		return 0, fmt.Errorf("staticanalysis: upsert codeql findings: %w", upsertErr)
	}
	logger.Info("staticanalysis: codeql complete",
		"workspace_id", workspaceID,
		"findings", n,
	)
	return n, nil
}
