-- 0001_initial.sql — core tables for clawde-proxy.
-- Purpose: Creates chat_messages, proxy_routes, request_log, and worktrees tables.
-- Ref: clawde-keystone-spec.md §7 Data Model Changes.
-- Applied by: db/migrations.go embedded FS runner (idempotent via _migrations table).

-- Chat messages stored per session.
CREATE TABLE IF NOT EXISTS chat_messages (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL,
    role        TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'system', 'tool')),
    content     TEXT NOT NULL,
    model       TEXT,
    tokens_in   INTEGER NOT NULL DEFAULT 0,
    tokens_out  INTEGER NOT NULL DEFAULT 0,
    latency_ms  INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    metadata    TEXT -- JSON blob for arbitrary per-message data
);

-- Routing rules: maps lane names to upstream model endpoints.
CREATE TABLE IF NOT EXISTS proxy_routes (
    id          TEXT PRIMARY KEY,
    lane        TEXT NOT NULL UNIQUE,
    upstream    TEXT NOT NULL,
    priority    INTEGER NOT NULL DEFAULT 0,
    enabled     INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Append-only log of every proxied request for analytics and debugging.
CREATE TABLE IF NOT EXISTS request_log (
    id              TEXT PRIMARY KEY,
    session_id      TEXT,
    lane            TEXT NOT NULL,
    upstream        TEXT NOT NULL,
    status_code     INTEGER,
    tokens_in       INTEGER NOT NULL DEFAULT 0,
    tokens_out      INTEGER NOT NULL DEFAULT 0,
    latency_ms      INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT,
    created_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);

-- Git worktrees known to this clawde-proxy instance.
CREATE TABLE IF NOT EXISTS worktrees (
    id          TEXT PRIMARY KEY,
    path        TEXT NOT NULL UNIQUE,
    branch      TEXT NOT NULL,
    session_id  TEXT,
    status      TEXT NOT NULL DEFAULT 'idle' CHECK (status IN ('idle', 'active', 'stale')),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
