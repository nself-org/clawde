// Package events — LISTEN/NOTIFY subscriber for Postgres channels.
//
// Purpose: Subscribe to Postgres LISTEN channels and dispatch events to
//          registered Go handlers. Channels: clawde_chunk_ready,
//          clawde_symbol_updated, clawde_eval_complete, clawde_quota_hit.
// Inputs:  Notifier interface (backed by pgx conn for real use; stub for tests).
// Outputs: Calls registered EventHandler on each notification.
// Constraints: No external broker (Postgres-native only). File ≤300 lines.
//
// SPORT: REGISTRY-FUNCTIONS.md → events.Subscriber.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
)

// ── Channels ──────────────────────────────────────────────────────────────────

const (
	ChannelChunkReady     = "clawde_chunk_ready"
	ChannelSymbolUpdated  = "clawde_symbol_updated"
	ChannelEvalComplete   = "clawde_eval_complete"
	ChannelQuotaHit       = "clawde_quota_hit"
)

// AllChannels is the canonical set of channels to LISTEN on.
var AllChannels = []string{
	ChannelChunkReady,
	ChannelSymbolUpdated,
	ChannelEvalComplete,
	ChannelQuotaHit,
}

// ── Notification payload ──────────────────────────────────────────────────────

// Notification is the decoded payload from a Postgres NOTIFY.
type Notification struct {
	Channel     string
	Payload     string
	Parsed      map[string]any // lazily parsed JSON payload
}

// ParsedField returns a string field from the JSON payload.
// Returns "" if the field is absent or not a string.
func (n *Notification) ParsedField(key string) string {
	if n.Parsed == nil {
		n.Parsed = make(map[string]any)
		_ = json.Unmarshal([]byte(n.Payload), &n.Parsed)
	}
	v, _ := n.Parsed[key].(string)
	return v
}

// ── Notifier interface (seam for testing) ─────────────────────────────────────

// Notifier provides the LISTEN/NOTIFY plumbing. The real implementation
// wraps a pgx connection; tests inject a stub.
type Notifier interface {
	// Listen registers interest in the given channel.
	Listen(ctx context.Context, channel string) error

	// WaitForNotification blocks until a notification arrives or ctx is cancelled.
	WaitForNotification(ctx context.Context) (*Notification, error)
}

// ── Handler ──────────────────────────────────────────────────────────────────

// EventHandler processes a single Postgres notification.
type EventHandler func(ctx context.Context, n *Notification)

// ── Subscriber ────────────────────────────────────────────────────────────────

// Subscriber holds channel → handler registrations and drives the LISTEN loop.
type Subscriber struct {
	notifier Notifier
	handlers map[string][]EventHandler
	mu       sync.RWMutex
	logger   *slog.Logger
}

// New creates a Subscriber. Register handlers before calling Start.
func New(notifier Notifier, logger *slog.Logger) *Subscriber {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}
	return &Subscriber{
		notifier: notifier,
		handlers: make(map[string][]EventHandler),
		logger:   logger,
	}
}

// On registers handler for the given channel. Safe to call concurrently.
func (s *Subscriber) On(channel string, handler EventHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[channel] = append(s.handlers[channel], handler)
}

// Start calls LISTEN for all channels registered in AllChannels, then
// processes notifications until ctx is cancelled.
func (s *Subscriber) Start(ctx context.Context) error {
	for _, ch := range AllChannels {
		if err := s.notifier.Listen(ctx, ch); err != nil {
			return err
		}
	}
	return s.loop(ctx)
}

// loop blocks, dispatching notifications until ctx is done.
func (s *Subscriber) loop(ctx context.Context) error {
	for {
		n, err := s.notifier.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // normal shutdown
			}
			s.logger.Error("wait for notification error", "err", err)
			continue
		}
		s.dispatch(ctx, n)
	}
}

func (s *Subscriber) dispatch(ctx context.Context, n *Notification) {
	s.mu.RLock()
	handlers := s.handlers[n.Channel]
	s.mu.RUnlock()
	if len(handlers) == 0 {
		return
	}
	for _, h := range handlers {
		h(ctx, n)
	}
}
