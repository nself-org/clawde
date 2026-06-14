-- Migration 0092 — trust_registry: trusted workspace registry for gate 5.
-- Idempotent: all DDL uses IF NOT EXISTS guards.
-- Canonical: P2-E1-W2-S4-T09 (trust supply-chain real impls).
--
-- Adds:
--   np_clawde_trusted_workspaces — workspace trust registry used by TrustRegistryCheck.
--
-- Multi-App Isolation: source_account_id TEXT NOT NULL DEFAULT 'primary'
-- (per nSelf convention — not cloud multi-tenancy).

-- ── up ────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS np_clawde_trusted_workspaces (
    workspace_id      TEXT        PRIMARY KEY,
    registered_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    source_account_id TEXT        NOT NULL DEFAULT 'primary'
);

COMMENT ON TABLE np_clawde_trusted_workspaces IS
    'Trusted workspace registry. Gate 5 (TrustRegistryCheck) denies any workspace_id not present here.';

-- ── down ──────────────────────────────────────────────────────────────────────
-- To reverse: DROP TABLE IF EXISTS np_clawde_trusted_workspaces;
