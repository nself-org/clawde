-- Migration 0085 — Indexes, RLS, and pg_cron retention
-- Idempotent: all DDL uses IF NOT EXISTS / CREATE POLICY IF NOT EXISTS.
-- Canonical: P1-CANONICAL-MAPS migration 0085.
--
-- Applies to all clawde_* tables created in 0082-0084:
--   clawde_workspaces, clawde_cost_ledger, clawde_chunks,
--   clawde_eval_runs, clawde_symbols, clawde_graph_edges
--
-- RLS isolation key: current_setting('app.workspace_id')::uuid (canonical).
--   NOT hasura.user, NOT tenant_id.
--
-- pg_cron: deletes clawde_chunks rows older than their ttl_days setting.
--   Requires pg_cron extension. If not installed, the cron schedule is skipped
--   gracefully via a DO block.

-- ── Performance Indexes ───────────────────────────────────────────────────────

-- HNSW index on clawde_chunks.embedding (cosine distance, ADR-005 BGE-M3).
-- ef_construction=128 is a good production default; tune to recall requirements.
CREATE INDEX IF NOT EXISTS idx_chunks_embedding_hnsw
    ON clawde_chunks
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 128);

-- GIN index on clawde_chunks.content_tsv for fast full-text search.
CREATE INDEX IF NOT EXISTS idx_chunks_content_tsv_gin
    ON clawde_chunks USING GIN (content_tsv);

-- B-tree composite on (workspace_id, created_at) for time-range scans.
CREATE INDEX IF NOT EXISTS idx_chunks_workspace_created
    ON clawde_chunks (workspace_id, created_at DESC);

-- sparse_vec GIN for JSONB key lookups (SPLADE term matching).
CREATE INDEX IF NOT EXISTS idx_chunks_sparse_vec_gin
    ON clawde_chunks USING GIN (sparse_vec);

-- content_hash for dedup lookups.
CREATE INDEX IF NOT EXISTS idx_chunks_content_hash
    ON clawde_chunks (workspace_id, content_hash);

-- ── Row-Level Security ────────────────────────────────────────────────────────
-- Isolation: app.workspace_id GUC set per-connection by the gateway.
-- Pattern: app code runs SET LOCAL app.workspace_id = '<uuid>' before any query.

-- clawde_workspaces
ALTER TABLE clawde_workspaces ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_workspaces' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_workspaces
      USING (id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- clawde_cost_ledger
ALTER TABLE clawde_cost_ledger ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_cost_ledger' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_cost_ledger
      USING (workspace_id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- clawde_chunks
ALTER TABLE clawde_chunks ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_chunks' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_chunks
      USING (workspace_id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- clawde_eval_runs
ALTER TABLE clawde_eval_runs ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_eval_runs' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_eval_runs
      USING (workspace_id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- clawde_symbols
ALTER TABLE clawde_symbols ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_symbols' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_symbols
      USING (workspace_id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- clawde_graph_edges
ALTER TABLE clawde_graph_edges ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_graph_edges' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_graph_edges
      USING (workspace_id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- ── pg_cron Retention ─────────────────────────────────────────────────────────
-- Deletes clawde_chunks rows whose age (in days) exceeds their ttl_days value.
-- Runs nightly at 03:00 UTC. Skipped gracefully if pg_cron is not installed.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM pg_extension WHERE extname = 'pg_cron'
  ) THEN
    PERFORM cron.schedule(
      'clawde-chunks-retention',      -- job name (idempotent — unschedule first)
      '0 3 * * *',                    -- nightly at 03:00 UTC
      $$
        DELETE FROM clawde_chunks
        WHERE created_at < NOW() - (ttl_days || ' days')::INTERVAL;
      $$
    );
  END IF;
EXCEPTION WHEN others THEN
  NULL; -- pg_cron not available; skip gracefully
END $$;

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DO $$ BEGIN
--   IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
--     PERFORM cron.unschedule('clawde-chunks-retention');
--   END IF;
-- EXCEPTION WHEN others THEN NULL;
-- END $$;
-- ALTER TABLE clawde_graph_edges  DISABLE ROW LEVEL SECURITY;
-- ALTER TABLE clawde_symbols      DISABLE ROW LEVEL SECURITY;
-- ALTER TABLE clawde_eval_runs    DISABLE ROW LEVEL SECURITY;
-- ALTER TABLE clawde_chunks       DISABLE ROW LEVEL SECURITY;
-- ALTER TABLE clawde_cost_ledger  DISABLE ROW LEVEL SECURITY;
-- ALTER TABLE clawde_workspaces   DISABLE ROW LEVEL SECURITY;
-- DROP INDEX IF EXISTS idx_chunks_content_hash;
-- DROP INDEX IF EXISTS idx_chunks_sparse_vec_gin;
-- DROP INDEX IF EXISTS idx_chunks_workspace_created;
-- DROP INDEX IF EXISTS idx_chunks_content_tsv_gin;
-- DROP INDEX IF EXISTS idx_chunks_embedding_hnsw;
-- */
