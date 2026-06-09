// Package gateway — Gemini GCP project pool selector.
//
// Purpose: Pick the lowest-utilization GCP project from all gemini entries in
//          the registry for a given lane, then return the corresponding
//          ProviderEntry. Utilization is tracked in Redis using INCR + TTL
//          (window = rate_limit.window_seconds). Logs a warning at 80% quota
//          pressure.
// Inputs:  *Registry, Lane, redis.Client.
// Outputs: *ProviderEntry (best candidate); error on total failure.
// Constraints: Redis key format: "gpool:{project_id}:{window_start}".
//              Falls back gracefully when Redis is unavailable (returns primary).
// SPORT: REGISTRY-FUNCTIONS.md → GeminiPoolPick.
package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	geminiQuotaWarnThreshold = 0.80 // log warning when utilization ≥ 80% of rpm
)

// GeminiPoolPick selects the gemini ProviderEntry with the lowest current
// request utilization for the given lane. All gemini entries for the lane are
// considered; non-gemini entries are ignored.
//
// When Redis is unavailable, the first gemini entry is returned without error
// so the gateway degrades gracefully.
func GeminiPoolPick(ctx context.Context, reg *Registry, lane Lane, rdb redis.Cmdable) (*ProviderEntry, error) {
	if reg == nil {
		return nil, fmt.Errorf("gemini_pool: registry is nil")
	}

	entries, err := LaneResolve(reg, lane)
	if err != nil {
		return nil, fmt.Errorf("gemini_pool: %w", err)
	}

	// Collect gemini entries only.
	var gemini []ProviderEntry
	for _, e := range entries {
		if e.Provider == "gemini" {
			gemini = append(gemini, e)
		}
	}
	if len(gemini) == 0 {
		return nil, fmt.Errorf("gemini_pool: no gemini entries for lane %q", lane)
	}

	if rdb == nil || len(gemini) == 1 {
		// No Redis or single entry — return without pool selection.
		return &gemini[0], nil
	}

	return pickLowestUtilization(ctx, gemini, rdb)
}

// pickLowestUtilization queries Redis for each project's current window count
// and returns the entry with the smallest count relative to its rpm limit.
func pickLowestUtilization(ctx context.Context, entries []ProviderEntry, rdb redis.Cmdable) (*ProviderEntry, error) {
	type scored struct {
		entry    ProviderEntry
		pressure float64 // requests_this_window / rpm (0–1+)
	}

	windowKey := func(projectID string, windowSeconds int) string {
		if windowSeconds <= 0 {
			windowSeconds = 60
		}
		slot := time.Now().Unix() / int64(windowSeconds)
		return fmt.Sprintf("gpool:%s:%d", projectID, slot)
	}

	best := scored{entry: entries[0], pressure: 2.0} // sentinel high pressure

	for _, e := range entries {
		ws := e.RateLimit.WindowSeconds
		key := windowKey(e.ProjectID, ws)

		count, err := rdb.Get(ctx, key).Int64()
		if err == redis.Nil {
			count = 0
		} else if err != nil {
			// Redis error — skip this entry, keep going.
			slog.WarnContext(ctx, "gemini_pool: redis GET failed, skipping entry",
				"project_id", e.ProjectID,
				"err", err)
			continue
		}

		rpm := int64(e.RateLimit.RPM)
		var pressure float64
		if rpm > 0 {
			pressure = float64(count) / float64(rpm)
		}

		if pressure >= geminiQuotaWarnThreshold {
			slog.WarnContext(ctx, "gemini_pool: quota pressure high",
				"project_id", e.ProjectID,
				"lane", string(e.Lane),
				"pressure_pct", fmt.Sprintf("%.0f%%", pressure*100),
			)
		}

		if pressure < best.pressure {
			best = scored{entry: e, pressure: pressure}
		}
	}

	return &best.entry, nil
}

// IncrGeminiWindow atomically increments the request counter for the given
// ProviderEntry's GCP project in the current rate-limit window. Should be
// called once per request just before dispatching.
func IncrGeminiWindow(ctx context.Context, rdb redis.Cmdable, e ProviderEntry) error {
	if rdb == nil {
		return nil
	}
	ws := e.RateLimit.WindowSeconds
	if ws <= 0 {
		ws = 60
	}
	slot := time.Now().Unix() / int64(ws)
	key := fmt.Sprintf("gpool:%s:%d", e.ProjectID, slot)

	pipe := rdb.(redis.Pipeliner)
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, time.Duration(ws*2)*time.Second)
	_, err := pipe.Exec(ctx)
	return err
}
