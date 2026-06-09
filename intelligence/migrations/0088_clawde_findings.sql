-- Migration 0088 — Static analysis findings table (clawde_findings)
-- Canonical: P1-CANONICAL-MAPS "CANONICAL 0088 for this dir" → clawde_findings.
-- Note: 0087 is owned by clawde_lane_ab_log (W11-T03). Do NOT use 0087 for findings.
--
-- Purpose: Persist per-workspace findings from Semgrep and CodeQL static analysis runs.
--          Enables GetFindings integration with RetrieveContext (file_path-based lookup).
--
-- RLS isolation: current_setting('app.workspace_id')::uuid — canonical GUC for all
--               clawde_* tables. NOT hasura.user, NOT tenant_id.
-- Trigger source: pgmq clawde_analyze_queue (payload {workspace_id, repo_path, tools}).
--
-- Idempotent: all DDL uses CREATE TABLE IF NOT EXISTS, CREATE INDEX IF NOT EXISTS,
--             CREATE POLICY IF NOT EXISTS.
--
-- SPORT: REGISTRY-MIGRATIONS.md → 0088_clawde_findings.sql

-- ── Table ─────────────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS clawde_findings (
    id            UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id  UUID        NOT NULL
                  REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    chunk_id      UUID
                  REFERENCES clawde_chunks(id) ON DELETE SET NULL,
    rule_id       TEXT        NOT NULL,
    source        TEXT        NOT NULL CHECK (source IN ('semgrep', 'codeql')),
    severity      TEXT        NOT NULL CHECK (severity IN ('critical', 'high', 'medium', 'low', 'info')),
    message       TEXT        NOT NULL,
    file_path     TEXT        NOT NULL,
    line_start    INT,
    line_end      INT,
    col_start     INT,
    col_end       INT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── Indexes ───────────────────────────────────────────────────────────────────

-- Primary worker scan: workspace isolation + file-path lookup for GetFindings.
CREATE INDEX IF NOT EXISTS clawde_findings_workspace_filepath_idx
    ON clawde_findings (workspace_id, file_path);

-- Severity triage: surface critical/high findings quickly per workspace.
CREATE INDEX IF NOT EXISTS clawde_findings_workspace_severity_idx
    ON clawde_findings (workspace_id, severity);

-- ── Row-Level Security ────────────────────────────────────────────────────────

ALTER TABLE clawde_findings ENABLE ROW LEVEL SECURITY;

-- Isolation key: current_setting('app.workspace_id')::uuid — canonical for all
-- clawde_* tables. Set per-connection by the gateway before any query.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_policies
        WHERE tablename = 'clawde_findings'
          AND policyname = 'clawde_findings_workspace_isolation'
    ) THEN
        EXECUTE $policy$
            CREATE POLICY clawde_findings_workspace_isolation
                ON clawde_findings
                USING (workspace_id = current_setting('app.workspace_id')::uuid)
        $policy$;
    END IF;
END
$$;
