-- Migration 0086 — pgmq queues, clawde_job table, pg_cron schedules, LISTEN/NOTIFY triggers
-- Idempotent: all DDL uses IF NOT EXISTS; pg_cron and pgmq blocks are wrapped in DO blocks.
-- Canonical: P1-CANONICAL-MAPS migration 0086.
-- Depends on: 0082 (workspaces), 0083 (chunks, eval_runs), 0084 (symbols).
-- Broker: Postgres-native only — no Redis/Kafka/RabbitMQ required.
-- RLS key: current_setting('app.workspace_id')::uuid (same GUC as 0085).

-- ── pgmq Extension ────────────────────────────────────────────────────────────

DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgmq') THEN
    CREATE EXTENSION pgmq;
  END IF;
EXCEPTION WHEN others THEN
  RAISE NOTICE 'pgmq extension not available; queue creation skipped. Install pg_extensions first.';
END $$;

-- ── Message Queues ────────────────────────────────────────────────────────────
-- Five canonical queues per P1-CANONICAL-MAPS.

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgmq') THEN
    -- Ingest queue: raw document/code ingestion jobs
    PERFORM pgmq.create_if_not_exists('clawde_ingest_queue');
    -- Embed queue: chunked text → vector embedding jobs
    PERFORM pgmq.create_if_not_exists('clawde_embed_queue');
    -- Analyze queue: static analysis / symbol extraction jobs
    PERFORM pgmq.create_if_not_exists('clawde_analyze_queue');
    -- Learn queue: GraphRAG edge learning, knowledge distillation jobs
    PERFORM pgmq.create_if_not_exists('clawde_learn_queue');
    -- Dead-letter queue: failed jobs archived here after max retries
    PERFORM pgmq.create_if_not_exists('clawde_dead_letter');
  END IF;
EXCEPTION WHEN others THEN
  RAISE NOTICE 'pgmq queue creation skipped: %', SQLERRM;
END $$;

-- ── clawde_job Table ──────────────────────────────────────────────────────────
-- Tracks all job lifecycle states. Immutable after insertion; status column is
-- the only mutable field (updated atomically by the worker on transitions).

CREATE TABLE IF NOT EXISTS clawde_job (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID       NOT NULL REFERENCES clawde_workspaces(id) ON DELETE CASCADE,
    queue       TEXT        NOT NULL CHECK (queue IN (
                                'clawde_ingest_queue',
                                'clawde_embed_queue',
                                'clawde_analyze_queue',
                                'clawde_learn_queue',
                                'clawde_dead_letter'
                            )),
    payload     JSONB       NOT NULL DEFAULT '{}',
    priority    SMALLINT    NOT NULL DEFAULT 5 CHECK (priority BETWEEN 1 AND 10),
    -- 1=failed/DLQ, 2=pending, 3=running, 4=done
    status      SMALLINT    NOT NULL DEFAULT 2 CHECK (status IN (1, 2, 3, 4)),
    retry_count SMALLINT    NOT NULL DEFAULT 0,
    visible_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

COMMENT ON TABLE  clawde_job IS 'Postgres-native job ledger; status 1=failed,2=pending,3=running,4=done';
COMMENT ON COLUMN clawde_job.priority   IS '1 (highest) … 10 (lowest)';
COMMENT ON COLUMN clawde_job.visible_at IS 'Job invisible to workers until this timestamp (used for backoff)';

-- Compound index: queue + visibility window (primary worker scan)
CREATE INDEX IF NOT EXISTS idx_clawde_job_queue_visible
    ON clawde_job (queue, visible_at)
    WHERE status = 2;  -- partial: only pending rows

-- Priority scan within a queue
CREATE INDEX IF NOT EXISTS idx_clawde_job_priority
    ON clawde_job (queue, priority DESC, created_at)
    WHERE status = 2;

-- RLS on clawde_job
ALTER TABLE clawde_job ENABLE ROW LEVEL SECURITY;
DO $$ BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'clawde_job' AND policyname = 'workspace_isolation'
  ) THEN
    CREATE POLICY workspace_isolation ON clawde_job
      USING (workspace_id = current_setting('app.workspace_id')::uuid);
  END IF;
END $$;

-- ── pg_cron Schedules ─────────────────────────────────────────────────────────
-- All schedules are idempotent: unschedule-then-schedule pattern.
-- Skipped gracefully when pg_cron is not installed.

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN

    -- 1. Re-embedding sweep — refresh stale chunk embeddings nightly at 03:00 UTC
    PERFORM cron.unschedule('clawde-reembed-sweep');
    PERFORM cron.schedule(
      'clawde-reembed-sweep',
      '0 3 * * *',
      $$
        UPDATE clawde_chunks
        SET embedding = NULL
        WHERE updated_at < NOW() - INTERVAL '7 days'
          AND embedding IS NOT NULL;
      $$
    );

    -- 2. Ragas evaluation sweep — run evals every 6 hours
    PERFORM cron.unschedule('clawde-ragas-eval');
    PERFORM cron.schedule(
      'clawde-ragas-eval',
      '0 */6 * * *',
      $$
        INSERT INTO clawde_eval_runs (workspace_id, suite, status)
        SELECT DISTINCT workspace_id, 'ragas_auto', 'pending'
        FROM clawde_chunks
        WHERE created_at > NOW() - INTERVAL '6 hours';
      $$
    );

    -- 3. DLQ sweep alert — check dead-letter depth every 5 minutes
    PERFORM cron.unschedule('clawde-dlq-alert');
    PERFORM cron.schedule(
      'clawde-dlq-alert',
      '*/5 * * * *',
      $$
        DO $inner$
        DECLARE v_count BIGINT;
        BEGIN
          SELECT COUNT(*) INTO v_count FROM clawde_job WHERE queue = 'clawde_dead_letter';
          IF v_count > 100 THEN
            RAISE WARNING 'clawde DLQ depth % exceeds threshold 100', v_count;
          END IF;
        END $inner$;
      $$
    );

    -- 4. Weekly GraphRAG re-summarization — every Monday at 00:00 UTC
    PERFORM cron.unschedule('clawde-graphrag-resummary');
    PERFORM cron.schedule(
      'clawde-graphrag-resummary',
      '0 0 * * 1',
      $$
        INSERT INTO clawde_job (workspace_id, queue, payload, priority)
        SELECT DISTINCT workspace_id, 'clawde_learn_queue',
               '{"task":"graphrag_resummary","trigger":"weekly_cron"}'::JSONB, 8
        FROM clawde_workspaces
        WHERE deleted_at IS NULL;
      $$
    );

  END IF;
EXCEPTION WHEN others THEN
  RAISE NOTICE 'pg_cron schedule skipped: %', SQLERRM;
END $$;

-- ── LISTEN/NOTIFY Trigger Functions ──────────────────────────────────────────
-- Four channels: clawde_chunk_ready, clawde_symbol_updated,
--                clawde_eval_complete, clawde_quota_hit.
-- Payload: JSON with table, op, row_id, workspace_id.

CREATE OR REPLACE FUNCTION clawde_notify_chunk_ready()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify(
    'clawde_chunk_ready',
    json_build_object(
      'table',        'clawde_chunks',
      'op',           TG_OP,
      'chunk_id',     NEW.id,
      'workspace_id', NEW.workspace_id
    )::TEXT
  );
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_chunk_ready ON clawde_chunks;
CREATE TRIGGER trg_chunk_ready
  AFTER INSERT OR UPDATE ON clawde_chunks
  FOR EACH ROW EXECUTE FUNCTION clawde_notify_chunk_ready();

-- ─────────────────────────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION clawde_notify_symbol_updated()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify(
    'clawde_symbol_updated',
    json_build_object(
      'table',        'clawde_symbols',
      'op',           TG_OP,
      'symbol_id',    NEW.id,
      'workspace_id', NEW.workspace_id
    )::TEXT
  );
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_symbol_updated ON clawde_symbols;
CREATE TRIGGER trg_symbol_updated
  AFTER INSERT OR UPDATE ON clawde_symbols
  FOR EACH ROW EXECUTE FUNCTION clawde_notify_symbol_updated();

-- ─────────────────────────────────────────────────────────────────────────────

CREATE OR REPLACE FUNCTION clawde_notify_eval_complete()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.status = 'done' AND (OLD.status IS DISTINCT FROM 'done') THEN
    PERFORM pg_notify(
      'clawde_eval_complete',
      json_build_object(
        'table',        'clawde_eval_runs',
        'op',           TG_OP,
        'eval_id',      NEW.id,
        'workspace_id', NEW.workspace_id,
        'suite',        NEW.suite
      )::TEXT
    );
  END IF;
  RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS trg_eval_complete ON clawde_eval_runs;
CREATE TRIGGER trg_eval_complete
  AFTER UPDATE ON clawde_eval_runs
  FOR EACH ROW EXECUTE FUNCTION clawde_notify_eval_complete();

-- ── Rollback ──────────────────────────────────────────────────────────────────
-- /* DOWN
-- DROP TRIGGER IF EXISTS trg_eval_complete ON clawde_eval_runs;
-- DROP FUNCTION IF EXISTS clawde_notify_eval_complete();
-- DROP TRIGGER IF EXISTS trg_symbol_updated ON clawde_symbols;
-- DROP FUNCTION IF EXISTS clawde_notify_symbol_updated();
-- DROP TRIGGER IF EXISTS trg_chunk_ready ON clawde_chunks;
-- DROP FUNCTION IF EXISTS clawde_notify_chunk_ready();
-- DO $$ BEGIN
--   IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_cron') THEN
--     PERFORM cron.unschedule('clawde-graphrag-resummary');
--     PERFORM cron.unschedule('clawde-dlq-alert');
--     PERFORM cron.unschedule('clawde-ragas-eval');
--     PERFORM cron.unschedule('clawde-reembed-sweep');
--   END IF;
-- EXCEPTION WHEN others THEN NULL;
-- END $$;
-- DROP TABLE IF EXISTS clawde_job;
-- DO $$ BEGIN
--   IF EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pgmq') THEN
--     PERFORM pgmq.drop_queue('clawde_dead_letter');
--     PERFORM pgmq.drop_queue('clawde_learn_queue');
--     PERFORM pgmq.drop_queue('clawde_analyze_queue');
--     PERFORM pgmq.drop_queue('clawde_embed_queue');
--     PERFORM pgmq.drop_queue('clawde_ingest_queue');
--   END IF;
-- EXCEPTION WHEN others THEN NULL;
-- END $$;
-- */
