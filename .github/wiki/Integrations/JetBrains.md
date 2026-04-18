# JetBrains Plugin

ClawDE integrates with JetBrains IDEs (IntelliJ IDEA, WebStorm, PyCharm, GoLand, Rider, and all other IDEs built on the IntelliJ Platform) via a native plugin.

## Installation

**From JetBrains Marketplace** (when published):

1. Open **Settings → Plugins → Marketplace**
2. Search for **ClawDE**
3. Click **Install** and restart the IDE

**Manual install** (development builds):

1. Download `clawd-jetbrains-{version}.zip` from the [GitHub Releases](https://github.com/nself-org/clawde/releases)
2. Go to **Settings → Plugins → ⚙️ → Install Plugin from Disk**
3. Select the ZIP file and restart

## Requirements

- IntelliJ Platform 2024.1+ (since-build 241)
- `clawd` daemon running on port 4300

## Features

### ClawDE Tool Window

Open from **View → Tool Windows → ClawDE** or the side panel icon.

- **Session list**: all active daemon sessions for the current project
- **Chat area**: scrolling message history
- **Input field**: type and press `<Enter>` to send

### Ask ClawDE (Inline Action)

Right-click any selection in the editor and choose **Ask ClawDE**. A dialog opens with the selected code pre-loaded as context. Type your question and the response streams into the tool window.

## Configuration

The plugin connects to `ws://localhost:4300` by default. The auth token is read from `~/.claw/auth.token`.

To change the daemon URL, go to **Settings → Tools → ClawDE** (available in v0.6.0+).

## Source

[`apps/integrations/jetbrains/`](https://github.com/nself-org/clawde/tree/main/integrations/jetbrains)
