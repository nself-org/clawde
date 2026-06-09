/**
 * Purpose: Drizzle-style migration spec for ag_traces table.
 * Provides up() and down() methods for schema management.
 * The actual SQL migration lives at: apps/daemon/src/storage/migrations/058_ai_gateway_traces.sql
 * This file is the TypeScript reference / Drizzle ORM companion for the observability layer.
 *
 * ag_traces stores per-stage pipeline trace records for nClaw AI gateway observability.
 * Schema matches TraceRecord interface in types.ts.
 *
 * Inputs: Database connection (SQLite via better-sqlite3 or libsql)
 * Outputs: ag_traces table + indexes created (up) / dropped (down)
 * Constraints: Idempotent — uses CREATE TABLE IF NOT EXISTS and DROP TABLE IF EXISTS.
 *   PII redaction is enforced at application layer (redact.ts) before any insert.
 * SPORT: REGISTRY-MIGRATIONS.md (migration 058); MASTER-TABLES.md (ag_traces DDL)
 */

/**
 * Up migration: creates ag_traces table and associated indexes.
 * Safe to run multiple times (IF NOT EXISTS).
 *
 * @param db - Database connection with exec/run method
 */
export function up(db: { exec: (sql: string) => void }): void {
  db.exec(`
    CREATE TABLE IF NOT EXISTS ag_traces (
      trace_id   TEXT    PRIMARY KEY NOT NULL,
      tenant_id  TEXT    NOT NULL,
      tool_id    TEXT    NOT NULL,
      stage      TEXT    NOT NULL
                         CHECK (stage IN ('embed','retrieve','rerank','generate','policy')),
      latency_ms INTEGER NOT NULL DEFAULT 0,
      token_count INTEGER NOT NULL DEFAULT 0,
      cost_usd   REAL    NOT NULL DEFAULT 0.0,
      model_id   TEXT,
      cache_hit  INTEGER NOT NULL DEFAULT 0 CHECK (cache_hit IN (0, 1)),
      error_code TEXT,
      redacted   INTEGER NOT NULL DEFAULT 0 CHECK (redacted IN (0, 1)),
      created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
    );

    CREATE INDEX IF NOT EXISTS idx_ag_traces_tenant_model_date
      ON ag_traces (tenant_id, model_id, date(created_at));

    CREATE INDEX IF NOT EXISTS idx_ag_traces_tool_id
      ON ag_traces (tool_id);

    CREATE INDEX IF NOT EXISTS idx_ag_traces_stage_created
      ON ag_traces (stage, created_at);
  `);
}

/**
 * Down migration: drops ag_traces table (and its indexes automatically).
 * Destructive — only use in development or rollback scenarios.
 *
 * @param db - Database connection with exec/run method
 */
export function down(db: { exec: (sql: string) => void }): void {
  db.exec(`DROP TABLE IF EXISTS ag_traces;`);
}
