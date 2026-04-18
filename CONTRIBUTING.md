# Contributing to ClawDE

## What This Is

ClawDE is an open-source AI development environment (Flutter desktop and mobile). It uses the nSelf backend for cloud sync and team features.

## Prerequisites

- Flutter 3.24+
- Dart 3.5+
- nSelf CLI (for backend, optional)

## Setup

```bash
git clone https://github.com/nself-org/clawde
cd clawde
flutter pub get
flutter run
```

## Development

```bash
flutter test        # run tests
flutter analyze     # static analysis
flutter build       # build for current platform
```

## Pull Requests

1. Fork and create a branch
2. `flutter analyze` must pass clean
3. Submit PR against `main`

## Commit Style

Conventional commits: `feat:`, `fix:`, `chore:`, `docs:`, `test:`
