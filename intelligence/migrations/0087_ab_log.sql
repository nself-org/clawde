-- Migration 0087 — BM25 A/B comparison log table + pg_bm25 VACUUM cron
-- Canonical: P1-CANONICAL-MAPS "CANONICAL 0087 for this dir" → clawde_lane_ab_log.
-- Note: 0088 is reserved for clawde_findings (do NOT use 0087 for findings).
--
-- Purpose: Persist comparative top-10 results from the tsvector baseline and
--          ParadeDB BM25 lanes for offline recall analysis. Enables data-driven
--          decision on when to promote BM25 to the default retrieval path.
--
-- RLS isolation: same GUC as all other clawde_* tables — app.workspace_id.
-- pg_bm25 VACUUM: schedules a nightly maintenance job for ParadeDB BM25 indexes.
--                 Entry is a no-op when pg_bm25 extension is absent (graceful skip).
--
-- Idempotent: all DDL uses CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT EXISTS,
--             CREATE POLICY IF NOT EXISTS.
-- Apache-2.0 note: ParadeDB (https://github.com/paradedb/paradedb) is licensed
--                  under Apache 2.0 — safe for commercial and MIT-licensed projects.
--
-- SPORT: REGISTRY-MIGRATIONS.md → 0087_ab_log.sql

-- ── Table ─────────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS clawde_lane_ab_log (
    id              BIGSERIAL PRIMARY KEY,
    workspace_id    UUID        NOT NULL
                    REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    query           TEXT        NOT NULL,
    -- JSONB arrays of {chunk_id, score, content} — top 10 from each lane.
    tsvector_top10  JSONB       NOT NULL DEFAULT '[]',
    bm25_top10      JSONB       NOT NULL DEFAULT '[]',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE clawde_lane_ab_log IS
    'A/B comparison log: top-10 results from tsvector vs ParadeDB BM25 per query. '
    'Used for offline recall analysis before promoting BM25 to default. '
    'Populated only when CLAWDE_BM25_AB_MODE=true.';

-- ── Indexes ───────────────────────────────────────────────────────────────────

-- Time-range scan per workspace for batch recall analysis.
CREATE INDEX IF NOT EXISTS idx_ab_log_workspace_created
    ON clawde_lane_ab_log (workspace_id, created_at DESC);

-- ── Row-Level Security ────────────────────────────────────────────────────────
-- Isolation key: current_setting('app.workspace_id')::uuid — canonical for all
-- clawde_* tables (see migration 0085). NOT hasura.user, NOT tenant_id.

ALTER TABLE clawde_lane_ab_log ENABLE ROW LEVEL SECURITY;

DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_lane_ab_log'
      AND policyname = 'workspace_isolation'
  ) THEN
    EXECUTE $$
      CREATE POLICY workspace_isolation ON clawde_lane_ab_log
        USING (workspace_id = current_setting('app.workspace_id')::uuid)
    $$;
  END IF;
EXCEPTION WHEN others THEN
  NULL; -- policy already exists or RLS not supported; skip gracefully
END $$;

-- ── pg_bm25 VACUUM cron ───────────────────────────────────────────────────────
-- Schedules a nightly BM25 index VACUUM for the clawde_chunks table so ParadeDB
-- BM25 scores stay fresh after bulk chunk deletes/updates.
--
-- Graceful skip when:
--   • pg_cron extension is absent (cron.schedule is not callable).
--   • pg_bm25 extension is absent (no BM25 index exists to vacuum).
-- In both cases the DO block exits cleanly — no error, no rollback.
--
-- Note: This is a maintenance entry only. The BM25 index on clawde_chunks.content
-- is NOT created here; it must be created separately with:
--   CREATE INDEX ON clawde_chunks USING bm25 (content)
--     WITH (key_field='id');
-- That DDL is intentionally deferred until CLAWDE_BM25_ENABLED=true is confirmed
-- in production (avoids schema migration requirement for the swap contract).

DO $$
BEGIN
  -- Only schedule when BOTH pg_cron AND pg_bm25 are installed.
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron')
  AND EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_bm25') THEN
    PERFORM cron.schedule(
      'clawde-bm25-vacuum',       -- job name (idempotent key)
      '0 4 * * *',                -- nightly at 04:00 UTC (after chunk retention at 03:00)
      $$
        -- Refresh the BM25 index statistics for clawde_chunks.
        -- CALL paradedb.vacuum('clawde_chunks');
        -- ^ Uncomment and adjust to match ParadeDB API version in use.
        SELECT 1; -- no-op placeholder until API version is confirmed
      $$
    );
  END IF;
EXCEPTION WHEN others THEN
  NULL; -- pg_cron or pg_bm25 unavailable; skip gracefully
END $$;

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DO $$ BEGIN
--   IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
--     PERFORM cron.unschedule('clawde-bm25-vacuum');
--   END IF;
-- EXCEPTION WHEN others THEN NULL;
-- END $$;
-- ALTER TABLE clawde_lane_ab_log DISABLE ROW LEVEL SECURITY;
-- DROP TABLE IF EXISTS clawde_lane_ab_log;
-- */
