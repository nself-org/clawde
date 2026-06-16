# Folder Structure

```text
clawde/                          # Repository root (nself-org/clawde)
│
├── apps/                        # All application code
│   ├── daemon/                  # clawd — Rust/Tokio daemon
│   │   ├── src/
│   │   │   ├── main.rs          # Entry point — starts the WebSocket server
│   │   │   ├── lib.rs           # Crate root
│   │   │   ├── config/          # Config loading (TOML + env)
│   │   │   ├── ipc/             # JSON-RPC 2.0 dispatcher and handlers
│   │   │   ├── session/         # Session lifecycle + AI runner trait
│   │   │   ├── repo/            # Git2 integration, filesystem watcher
│   │   │   └── db/              # SQLite schema + sqlx queries
│   │   ├── packs/               # First-party coding standard packs
│   │   ├── scripts/             # Install scripts, Homebrew template
│   │   ├── examples/            # Plugin examples
│   │   ├── integrations/        # JetBrains, Neovim integrations
│   │   └── Cargo.toml
│   │
│   ├── desktop/                 # Tauri 2 + React + Vite (macOS / Windows / Linux)
│   │   ├── src/
│   │   │   ├── components/      # React UI components
│   │   │   ├── hooks/           # Custom React hooks
│   │   │   ├── lib/             # Shared utilities and daemon client
│   │   │   ├── stores/          # State management
│   │   │   ├── types/           # TypeScript type definitions
│   │   │   └── __tests__/       # Jest tests
│   │   ├── src-tauri/           # Rust native shell (Tauri 2)
│   │   ├── vite.config.ts
│   │   └── package.json         # @clawde/desktop — pnpm
│   │
│   ├── mobile/                  # React Native + Expo (iOS + Android)
│   │   ├── App.tsx              # Entry point
│   │   ├── src/                 # Screens, components, hooks
│   │   ├── ios/                 # Native iOS project
│   │   ├── android/             # Native Android project
│   │   └── package.json         # @clawde/mobile — pnpm
│   │
│   ├── packages/
│   │   └── clawd_plugin_abi/    # Rust crate: plugin ABI (shared types for plugins)
│   │       └── Cargo.toml
│   │
│   └── packages-flutter-archive/ # Retired Flutter/Dart packages (archive — not built)
│       │                         # clawd_client, clawd_core, clawd_proto, clawd_ui
│       └── (archived source)
│
├── site/                        # Website (clawde.io) — coming soon
│
├── .github/
│   ├── workflows/ci.yml         # cargo clippy+test · pnpm typecheck+test
│   ├── actions/review/          # ClawDE AI Review GitHub Action
│   ├── ISSUE_TEMPLATE/          # Bug report + feature request forms
│   └── wiki/                    # GitHub Wiki source (synced on push)
│       ├── brand/               # Brand assets (icons, logos, favicon)
│       │   ├── icon.png         # 512x512 master icon
│       │   ├── logo.png         # Horizontal wordmark
│       │   └── icons/           # Full size array (16px to 4096px)
│       ├── Branding/            # Brand guide and asset index
│       ├── Features/            # Feature detail pages
│       ├── Home.md              # Wiki landing page
│       └── _Sidebar.md          # Wiki navigation
│
├── .gitignore
├── README.md
└── LICENSE
```
