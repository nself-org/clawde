# Deep Links

ClawDE registers the `clawde://` URI scheme on install. Deep links let you open files, folders, commands, or sessions directly from a terminal, script, CI job, or launcher tool.

## Supported URIs

### Open a file

```
clawde://file?path=<absolute-path>
```

Opens the specified file in ClawDE's editor. The file must be an absolute path. If the file is outside the current project root, ClawDE asks whether to add the parent folder as a workspace root.

Example:

```bash
open "clawde://file?path=/Users/you/project/src/main.rs"
```

### Open a folder

```
clawde://folder?path=<absolute-path>
```

Sets the given directory as the active project root and opens it in the file tree. If ClawDE already has an active session, a new session is created in the specified folder.

Example:

```bash
open "clawde://folder?path=/Users/you/projects/my-app"
```

### Run a command

```
clawde://command?name=<commandName>
```

Executes a named ClawDE command. Command names are case-sensitive. The supported names are:

| Name | Action |
| --- | --- |
| `new_session` | Start a new session in the current project root |
| `search` | Open the search panel |
| `settings` | Open the Settings window |

Example:

```bash
open "clawde://command?name=new_session"
open "clawde://command?name=search"
open "clawde://command?name=settings"
```

### Jump to a session

```
clawde://session/<id>
```

Brings ClawDE to the foreground and switches to the specified session. Session IDs are shown in the session list sidebar and returned by the `session.list` JSON-RPC method.

Example:

```bash
open "clawde://session/sess-0a1b2c3d"
```

## Invoking deep links from the terminal

### macOS

```bash
open "clawde://file?path=/absolute/path/to/file.go"
```

### Linux

```bash
xdg-open "clawde://file?path=/absolute/path/to/file.go"
```

### Windows (PowerShell)

```powershell
Start-Process "clawde://file?path=C:\Users\you\project\src\main.go"
```

### Windows (Command Prompt)

```cmd
start clawde://file?path=C:\Users\you\project\src\main.go
```

## Integration with launcher tools

### Alfred (macOS)

Create a workflow with a **Run Script** action:

```bash
open "clawde://file?path={query}"
```

Trigger it with a keyword (e.g. `ce`) to open any file in ClawDE by path.

For session switching, a File Filter action pointed at your projects directory works well: configure the action to pass the selected folder path to `clawde://folder?path={filepath}`.

### Raycast (macOS)

Use the **Open URL** action in a Raycast script command:

```bash
#!/bin/bash
# @raycast.schemaVersion 1
# @raycast.title Open in ClawDE
# @raycast.mode silent

open "clawde://folder?path=$1"
```

Raycast's built-in file picker can supply `$1`. Add the script to your Raycast extensions directory and assign a hotkey.

### Shell aliases

Add to your shell profile for quick access:

```bash
# Open current directory in ClawDE
alias cde='open "clawde://folder?path=$(pwd)"'

# Open a specific file in ClawDE
function cdef() {
  open "clawde://file?path=$(realpath "$1")"
}
```

## Path encoding

Paths with spaces or special characters must be percent-encoded. Most shells handle this automatically when you use double quotes around the full URI. If you are constructing URIs programmatically, encode spaces as `%20` and other reserved characters per RFC 3986.

Example of a path with a space:

```bash
open "clawde://file?path=/Users/you/my%20project/src/main.rs"
```

## Related

- [[Getting-Started]] — install ClawDE and run your first session
- [[Configuration]] — global hotkey and URI handler settings
- [[Daemon-Reference|Daemon API Reference]] — `session.list` to retrieve session IDs
