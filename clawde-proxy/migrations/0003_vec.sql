-- 0003_vec.sql — sqlite-vec vector table for message embeddings.
-- Purpose: Adds message_embeddings vec0 virtual table for BGE-M3 semantic search.
-- Ref: clawde-keystone-spec.md §7.
-- Applied by: db/migrations.go embedded FS runner (idempotent).
-- Note: sqlite-vec extension must be loaded before this migration runs.
--   If the extension is unavailable, this migration is skipped gracefully by the runner.

CREATE VIRTUAL TABLE IF NOT EXISTS message_embeddings USING vec0(
    message_id TEXT,
    embedding FLOAT[1024]
);
