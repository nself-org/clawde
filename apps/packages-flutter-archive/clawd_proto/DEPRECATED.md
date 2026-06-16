# clawd_proto — DEPRECATED

**Status:** Archived (P3-E4-W3-S6-T02)

This Dart package was part of the historical Flutter client for ClawDE. It is no longer in use:
- ClawDE desktop was migrated from Flutter to Vite + React (Tauri 2).
- ClawDE mobile was migrated from Flutter to React Native.

This package is retained in `apps/packages-flutter-archive/` for historical reference and audit purposes only. Do not use it in new code.

**Last modified:** 2026-06 (archived)
**Protocol migration:** The original Protobuf schemas were superseded by:
- JSON-RPC 2.0 over WebSocket (Rust daemon ↔ clients)
- GraphQL for nSelf backend integration

For questions about the original protocol design or FFI bindings, see:
- Daemon Protocol: `apps/daemon/` (Rust, JSON-RPC 2.0)
- Desktop: `apps/desktop/src/` (Vite + React)
- Mobile: `apps/mobile/src/` (React Native)
