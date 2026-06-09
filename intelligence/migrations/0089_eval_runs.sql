-- Migration 0089 — clawde_eval_runs full schema (supersedes 0083 stub)
-- Idempotent: ALTER TABLE IF NOT EXISTS / ADD COLUMN IF NOT EXISTS.
-- Canonical: P1-CANONICAL-MAPS migration 0089 (W12-S12-T04).
--
-- 0083 created a minimal stub (id, workspace_id, name, status, created_at).
-- This migration adds the full eval-harness columns.  A DB that ran 0083
-- will pick up the new columns; a fresh DB gets the full table in one shot.
--
-- Tie-break rule (ADR-005/006): BGE-M3 is default.
-- Gemini text-embedding-004 wins ONLY when recall@10 > BGE × 1.05 AND p95_ms < 200.
-- Ties resolve to BGE-M3.
--
-- vector dimension: 1024 (ADR-005 BGE-M3).
-- Column name canonical: mrr_at_10 (not mrr_at10, not mrr10).
-- RLS policy uses app.workspace_id (not hasura.user).

-- ── Ensure full table exists (fresh DB path) ────────────────────────────────
CREATE TABLE IF NOT EXISTS clawde_eval_runs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    provider     TEXT        NOT NULL DEFAULT '',     -- 'bge-m3' | 'gemini-text-embedding-004'
    dataset      TEXT        NOT NULL DEFAULT '',     -- dataset name / path key
    recall_at_5  DOUBLE PRECISION NOT NULL DEFAULT 0,
    recall_at_10 DOUBLE PRECISION NOT NULL DEFAULT 0,
    mrr_at_10    DOUBLE PRECISION NOT NULL DEFAULT 0, -- canonical column name
    p50_ms       INTEGER     NOT NULL DEFAULT 0,
    p95_ms       INTEGER     NOT NULL DEFAULT 0,
    run_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    metadata     JSONB,
    -- Legacy 0083 columns kept for compat; will be dropped in a future cleanup migration.
    name         TEXT        NOT NULL DEFAULT '',
    status       TEXT        NOT NULL DEFAULT 'done',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Upgrade path for DBs that ran 0083 stub ─────────────────────────────────
DO $$
BEGIN
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS provider     TEXT             NOT NULL DEFAULT '';
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS dataset      TEXT             NOT NULL DEFAULT '';
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS recall_at_5  DOUBLE PRECISION NOT NULL DEFAULT 0;
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS recall_at_10 DOUBLE PRECISION NOT NULL DEFAULT 0;
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS mrr_at_10    DOUBLE PRECISION NOT NULL DEFAULT 0;
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS p50_ms       INTEGER          NOT NULL DEFAULT 0;
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS p95_ms       INTEGER          NOT NULL DEFAULT 0;
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS run_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW();
    ALTER TABLE clawde_eval_runs ADD COLUMN IF NOT EXISTS metadata     JSONB;
EXCEPTION WHEN OTHERS THEN
    -- Column already present; ignore.
    NULL;
END;
$$;

-- ── Index ────────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS clawde_eval_runs_workspace_run_at_idx
    ON clawde_eval_runs (workspace_id, run_at DESC);

-- ── RLS ──────────────────────────────────────────────────────────────────────
-- Uses app.workspace_id session variable (NOT hasura.user).
ALTER TABLE clawde_eval_runs ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE tablename = 'clawde_eval_runs' AND policyname = 'eval_runs_workspace_isolation'
    ) THEN
        EXECUTE $p$
            CREATE POLICY eval_runs_workspace_isolation ON clawde_eval_runs
                USING (workspace_id = current_setting('app.workspace_id')::uuid)
        $p$;
    END IF;
END;
$$;

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DROP TABLE IF EXISTS clawde_eval_runs;
-- */
