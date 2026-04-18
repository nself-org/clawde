# Repo Intelligence

Automatic detection, profiling, and monitoring of repository characteristics. ClawDE understands your codebase so AI tools can work more effectively.

## Overview

When you register a repository with ClawDE, the repo intelligence engine scans it to build a complete profile: what languages and frameworks you use, your coding conventions, your build tools, your test setup. This profile drives AI configuration generation and drift detection.

## Capabilities

| Feature | Description |
| --- | --- |
| Stack detection | Languages, frameworks, build tools, linters, CI systems |
| Convention inference | Naming patterns, file organization, code style |
| Dependency analysis | Cross-package and cross-repo dependency graphs |
| AI config generation | Generate `.claude/`, `.codex/`, `.cursor/` configurations |
| Drift detection | Monitor for divergence between config and actual codebase |
| Drift scoring | Quantified metrics showing how much drift has accumulated |
| Validator engine | Pluggable lint, test, typecheck, format, build validators |
| CI mode | `clawde ci verify` for headless validation in CI pipelines |

## How It Works

1. **Scan** — Analyze the repo's files, package.json, Cargo.toml, etc.
2. **Profile** — Generate a `RepoProfile` with detected stack, conventions, and dependencies
3. **Generate** — Create or update AI tool configurations based on the profile
4. **Monitor** — Watch for changes that indicate drift from the profile
5. **Report** — Surface drift scores and improvement suggestions

## AI Configuration Generation

ClawDE generates configuration for every major AI coding tool:

- **Claude Code** — `.claude/CLAUDE.md`, rules, settings
- **Codex** — `.codex/AGENTS.md`, configuration
- **Cursor** — `.cursor/rules/*.mdc`

Each generation respects overwrite/improve/skip semantics — you choose what ClawDE manages and what you control manually.

## Drift Detection

Over time, codebases evolve. New dependencies appear, conventions shift, and AI configurations become stale. ClawDE's drift detector continuously monitors for:

- New languages or frameworks not reflected in AI configs
- Changed conventions that AI tools should know about
- Outdated validator configurations
- Missing or misconfigured tool settings
