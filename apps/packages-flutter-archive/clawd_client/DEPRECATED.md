# clawd_client — DEPRECATED

**Status:** Archived (P3-E4-W3-S6-T02)

This Dart package was part of the historical Flutter client for ClawDE. It is no longer in use:
- ClawDE desktop was migrated from Flutter to Vite + React (Tauri 2).
- ClawDE mobile was migrated from Flutter to React Native.

This package is retained in `apps/packages-flutter-archive/` for historical reference and audit purposes only. Do not use it in new code.

**Last modified:** 2026-06 (archived)
**Migration path:** Shared logic from this package was evaluated and either:
1. Ported to @nself/* TypeScript packages (if reusable across projects), or
2. Inlined into the consuming Vite/RN apps (if ClawDE-specific).

For questions about the original implementation or FFI/RPC patterns, see:
- Desktop: `apps/desktop/src/` (Vite + React)
- Mobile: `apps/mobile/src/` (React Native)
- Daemon: `apps/daemon/` (Rust, owned by clawd)
