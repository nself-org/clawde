-- Migration 0082 — clawde_workspaces + clawde_cost_ledger
-- Idempotent: all DDL uses IF NOT EXISTS / CREATE EXTENSION IF NOT EXISTS.
-- Canonical: P1-CANONICAL-MAPS migration 0082 (supersedes orphan 0081).
--
-- NOTE: This migration folds 0081_cost_ledger.sql (non-canonical number created
-- by W10-T02) into the canonical 0082 slot and adds the parent clawde_workspaces
-- table. The orphan 0081_cost_ledger.sql has been deleted.
--
-- workspace_id is UUID FK → clawde_workspaces on all clawde_* tables.
-- Isolation column: app.workspace_id (GUC) — NOT tenant_id, NOT hasura.user.

-- ── pgvector extension ────────────────────────────────────────────────────────
CREATE EXTENSION IF NOT EXISTS vector;

-- ── clawde_workspaces ─────────────────────────────────────────────────────────
-- Parent table: every workspace is one isolated ClawDE installation / user.
CREATE TABLE IF NOT EXISTS clawde_workspaces (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT        NOT NULL DEFAULT '',
    owner_id    TEXT        NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index: owner lookup.
CREATE INDEX IF NOT EXISTS idx_workspaces_owner
    ON clawde_workspaces (owner_id);

-- ── clawde_cost_ledger ────────────────────────────────────────────────────────
-- Token-usage audit log. No prompt/completion content — metadata only.
-- Unified schema: id UUID PK (canonical) + workspace FK + all gateway columns.
CREATE TABLE IF NOT EXISTS clawde_cost_ledger (
    id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id     UUID        NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    provider         TEXT        NOT NULL,
    model            TEXT        NOT NULL,
    lane             TEXT        NOT NULL,
    user_id          TEXT        NOT NULL DEFAULT '',
    tokens_in        INTEGER     NOT NULL DEFAULT 0,
    tokens_out       INTEGER     NOT NULL DEFAULT 0,
    cost_usd_estimate NUMERIC(18, 8) NOT NULL DEFAULT 0,
    latency_ms       BIGINT      NOT NULL DEFAULT 0,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Primary query pattern: cost by workspace over time.
CREATE INDEX IF NOT EXISTS idx_cost_ledger_workspace_created
    ON clawde_cost_ledger (workspace_id, created_at DESC);

-- Secondary: cost breakdown by provider.
CREATE INDEX IF NOT EXISTS idx_cost_ledger_provider_created
    ON clawde_cost_ledger (provider, created_at DESC);

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DROP TABLE IF EXISTS clawde_cost_ledger;
-- DROP TABLE IF EXISTS clawde_workspaces;
-- DROP EXTENSION IF EXISTS vector;
-- */
