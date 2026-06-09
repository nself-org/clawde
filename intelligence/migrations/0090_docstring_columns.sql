-- Migration 0090 — docstring + doc_type columns (W14-S14-T04).
-- Idempotent: every statement uses IF NOT EXISTS, so applying it twice is a no-op.
-- Canonical: P1-CANONICAL-MAPS migration 0090 (W14-S14-T04).
--
-- Adds:
--   clawde_symbols.docstring  — extracted leading doc comment for a symbol.
--   clawde_chunks.doc_type    — provenance of a chunk: code | markdown | docstring | comment.
--
-- The doc_type CHECK constraint is added separately and guarded so re-running
-- this migration on a DB that already has the constraint does not error.

-- 1) Symbol docstring column (W12-T01 tree-sitter extraction target).
ALTER TABLE clawde_symbols
  ADD COLUMN IF NOT EXISTS docstring text;

-- 2) Chunk doc_type column with a safe default for existing rows.
ALTER TABLE clawde_chunks
  ADD COLUMN IF NOT EXISTS doc_type text NOT NULL DEFAULT 'code';

-- 3) doc_type enum CHECK constraint — added only when absent (idempotent).
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint
    WHERE conname = 'clawde_chunks_doc_type_check'
  ) THEN
    ALTER TABLE clawde_chunks
      ADD CONSTRAINT clawde_chunks_doc_type_check
      CHECK (doc_type IN ('code', 'markdown', 'docstring', 'comment'));
  END IF;
END $$;

-- 4) Index for doc_type filtering during retrieval (idempotent).
CREATE INDEX IF NOT EXISTS clawde_chunks_doc_type_idx
  ON clawde_chunks (workspace_id, doc_type);
