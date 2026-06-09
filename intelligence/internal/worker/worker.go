// Package worker — Postgres-native job worker pool.
//
// Purpose: Read jobs from pgmq queues, dispatch them to typed handlers,
//          acknowledge on success or archive to DLQ after max retries.
//          No external broker dependency (no Redis/Kafka/RabbitMQ).
// Inputs:  DB pool (pgx), env vars CLAWDE_WORKER_N and CLAWDE_JOB_MAX_RETRIES.
// Outputs: Processed jobs deleted from queue; failed jobs moved to DLQ.
// Constraints:
//   - File ≤500 lines.
//   - Backpressure: pause ingest when embed queue depth >10K;
//     emit clawde_backpressure NOTIFY on transition.
//   - OTel span per job: queue_name, job_id, processing_time_ms, retry_count.
//
// SPORT: REGISTRY-SERVICES.md → clawde-worker-pool.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// ── Constants & defaults ──────────────────────────────────────────────────────

const (
	defaultWorkerN      = 10
	defaultMaxRetries   = 3
	embedQueueDepthMax  = 10_000 // backpressure threshold
	dlqAlertThreshold   = 100
	pollInterval        = 500 * time.Millisecond
	dlqSweepInterval    = 5 * time.Minute
	backpressureChannel = "clawde_backpressure"
)

// Queue names must match migration 0086.
const (
	QueueIngest   = "clawde_ingest_queue"
	QueueEmbed    = "clawde_embed_queue"
	QueueAnalyze  = "clawde_analyze_queue"
	QueueLearn    = "clawde_learn_queue"
	QueueDead     = "clawde_dead_letter"
)

// ── Interfaces (seam for testing without a real DB) ───────────────────────────

// QueueStore is the storage interface the worker depends on.
// The real implementation wraps pgmq via pgx; tests inject a stub.
type QueueStore interface {
	// ReadMessage reads the next available message from the named queue.
	// Returns (nil, nil) when the queue is empty.
	ReadMessage(ctx context.Context, queue string) (*Message, error)

	// DeleteMessage acknowledges successful processing.
	DeleteMessage(ctx context.Context, queue string, msgID int64) error

	// ArchiveToDLQ moves a failed message to the dead-letter queue.
	ArchiveToDLQ(ctx context.Context, msg *Message, reason string) error

	// QueueDepth returns the approximate number of pending messages.
	QueueDepth(ctx context.Context, queue string) (int64, error)

	// Notify sends a Postgres LISTEN/NOTIFY notification.
	Notify(ctx context.Context, channel, payload string) error

	// IncrRetry increments the retry_count in clawde_job and sets the next visible_at.
	IncrRetry(ctx context.Context, jobID string, backoff time.Duration) error
}

// Tracer wraps OTel span creation. Injected for testability.
type Tracer interface {
	StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func(error))
}

// ── Job message ────────────────────────────────────────────────────────────────

// Message represents a pgmq message read from a queue.
type Message struct {
	MsgID      int64           // pgmq internal message id
	JobID      string          // clawde_job.id (UUID string from payload)
	Queue      string          // source queue name
	Payload    json.RawMessage // raw JSONB from pgmq
	RetryCount int             // current retry_count from clawde_job
	EnqueuedAt time.Time
}

// ── Handler ────────────────────────────────────────────────────────────────────

// Handler processes a single job message. Returning a non-nil error triggers retry.
type Handler func(ctx context.Context, msg *Message) error

// ── Pool ──────────────────────────────────────────────────────────────────────

// Pool is the worker pool. Call Start to begin processing; Stop to drain and halt.
type Pool struct {
	store      QueueStore
	tracer     Tracer
	handlers   map[string]Handler // queue → handler
	workerN    int
	maxRetries int
	logger     *slog.Logger

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// backpressure state
	bpMu      sync.Mutex
	bpActive  bool
}

// Config holds Pool construction options.
type Config struct {
	Store      QueueStore
	Tracer     Tracer              // nil → no-op tracer
	Handlers   map[string]Handler
	Logger     *slog.Logger        // nil → discard
}

// New creates a Pool from environment + config.
// CLAWDE_WORKER_N and CLAWDE_JOB_MAX_RETRIES override defaults.
func New(cfg Config) *Pool {
	n := defaultWorkerN
	if v := os.Getenv("CLAWDE_WORKER_N"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			n = parsed
		}
	}
	maxR := defaultMaxRetries
	if v := os.Getenv("CLAWDE_JOB_MAX_RETRIES"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			maxR = parsed
		}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	tracer := cfg.Tracer
	if tracer == nil {
		tracer = noopTracer{}
	}
	return &Pool{
		store:      cfg.Store,
		tracer:     tracer,
		handlers:   cfg.Handlers,
		workerN:    n,
		maxRetries: maxR,
		logger:     logger,
	}
}

// Start launches N goroutines per queue plus the DLQ sweep goroutine.
// ctx should be the application root context.
func (p *Pool) Start(ctx context.Context) {
	ctx, p.cancel = context.WithCancel(ctx)
	queues := []string{QueueIngest, QueueEmbed, QueueAnalyze, QueueLearn}
	for _, q := range queues {
		for i := 0; i < p.workerN; i++ {
			p.wg.Add(1)
			go p.runWorker(ctx, q)
		}
	}
	p.wg.Add(1)
	go p.runDLQSweep(ctx)
	p.wg.Add(1)
	go p.runBackpressureMonitor(ctx)
}

// Stop cancels the pool context and waits for all goroutines to exit.
func (p *Pool) Stop() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

// ── Worker goroutine ──────────────────────────────────────────────────────────

func (p *Pool) runWorker(ctx context.Context, queue string) {
	defer p.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Backpressure: skip ingest if embed queue is saturated.
		if queue == QueueIngest && p.isBackpressureActive() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
				continue
			}
		}

		msg, err := p.store.ReadMessage(ctx, queue)
		if err != nil {
			p.logger.Error("read message failed", "queue", queue, "err", err)
			time.Sleep(pollInterval)
			continue
		}
		if msg == nil {
			// Queue empty — back-off briefly.
			select {
			case <-ctx.Done():
				return
			case <-time.After(pollInterval):
				continue
			}
		}

		p.processMessage(ctx, msg)
	}
}

func (p *Pool) processMessage(ctx context.Context, msg *Message) {
	start := time.Now()
	spanAttrs := map[string]string{
		"queue_name":  msg.Queue,
		"job_id":      msg.JobID,
		"retry_count": strconv.Itoa(msg.RetryCount),
	}
	spanCtx, end := p.tracer.StartSpan(ctx, "worker.process_job", spanAttrs)

	handler, ok := p.handlers[msg.Queue]
	if !ok {
		p.logger.Warn("no handler registered", "queue", msg.Queue)
		end(nil)
		return
	}

	err := handler(spanCtx, msg)
	elapsed := time.Since(start).Milliseconds()
	spanAttrs["processing_time_ms"] = strconv.FormatInt(elapsed, 10)

	if err == nil {
		if delErr := p.store.DeleteMessage(ctx, msg.Queue, msg.MsgID); delErr != nil {
			p.logger.Error("ack failed", "queue", msg.Queue, "job", msg.JobID, "err", delErr)
		} else {
			p.logger.Info("job done", "queue", msg.Queue, "job", msg.JobID, "ms", elapsed)
		}
		end(nil)
		return
	}

	// Error path: retry or DLQ.
	p.logger.Warn("job failed", "queue", msg.Queue, "job", msg.JobID, "retry", msg.RetryCount, "err", err)
	if msg.RetryCount >= p.maxRetries {
		if dlqErr := p.store.ArchiveToDLQ(ctx, msg, err.Error()); dlqErr != nil {
			p.logger.Error("dlq archive failed", "job", msg.JobID, "err", dlqErr)
		}
	} else {
		backoff := time.Duration(1<<uint(msg.RetryCount)) * time.Second
		if incrErr := p.store.IncrRetry(ctx, msg.JobID, backoff); incrErr != nil {
			p.logger.Error("retry incr failed", "job", msg.JobID, "err", incrErr)
		}
	}
	end(err)
}

// ── DLQ sweep goroutine ───────────────────────────────────────────────────────

func (p *Pool) runDLQSweep(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(dlqSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			depth, err := p.store.QueueDepth(ctx, QueueDead)
			if err != nil {
				p.logger.Error("dlq depth check failed", "err", err)
				continue
			}
			if depth > dlqAlertThreshold {
				p.logger.Warn("DLQ depth exceeds threshold",
					"queue", QueueDead, "depth", depth, "threshold", dlqAlertThreshold)
			}
		}
	}
}

// ── Backpressure monitor ──────────────────────────────────────────────────────

func (p *Pool) runBackpressureMonitor(ctx context.Context) {
	defer p.wg.Done()
	ticker := time.NewTicker(pollInterval * 10)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			depth, err := p.store.QueueDepth(ctx, QueueEmbed)
			if err != nil {
				continue
			}
			p.bpMu.Lock()
			wasActive := p.bpActive
			p.bpActive = depth > embedQueueDepthMax
			transitioned := p.bpActive != wasActive
			active := p.bpActive
			p.bpMu.Unlock()

			if transitioned {
				state := "active"
				if !active {
					state = "cleared"
				}
				payload := fmt.Sprintf(`{"state":"%s","embed_depth":%d}`, state, depth)
				if err := p.store.Notify(ctx, backpressureChannel, payload); err != nil {
					p.logger.Error("backpressure notify failed", "err", err)
				}
				p.logger.Info("backpressure", "state", state, "embed_depth", depth)
			}
		}
	}
}

func (p *Pool) isBackpressureActive() bool {
	p.bpMu.Lock()
	defer p.bpMu.Unlock()
	return p.bpActive
}

// ── no-op tracer ──────────────────────────────────────────────────────────────

type noopTracer struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string, _ map[string]string) (context.Context, func(error)) {
	return ctx, func(error) {}
}
