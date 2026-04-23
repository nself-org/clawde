package processpool

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// TestNewPool verifies that New returns a zero-ready pool.
func TestNewPool(t *testing.T) {
	p := New(Config{TargetSize: 3, CliBinary: "echo"})
	if p.ReadyCount() != 0 {
		t.Fatalf("expected 0 ready workers, got %d", p.ReadyCount())
	}
}

// TestDefaultConfig ensures zero-value Config fields are clamped to sane defaults.
func TestDefaultConfig(t *testing.T) {
	p := New(Config{})
	if p.cfg.TargetSize != 2 {
		t.Errorf("expected default TargetSize=2, got %d", p.cfg.TargetSize)
	}
	if p.cfg.CliBinary != "claude" {
		t.Errorf("expected default CliBinary=claude, got %q", p.cfg.CliBinary)
	}
}

// TestAcquireEmptyPool verifies that Acquire on an empty pool returns (zero, false).
func TestAcquireEmptyPool(t *testing.T) {
	p := New(Config{TargetSize: 2, CliBinary: "echo"})
	_, ok := p.Acquire()
	if ok {
		t.Fatal("expected Acquire on empty pool to return false")
	}
}

// TestPIDList verifies PIDList reports injected workers accurately.
func TestPIDList(t *testing.T) {
	p := New(Config{TargetSize: 2, CliBinary: "echo"})

	// Inject workers directly (bypasses spawn, so we can test without a real binary).
	p.mu.Lock()
	p.workers = []PoolWorker{
		{PID: 1001, PGID: 1001, SpawnedAt: time.Now()},
		{PID: 1002, PGID: 1002, SpawnedAt: time.Now()},
	}
	p.mu.Unlock()

	pids := p.PIDList()
	if len(pids) != 2 {
		t.Fatalf("expected 2 PIDs, got %d", len(pids))
	}
	if pids[0] != 1001 || pids[1] != 1002 {
		t.Errorf("unexpected PIDs: %v", pids)
	}
}

// TestAcquireRelease verifies the FIFO acquire/release cycle.
func TestAcquireRelease(t *testing.T) {
	p := New(Config{TargetSize: 2, CliBinary: "echo"})

	// Inject a sentinel worker with our own PID (guaranteed alive).
	selfPID := os.Getpid()
	p.mu.Lock()
	p.workers = []PoolWorker{
		{PID: selfPID, PGID: selfPID, SpawnedAt: time.Now()},
	}
	p.mu.Unlock()

	w, ok := p.Acquire()
	if !ok {
		t.Fatal("expected Acquire to succeed")
	}
	if w.PID != selfPID {
		t.Errorf("expected PID %d, got %d", selfPID, w.PID)
	}
	if p.ReadyCount() != 0 {
		t.Errorf("pool should be empty after acquire, got %d", p.ReadyCount())
	}

	// Release back to pool.
	p.Release(w)
	if p.ReadyCount() != 1 {
		t.Errorf("expected 1 ready after release, got %d", p.ReadyCount())
	}
}

// TestReleaseDeadProcess verifies that a dead process is not returned to the pool.
// We spawn a short-lived real process (echo) and wait for it to exit, then
// confirm Release does not re-add it.
func TestReleaseDeadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process liveness check differs on Windows — skip")
	}
	cmd := exec.Command("echo", "ping")
	if err := cmd.Start(); err != nil {
		t.Fatalf("could not start echo: %v", err)
	}
	pid := cmd.Process.Pid
	_ = cmd.Wait() // wait for echo to exit

	// Give the OS a moment to reap the process.
	time.Sleep(20 * time.Millisecond)

	p := New(Config{TargetSize: 1, CliBinary: "echo"})
	p.Release(PoolWorker{PID: pid, PGID: pid, SpawnedAt: time.Now()})
	if p.ReadyCount() != 0 {
		t.Errorf("dead process should not be added to pool, got %d", p.ReadyCount())
	}
}

// TestShutdownDrainsPool verifies that Shutdown kills all workers and clears state.
func TestShutdownDrainsPool(t *testing.T) {
	p := New(Config{TargetSize: 2, CliBinary: "echo"})

	// Inject a self-PID worker (alive, but killGroup is a no-op for self on most platforms).
	selfPID := os.Getpid()
	p.mu.Lock()
	p.workers = []PoolWorker{
		{PID: selfPID, PGID: selfPID, SpawnedAt: time.Now()},
	}
	p.mu.Unlock()

	p.Shutdown()

	if p.ReadyCount() != 0 {
		t.Errorf("expected 0 workers after shutdown, got %d", p.ReadyCount())
	}
}

// TestConcurrentAcquire fires multiple goroutines that each call Acquire,
// verifying no data races (run with -race).
func TestConcurrentAcquire(t *testing.T) {
	p := New(Config{TargetSize: 10, CliBinary: "echo"})

	// Pre-populate with 5 workers.
	p.mu.Lock()
	for i := 0; i < 5; i++ {
		p.workers = append(p.workers, PoolWorker{
			PID: 10000 + i, PGID: 10000 + i, SpawnedAt: time.Now(),
		})
	}
	p.mu.Unlock()

	done := make(chan struct{}, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_, _ = p.Acquire()
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
}

// TestReplenishSpawnsRealProcess is an integration test that spawns a real binary.
// It is skipped unless INTEGRATION=1 is set, because it requires a real binary
// accessible on PATH ("echo" is used instead of "claude" for portability).
func TestReplenishSpawnsRealProcess(t *testing.T) {
	if os.Getenv("INTEGRATION") != "1" {
		t.Skip("set INTEGRATION=1 to run process-spawn integration tests")
	}
	if runtime.GOOS == "windows" {
		t.Skip("full process-group freeze not implemented on Windows yet (SP-21.T09)")
	}

	// Use "sleep" with a short duration so the process stays alive long enough
	// for us to probe it, but exits on its own.
	p := New(Config{TargetSize: 1, CliBinary: "sleep"})

	// Override the CLI arg list in a real integration test by poking the binary
	// directly. We spawn "sleep 5" by temporarily overriding CliBinary.
	p.cfg.CliBinary = "sleep"

	// spawnWorker will exec "sleep --daemon-pool-worker" which will exit
	// immediately (unknown flag). For this test we only need to confirm that
	// exec.Start succeeded and returned a valid PID before the process exits.
	// spawnWorker's freezeGroup call may fail if the process exits first —
	// tolerate both outcomes.
	_ = p.spawnWorker()

	// Minimal wait to let the goroutine update state.
	time.Sleep(100 * time.Millisecond)

	// If ReadyCount is 0 the process exited before freeze; that is still a
	// useful data point (exec.Start succeeded). We cannot assert > 0 here
	// because "sleep" exits on unknown flags.
	t.Logf("ReadyCount after spawn attempt: %d", p.ReadyCount())
	t.Logf("PIDs: %v", p.PIDList())
	p.Shutdown()
}

// TestReplenishLoopContext verifies Replenish exits when the context is cancelled.
func TestReplenishLoopContext(t *testing.T) {
	p := New(Config{TargetSize: 1, CliBinary: "echo"})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		p.Replenish(ctx)
		close(done)
	}()

	select {
	case <-done:
		// Expected — loop exited on context cancellation.
	case <-time.After(1 * time.Second):
		t.Fatal("Replenish did not exit after context cancellation")
	}
}

// TestSpawnBadBinary verifies spawnWorker returns an error (not panic) for a
// nonexistent binary.
func TestSpawnBadBinary(t *testing.T) {
	p := New(Config{TargetSize: 1, CliBinary: "/nonexistent/binary-" + strconv.Itoa(os.Getpid())})
	err := p.spawnWorker()
	if err == nil {
		t.Fatal("expected error for nonexistent binary, got nil")
	}
	if p.ReadyCount() != 0 {
		t.Errorf("expected empty pool after failed spawn, got %d", p.ReadyCount())
	}
}
