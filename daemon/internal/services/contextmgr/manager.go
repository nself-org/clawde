// Package contextmgr implements context window management for ClawDE sessions.
//
// When the context window fills, older messages are summarised (via the plugin-ai
// API if ClawDE+ server mode is connected, or extractive fallback otherwise) and
// stored in SQLite. The summary is prepended as a synthetic ContextMessage with
// IsSummary=true so callers see a coherent timeline even after truncation.
//
// Implements spec F44.3.4 / SP-10.N06.
package contextmgr

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	_ "github.com/mattn/go-sqlite3" // CGo SQLite driver
)

// maxExtractiveLen is the maximum rune length of an extractive summary.
const maxExtractiveLen = 2000

// ContextRow is a single message row loaded from SQLite.
type ContextRow struct {
	ID         string
	Role       string
	Content    string
	TokenEst   int64
}

// ContextMessage is a message ready for inclusion in a context window.
type ContextMessage struct {
	ID         string
	Role       string
	Content    string
	TokenEst   int64
	IsSummary  bool
}

// SummaryRecord is persisted in context_summaries after a snapshot.
type SummaryRecord struct {
	ID          string
	SessionID   string
	Summary     string
	CoveredFrom string // oldest message_id in summarised range
	CoveredTo   string // newest message_id in summarised range
	TokenEst    int64
	CreatedAt   string
}

// Manager handles context window snapshots and restoration.
type Manager struct {
	db         *sql.DB
	aiEndpoint string // optional: plugin-ai API endpoint (e.g. http://localhost:9001/summarise)
}

// New creates a Manager. db must be an open *sql.DB backed by SQLite.
// If aiEndpoint is empty, the extractive fallback is always used.
func New(db *sql.DB, aiEndpoint string) *Manager {
	return &Manager{db: db, aiEndpoint: aiEndpoint}
}

// NewFromFile opens (or creates) a SQLite database at path and returns a Manager.
// The caller is responsible for calling db.Close().
func NewFromFile(path, aiEndpoint string) (*Manager, *sql.DB, error) {
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, nil, fmt.Errorf("contextmgr: open sqlite3 %q: %w", path, err)
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("contextmgr: migrate: %w", err)
	}
	return New(db, aiEndpoint), db, nil
}

// RunMigrations creates the context_summaries table if it does not exist.
// It is safe to call multiple times (idempotent via CREATE IF NOT EXISTS).
func RunMigrations(db *sql.DB) error {
	return runMigrations(db)
}

// runMigrations is the internal implementation.
func runMigrations(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS context_summaries (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    summary      TEXT NOT NULL,
    covered_from TEXT NOT NULL,
    covered_to   TEXT NOT NULL,
    token_est    INTEGER NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);
CREATE INDEX IF NOT EXISTS idx_cs_session ON context_summaries(session_id, created_at DESC);
`)
	return err
}

// Snapshot generates a summary for the given messages, persists it, and returns
// the SummaryRecord. serverMode is taken from the CLAWDE_SERVER_MODE env var when
// true; if the AI call fails the extractive fallback is used transparently.
func (m *Manager) Snapshot(ctx context.Context, sessionID string, messages []ContextRow) (*SummaryRecord, error) {
	if len(messages) == 0 {
		return nil, fmt.Errorf("contextmgr: Snapshot called with empty message slice")
	}

	serverMode := os.Getenv("CLAWDE_SERVER_MODE") == "true"

	var summary string
	if serverMode && m.aiEndpoint != "" {
		var err error
		summary, err = m.aiSummarise(ctx, messages)
		if err != nil {
			slog.Warn("contextmgr: AI summarise failed, using extractive fallback",
				"session_id", sessionID, "err", err)
			summary = extractiveSummary(messages)
		}
	} else {
		summary = extractiveSummary(messages)
	}

	coveredFrom := messages[0].ID
	coveredTo := messages[len(messages)-1].ID
	tokenEst := int64(utf8.RuneCountInString(summary) / 4)
	if tokenEst < 1 {
		tokenEst = 1
	}

	id := newID()
	now := time.Now().UTC().Format(time.RFC3339)

	_, err := m.db.ExecContext(ctx,
		`INSERT INTO context_summaries (id, session_id, summary, covered_from, covered_to, token_est, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, sessionID, summary, coveredFrom, coveredTo, tokenEst, now,
	)
	if err != nil {
		return nil, fmt.Errorf("contextmgr: persist summary: %w", err)
	}

	rec := &SummaryRecord{
		ID:          id,
		SessionID:   sessionID,
		Summary:     summary,
		CoveredFrom: coveredFrom,
		CoveredTo:   coveredTo,
		TokenEst:    tokenEst,
		CreatedAt:   now,
	}
	slog.Info("contextmgr: snapshot persisted",
		"session_id", sessionID,
		"id", id,
		"covered_from", coveredFrom,
		"covered_to", coveredTo,
		"token_est", tokenEst,
	)
	return rec, nil
}

// Restore returns the most recent cached SummaryRecord for sessionID, or nil if
// none exists. A nil return with nil error means no snapshot has been taken yet.
func (m *Manager) Restore(ctx context.Context, sessionID string) (*SummaryRecord, error) {
	row := m.db.QueryRowContext(ctx,
		`SELECT id, session_id, summary, covered_from, covered_to, token_est, created_at
		 FROM context_summaries
		 WHERE session_id = ?
		 ORDER BY created_at DESC
		 LIMIT 1`,
		sessionID,
	)
	var rec SummaryRecord
	err := row.Scan(&rec.ID, &rec.SessionID, &rec.Summary, &rec.CoveredFrom, &rec.CoveredTo, &rec.TokenEst, &rec.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("contextmgr: restore: %w", err)
	}
	return &rec, nil
}

// SummaryMessage converts a SummaryRecord into a ContextMessage ready for
// prepending to a context window.
func SummaryMessage(rec *SummaryRecord) ContextMessage {
	return ContextMessage{
		ID:        rec.ID,
		Role:      "system",
		Content:   rec.Summary,
		TokenEst:  rec.TokenEst,
		IsSummary: true,
	}
}

// extractiveSummary builds a plain-text summary by taking the first sentence of
// each message and concatenating, truncated to maxExtractiveLen runes.
func extractiveSummary(messages []ContextRow) string {
	var parts []string
	for _, m := range messages {
		sentence := firstSentence(m.Content)
		if sentence != "" {
			parts = append(parts, sentence)
		}
	}
	joined := strings.Join(parts, " ")
	return truncateRunes(joined, maxExtractiveLen)
}

// firstSentence returns the first sentence of text (up to . ! ? or 200 runes).
func firstSentence(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for i, r := range text {
		if r == '.' || r == '!' || r == '?' {
			return strings.TrimSpace(text[:i+1])
		}
	}
	return truncateRunes(text, 200)
}

// truncateRunes returns s truncated to at most n runes.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	i := 0
	for pos := range s {
		if i >= n {
			return s[:pos]
		}
		i++
	}
	return s
}

// aiSummarise calls the plugin-ai summarise endpoint with the message contents.
// Returns the summary string or an error; callers fall back to extractiveSummary on error.
func (m *Manager) aiSummarise(ctx context.Context, messages []ContextRow) (string, error) {
	var builder strings.Builder
	builder.WriteString(`{"messages":[`)
	for i, msg := range messages {
		if i > 0 {
			builder.WriteByte(',')
		}
		// Minimal JSON encoding: escape quotes and backslashes only.
		content := strings.ReplaceAll(msg.Content, `\`, `\\`)
		content = strings.ReplaceAll(content, `"`, `\"`)
		role := strings.ReplaceAll(msg.Role, `"`, `\"`)
		fmt.Fprintf(&builder, `{"role":%q,"content":%q}`, role, content)
	}
	builder.WriteString(`],"max_tokens":200}`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.aiEndpoint,
		strings.NewReader(builder.String()))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("plugin-ai returned %d", resp.StatusCode)
	}

	var buf strings.Builder
	if _, err := io.Copy(&buf, resp.Body); err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	// Expect plain-text summary body. Trim whitespace.
	result := strings.TrimSpace(buf.String())
	if result == "" {
		return "", fmt.Errorf("plugin-ai returned empty summary")
	}
	return truncateRunes(result, maxExtractiveLen), nil
}

// newID returns a compact unique ID using nanosecond timestamp + pid as entropy.
// Not cryptographically random — sufficient for row uniqueness within one process.
func newID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
}
