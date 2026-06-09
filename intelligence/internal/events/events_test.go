// Package events — unit tests for LISTEN/NOTIFY subscriber.
package events

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// ── Stub notifier ─────────────────────────────────────────────────────────────

type stubNotifier struct {
	queue chan *Notification
}

func newStubNotifier() *stubNotifier {
	return &stubNotifier{queue: make(chan *Notification, 64)}
}

func (s *stubNotifier) Listen(_ context.Context, _ string) error { return nil }

func (s *stubNotifier) WaitForNotification(ctx context.Context) (*Notification, error) {
	select {
	case n := <-s.queue:
		return n, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// ── Tests ─────────────────────────────────────────────────────────────────────

// TestSubscriber_DispatchToHandler verifies a notification reaches its handler.
func TestSubscriber_DispatchToHandler(t *testing.T) {
	notifier := newStubNotifier()
	sub := New(notifier, nil)

	var called atomic.Int32
	sub.On(ChannelChunkReady, func(_ context.Context, n *Notification) {
		if n.Channel != ChannelChunkReady {
			t.Errorf("unexpected channel: %q", n.Channel)
		}
		called.Add(1)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = sub.Start(ctx)
	}()

	// Emit one notification and wait for dispatch.
	notifier.queue <- &Notification{
		Channel: ChannelChunkReady,
		Payload: `{"chunk_id":"abc","workspace_id":"ws-1"}`,
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if called.Load() > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if called.Load() != 1 {
		t.Fatalf("expected 1 handler call, got %d", called.Load())
	}
}

// TestNotification_ParsedField verifies JSON field extraction.
func TestNotification_ParsedField(t *testing.T) {
	n := &Notification{
		Channel: ChannelEvalComplete,
		Payload: `{"eval_id":"eval-42","workspace_id":"ws-xyz","suite":"ragas"}`,
	}
	if got := n.ParsedField("eval_id"); got != "eval-42" {
		t.Errorf("ParsedField(eval_id): got %q, want %q", got, "eval-42")
	}
	if got := n.ParsedField("suite"); got != "ragas" {
		t.Errorf("ParsedField(suite): got %q, want %q", got, "ragas")
	}
	if got := n.ParsedField("absent"); got != "" {
		t.Errorf("ParsedField(absent): got %q, want empty", got)
	}
}

// TestSubscriber_MultipleChannels verifies handlers for different channels are independent.
func TestSubscriber_MultipleChannels(t *testing.T) {
	notifier := newStubNotifier()
	sub := New(notifier, nil)

	var chunkCalls, symbolCalls atomic.Int32
	sub.On(ChannelChunkReady, func(_ context.Context, _ *Notification) { chunkCalls.Add(1) })
	sub.On(ChannelSymbolUpdated, func(_ context.Context, _ *Notification) { symbolCalls.Add(1) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = sub.Start(ctx) }()

	notifier.queue <- &Notification{Channel: ChannelChunkReady, Payload: `{}`}
	notifier.queue <- &Notification{Channel: ChannelSymbolUpdated, Payload: `{}`}
	notifier.queue <- &Notification{Channel: ChannelChunkReady, Payload: `{}`}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if chunkCalls.Load() >= 2 && symbolCalls.Load() >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()

	if chunkCalls.Load() != 2 {
		t.Errorf("chunk calls: got %d, want 2", chunkCalls.Load())
	}
	if symbolCalls.Load() != 1 {
		t.Errorf("symbol calls: got %d, want 1", symbolCalls.Load())
	}
}

// TestListenNotify_Integration_Postgres is a placeholder for live PG tests.
// Requires CLAWDE_TEST_PG_DSN. Skipped in CI.
func TestListenNotify_Integration_Postgres(t *testing.T) {
	t.Skip("Skipping live LISTEN/NOTIFY test: CLAWDE_TEST_PG_DSN not set")
}
