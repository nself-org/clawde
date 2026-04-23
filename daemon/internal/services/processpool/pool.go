// Package processpool manages a pool of pre-warmed CLI processes for fast
// cold session resume. Workers are SIGSTOP'd (frozen) immediately after
// spawn and thawed via SIGCONT when a session claims one.
//
// Pool targets target_size warm workers at all times. After a worker is
// claimed, Replenish() is called to spawn a replacement.
package processpool

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"time"
)

// PoolWorker represents a single pre-warmed CLI process.
type PoolWorker struct {
	PID       int
	PGID      int
	SpawnedAt time.Time
}

// Config holds pool construction parameters.
type Config struct {
	// TargetSize is the number of warm workers to maintain.
	TargetSize int
	// CliBinary is the path (or name on $PATH) of the CLI to spawn.
	CliBinary string
}

// Pool manages a set of frozen CLI worker processes.
type Pool struct {
	cfg        Config
	workers    []PoolWorker
	mu         sync.RWMutex
	readyQueue chan PoolWorker
	wg         sync.WaitGroup
}

// New constructs a Pool with the supplied Config.
func New(cfg Config) *Pool {
	if cfg.TargetSize <= 0 {
		cfg.TargetSize = 2
	}
	if cfg.CliBinary == "" {
		cfg.CliBinary = "claude"
	}
	return &Pool{
		cfg:        cfg,
		readyQueue: make(chan PoolWorker, cfg.TargetSize*2),
	}
}

// Replenish runs in a loop, keeping the pool at cfg.TargetSize.
// Call this in a goroutine; it exits when ctx is cancelled.
func (p *Pool) Replenish(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		p.mu.RLock()
		current := len(p.workers)
		p.mu.RUnlock()

		if current < p.cfg.TargetSize {
			slog.Info("processpool: replenishing",
				"current", current,
				"target", p.cfg.TargetSize,
			)
			if err := p.spawnWorker(); err != nil {
				slog.Error("processpool: spawn failed", "err", err)
				// Back off briefly to avoid tight loops on persistent failures.
				select {
				case <-ctx.Done():
					return
				case <-time.After(500 * time.Millisecond):
				}
			}
		} else {
			// Pool is full — wait before rechecking.
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
		}
	}
}

// spawnWorker launches one CLI process, freezes it, and stores it in the pool.
func (p *Pool) spawnWorker() error {
	cmd := exec.Command(p.cfg.CliBinary, "--daemon-pool-worker") //nolint:gosec
	setSysProcAttr(cmd) // platform-specific: sets Setpgid on unix, noop on windows

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("processpool: exec.Start(%q): %w", p.cfg.CliBinary, err)
	}

	pid := cmd.Process.Pid
	pgid, err := getPGID(pid) // platform-specific
	if err != nil {
		// Non-fatal: we have the PID; PGID is best-effort for group freeze.
		slog.Warn("processpool: could not get PGID, falling back to PID", "pid", pid, "err", err)
		pgid = pid
	}

	if err := freezeGroup(pgid); err != nil { // platform-specific
		// If freeze fails, kill the rogue process and propagate the error.
		_ = cmd.Process.Kill()
		return fmt.Errorf("processpool: freeze PGID %d: %w", pgid, err)
	}

	worker := PoolWorker{
		PID:       pid,
		PGID:      pgid,
		SpawnedAt: time.Now(),
	}

	p.mu.Lock()
	p.workers = append(p.workers, worker)
	p.mu.Unlock()

	select {
	case p.readyQueue <- worker:
	default:
		// readyQueue full — the worker is still registered; caller can drain later.
	}

	slog.Info("processpool: worker spawned and frozen",
		"pid", pid,
		"pgid", pgid,
	)
	return nil
}

// Acquire removes and returns one frozen worker from the pool, or a zero
// value and false if the pool is empty.
func (p *Pool) Acquire() (PoolWorker, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.workers) == 0 {
		return PoolWorker{}, false
	}
	w := p.workers[0]
	p.workers = p.workers[1:]
	return w, true
}

// Release returns a worker to the pool if the process is still alive.
func (p *Pool) Release(w PoolWorker) {
	if !isAlive(w.PID) { // platform-specific
		slog.Info("processpool: worker process exited, not returning to pool", "pid", w.PID)
		return
	}
	p.mu.Lock()
	p.workers = append(p.workers, w)
	p.mu.Unlock()
}

// ReadyCount returns the number of frozen workers available.
func (p *Pool) ReadyCount() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.workers)
}

// PIDList returns PIDs of all registered workers (for diagnostics).
func (p *Pool) PIDList() []int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]int, len(p.workers))
	for i, w := range p.workers {
		out[i] = w.PID
	}
	return out
}

// Shutdown kills all pool workers and drains the ready queue.
func (p *Pool) Shutdown() {
	p.mu.Lock()
	workers := p.workers
	p.workers = nil
	p.mu.Unlock()

	for _, w := range workers {
		killGroup(w.PGID) // platform-specific
		slog.Info("processpool: worker killed on shutdown", "pid", w.PID)
	}

	// Drain channel.
	for {
		select {
		case <-p.readyQueue:
		default:
			return
		}
	}
}
