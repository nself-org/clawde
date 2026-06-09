// Package gateway — sliding-window rate limiter backed by Redis.
//
// Purpose: Enforce per-user, per-lane rate limits defined in model_registry.yaml.
//          Uses Redis ZADD sliding-window (score = unix timestamp in ms, member =
//          request UUID). Limits come from the primary ProviderEntry's RateLimit
//          block — no hard-coded numbers here.
// Inputs:  redis.Cmdable, ProviderEntry (with RateLimit), LaneRequest.
// Outputs: nil (allowed) or *GatewayError{Code: "rate_limit"} (denied).
// Constraints: Key format: "rate:{user_id}:{lane}". TTL = window_seconds * 2.
//              RPM=0 means "no limit".
// SPORT: REGISTRY-FUNCTIONS.md → EnforceRateLimit.
package gateway

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// EnforceRateLimit checks whether the user identified by req.RequestID has
// exceeded the RPM limit for the lane. It uses a Redis sorted-set sliding
// window keyed by "rate:{user_id}:{lane}".
//
// Returns nil if the request is allowed, or *GatewayError{Code:"rate_limit"}
// if the limit is exceeded.
//
// If rdb is nil or if the primary entry has RPM=0, the call is always allowed.
func EnforceRateLimit(ctx context.Context, rdb redis.Cmdable, entry ProviderEntry, req LaneRequest) error {
	if rdb == nil {
		return nil
	}

	rpm := entry.RateLimit.RPM
	if rpm == 0 {
		return nil // unlimited
	}

	ws := entry.RateLimit.WindowSeconds
	if ws <= 0 {
		ws = 60
	}

	userID := req.RequestID
	if userID == "" {
		userID = req.WorkspaceID // fall back to workspace scope
	}
	if userID == "" {
		return nil // no identity — cannot rate-limit, allow
	}

	key := fmt.Sprintf("rate:%s:%s", userID, string(req.Lane))
	now := time.Now()
	nowMs := now.UnixMilli()
	windowMs := int64(ws) * 1000
	cutoff := nowMs - windowMs

	// Use a pipeline: remove stale members, count current, add new member.
	pipe := rdb.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "-inf", strconv.FormatInt(cutoff, 10))
	countCmd := pipe.ZCard(ctx, key)
	pipe.ZAdd(ctx, key, redis.Z{Score: float64(nowMs), Member: strconv.FormatInt(nowMs, 10)})
	pipe.Expire(ctx, key, time.Duration(ws*2)*time.Second)
	if _, err := pipe.Exec(ctx); err != nil {
		// Redis failure — fail open (allow the request, log in caller if desired).
		return nil
	}

	count := countCmd.Val() // count BEFORE adding current request
	if count >= int64(rpm) {
		return &GatewayError{
			Lane:     req.Lane,
			Provider: entry.Provider,
			Code:     "rate_limit",
			Cause:    fmt.Errorf("rpm limit %d exceeded for user %s on lane %s", rpm, userID, req.Lane),
		}
	}
	return nil
}
