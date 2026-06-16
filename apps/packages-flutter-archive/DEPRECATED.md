# Flutter Packages Archive — DEPRECATED

**Status:** ARCHIVED — Flutter shared packages superseded by TypeScript `@nself/*` packages (P3-E4).
**Archived:** 2026-06-16 (T-P3-E4-W4-S11-T02)
**Replaced by:** `clawde/apps/packages/` — TypeScript packages using shared `@nself/*` ecosystem

## What Was Here

Four Flutter Dart packages that formed the ClawDE Flutter client SDK:

| Package | Description |
|---|---|
| `clawd_core` | Shared state management and business logic (Riverpod providers) |
| `clawd_client` | Typed WebSocket/JSON-RPC 2.0 client, connects to clawd daemon on port 4300 |
| `clawd_proto` | Protobuf definitions for daemon protocol |
| `clawd_ui` | Flutter UI components for ClawDE |

## Why Archived

ClawDE migrated from Flutter to Tauri 2 (desktop) and React Native (mobile) per ASI Policy 2.
All shared logic is now in TypeScript packages under `clawde/apps/packages/` and the `@nself/*`
monorepo packages consumed from `packages/`.

## Do Not Use

These packages are frozen. No bug fixes. No new features.
Do not add this directory to any Flutter project's `pubspec.yaml`.
