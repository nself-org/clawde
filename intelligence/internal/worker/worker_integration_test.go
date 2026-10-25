// Package worker — Postgres-backed integration tests.
//
// Purpose: Exercise pgxQueueStore and Pool against a real Postgres, covering
//
//	read (pending -> running), ack (-> done), DLQ archive, retry
//	backoff and the LISTEN/NOTIFY round trip.
//
// Inputs:  CLAWDE_TEST_PG_DSN — DSN of a disposable Postgres. Tests skip when
//
//	it is unset. Each test creates its own throwaway schema and drops
//	it afterwards, so no existing table is touched.
//
// Constraints: The clawde_job columns below mirror migration 0086; update
//
//	them together.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Job status codes from migration 0086.
const (
	jobFailed  = 1
	jobPending = 2
	jobRunning = 3
	jobDone    = 4
)

const createJobTable = `
CREATE TABLE clawde_job (
    id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    queue       TEXT        NOT NULL,
    payload     JSONB       NOT NULL DEFAULT '{}',
    priority    SMALLINT    NOT NULL DEFAULT 5,
    status      SMALLINT    NOT NULL DEFAULT 2,
    retry_count SMALLINT    NOT NULL DEFAULT 0,
    visible_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// newPGTestPool connects to CLAWDE_TEST_PG_DSN with search_path pinned to a
// fresh schema holding an empty clawde_job table. Skips when no DSN is set.
func newPGTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("CLAWDE_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("Skipping Postgres integration test: CLAWDE_TEST_PG_DSN not set")
	}
	ctx := context.Background()
	schema := "clawde_worker_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect with search_path: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, createJobTable); err != nil {
		t.Fatalf("create clawde_job: %v", err)
	}
	return pool
}

func insertJob(t *testing.T, pool *pgxpool.Pool, queue string, payload string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO clawde_job (queue, payload) VALUES ($1, $2::jsonb) RETURNING id::text`,
		queue, payload).Scan(&id)
	if err != nil {
		t.Fatalf("insert job: %v", err)
	}
	return id
}

type jobRow struct {
	Queue      string
	Status     int
	RetryCount int
	VisibleAt  time.Time
}

func getJob(t *testing.T, pool *pgxpool.Pool, id string) jobRow {
	t.Helper()
	var r jobRow
	err := pool.QueryRow(context.Background(),
		`SELECT queue, status, retry_count, visible_at FROM clawde_job WHERE id = $1`, id).
		Scan(&r.Queue, &r.Status, &r.RetryCount, &r.VisibleAt)
	if err != nil {
		t.Fatalf("get job %s: %v", id, err)
	}
	return r
}

// TestPool_Integration_Postgres drives a job through read, ack, retry and DLQ
// against a live Postgres. Run locally:
//
//	CLAWDE_TEST_PG_DSN=postgres://... go test -run Integration ./internal/worker/
func TestPool_Integration_Postgres(t *testing.T) {
	// P6-E11-W2-S3-T15 (CI-masking sweep): this used to read `dsn := ""` — a
	// hardcoded empty string that could never be satisfied by any
	// environment (bug ledger: clawde-worker-test-always-skips.md).
	pool := newPGTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	store := NewPgxQueueStore(pool)

	t.Run("read marks the job running and returns its payload", func(t *testing.T) {
		id := insertJob(t, pool, QueueIngest, `{"path":"a.go"}`)
		msg, err := store.ReadMessage(ctx, QueueIngest)
		if err != nil || msg == nil {
			t.Fatalf("ReadMessage = %v, %v; want a message", msg, err)
		}
		if msg.JobID != id || msg.Queue != QueueIngest {
			t.Fatalf("got job %s on %s, want %s on %s", msg.JobID, msg.Queue, id, QueueIngest)
		}
		var payload map[string]string
		if err := json.Unmarshal(msg.Payload, &payload); err != nil || payload["path"] != "a.go" {
			t.Fatalf("payload = %s (%v), want path=a.go", msg.Payload, err)
		}
		if got := getJob(t, pool, id).Status; got != jobRunning {
			t.Fatalf("status after read = %d, want %d", got, jobRunning)
		}
		// A running job is not handed out twice, and the queue is now empty.
		if again, err := store.ReadMessage(ctx, QueueIngest); err != nil || again != nil {
			t.Fatalf("second ReadMessage = %v, %v; want nil, nil", again, err)
		}
		if d, err := store.QueueDepth(ctx, QueueIngest); err != nil || d != 0 {
			t.Fatalf("QueueDepth = %d, %v; want 0", d, err)
		}
	})

	t.Run("successful handler acks the job as done", func(t *testing.T) {
		id := insertJob(t, pool, QueueEmbed, `{}`)
		p := New(Config{Store: store, Handlers: map[string]Handler{
			QueueEmbed: func(context.Context, *Message) error { return nil },
		}})
		msg, err := store.ReadMessage(ctx, QueueEmbed)
		if err != nil || msg == nil {
			t.Fatalf("ReadMessage = %v, %v", msg, err)
		}
		p.processMessage(ctx, msg)
		if got := getJob(t, pool, id).Status; got != jobDone {
			t.Fatalf("status after successful handler = %d, want %d (done)", got, jobDone)
		}
	})

	t.Run("failing handler is retried with backoff then moved to the DLQ", func(t *testing.T) {
		id := insertJob(t, pool, QueueAnalyze, `{}`)
		fail := func(context.Context, *Message) error { return fmt.Errorf("boom") }
		p := New(Config{Store: store, Handlers: map[string]Handler{QueueAnalyze: fail}})
		p.maxRetries = 1

		msg, err := store.ReadMessage(ctx, QueueAnalyze)
		if err != nil || msg == nil {
			t.Fatalf("ReadMessage = %v, %v", msg, err)
		}
		p.processMessage(ctx, msg)
		row := getJob(t, pool, id)
		if row.Status != jobPending || row.RetryCount != 1 || !row.VisibleAt.After(time.Now()) {
			t.Fatalf("after first failure = %+v, want pending, retry_count 1, visible_at in the future", row)
		}
		// Backoff hides the job from the next read.
		if hidden, err := store.ReadMessage(ctx, QueueAnalyze); err != nil || hidden != nil {
			t.Fatalf("ReadMessage during backoff = %v, %v; want nil, nil", hidden, err)
		}

		// Past the retry limit the job lands in the dead-letter queue.
		if _, err := pool.Exec(ctx, `UPDATE clawde_job SET visible_at = NOW() WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
		msg, err = store.ReadMessage(ctx, QueueAnalyze)
		if err != nil || msg == nil || msg.RetryCount != 1 {
			t.Fatalf("ReadMessage after backoff = %+v, %v; want retry_count 1", msg, err)
		}
		p.processMessage(ctx, msg)
		row = getJob(t, pool, id)
		if row.Queue != QueueDead || row.Status != jobFailed {
			t.Fatalf("after exhausting retries = %+v, want queue %s status %d", row, QueueDead, jobFailed)
		}
	})
}

// TestListenNotify_Integration_Postgres verifies NOTIFY received by Go subscriber <1s.
// Skipped without CLAWDE_TEST_PG_DSN.
func TestListenNotify_Integration_Postgres(t *testing.T) {
	// P6-E11-W2-S3-T15: same always-skip fix as TestPool_Integration_Postgres above.
	pool := newPGTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	listener, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire listener: %v", err)
	}
	defer listener.Release()
	if _, err := listener.Exec(ctx, "LISTEN "+backpressureChannel); err != nil {
		t.Fatalf("LISTEN: %v", err)
	}

	const payload = `{"queue":"clawde_embed_queue","active":true}`
	start := time.Now()
	if err := NewPgxQueueStore(pool).Notify(ctx, backpressureChannel, payload); err != nil {
		t.Fatalf("Notify: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, time.Second)
	defer waitCancel()
	n, err := listener.Conn().WaitForNotification(waitCtx)
	if err != nil {
		t.Fatalf("no notification within 1s: %v", err)
	}
	if n.Channel != backpressureChannel || n.Payload != payload {
		t.Fatalf("notification = %q %q, want %q %q", n.Channel, n.Payload, backpressureChannel, payload)
	}
	if elapsed := time.Since(start); elapsed >= time.Second {
		t.Fatalf("notification took %s, want <1s", elapsed)
	}
}
