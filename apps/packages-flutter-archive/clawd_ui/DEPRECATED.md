# clawd_ui — DEPRECATED

**Status:** Archived (P3-E4-W3-S6-T02)

This Dart package was part of the historical Flutter client for ClawDE. It is no longer in use:
- ClawDE desktop was migrated from Flutter to Vite + React (Tauri 2).
- ClawDE mobile was migrated from Flutter to React Native.

This package is retained in `apps/packages-flutter-archive/` for historical reference and audit purposes only. Do not use it in new code.

**Last modified:** 2026-06 (archived)
**Migration path:** UI components were either:
1. Ported to React (desktop) or React Native components (mobile), or
2. Superseded by @nself/ui Radix/shadcn components.

For questions about the original implementation or design patterns, see:
- Desktop: `apps/desktop/src/` (Vite + React)
- Mobile: `apps/mobile/src/` (React Native)
- Shared UI: `../../../packages/@nself/ui` (TypeScript)
