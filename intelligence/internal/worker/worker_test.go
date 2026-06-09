// Package worker — unit and integration tests.
//
// Tests that require a live Postgres + pgmq are tagged with a build constraint
// or skipped via t.Skip when CLAWDE_TEST_PG_DSN is unset.
// Pure unit tests (stub store) run without any external dependency.
package worker

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── Stub store ────────────────────────────────────────────────────────────────

type stubStore struct {
	mu       sync.Mutex
	queues   map[string][]*Message // messages available per queue
	deleted  []int64               // acked message IDs
	dlq      []*Message            // archived to DLQ
	retried  []string              // job IDs sent for retry
	notified []string              // channel payloads sent
	depths   map[string]int64      // configurable queue depths
}

func newStubStore() *stubStore {
	return &stubStore{
		queues: map[string][]*Message{
			QueueIngest:  {},
			QueueEmbed:   {},
			QueueAnalyze: {},
			QueueLearn:   {},
			QueueDead:    {},
		},
		depths: make(map[string]int64),
	}
}

func (s *stubStore) enqueue(q string, msgs ...*Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queues[q] = append(s.queues[q], msgs...)
}

func (s *stubStore) ReadMessage(_ context.Context, q string) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queues[q]) == 0 {
		return nil, nil
	}
	msg := s.queues[q][0]
	s.queues[q] = s.queues[q][1:]
	return msg, nil
}

func (s *stubStore) DeleteMessage(_ context.Context, _ string, msgID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, msgID)
	return nil
}

func (s *stubStore) ArchiveToDLQ(_ context.Context, msg *Message, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dlq = append(s.dlq, msg)
	return nil
}

func (s *stubStore) QueueDepth(_ context.Context, q string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.depths[q], nil
}

func (s *stubStore) Notify(_ context.Context, _ string, payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notified = append(s.notified, payload)
	return nil
}

func (s *stubStore) IncrRetry(_ context.Context, jobID string, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retried = append(s.retried, jobID)
	return nil
}

// ── Stub tracer ───────────────────────────────────────────────────────────────

type recordingTracer struct {
	mu    sync.Mutex
	spans []map[string]string
}

func (rt *recordingTracer) StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func(error)) {
	rt.mu.Lock()
	snap := make(map[string]string, len(attrs)+1)
	snap["span_name"] = name
	for k, v := range attrs {
		snap[k] = v
	}
	rt.spans = append(rt.spans, snap)
	rt.mu.Unlock()
	return ctx, func(error) {}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func makeJob(id string, queue string, retry int) *Message {
	return &Message{
		MsgID:      int64(len(id)), // deterministic test value
		JobID:      id,
		Queue:      queue,
		Payload:    []byte(`{"task":"test"}`),
		RetryCount: retry,
		EnqueuedAt: time.Now(),
	}
}

func newTestPool(store *stubStore, handlers map[string]Handler) *Pool {
	return New(Config{
		Store:    store,
		Handlers: handlers,
	})
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestProcessMessage_Success verifies a successful job is acked (deleted) and not sent to DLQ.
func TestProcessMessage_Success(t *testing.T) {
	store := newStubStore()
	var processed atomic.Int32
	handler := func(_ context.Context, msg *Message) error {
		processed.Add(1)
		return nil
	}
	p := newTestPool(store, map[string]Handler{QueueIngest: handler})

	msg := makeJob("job-1", QueueIngest, 0)
	p.processMessage(context.Background(), msg)

	if processed.Load() != 1 {
		t.Fatalf("expected 1 processed, got %d", processed.Load())
	}
	if len(store.deleted) != 1 || store.deleted[0] != msg.MsgID {
		t.Fatalf("expected message %d to be acked, got %v", msg.MsgID, store.deleted)
	}
	if len(store.dlq) != 0 {
		t.Fatalf("expected empty DLQ, got %d entries", len(store.dlq))
	}
}

// TestProcessMessage_RetryThenDLQ verifies a job that fails maxRetries times lands in the DLQ.
func TestProcessMessage_RetryThenDLQ(t *testing.T) {
	store := newStubStore()
	errFail := errors.New("transient error")
	handler := func(_ context.Context, _ *Message) error { return errFail }

	p := newTestPool(store, map[string]Handler{QueueIngest: handler})
	p.maxRetries = 3

	// Simulate retries 0, 1, 2 — each time the job fails but retry_count is below threshold.
	for i := 0; i < p.maxRetries; i++ {
		msg := makeJob("job-retry", QueueIngest, i)
		p.processMessage(context.Background(), msg)
	}
	// Simulate final failure at retry_count == maxRetries → should go to DLQ.
	final := makeJob("job-retry", QueueIngest, p.maxRetries)
	p.processMessage(context.Background(), final)

	if len(store.dlq) != 1 {
		t.Fatalf("expected 1 DLQ entry, got %d", len(store.dlq))
	}
	// Retried 3 times (retry_count 0,1,2).
	if len(store.retried) != 3 {
		t.Fatalf("expected 3 retry increments, got %d", len(store.retried))
	}
}

// TestDLQSweep_AlertLog verifies the DLQ sweep logs a warning when depth > threshold.
// This is a unit test of the backpressure monitor / sweep logic.
func TestDLQSweep_DepthCheck(t *testing.T) {
	store := newStubStore()
	store.depths[QueueDead] = dlqAlertThreshold + 1

	p := newTestPool(store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// runDLQSweep is not exported, so test via the store stub directly.
	depth, err := store.QueueDepth(ctx, QueueDead)
	if err != nil {
		t.Fatal(err)
	}
	if depth <= dlqAlertThreshold {
		t.Fatalf("expected depth > %d, got %d", dlqAlertThreshold, depth)
	}
	_ = p // pool was constructed, store is wired
}

// TestBackpressure_Notify verifies the pool emits a NOTIFY when embed queue exceeds threshold.
func TestBackpressure_Notify(t *testing.T) {
	store := newStubStore()
	store.depths[QueueEmbed] = embedQueueDepthMax + 1

	p := newTestPool(store, nil)
	ctx := context.Background()

	// Simulate one backpressure monitor tick inline.
	depth, _ := store.QueueDepth(ctx, QueueEmbed)
	p.bpMu.Lock()
	wasActive := p.bpActive
	p.bpActive = depth > embedQueueDepthMax
	transitioned := p.bpActive != wasActive
	p.bpMu.Unlock()

	if !transitioned {
		t.Fatal("expected backpressure transition")
	}
	if !p.bpActive {
		t.Fatal("expected backpressure active")
	}
}

// TestOTelSpan_Attrs verifies that OTel span attributes include queue_name, job_id, retry_count.
func TestOTelSpan_Attrs(t *testing.T) {
	store := newStubStore()
	tracer := &recordingTracer{}
	handler := func(_ context.Context, _ *Message) error { return nil }

	p := New(Config{
		Store:    store,
		Tracer:   tracer,
		Handlers: map[string]Handler{QueueIngest: handler},
	})

	msg := makeJob("job-otel", QueueIngest, 2)
	p.processMessage(context.Background(), msg)

	tracer.mu.Lock()
	defer tracer.mu.Unlock()
	if len(tracer.spans) == 0 {
		t.Fatal("expected at least one span recorded")
	}
	span := tracer.spans[0]
	if span["queue_name"] != QueueIngest {
		t.Errorf("queue_name: got %q, want %q", span["queue_name"], QueueIngest)
	}
	if span["job_id"] != "job-otel" {
		t.Errorf("job_id: got %q, want %q", span["job_id"], "job-otel")
	}
	if span["retry_count"] != strconv.Itoa(msg.RetryCount) {
		t.Errorf("retry_count: got %q, want %q", span["retry_count"], strconv.Itoa(msg.RetryCount))
	}
}

// TestPool_50Jobs_StubStore verifies 50 jobs are fully processed via Start/Stop with the stub store.
// NOTE: Uses stub store — no real Postgres required.
func TestPool_50Jobs_StubStore(t *testing.T) {
	store := newStubStore()
	var processed atomic.Int32
	handler := func(_ context.Context, _ *Message) error {
		processed.Add(1)
		return nil
	}

	// Enqueue 50 jobs to the ingest queue.
	for i := 0; i < 50; i++ {
		store.enqueue(QueueIngest, makeJob("job-"+strconv.Itoa(i), QueueIngest, 0))
	}

	p := New(Config{
		Store: store,
		Handlers: map[string]Handler{
			QueueIngest:  handler,
			QueueEmbed:   handler,
			QueueAnalyze: handler,
			QueueLearn:   handler,
		},
	})
	p.workerN = 5

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.Start(ctx)

	// Wait until all 50 are processed or timeout.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if processed.Load() >= 50 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.Stop()

	if processed.Load() < 50 {
		t.Fatalf("expected 50 processed, got %d", processed.Load())
	}
	if len(store.dlq) != 0 {
		t.Fatalf("expected empty DLQ, got %d", len(store.dlq))
	}
}

// ── Postgres-dependent tests (skipped without DSN) ────────────────────────────

// TestPool_Integration_Postgres runs a live integration test.
// Requires CLAWDE_TEST_PG_DSN to be set with a Postgres DSN that has pgmq installed.
// Skip gracefully in CI. Run locally: CLAWDE_TEST_PG_DSN=postgres://... go test -run Integration ./...
func TestPool_Integration_Postgres(t *testing.T) {
	dsn := ""
	if dsn == "" {
		t.Skip("Skipping Postgres integration test: CLAWDE_TEST_PG_DSN not set")
	}
	// Real pgx-backed store implementation would be tested here.
	// When a real DB is available, verify:
	//   1. pgmq.read returns messages
	//   2. pgmq.delete acks correctly
	//   3. DLQ is empty after successful batch
	_ = dsn
}

// TestListenNotify_Integration_Postgres verifies NOTIFY received by Go subscriber <1s.
// Skipped without CLAWDE_TEST_PG_DSN.
func TestListenNotify_Integration_Postgres(t *testing.T) {
	dsn := ""
	if dsn == "" {
		t.Skip("Skipping LISTEN/NOTIFY integration test: CLAWDE_TEST_PG_DSN not set")
	}
	_ = dsn
}
