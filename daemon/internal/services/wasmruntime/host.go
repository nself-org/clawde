// Package wasmruntime implements the clawd host ABI for WASM plugins.
//
// Host module name: "clawd"
// Exported functions:
//   - log(ptr i32, len i32)                                    — UTF-8 log message
//   - get_config(keyPtr i32, keyLen i32, outPtr i32, outLen i32) i32 — copy config value into WASM memory
package wasmruntime

import (
	"context"
	"log/slog"
	"unicode/utf8"

	"github.com/tetratelabs/wazero/api"
)

// hostModuleName is the WASM import namespace exposed to plugins.
const hostModuleName = "clawd"

// configProvider is a function that returns a config value for a given key.
// Injected at plugin instantiation time.
type configProvider func(key string) (string, bool)

// hostLog returns an api.GoModuleFunc implementing clawd::log.
//
// Signature (WAT): (import "clawd" "log" (func (param i32 i32)))
// stack[0] = ptr  — offset in WASM linear memory where the UTF-8 string starts
// stack[1] = len  — byte length of the string
//
// wazero GoModuleFunc convention: results are written back into stack[0..N-1].
// This function has no results so it only reads from the stack.
func hostLog(pluginName string) api.GoModuleFunc {
	return func(_ context.Context, mod api.Module, stack []uint64) {
		if len(stack) < 2 {
			slog.Warn("wasmruntime: clawd::log called with wrong arity",
				"plugin", pluginName, "got", len(stack))
			return
		}
		ptr := uint32(stack[0])
		length := uint32(stack[1])

		mem := mod.Memory()
		if mem == nil {
			slog.Warn("wasmruntime: clawd::log — no memory exported", "plugin", pluginName)
			return
		}

		buf, ok := mem.Read(ptr, length)
		if !ok {
			slog.Warn("wasmruntime: clawd::log — out-of-bounds memory read",
				"plugin", pluginName, "ptr", ptr, "len", length)
			return
		}

		msg := string(buf)
		if !utf8.ValidString(msg) {
			slog.Warn("wasmruntime: clawd::log — invalid UTF-8 from plugin", "plugin", pluginName)
			return
		}

		slog.Info("plugin log", "plugin", pluginName, "msg", msg)
	}
}

// hostGetConfig returns an api.GoModuleFunc implementing clawd::get_config.
//
// Signature (WAT):
//
//	(import "clawd" "get_config"
//	  (func (param i32 i32 i32 i32) (result i32)))
//
// stack[0] = keyPtr  — offset of key string in WASM linear memory
// stack[1] = keyLen  — byte length of key
// stack[2] = outPtr  — offset of output buffer in WASM linear memory
// stack[3] = outCap  — capacity of output buffer in bytes
// returns (via stack[0]): number of bytes written, or 0xFFFFFFFF (-1 as i32) on error
//
// wazero GoModuleFunc convention: the result replaces stack[0] on return.
func hostGetConfig(pluginName string, cfg configProvider) api.GoModuleFunc {
	return func(_ context.Context, mod api.Module, stack []uint64) {
		const errResult = uint64(0xFFFFFFFF) // -1 as i32 in WASM (sign-extended)

		if len(stack) < 4 {
			slog.Warn("wasmruntime: clawd::get_config called with wrong arity",
				"plugin", pluginName, "got", len(stack))
			stack[0] = errResult
			return
		}

		keyPtr := uint32(stack[0])
		keyLen := uint32(stack[1])
		outPtr := uint32(stack[2])
		outCap := uint32(stack[3])

		mem := mod.Memory()
		if mem == nil {
			stack[0] = errResult
			return
		}

		// Read the key from WASM memory.
		keyBuf, ok := mem.Read(keyPtr, keyLen)
		if !ok {
			slog.Warn("wasmruntime: clawd::get_config — key out-of-bounds",
				"plugin", pluginName, "ptr", keyPtr, "len", keyLen)
			stack[0] = errResult
			return
		}
		key := string(keyBuf)

		val, found := cfg(key)
		if !found {
			// Return 0 bytes written — key not found is not an error.
			stack[0] = 0
			return
		}

		valBytes := []byte(val)
		written := uint32(len(valBytes))
		if written > outCap {
			written = outCap
		}

		if !mem.Write(outPtr, valBytes[:written]) {
			slog.Warn("wasmruntime: clawd::get_config — write out-of-bounds",
				"plugin", pluginName, "outPtr", outPtr, "written", written)
			stack[0] = errResult
			return
		}

		stack[0] = uint64(written)
	}
}
