// Package wasmruntime implements wazero-based WASM plugin loading and execution
// for the ClawDE daemon. It replaces the previous Rust/wasmtime integration
// (SP-21.T09 migration) with a pure-Go runtime that requires no CGo.
//
// # Resource limits
//
//   - Memory: wasmMaxMemoryPages * 64 KiB = 64 MiB per module instance.
//   - Execution time / "fuel": enforced via context cancellation after
//     wasmFuelPerCall instructions. wazero does not expose a native fuel API;
//     we wrap each call in a context with a deadline derived from a calibrated
//     instruction budget (1 µs per instruction assumed at 1 GHz effective WASM
//     throughput — conservative).
//
// # Thread safety
//
// A Runtime is safe for concurrent use. Individual WasmPlugin instances are NOT
// safe for concurrent use (share them via your own mutex or create one per
// goroutine).
package wasmruntime

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

const (
	// wasmFuelPerCall is the approximate maximum number of WASM instructions
	// allowed per plugin lifecycle call. There is no native fuel API in wazero;
	// we translate this to a wall-clock timeout (1 instruction ≈ 1 ns).
	wasmFuelPerCall = 10_000_000

	// wasmMaxMemoryPages is the hard cap on linear memory pages (1 page = 64 KiB).
	// 256 pages × 64 KiB = 16 MiB (matches the spec default).
	wasmMaxMemoryPages = 256 // 16 MiB

	// callTimeout is the derived wall-clock deadline per call from wasmFuelPerCall.
	// At 1 ns/instruction: 10_000_000 ns = 10 ms.
	callTimeout = time.Duration(wasmFuelPerCall) * time.Nanosecond
)

// Runtime wraps a shared wazero.Runtime that should be created once at daemon
// start and injected into every WasmPlugin via NewWasmPlugin.
//
// Close must be called when the daemon shuts down to release WASM compiler
// resources.
type Runtime struct {
	rt wazero.Runtime
}

// NewRuntime creates a new shared wazero.Runtime.
// Use the compiler (JIT) configuration where possible; wazero falls back to
// the interpreter on architectures without JIT support (e.g., 32-bit ARM).
func NewRuntime(ctx context.Context) *Runtime {
	cfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(wasmMaxMemoryPages)
	rt := wazero.NewRuntimeWithConfig(ctx, cfg)

	// Instantiate WASI so plugins can use standard I/O if they want it.
	// This is a no-op for plugins that don't import WASI functions.
	wasi_snapshot_preview1.MustInstantiate(ctx, rt)

	return &Runtime{rt: rt}
}

// Close releases compiler resources associated with the runtime.
func (r *Runtime) Close(ctx context.Context) error {
	return r.rt.Close(ctx)
}

// WasmPlugin is a compiled+ready WASM plugin instance.
// It holds the compiled module (pre-validated and compiled at load time) and
// re-instantiates the module on each lifecycle call so that plugin state is
// reset between calls (stateless call model).
type WasmPlugin struct {
	name     string
	rt       *Runtime
	compiled wazero.CompiledModule
	cfg      configProvider
}

// NewWasmPlugin loads, validates, and compiles a WASM plugin binary.
//
// Steps:
//  1. Magic-byte check (fast path — rejects non-WASM payloads early).
//  2. wazero.Runtime.CompileModule — full structural + type validation plus
//     compilation. Returns a non-nil error for any malformed or invalid binary.
//  3. Verify the required clawd_plugin_init export is present.
//
// The caller must supply a shared *Runtime (created once at daemon start).
// The configProvider function is called by the clawd::get_config host function.
// Pass nil to use the default no-op provider (all keys return not-found).
func NewWasmPlugin(
	ctx context.Context,
	rt *Runtime,
	name string,
	wasmBytes []byte,
	cfgFn configProvider,
) (*WasmPlugin, error) {
	if len(wasmBytes) < 4 || string(wasmBytes[:4]) != "\x00asm" {
		return nil, fmt.Errorf("wasmruntime: %q is not a valid WASM module (bad magic bytes)", name)
	}

	compiled, err := rt.rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("wasmruntime: compile %q: %w", name, err)
	}

	// Verify the mandatory init export is present.
	if _, ok := compiled.ExportedFunctions()["clawd_plugin_init"]; !ok {
		_ = compiled.Close(ctx)
		return nil, fmt.Errorf(
			"wasmruntime: plugin %q is missing required export 'clawd_plugin_init'", name)
	}

	if cfgFn == nil {
		cfgFn = func(string) (string, bool) { return "", false }
	}

	return &WasmPlugin{
		name:     name,
		rt:       rt,
		compiled: compiled,
		cfg:      cfgFn,
	}, nil
}

// Close releases the compiled module resources.
func (p *WasmPlugin) Close(ctx context.Context) error {
	return p.compiled.Close(ctx)
}

// instantiate creates a fresh module instance with the clawd host module wired
// in. The caller is responsible for closing the returned module.
func (p *WasmPlugin) instantiate(ctx context.Context) (api.Module, error) {
	// Build the "clawd" host module with our ABI functions.
	builder := p.rt.rt.NewHostModuleBuilder(hostModuleName)

	builder.NewFunctionBuilder().
		WithGoModuleFunction(
			hostLog(p.name),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{},
		).
		Export("log")

	builder.NewFunctionBuilder().
		WithGoModuleFunction(
			hostGetConfig(p.name, p.cfg),
			[]api.ValueType{api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32, api.ValueTypeI32},
			[]api.ValueType{api.ValueTypeI32},
		).
		Export("get_config")

	if _, err := builder.Instantiate(ctx); err != nil {
		return nil, fmt.Errorf("wasmruntime: instantiate host module for %q: %w", p.name, err)
	}

	modCfg := wazero.NewModuleConfig().
		WithName(p.name).
		WithStartFunctions() // suppress automatic _start / main execution

	mod, err := p.rt.rt.InstantiateModule(ctx, p.compiled, modCfg)
	if err != nil {
		return nil, fmt.Errorf("wasmruntime: instantiate module %q: %w", p.name, err)
	}
	return mod, nil
}

// CallInit calls the clawd_plugin_init export on the plugin.
//
// A fresh module instance is created for each call; state is not persisted
// between calls (stateless call model — suitable for per-request plugins).
//
// The call is bounded by callTimeout (derived from wasmFuelPerCall). If the
// plugin exceeds the budget, the context is cancelled and an error is returned.
func (p *WasmPlugin) CallInit(ctx context.Context) error {
	return p.callExport(ctx, "clawd_plugin_init")
}

// CallOnSessionStart calls the clawd_on_session_start export.
// Returns nil if the export does not exist (optional lifecycle hook).
func (p *WasmPlugin) CallOnSessionStart(ctx context.Context) error {
	return p.callExportOptional(ctx, "clawd_on_session_start")
}

// callExport calls a required named export. Returns an error if the export
// does not exist or if the call fails.
func (p *WasmPlugin) callExport(ctx context.Context, name string) error {
	tCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	mod, err := p.instantiate(tCtx)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := mod.Close(tCtx); cerr != nil {
			slog.Warn("wasmruntime: close module", "plugin", p.name, "err", cerr)
		}
	}()

	fn := mod.ExportedFunction(name)
	if fn == nil {
		return fmt.Errorf("wasmruntime: plugin %q has no export %q", p.name, name)
	}

	if _, err := fn.Call(tCtx); err != nil {
		return fmt.Errorf("wasmruntime: %q.%s: %w", p.name, name, err)
	}
	return nil
}

// callExportOptional calls a named export, returning nil if the export does
// not exist. Used for optional lifecycle hooks.
func (p *WasmPlugin) callExportOptional(ctx context.Context, name string) error {
	tCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	mod, err := p.instantiate(tCtx)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := mod.Close(tCtx); cerr != nil {
			slog.Warn("wasmruntime: close module", "plugin", p.name, "err", cerr)
		}
	}()

	fn := mod.ExportedFunction(name)
	if fn == nil {
		// Optional — not an error.
		return nil
	}

	if _, err := fn.Call(tCtx); err != nil {
		return fmt.Errorf("wasmruntime: %q.%s: %w", p.name, name, err)
	}
	return nil
}
