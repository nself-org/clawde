# ClawDE Versioning

ClawDE follows semantic versioning (semver) independent of the nSelf CLI.

## Current Versions

| Component        | Version | Package file                                        |
|------------------|---------|-----------------------------------------------------|
| Desktop app      | 0.3.2   | apps/desktop/package.json                           |
| Mobile app       | 0.3.2   | apps/mobile/package.json                            |
| clawd daemon     | 0.1.0   | apps/daemon/Cargo.toml                              |
| clawd_plugin_abi | 0.1.0   | apps/packages/clawd_plugin_abi/Cargo.toml           |

## Versioning Policy

Desktop + mobile apps share one canonical app version — bumped together in each
release. The daemon and clawd_plugin_abi Rust crate version independently while
pre-1.0; they are internal crates and do not need to match the app version.

ClawDE does NOT track nSelf CLI versions. A ClawDE release is independent of
whether nSelf CLI ships anything simultaneously.

## Release Cadence

- Patch (0.3.x): bug fixes, no API changes
- Minor (0.x.0): new features, backward-compatible API additions
- Major (x.0.0): breaking daemon API changes (IPC protocol version bump required)

## How to Release

1. Bump the canonical app version in apps/desktop/package.json and
   apps/mobile/package.json (both must match)
2. Update this file's version table and apps/CHANGELOG.md
3. Run CI: `cd apps/daemon && cargo test && cd ../desktop && pnpm test && cd ../mobile && pnpm test`
4. Tag: `git tag v{version}`
5. Push tag — GitHub Actions builds and publishes

ClawDE uses the `v{version}` git tag prefix.
