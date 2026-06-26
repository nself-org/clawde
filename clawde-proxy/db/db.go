// Package db provides the SQLite connection pool and startup helpers for clawde-proxy.
// Purpose: Open a WAL-mode SQLite connection with foreign keys enabled; expose a shared *sql.DB.
// Inputs:  dbPath string (file path or ":memory:").
// Outputs: *sql.DB ready for queries; error on open/pragma failure.
// Constraints: Uses mattn/go-sqlite3 (CGO). WAL mode and FK enforcement are set via PRAGMA.
//   Connection pool: SetMaxOpenConns(1) for SQLite write safety (single writer).
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// Open opens a SQLite database at dbPath, enables WAL mode and foreign keys,
// and returns a connection pool safe for concurrent reads with single-writer semantics.
// Purpose: Centralise DB open + PRAGMA setup so every caller gets the same settings.
// Inputs:  dbPath — file path (creates if absent) or ":memory:" for tests.
// Outputs: *sql.DB ready for use; error on open or PRAGMA failure.
// Constraints: Caller must call db.Close() on shutdown.
func Open(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		return nil, fmt.Errorf("db.Open: open %q: %w", dbPath, err)
	}

	// SQLite supports only one writer at a time; cap open connections to avoid SQLITE_BUSY storms.
	db.SetMaxOpenConns(1)

	// Verify the connection is usable.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("db.Open: ping %q: %w", dbPath, err)
	}

	return db, nil
}
