-- Migration 0084 — clawde_symbols + clawde_graph_edges
-- Idempotent: all DDL uses IF NOT EXISTS.
-- Canonical: P1-CANONICAL-MAPS migration 0084.
--
-- clawde_symbols: code symbol index (functions, classes, methods, types, consts).
-- clawde_graph_edges: dependency / call-graph edges between symbols or chunks.
--
-- Isolation: workspace_id UUID FK → clawde_workspaces.

-- ── clawde_symbols ────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS clawde_symbols (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    name         TEXT        NOT NULL,
    kind         TEXT        NOT NULL CHECK (kind IN ('function','class','method','type','const')),
    file_path    TEXT        NOT NULL DEFAULT '',
    line_start   INTEGER     NOT NULL DEFAULT 0,
    line_end     INTEGER     NOT NULL DEFAULT 0,
    signature    TEXT,                                -- full type/param signature
    doc          TEXT,                                -- extracted doc-comment
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index: workspace + file for fast file-scoped lookups.
CREATE INDEX IF NOT EXISTS idx_symbols_workspace_file
    ON clawde_symbols (workspace_id, file_path);

-- Index: name search within workspace.
CREATE INDEX IF NOT EXISTS idx_symbols_workspace_name
    ON clawde_symbols (workspace_id, name);

-- ── clawde_graph_edges ────────────────────────────────────────────────────────
-- Directed edges in the knowledge graph (calls, imports, references).
CREATE TABLE IF NOT EXISTS clawde_graph_edges (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID        NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    src_type     TEXT        NOT NULL CHECK (src_type IN ('symbol','chunk')),
    src_id       UUID        NOT NULL,   -- FK to clawde_symbols.id or clawde_chunks.id
    dst_type     TEXT        NOT NULL CHECK (dst_type IN ('symbol','chunk')),
    dst_id       UUID        NOT NULL,
    edge_kind    TEXT        NOT NULL DEFAULT 'calls',  -- calls|imports|references|embeds
    weight       REAL        NOT NULL DEFAULT 1.0,
    metadata     JSONB,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index: outgoing edges from a source node.
CREATE INDEX IF NOT EXISTS idx_graph_edges_src
    ON clawde_graph_edges (workspace_id, src_type, src_id);

-- Index: incoming edges to a destination node (reverse traversal).
CREATE INDEX IF NOT EXISTS idx_graph_edges_dst
    ON clawde_graph_edges (workspace_id, dst_type, dst_id);

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DROP TABLE IF EXISTS clawde_graph_edges;
-- DROP TABLE IF EXISTS clawde_symbols;
-- */
