-- Migration 001: context_summaries table
-- Stores AI-generated or extractive summaries of truncated conversation history.
-- Written when the context window fills and older messages cannot fit in the budget.
-- The summary is prepended as a synthetic ContextMessage{IsSummary: true} on restore.

CREATE TABLE IF NOT EXISTS context_summaries (
    id           TEXT PRIMARY KEY,
    session_id   TEXT NOT NULL,
    summary      TEXT NOT NULL,
    covered_from TEXT NOT NULL,  -- oldest message_id in the summarised range
    covered_to   TEXT NOT NULL,  -- newest message_id in the summarised range
    token_est    INTEGER NOT NULL,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_cs_session ON context_summaries(session_id, created_at DESC);
