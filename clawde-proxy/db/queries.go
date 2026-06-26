// Package db — typed query functions for clawde-proxy.
// Purpose: Centralise all SQL reads/writes into typed Go functions; no raw SQL in callers.
// Inputs:  *sql.DB; typed structs for inserts/returns.
// Outputs: Typed results or error.
// Constraints: All functions use parameterised queries (no interpolation). IDs are caller-supplied UUIDs.
package db

import (
	"database/sql"
	"fmt"
	"time"
)

// ChatMessage is a row from the chat_messages table.
type ChatMessage struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	Model     string
	TokensIn  int
	TokensOut int
	LatencyMS int
	CreatedAt time.Time
	Metadata  string
}

// ProxyRoute is a row from the proxy_routes table.
type ProxyRoute struct {
	ID        string
	Lane      string
	Upstream  string
	Priority  int
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Worktree is a row from the worktrees table.
type Worktree struct {
	ID        string
	Path      string
	Branch    string
	SessionID string
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InsertChatMessage inserts a new message into chat_messages.
// Purpose: Persist a chat turn (user or assistant) for a session.
// Inputs:  db, ChatMessage with all fields populated (ID must be a unique UUID).
// Outputs: error on constraint violation or DB failure.
// Constraints: Role must be one of: user, assistant, system, tool.
func InsertChatMessage(db *sql.DB, m ChatMessage) error {
	_, err := db.Exec(`
		INSERT INTO chat_messages (id, session_id, role, content, model, tokens_in, tokens_out, latency_ms, metadata)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.ID, m.SessionID, m.Role, m.Content, m.Model, m.TokensIn, m.TokensOut, m.LatencyMS, m.Metadata)
	if err != nil {
		return fmt.Errorf("InsertChatMessage: %w", err)
	}
	return nil
}

// GetChatMessages returns all messages for sessionID ordered by rowid ascending.
// Purpose: Reconstruct the conversation history for a session.
// Inputs:  db, sessionID string.
// Outputs: []ChatMessage in insertion order; nil slice if no messages found.
// Constraints: Returns an empty slice (not an error) when no messages exist.
func GetChatMessages(db *sql.DB, sessionID string) ([]ChatMessage, error) {
	rows, err := db.Query(`
		SELECT id, session_id, role, content, COALESCE(model,''), tokens_in, tokens_out, latency_ms,
		       created_at, COALESCE(metadata,'')
		FROM chat_messages WHERE session_id = ? ORDER BY rowid ASC
	`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("GetChatMessages: %w", err)
	}
	defer rows.Close()

	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var createdAt string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.Content, &m.Model,
			&m.TokensIn, &m.TokensOut, &m.LatencyMS, &createdAt, &m.Metadata); err != nil {
			return nil, fmt.Errorf("GetChatMessages scan: %w", err)
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

// InsertRequestLog appends a row to request_log.
// Purpose: Audit every proxied request for analytics and debugging.
// Inputs:  db, id, sessionID, lane, upstream, statusCode, tokensIn, tokensOut, latencyMS, errMsg.
// Outputs: error on DB failure.
// Constraints: This is append-only; no updates to request_log.
func InsertRequestLog(db *sql.DB, id, sessionID, lane, upstream string, statusCode, tokensIn, tokensOut, latencyMS int, errMsg string) error {
	_, err := db.Exec(`
		INSERT INTO request_log (id, session_id, lane, upstream, status_code, tokens_in, tokens_out, latency_ms, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, sessionID, lane, upstream, statusCode, tokensIn, tokensOut, latencyMS, errMsg)
	if err != nil {
		return fmt.Errorf("InsertRequestLog: %w", err)
	}
	return nil
}

// GetProxyRoutes returns all enabled proxy_routes ordered by priority ascending.
// Purpose: Load the routing table at startup and on hot-reload.
// Inputs:  db.
// Outputs: []ProxyRoute; empty slice if none configured.
// Constraints: Returns only enabled=1 routes.
func GetProxyRoutes(db *sql.DB) ([]ProxyRoute, error) {
	rows, err := db.Query(`
		SELECT id, lane, upstream, priority, enabled, created_at, updated_at
		FROM proxy_routes WHERE enabled = 1 ORDER BY priority ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("GetProxyRoutes: %w", err)
	}
	defer rows.Close()

	var routes []ProxyRoute
	for rows.Next() {
		var r ProxyRoute
		var createdAt, updatedAt string
		var enabled int
		if err := rows.Scan(&r.ID, &r.Lane, &r.Upstream, &r.Priority, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("GetProxyRoutes scan: %w", err)
		}
		r.Enabled = enabled == 1
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		r.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		routes = append(routes, r)
	}
	return routes, rows.Err()
}

// GetWorktrees returns all known worktrees ordered by created_at ascending.
// Purpose: Load the worktree registry at startup or on request.
// Inputs:  db.
// Outputs: []Worktree; empty slice if none registered.
// Constraints: Returns all statuses (idle, active, stale).
func GetWorktrees(db *sql.DB) ([]Worktree, error) {
	rows, err := db.Query(`
		SELECT id, path, branch, COALESCE(session_id,''), status, created_at, updated_at
		FROM worktrees ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("GetWorktrees: %w", err)
	}
	defer rows.Close()

	var wts []Worktree
	for rows.Next() {
		var w Worktree
		var createdAt, updatedAt string
		if err := rows.Scan(&w.ID, &w.Path, &w.Branch, &w.SessionID, &w.Status, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("GetWorktrees scan: %w", err)
		}
		w.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		w.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		wts = append(wts, w)
	}
	return wts, rows.Err()
}

// UpsertWorktree inserts or updates a worktree record by path (unique key).
// Purpose: Register a new worktree or update status/branch/sessionID on reconnect.
// Inputs:  db, Worktree with all fields populated.
// Outputs: error on DB failure.
// Constraints: Uses INSERT OR REPLACE semantics; id must be stable across calls for the same path.
func UpsertWorktree(db *sql.DB, w Worktree) error {
	_, err := db.Exec(`
		INSERT INTO worktrees (id, path, branch, session_id, status, updated_at)
		VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		ON CONFLICT(path) DO UPDATE SET
			branch     = excluded.branch,
			session_id = excluded.session_id,
			status     = excluded.status,
			updated_at = excluded.updated_at
	`, w.ID, w.Path, w.Branch, w.SessionID, w.Status)
	if err != nil {
		return fmt.Errorf("UpsertWorktree: %w", err)
	}
	return nil
}
