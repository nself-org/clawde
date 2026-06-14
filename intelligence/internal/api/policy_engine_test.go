// Package api — unit tests for real TrustRegistry and SupplyChainCheck impls.
//
// Purpose: Verify TrustRegistry.Check behaves fail-closed on DB error,
//          correctly identifies present/absent workspaces via stub pgx,
//          and that SupplyChainCheck allows exactly the 4 known HTTP paths
//          while denying unknown ones.
//
// Tests use a stub pgx pool seam (stubDB) — no real Postgres required.
//
// SPORT: REGISTRY-FUNCTIONS.md — TrustRegistryCheck, SupplyChainCheck.
package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- stub DB seam ----

// TrustRegistryWithFunc creates a TrustRegistry that uses fn instead of pgx.
// Used only in tests to inject controlled DB responses without a real pool.
func TrustRegistryWithFunc(fn trustDBFunc) *TrustRegistry {
	return &TrustRegistry{pool: nil, dbFn: fn}
}

// ---- TrustRegistry.Check unit tests ----

func TestTrustRegistry_EmptyWorkspace_Error(t *testing.T) {
	tr := NewTrustRegistry(nil) // dev mode
	td, err := tr.Check(context.Background(), "")
	assert.Error(t, err)
	assert.False(t, td.Trusted)
}

func TestTrustRegistry_DevMode_NilPool_Trusted(t *testing.T) {
	tr := NewTrustRegistry(nil)
	td, err := tr.Check(context.Background(), "ws-devmode")
	require.NoError(t, err)
	assert.True(t, td.Trusted, "nil pool dev mode must return Trusted:true")
}

func TestTrustRegistry_DBMiss_Denied(t *testing.T) {
	// Workspace absent from registry → Trusted:false, no error.
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		return false, nil
	})
	td, err := tr.Check(context.Background(), "ws-unknown")
	require.NoError(t, err)
	assert.False(t, td.Trusted, "workspace absent from registry must be denied")
}

func TestTrustRegistry_DBHit_Trusted(t *testing.T) {
	// Workspace present in registry → Trusted:true.
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		return true, nil
	})
	td, err := tr.Check(context.Background(), "ws-registered")
	require.NoError(t, err)
	assert.True(t, td.Trusted, "registered workspace must be trusted")
}

func TestTrustRegistry_DBError_FailClosed(t *testing.T) {
	// DB error → fail-closed: Trusted:false + non-nil error.
	dbErr := errors.New("connection refused")
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		return false, dbErr
	})
	td, err := tr.Check(context.Background(), "ws-abc")
	assert.Error(t, err, "DB error must propagate as non-nil error")
	assert.False(t, td.Trusted, "DB error must return Trusted:false (fail-closed)")
}

func TestTrustRegistry_CacheTTL_WallClock(t *testing.T) {
	// Positive result is cached; a subsequent DB call should NOT be made within TTL.
	calls := 0
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		calls++
		return true, nil
	})

	// First call — hits DB.
	td, err := tr.Check(context.Background(), "ws-cached")
	require.NoError(t, err)
	assert.True(t, td.Trusted)
	assert.Equal(t, 1, calls, "first call must hit DB")

	// Second call within TTL — should hit cache, not DB.
	td, err = tr.Check(context.Background(), "ws-cached")
	require.NoError(t, err)
	assert.True(t, td.Trusted)
	assert.Equal(t, 1, calls, "second call within TTL must use cache (DB call count unchanged)")
}

func TestTrustRegistry_CacheTTL_Expired(t *testing.T) {
	// Cache entry that has already expired should trigger a fresh DB call.
	calls := 0
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		calls++
		return true, nil
	})

	// Pre-seed the cache with an already-expired entry.
	tr.cache.Store("ws-expired", trustCacheEntry{
		trusted:   true,
		expiresAt: time.Now().Add(-1 * time.Second), // expired
	})

	td, err := tr.Check(context.Background(), "ws-expired")
	require.NoError(t, err)
	assert.True(t, td.Trusted)
	assert.Equal(t, 1, calls, "expired cache entry must trigger a fresh DB call")
}

func TestTrustRegistry_NegativeResult_NotCached(t *testing.T) {
	// Negative (not found) results must NOT be cached so new registrations take effect.
	calls := 0
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		calls++
		return false, nil
	})

	tr.Check(context.Background(), "ws-absent") //nolint:errcheck
	tr.Check(context.Background(), "ws-absent") //nolint:errcheck
	assert.Equal(t, 2, calls, "negative results must not be cached — each call hits DB")
}

// ---- SupplyChainCheck unit tests ----

func TestSupplyChainCheck_AllowedPaths(t *testing.T) {
	allowed := []string{
		"/v1/retrieve",
		"/v1/complete",
		"/v1/embed",
		"/v1/rerank",
	}
	for _, path := range allowed {
		t.Run(path, func(t *testing.T) {
			dec, err := SupplyChainCheck(context.Background(), path)
			require.NoError(t, err)
			assert.True(t, dec.Allowed, "expected path %q to be allowed", path)
		})
	}
}

func TestSupplyChainCheck_UnknownPath_Denied(t *testing.T) {
	unknown := []string{
		"/unknown/Method",
		"/v1/admin",
		"/v2/retrieve",
		"/gateway.v1.GatewayService/Complete",
		"",
	}
	for _, path := range unknown {
		t.Run(path, func(t *testing.T) {
			dec, err := SupplyChainCheck(context.Background(), path)
			assert.False(t, dec.Allowed, "expected path %q to be denied", path)
			_ = err // empty path returns error; unknown paths return nil error + denied
		})
	}
}

// ---- concurrent safety ----

func TestTrustRegistry_ConcurrentAccess_NoRace(t *testing.T) {
	// Verifies sync.Map usage is race-free under concurrent load.
	tr := TrustRegistryWithFunc(func(_ context.Context, _ string) (bool, error) {
		return true, nil
	})
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr.Check(context.Background(), "ws-concurrent") //nolint:errcheck
		}()
	}
	wg.Wait()
}
