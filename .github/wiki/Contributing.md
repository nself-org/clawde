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
cd apps

# Dart dependencies
dart pub global activate melos
melos bootstrap

# Rust
cd daemon && cargo build
```

## Code conventions

### Rust (daemon)

- `clippy` clean — `cargo clippy --all-targets -- -D warnings`
- No `unwrap()` in production code — use `?` operator
- `rustfmt` formatted — `cargo fmt`
- Tests for all business logic

### Dart / Flutter

- `flutter analyze` clean
- `dart format .` formatted
- `flutter_lints` enabled in every package
- Riverpod providers in `clawd_core`, not in app code
- Widgets that are used in both apps go in `clawd_ui`
- No raw WebSocket calls in app code — use `clawd_client`

## Branching

- `main` — always releasable
- `feat/<name>` — new features
- `fix/<name>` — bug fixes
- `chore/<name>` — tooling, deps, docs

## Pull requests

1. Fork and create a branch from `main`
2. Make your changes
3. Run the full CI check locally:
   ```bash
   melos analyze && melos test
   cargo clippy --all-targets -- -D warnings && cargo test
   ```
4. Open a PR — fill out the template completely

## What we accept

- Bug fixes with a failing test case
- Performance improvements to the daemon
- New AI provider runners (see `daemon/src/session/` for the `Runner` trait)
- UI improvements to shared widgets in `clawd_ui`
- Documentation improvements

## What belongs in the private `web` repo

The marketing site, admin dashboard, and backend infrastructure are in a separate private repository (`clawde-io/web`). Contributions to those require access to that repo.
