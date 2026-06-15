/**
 * Purpose: Drizzle-style migration spec for ag_traces table, plus dual-write
 * trace writer that persists to SQLite ag_traces AND exports to an OTLP
 * collector when OTEL_EXPORTER_OTLP_ENDPOINT is set.
 *
 * The actual SQL migration lives at: apps/daemon/src/storage/migrations/058_ai_gateway_traces.sql
 * This file is the TypeScript reference / Drizzle ORM companion for the observability layer.
 *
 * ag_traces stores per-stage pipeline trace records for nClaw AI gateway observability.
 * Schema matches TraceRecord interface in types.ts.
 *
 * Inputs:
 *   - Database connection (SQLite via better-sqlite3 or libsql) for DDL and writeTrace
 *   - TraceRecord (types.ts) for writeTrace
 *   - OTEL_EXPORTER_OTLP_ENDPOINT env var — enables OTel export when set
 * Outputs:
 *   - ag_traces table + indexes created (up) / dropped (down)
 *   - TraceRecord persisted to SQLite AND forwarded to OTel collector (dual-write)
 * Constraints:
 *   - Idempotent DDL — uses CREATE TABLE IF NOT EXISTS and DROP TABLE IF EXISTS.
 *   - PII redaction is enforced at application layer (redact.ts) before any insert.
 *   - OTel export is best-effort: SQLite write always happens; OTLP failure is logged, not thrown.
 *   - initOtelExporter() must be called once at daemon startup before writeTrace().
 * SPORT: REGISTRY-MIGRATIONS.md (migration 058); MASTER-TABLES.md (ag_traces DDL);
 *        REGISTRY-SERVICES.md (observability — conditional-active OTel export)
 */

import type { TraceRecord } from "./types";

// ─── OTel OTLP exporter (conditional) ────────────────────────────────────────

/**
 * Lazily-resolved OTLPTraceExporter instance.
 * Null when OTEL_EXPORTER_OTLP_ENDPOINT is not set (nop path).
 */
let _otlpExporter: OTLPTraceExporterLike | null = null;

/**
 * Minimal interface for the OTLP exporter so we can import dynamically and
 * keep the type surface narrow.  The full class from
 * @opentelemetry/exporter-trace-otlp-http satisfies this interface.
 */
interface OTLPTraceExporterLike {
  export(spans: OtlpSpan[], resultCallback: (result: { code: number }) => void): void;
  shutdown(): Promise<void>;
}

/** Minimal span shape forwarded to the OTLP collector. */
interface OtlpSpan {
  name: string;
  attributes: Record<string, string | number | boolean>;
  startTimeUnixNano: bigint;
  endTimeUnixNano: bigint;
}

/**
 * Initialises the OTLPTraceExporter when OTEL_EXPORTER_OTLP_ENDPOINT is set.
 * Call once at daemon startup before any writeTrace() calls.
 *
 * When the env var is absent this is a nop and subsequent writeTrace() calls
 * skip the OTel export path without error.
 *
 * @returns Promise that resolves when the exporter is ready (or nop).
 */
export async function initOtelExporter(): Promise<void> {
  const endpoint = process.env["OTEL_EXPORTER_OTLP_ENDPOINT"];
  if (!endpoint) {
    return; // nop — OTel disabled
  }

  try {
    // Dynamic import keeps @opentelemetry/exporter-trace-otlp-http out of the
    // module graph for deployments that do not set the endpoint.
    const { OTLPTraceExporter } = await import(
      "@opentelemetry/exporter-trace-otlp-http"
    );
    _otlpExporter = new OTLPTraceExporter({ url: `${endpoint}/v1/traces` }) as OTLPTraceExporterLike;
  } catch (err) {
    // Package not installed — OTel export silently disabled.
    console.warn("otlp-exporter: @opentelemetry/exporter-trace-otlp-http not available, OTel export disabled:", err);
  }
}

/**
 * Shuts down the OTLPTraceExporter, flushing any buffered spans.
 * Call on daemon graceful shutdown after SQLite is closed.
 */
export async function shutdownOtelExporter(): Promise<void> {
  if (_otlpExporter) {
    await _otlpExporter.shutdown();
    _otlpExporter = null;
  }
}

// ─── Dual-write trace writer ──────────────────────────────────────────────────

/**
 * Database connection shape for trace inserts (subset of better-sqlite3 / libsql).
 */
interface TraceDb {
  prepare(sql: string): { run(...args: unknown[]): void };
}

/**
 * Writes a TraceRecord to BOTH ag_traces (SQLite) AND the OTel collector
 * when OTEL_EXPORTER_OTLP_ENDPOINT is set.
 *
 * The SQLite write is synchronous and always happens first.
 * The OTel export is fire-and-forget (best-effort) — failures are logged
 * but do NOT propagate so a collector outage never breaks the AI gateway.
 *
 * @param db     - SQLite database connection with prepare() method
 * @param record - Fully-redacted TraceRecord (PII already stripped by redact.ts)
 */
export function writeTrace(db: TraceDb, record: TraceRecord): void {
  // 1. SQLite insert (primary, synchronous, always happens)
  const stmt = db.prepare(`
    INSERT OR REPLACE INTO ag_traces
      (trace_id, tenant_id, tool_id, stage, latency_ms, token_count,
       cost_usd, model_id, cache_hit, error_code, redacted)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
  `);
  stmt.run(
    record.trace_id,
    record.tenant_id,
    record.tool_id,
    record.stage,
    record.latency_ms,
    record.token_count,
    record.cost_usd,
    record.model_id,
    record.cache_hit ? 1 : 0,
    record.error_code,
    record.redacted ? 1 : 0,
  );

  // 2. OTel OTLP export (secondary, best-effort, nop when exporter not init'd)
  if (!_otlpExporter) return;

  const nowNs = BigInt(Date.now()) * 1_000_000n;
  const startNs = nowNs - BigInt(record.latency_ms) * 1_000_000n;

  const span: OtlpSpan = {
    name: `ag_trace.${record.stage}`,
    attributes: {
      "ag.trace_id": record.trace_id,
      "ag.tenant_id": record.tenant_id,
      "ag.tool_id": record.tool_id,
      "ag.stage": record.stage,
      "ag.latency_ms": record.latency_ms,
      "ag.token_count": record.token_count,
      "ag.cost_usd": record.cost_usd,
      ...(record.model_id ? { "ag.model_id": record.model_id } : {}),
      "ag.cache_hit": record.cache_hit,
      ...(record.error_code ? { "ag.error_code": record.error_code } : {}),
    },
    startTimeUnixNano: startNs,
    endTimeUnixNano: nowNs,
  };

  _otlpExporter.export([span], (result) => {
    if (result.code !== 0) {
      console.warn("otlp-exporter: span export failed, code:", result.code);
    }
  });
}

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
