-- Migration 0083 — clawde_chunks + clawde_eval_runs (stub)
-- Idempotent: all DDL uses IF NOT EXISTS.
-- Canonical: P1-CANONICAL-MAPS migration 0083.
--
-- clawde_chunks: primary knowledge store — vector(1024) + BM25 tsvector +
--   optional sparse_vec JSONB. Full-text column name is content_tsv (canonical).
-- clawde_eval_runs: STUB — minimal schema only. Full schema owned by 0089 (W12-T04).
--
-- vector dimension: 1024 (ADR-005 BGE-M3).
-- tsvector column: content_tsv GENERATED ALWAYS AS (...) STORED.
-- Isolation: workspace_id UUID FK → clawde_workspaces.

-- ── clawde_chunks ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS clawde_chunks (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    content       TEXT        NOT NULL,
    -- Dense vector embedding (BGE-M3, 1024-dim, ADR-005).
    embedding     vector(1024),
    -- Full-text search column. CANONICAL NAME: content_tsv (not chunk_tsv, not ts_body).
    content_tsv   TSVECTOR    GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
    -- Sparse vector for SPLADE / hybrid BM25 lanes stored as JSONB {term→weight}.
    sparse_vec    JSONB,
    source_type   TEXT        NOT NULL DEFAULT '',   -- e.g. 'file', 'conversation', 'symbol'
    source_ref    TEXT        NOT NULL DEFAULT '',   -- path, URL, or entity ref
    chunk_index   INTEGER     NOT NULL DEFAULT 0,
    content_hash  TEXT        NOT NULL DEFAULT '',   -- SHA-256 of content for dedup
    metadata      JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ttl_days      INTEGER     NOT NULL DEFAULT 90    -- retention days; enforced by pg_cron in 0085
);

-- ── clawde_eval_runs ──────────────────────────────────────────────────────────
-- STUB: minimal schema placeholder. Full schema (datasets, scores, comparisons)
-- is owned by migration 0089 (W12-T04). Do NOT add columns here.
CREATE TABLE IF NOT EXISTS clawde_eval_runs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'pending',  -- pending|running|done|failed
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DROP TABLE IF EXISTS clawde_eval_runs;
-- DROP TABLE IF EXISTS clawde_chunks;
-- */
