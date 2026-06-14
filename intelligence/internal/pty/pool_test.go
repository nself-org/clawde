// pool_test.go — Tests for the PTY pool.
//
// Purpose: Verify Pool lifecycle: Start pre-warms slots, Acquire/Release work
//          correctly, idle timeout reaps and recreates slots, Stop drains cleanly,
//          and the pool is race-free under -race.
//
// Constraints: No network. No DB. Tests run on any platform.
//              Uses a small pool size (2) for fast CI.
// SPORT: REGISTRY-FUNCTIONS.md → pty.Pool tests.
package pty

import (
	"context"
	"testing"
	"time"
)

func TestPool_StartPrewarmsSlots(t *testing.T) {
	t.Parallel()
	p := NewPool(2, 30*time.Second, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	defer p.Stop()

	// Both slots should be available immediately after Start.
	ctx := context.Background()
	s1, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire slot 1: %v", err)
	}
	s2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire slot 2: %v", err)
	}
	p.Release(s1)
	p.Release(s2)
}

func TestPool_AcquireReleaseCycle(t *testing.T) {
	t.Parallel()
	p := NewPool(1, 30*time.Second, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	defer p.Stop()

	ctx := context.Background()
	slot, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if slot == nil {
		t.Fatal("Acquire returned nil slot")
	}
	// Release and re-acquire — pool should reuse.
	p.Release(slot)
	slot2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	p.Release(slot2)
}

func TestPool_AcquireBlocksWhenEmpty(t *testing.T) {
	t.Parallel()
	p := NewPool(1, 30*time.Second, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	defer p.Stop()

	ctx := context.Background()
	s, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	// With pool empty, Acquire with short deadline must fail.
	deadlineCtx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	_, err2 := p.Acquire(deadlineCtx)
	if err2 == nil {
		t.Fatal("expected deadline exceeded error on empty pool, got nil")
	}
	p.Release(s)
}

func TestPool_AcquireCanceledContext(t *testing.T) {
	t.Parallel()
	p := NewPool(1, 30*time.Second, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	defer p.Stop()

	ctx := context.Background()
	// Drain the pool.
	s, _ := p.Acquire(ctx)

	// Cancel before Acquire.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := p.Acquire(canceled)
	if err == nil {
		t.Fatal("expected error on canceled context, got nil")
	}
	p.Release(s)
}

func TestPool_StopPreventsAcquire(t *testing.T) {
	t.Parallel()
	p := NewPool(2, 30*time.Second, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	p.Stop()

	ctx := context.Background()
	_, err := p.Acquire(ctx)
	if err == nil {
		t.Fatal("expected error after Stop, got nil")
	}
}

func TestPool_ReleaseNilIsNoop(t *testing.T) {
	t.Parallel()
	p := NewPool(1, 30*time.Second, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	defer p.Stop()
	// Should not panic.
	p.Release(nil)
}

func TestPool_IdleReaperRecreatesSlots(t *testing.T) {
	t.Parallel()
	// Use a very short idle timeout to trigger the reaper quickly.
	idleTimeout := 100 * time.Millisecond
	p := NewPool(1, idleTimeout, nil)
	if err := p.Start(); err != nil {
		t.Fatalf("Pool.Start() error: %v", err)
	}
	defer p.Stop()

	ctx := context.Background()
	// Acquire and immediately release — sets lastUsed.
	s, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	p.Release(s)

	// Wait for idle timeout + reaper cycle.
	time.Sleep(idleTimeout * 3)

	// Pool should still have a slot after reaper recreated it.
	s2, err := p.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire after reaper cycle: %v", err)
	}
	p.Release(s2)
}

func TestPoolSizeFromEnv_Default(t *testing.T) {
	t.Setenv("CLAWDE_PTY_POOL_SIZE", "")
	if got := PoolSizeFromEnv(); got != DefaultPoolSize {
		t.Errorf("PoolSizeFromEnv() = %d, want %d", got, DefaultPoolSize)
	}
}

func TestPoolSizeFromEnv_Custom(t *testing.T) {
	t.Setenv("CLAWDE_PTY_POOL_SIZE", "8")
	if got := PoolSizeFromEnv(); got != 8 {
		t.Errorf("PoolSizeFromEnv() = %d, want 8", got)
	}
}

func TestPoolSizeFromEnv_Invalid(t *testing.T) {
	t.Setenv("CLAWDE_PTY_POOL_SIZE", "notanumber")
	if got := PoolSizeFromEnv(); got != DefaultPoolSize {
		t.Errorf("PoolSizeFromEnv() invalid = %d, want %d", got, DefaultPoolSize)
	}
}
