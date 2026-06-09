-- Migration 0091 — clawde_quota: per-workspace daily request quota enforcement.
-- Idempotent: all DDL uses IF NOT EXISTS / DO NOTHING guards.
-- Canonical: P1-E5-W17-S17-T02 (multi-tenant auth quota).
--
-- Adds:
--   clawde_quota — per-workspace daily request counter with tier + rolling window.
--
-- RLS isolation: uses app.workspace_id GUC (same pattern as all clawde_* tables).
-- The enforcer resets daily_count when window_start < today UTC.

-- ── clawde_quota ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS clawde_quota (
    workspace_id  UUID        PRIMARY KEY REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    tier          TEXT        NOT NULL DEFAULT 'free',
    daily_count   INTEGER     NOT NULL DEFAULT 0,
    window_start  TIMESTAMPTZ NOT NULL DEFAULT date_trunc('day', NOW() AT TIME ZONE 'UTC'),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE clawde_quota IS
    'Per-workspace daily request quota. Tier maps to DailyLimit: free=100, pro=10000, enterprise=unlimited(-1).';

-- ── Tier CHECK constraint (idempotent guard) ──────────────────────────────────
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'clawde_quota_tier_check'
          AND conrelid = 'clawde_quota'::regclass
    ) THEN
        ALTER TABLE clawde_quota
            ADD CONSTRAINT clawde_quota_tier_check
            CHECK (tier IN ('free', 'pro', 'enterprise'));
    END IF;
END $$;

-- ── Index ─────────────────────────────────────────────────────────────────────
CREATE INDEX IF NOT EXISTS idx_quota_workspace_window
    ON clawde_quota (workspace_id, window_start);

-- ── RLS ───────────────────────────────────────────────────────────────────────
ALTER TABLE clawde_quota ENABLE ROW LEVEL SECURITY;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE tablename = 'clawde_quota'
          AND policyname = 'clawde_quota_workspace_isolation'
    ) THEN
        CREATE POLICY clawde_quota_workspace_isolation ON clawde_quota
            USING (workspace_id = current_setting('app.workspace_id', true)::uuid);
    END IF;
END $$;

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DROP TABLE IF EXISTS clawde_quota;
-- */
