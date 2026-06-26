// Package db — migration runner for clawde-proxy SQLite schema.
// Purpose: Embedded-FS migration runner; reads migrations/ in order, skips already-applied.
// Inputs:  *sql.DB opened by db.Open; embedded migration files via go:embed.
// Outputs: All schema migrations applied; _migrations tracking table maintained.
// Constraints: Idempotent — second call is always a no-op. Runs in a transaction per migration.
//   Migration 0003_vec.sql is skipped gracefully if sqlite-vec extension is unavailable.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"
)

//go:embed migrations/0001_initial.sql migrations/0002_fts.sql migrations/0003_vec.sql seeds/default_routes.sql
var migrationFS embed.FS

// Migrate applies all pending migrations and seeds to db in lexicographic order.
// Purpose: Idempotent schema bootstrapper; ensures _migrations table tracks applied files.
// Inputs:  db — open *sql.DB from db.Open().
// Outputs: nil on success; error with filename context on any failure.
// Constraints: Each migration runs in its own transaction. Already-applied files are skipped.
func Migrate(db *sql.DB) error {
	if err := ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("migrate: ensure _migrations table: %w", err)
	}

	files, err := collectMigrationFiles()
	if err != nil {
		return fmt.Errorf("migrate: collect files: %w", err)
	}

	for _, f := range files {
		applied, err := isMigrationApplied(db, f)
		if err != nil {
			return fmt.Errorf("migrate: check applied %s: %w", f, err)
		}
		if applied {
			continue
		}

		if err := applyMigration(db, f); err != nil {
			return err
		}
	}
	return nil
}

// ensureMigrationsTable creates the _migrations tracking table if absent.
func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS _migrations (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			filename   TEXT NOT NULL UNIQUE,
			applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		)
	`)
	return err
}

// collectMigrationFiles returns migration filenames in sorted order.
// Migrations come first (migrations/), then seeds (seeds/).
func collectMigrationFiles() ([]string, error) {
	var migrations []string

	// Hardcoded ordered list matching the embed directive.
	migrations = []string{
		"migrations/0001_initial.sql",
		"migrations/0002_fts.sql",
		"migrations/0003_vec.sql",
		"seeds/default_routes.sql",
	}

	return migrations, nil
}

// isMigrationApplied returns true if filename is recorded in _migrations.
func isMigrationApplied(db *sql.DB, filename string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM _migrations WHERE filename = ?`, filename).Scan(&count)
	return count > 0, err
}

// applyMigration runs a single migration file inside a transaction and records it.
func applyMigration(db *sql.DB, filename string) error {
	content, err := migrationFS.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("migrate: read %s: %w", filename, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("migrate: begin tx for %s: %w", filename, err)
	}

	if _, err := tx.Exec(string(content)); err != nil {
		tx.Rollback()
		// 0002_fts.sql (FTS5) and 0003_vec.sql (sqlite-vec) depend on optional SQLite extensions.
		// Skip gracefully if the extension is not compiled into the host SQLite build.
		isOptional := strings.Contains(filename, "0002_fts") || strings.Contains(filename, "0003_vec")
		if isOptional && (strings.Contains(err.Error(), "no such module") || strings.Contains(err.Error(), "unknown module")) {
			if err2 := recordApplied(db, filename); err2 != nil {
				return fmt.Errorf("migrate: record skip for %s: %w", filename, err2)
			}
			return nil
		}
		return fmt.Errorf("migrate: exec %s: %w", filename, err)
	}

	if _, err := tx.Exec(`INSERT INTO _migrations (filename) VALUES (?)`, filename); err != nil {
		tx.Rollback()
		return fmt.Errorf("migrate: record %s: %w", filename, err)
	}

	return tx.Commit()
}

// recordApplied inserts a migration record outside a transaction (for graceful-skip cases).
func recordApplied(db *sql.DB, filename string) error {
	_, err := db.Exec(`INSERT OR IGNORE INTO _migrations (filename) VALUES (?)`, filename)
	return err
}

// MustMigrate calls Migrate and panics on error. Convenience for tests.
func MustMigrate(db *sql.DB) {
	if err := Migrate(db); err != nil {
		panic("clawde-proxy migration failed: " + err.Error())
	}
}

// TableNames returns the list of user-created table names in db. Used for acceptance testing.
func TableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE '_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// VirtualTableNames returns virtual table names (fts5, vec0). Used for acceptance testing.
func VirtualTableNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table' AND sql LIKE '%USING%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// ProxyRouteExists checks that the default_routes seed inserted the local lane row.
func ProxyRouteExists(db *sql.DB, lane string) (bool, error) {
	var count int
	err := db.QueryRow(`SELECT COUNT(*) FROM proxy_routes WHERE lane = ?`, lane).Scan(&count)
	return count > 0, err
}

// ensure *sql.DB is used in this file (avoids unused-import if queries.go isn't compiled first).
var _ *sql.DB
