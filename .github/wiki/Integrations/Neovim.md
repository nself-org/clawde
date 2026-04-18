# Neovim Plugin

ClawDE integrates with Neovim via `clawd.nvim`, a Lua plugin that connects directly to the `clawd` daemon over WebSocket.

## Installation

### lazy.nvim

```lua
{
  "clawde-io/clawd.nvim",
  dependencies = { "nvim-lua/plenary.nvim", "MunifTanjim/nui.nvim" },
  config = function()
    require("clawd").setup()
  end,
}
```

### packer.nvim

```lua
use {
  "clawde-io/clawd.nvim",
  requires = { "nvim-lua/plenary.nvim", "MunifTanjim/nui.nvim" },
}
```

## Requirements

- Neovim 0.10+
- `clawd` daemon running on port 4300
- `plenary.nvim` and `nui.nvim`

## Commands

| Command | Description |
| --- | --- |
| `:ClawdChat` | Open a floating chat window connected to the daemon |
| `:'<,'>ClawdAsk` | Ask about visually selected code |
| `:ClawdSessions` | Show all active daemon sessions |

## Chat Window

The `:ClawdChat` command opens a split popup with a scrolling output area and an input prompt. Type a message and press `<Enter>` to send. Responses stream token-by-token.

- `q` / `<Esc>`: close the window
- The session persists across messages in the same window

## Ask About Code

Select code in visual mode, then run `:'<,'>ClawdAsk`. The plugin sends the selected lines plus the file name as context. You will be prompted for your question, or pass it as an argument:

```
:'<,'>ClawdAsk Explain the error handling pattern
```

## Configuration

```lua
require("clawd").setup({
  daemon_url = "ws://127.0.0.1:4300",
  auth_token_path = vim.fn.expand("~/.claw/auth.token"),
  provider = "claude",
  window = { width = 80, height = 30, border = "rounded" },
})
```

## Source

[`apps/integrations/neovim/`](https://github.com/nself-org/clawde/tree/main/integrations/neovim)
