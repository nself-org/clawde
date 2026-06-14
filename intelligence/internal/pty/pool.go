// pool.go — PTY pool for pre-warmed shell slots used by ExecuteShellActivity.
//
// Purpose: Maintain a pool of N pre-warmed PTY processes so that exec_shell
//          requests avoid the cold-start cost of forking a new shell. Each slot
//          wraps a /bin/sh process started with os.StartProcess. Idle slots are
//          reaped and recreated after idleTimeout (default 30s).
//
// Inputs:  CLAWDE_PTY_POOL_SIZE (env, default 4) controls pool size.
//          CLAWDE_SANDBOX_ENABLED must be "1" before Start() is called (enforced
//          by the caller in cmd/server/main.go).
//
// Outputs: Slot — caller borrows a slot, runs a command via its Cmd/Stdin/Stdout,
//          then returns it via Release(). Release kills and recreates exited slots.
//
// Constraints: File ≤300 lines. Race-free under -race.
//              Acquire blocks on ctx; Release always returns slot to channel.
//              Idle reaper must not race with Acquire — channel select pattern.
//
// SPORT: REGISTRY-SERVICES.md → clawde-intelligence PTY pool.
package pty

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"sync"
	"time"
)

// DefaultPoolSize is the number of pre-warmed PTY slots.
const DefaultPoolSize = 4

// defaultIdleTimeout is the duration after which an idle slot is reaped.
const defaultIdleTimeout = 30 * time.Second

// Slot represents a single pre-warmed PTY slot.
//
// The caller MAY interact with Stdin/Stdout; the embedded process is /bin/sh.
// A slot borrowed via Acquire must always be returned via Release — even on error.
type Slot struct {
	// process is the /bin/sh process.
	process *os.Process
	// Stdin is the write end of the shell's stdin pipe.
	Stdin io.WriteCloser
	// Stdout is the read end of the shell's stdout pipe.
	Stdout io.ReadCloser
	// lastUsed records when this slot was last released (for idle reaping).
	lastUsed time.Time
}

// alive returns true when the underlying process is still running.
func (s *Slot) alive() bool {
	if s.process == nil {
		return false
	}
	// FindProcess succeeds on all platforms; Wait would consume the exit status.
	// Use os.FindProcess + Signal(0) to probe liveness without reaping.
	p, err := os.FindProcess(s.process.Pid)
	if err != nil {
		return false
	}
	return p.Signal(os.Signal(nil)) == nil // nolint:staticcheck
}

// kill terminates the slot's process group and closes pipes.
func (s *Slot) kill() {
	if s.Stdin != nil {
		_ = s.Stdin.Close()
	}
	if s.Stdout != nil {
		_ = s.Stdout.Close()
	}
	if s.process != nil {
		_ = s.process.Kill()
		// Wait to reap the zombie; ignore errors (process may already be gone).
		_, _ = s.process.Wait()
	}
}

// Pool manages N pre-warmed PTY slots.
//
// Purpose: Allow ExecuteShellActivity to acquire a ready-to-use shell slot
//          without incurring fork+exec latency per request.
// Inputs:  Size — number of slots (≥1); IdleTimeout — max idle before reap.
// Outputs: Slot via Acquire; nil after Stop.
// Constraints: Pool.Start() must be called exactly once. Stop() cancels all goroutines.
type Pool struct {
	size        int
	idleTimeout time.Duration
	logger      *slog.Logger

	slots  chan *Slot
	stopCh chan struct{}
	wg     sync.WaitGroup
	once   sync.Once // guards Start
}

// NewPool constructs a Pool with the given size and idle timeout.
//
// Inputs:  size — slot count (clamped to ≥1); idleTimeout — 0 uses default 30s.
// Outputs: *Pool (not started; call Start() before use).
func NewPool(size int, idleTimeout time.Duration, logger *slog.Logger) *Pool {
	if size < 1 {
		size = DefaultPoolSize
	}
	if idleTimeout <= 0 {
		idleTimeout = defaultIdleTimeout
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Pool{
		size:        size,
		idleTimeout: idleTimeout,
		logger:      logger,
		slots:       make(chan *Slot, size),
		stopCh:      make(chan struct{}),
	}
}

// PoolSizeFromEnv reads CLAWDE_PTY_POOL_SIZE and returns the parsed value,
// falling back to DefaultPoolSize on parse error or missing env.
func PoolSizeFromEnv() int {
	raw := os.Getenv("CLAWDE_PTY_POOL_SIZE")
	if raw == "" {
		return DefaultPoolSize
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return DefaultPoolSize
	}
	return n
}

// Start pre-warms all N slots and starts the idle-reaper goroutine.
// Start is idempotent (once.Do guard); a second call is a no-op.
//
// Returns an error only when zero slots could be created.
func (p *Pool) Start() error {
	var startErr error
	p.once.Do(func() {
		created := 0
		for i := 0; i < p.size; i++ {
			slot, err := newSlot()
			if err != nil {
				p.logger.Warn("pty pool: failed to pre-warm slot", "index", i, "error", err)
				continue
			}
			slot.lastUsed = time.Now()
			p.slots <- slot
			created++
		}
		if created == 0 {
			startErr = fmt.Errorf("pty pool: failed to create any slots (size=%d)", p.size)
			return
		}
		p.logger.Info("pty pool started", "size", p.size, "created", created)

		// Start idle-reaper.
		p.wg.Add(1)
		go p.reaper()
	})
	return startErr
}

// Stop signals all goroutines to exit and waits for them to finish.
// After Stop, Acquire always returns an error.
func (p *Pool) Stop() {
	select {
	case <-p.stopCh:
		// already stopped
	default:
		close(p.stopCh)
	}
	p.wg.Wait()
	// Drain remaining slots and kill them.
	for {
		select {
		case slot := <-p.slots:
			slot.kill()
		default:
			return
		}
	}
}

// Acquire borrows a slot from the pool. Blocks until one is available or ctx is
// canceled. The caller MUST call Release on the returned slot when done.
//
// Returns an error when ctx is done or the pool is stopped.
func (p *Pool) Acquire(ctx context.Context) (*Slot, error) {
	select {
	case <-p.stopCh:
		return nil, fmt.Errorf("pty pool: pool is stopped")
	case <-ctx.Done():
		return nil, ctx.Err()
	case slot := <-p.slots:
		return slot, nil
	}
}

// Release returns a slot to the pool. If the slot's process has exited, it is
// killed and a fresh slot is created in its place.
// Release must always be called after Acquire, even on error.
func (p *Pool) Release(slot *Slot) {
	if slot == nil {
		return
	}
	slot.lastUsed = time.Now()

	// If the process has died, recreate.
	if !slot.alive() {
		slot.kill()
		fresh, err := newSlot()
		if err != nil {
			p.logger.Warn("pty pool: failed to recreate slot after release", "error", err)
			// Return nothing — pool size shrinks by 1. Reaper will notice idle slots.
			return
		}
		fresh.lastUsed = time.Now()
		slot = fresh
	}

	// Non-blocking send: if pool is full (shouldn't happen) or stopped, kill the slot.
	select {
	case p.slots <- slot:
	default:
		slot.kill()
	}
}

// reaper runs until stopCh is closed, periodically checking for idle slots and
// killing+recreating them. Uses a channel select to avoid races with Acquire.
func (p *Pool) reaper() {
	defer p.wg.Done()
	ticker := time.NewTicker(p.idleTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-p.stopCh:
			return
		case <-ticker.C:
			p.reaperTick()
		}
	}
}

// reaperTick drains all current slots, checks each for idleness, kills idle
// ones, recreates them, then returns all slots to the channel.
func (p *Pool) reaperTick() {
	// Collect all available slots without blocking.
	var collected []*Slot
	for {
		select {
		case slot := <-p.slots:
			collected = append(collected, slot)
		default:
			goto done
		}
	}
done:
	for _, slot := range collected {
		if time.Since(slot.lastUsed) > p.idleTimeout || !slot.alive() {
			slot.kill()
			fresh, err := newSlot()
			if err != nil {
				p.logger.Warn("pty pool: reaper failed to recreate slot", "error", err)
				continue
			}
			fresh.lastUsed = time.Now()
			slot = fresh
		}
		select {
		case p.slots <- slot:
		case <-p.stopCh:
			slot.kill()
		}
	}
}

// newSlot forks a /bin/sh process connected via stdin/stdout pipes.
// On Linux production builds with CLAWDE_SANDBOX_ENABLED=1 the caller is
// expected to have applied the seccomp filter before exec via sandbox.Apply.
func newSlot() (*Slot, error) {
	// Use os.Pipe pairs for stdin/stdout.
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pty: stdin pipe: %w", err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, fmt.Errorf("pty: stdout pipe: %w", err)
	}

	attr := &os.ProcAttr{
		Files: []*os.File{stdinR, stdoutW, stdoutW},
		Env:   os.Environ(),
	}
	proc, err := os.StartProcess("/bin/sh", []string{"/bin/sh"}, attr)
	// Close the child-side pipe ends in the parent — they are inherited by the child.
	_ = stdinR.Close()
	_ = stdoutW.Close()
	if err != nil {
		_ = stdinW.Close()
		_ = stdoutR.Close()
		return nil, fmt.Errorf("pty: start /bin/sh: %w", err)
	}

	return &Slot{
		process: proc,
		Stdin:   stdinW,
		Stdout:  stdoutR,
		lastUsed: time.Now(),
	}, nil
}
