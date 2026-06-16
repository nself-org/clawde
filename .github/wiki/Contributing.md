# Contributing

Thanks for your interest in contributing to ClawDE.

## Before you start

- Check [open issues](https://github.com/nself-org/clawde/issues) to avoid duplicate work
- For significant changes, open an issue first to discuss the approach
- Read the [[Architecture]] page to understand how the pieces fit

## Setup

See [[Getting-Started]] for full setup instructions. Quick version:

```bash
git clone https://github.com/nself-org/clawde.git

# Daemon (Rust)
cd apps/daemon && cargo build

# Desktop (Tauri 2 + React)
cd ../desktop && pnpm install

# Mobile (React Native + Expo)
cd ../mobile && pnpm install
```

## Code conventions

### Rust (daemon)

- `clippy` clean — `cargo clippy --all-targets -- -D warnings`
- No `unwrap()` in production code — use `?` operator
- `rustfmt` formatted — `cargo fmt`
- Tests for all business logic

### TypeScript (desktop + mobile)

- `pnpm typecheck` clean (tsc --noEmit)
- `pnpm test` passes (jest)
- Prettier + eslint formatted
- No raw WebSocket calls in app code — use the daemon client module

## Branching

- `main` — always releasable
- `feat/<name>` — new features
- `fix/<name>` — bug fixes
- `chore/<name>` — tooling, deps, docs

## Commit style

Conventional commits: `feat:`, `fix:`, `chore:`, `docs:`, `test:` (e.g. `feat(ui): add ChatBubble`).

## Pull requests

1. Fork and create a branch from `main`
2. Make your changes
3. Run the full CI check locally:
   ```bash
   # Daemon
   cd apps/daemon && cargo clippy --all-targets -- -D warnings && cargo test
   # Desktop
   cd ../desktop && pnpm typecheck && pnpm test
   # Mobile
   cd ../mobile && pnpm test
   ```
4. Open a PR — fill out the template completely

## What we accept

- Bug fixes with a failing test case
- Performance improvements to the daemon
- New AI provider runners (see `daemon/src/session/` for the `Runner` trait)
- UI improvements to the desktop or mobile apps
- Documentation improvements

## What belongs in the private `web` repo

The marketing site, admin dashboard, and backend infrastructure are in a separate private repository (`clawde-io/web`). Contributions to those require access to that repo.
