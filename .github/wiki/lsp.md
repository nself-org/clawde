# LSP Core — VS Code Extension Setup

ClawDE ships a Language Server Protocol (LSP) stdio bridge that enables code intelligence features (hover, completion, go-to-definition) in VS Code for projects open in ClawDE.

## How it works

The ClawDE desktop app spawns `nself lsp stdio` as a child process when the LSP bridge starts. The bridge:

1. Reads JSON-RPC 2.0 messages framed with `Content-Length` headers from the process stdout.
2. Routes responses back to pending requests; forwards notifications to registered handlers.
3. Kills the process cleanly on desktop window close — no zombie `nself lsp` processes.

The VS Code extension connects to this bridge via the `nself-lsp` npm package rather than hardcoding a binary path.

## VS Code extension

Install the ClawDE VS Code extension from the Marketplace or via:

```sh
code --install-extension nself.clawde
```

The extension activates automatically when a workspace contains a `.clawd/` directory (created by `nself clawde init`).

### Supported features

| Feature | Status |
|---|---|
| Hover documentation | Supported |
| Completion | Supported |
| Go-to-definition | Planned |
| Diagnostics | Planned |
| JetBrains / Zed / Neovim | Planned (same LSP core) |

## Troubleshooting

**LSP not starting:** Run `nself doctor` to verify the daemon is healthy. The bridge requires the clawd daemon to be running (`nself start`).

**Zombie processes:** The bridge calls `child.kill()` on window close. If a zombie remains after a crash, run `pkill -f 'nself lsp stdio'`.

**Missing hover:** Ensure the VS Code workspace root matches the project path set in ClawDE. The LSP server indexes files relative to the project root.

## Implementation

- Bridge: `clawde/apps/desktop/src/lib/lsp-bridge.ts`
- Types: `LspBridge`, `LspRequest`, `LspResponse`, `LspNotification`
- Singleton: `lspBridge` (exported from `lsp-bridge.ts`)
- SPORT: T-P3-E5-W1-S2-T02 / C23
