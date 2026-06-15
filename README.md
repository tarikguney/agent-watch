# agent-watch

A zero-setup CLI dashboard for monitoring Claude Code and GitHub Copilot CLI agents in real time.

Run `agent-watch` and instantly see what all your running sessions are doing -- which project, current action, status, and how long they've been running. Designed to live in a tmux/psmux pane as your agent task manager.

![agent-watch collapsed view](screenshot1-collapsed.png)

## How it works

agent-watch discovers running agent processes from the OS process list, matches each to its most recent session transcript, and renders a continuously-updating dashboard.

No hooks to configure, no agents to register, no setup. It discovers running processes and reads what's already on disk.

## Prerequisites

Install Go 1.21 or later:

```bash
# macOS
brew install go

# Windows
winget install GoLang.Go

# Linux (Debian/Ubuntu)
sudo apt install golang-go

# Linux (Fedora)
sudo dnf install golang
```

### psmux (required for prompt broadcasting)

The **broadcasting** feature (sending one prompt to many agents at once) requires a tmux-compatible multiplexer. On Windows this is [**psmux**](https://github.com/psmux/psmux), which must be installed and on your `PATH`, with your agents running inside psmux panes. tmux and pmux are also supported. Monitoring works without a multiplexer; only broadcasting needs one.

## Installation

```bash
go install github.com/tarikguney/agent-watch@latest
```

## Usage

```bash
# Just run it
agent-watch

# Select provider (default: all)
agent-watch --provider all
agent-watch --provider claude
agent-watch --provider copilot

# Custom refresh interval
agent-watch --refresh 1s

# Custom Claude directory
agent-watch --claude-dir /path/to/.claude

# Custom Copilot directory
agent-watch --provider copilot --copilot-dir /path/to/.copilot

# Compact mode for narrow tmux panes
agent-watch --compact

# Disable Windows notifications
agent-watch --windows-notifications=false

# Send a sample Windows notification and exit
agent-watch --test-windows-notification
```

## Keyboard shortcuts

| Key | Action |
|---|---|
| `↑` / `↓` (or `k` / `j`) | Move the cursor between sessions |
| `Enter` | Toggle expansion for the selected session (show last prompt + response) |
| `Space` | Mark / unmark the selected session for broadcasting |
| `v` | Select or deselect all visible sessions |
| `s` | Open the multiline broadcast dialog (`Ctrl+D` send, `Enter` newline, `Esc` cancel) |
| `Esc` | Clear the broadcast selection |
| `a` / `l` / `p` | Filter view to all / Claude / Copilot sessions |
| `n` | Toggle Windows notifications on / off |
| `m` | Mute or unmute Windows notifications for the selected row for the current run |
| `r` | Toggle sort mode: project (A-Z, default) / recent activity |
| `e` | Expand all rows |
| `c` | Collapse all rows |
| `g` | Go to the session's tmux/psmux window (jumps the active client) |
| `x` | Kill the selected session's process (asks for confirmation) |
| `q` / `Ctrl+C` | Quit |

## Dashboard columns

- **PID** — the OS process ID of the running process. A `>` marker highlights the cursor.
- **TMUX SESSION/WINDOW** — the `session/window` name when the session is running inside tmux, psmux, or pmux. Hidden automatically when no session is in a multiplexer.
- **PROVIDER** — `CLAUDE` or `COPILOT`, shown as a badge for each row.
- **PROJECT** — the project name derived from the session's working directory.
- **STATUS** — what the agent is doing right now (see below).
- **CURRENT ACTION** — the active tool call or a human-readable description of the current phase.
- **DURATION** — elapsed time since the session started.

When a row is expanded, two extra lines appear beneath it:

- `» prompt:` — the most recent user prompt (or the original task if no new prompt has been sent).
- `» response:` — the latest response text from the agent.

## Status indicators

| Status | Meaning |
|---|---|
| **Thinking** | The agent is in an extended-thinking block |
| **Tool Use** | A tool call is in flight (the tool name shows in *Current Action*) |
| **Streaming** | Claude is streaming a response token by token |
| **Responding** | The agent is actively producing a reply (generic working state) |
| **Waiting** | Process is up but the session has not received its first prompt yet |
| **Idle** | Process is running and Claude is waiting for user input |
| **Interrupted** | The last turn was cancelled by the user |
| **Done** | Session completed normally |
| **Error** | The last tool call returned an error |

## tmux / psmux integration

agent-watch detects tmux, psmux, and pmux automatically (set `CLAUDE_WATCH_TMUX_BIN` to override). When a session process lives inside a multiplexer pane, the dashboard shows its `session/window` name and lets you jump straight to it with `g`. If programmatic switching fails (e.g. you're attached from a different client), the status bar prints the manual `Ctrl+B, s` navigation hint for that session.

## Broadcasting prompts

> **Requires psmux** (or tmux/pmux) installed and on your `PATH`, with your agents running inside its panes. See [Prerequisites](#psmux-required-for-prompt-broadcasting).

You can send the **same prompt to several running agents at once**, directly from the dashboard:

1. Mark the sessions you want with `Space` (a gold `*` appears in the gutter and the title bar shows `[selected: N]`). Use `v` to mark or clear every visible row, and `Esc` to clear the selection.
2. Press `s` to open the prompt dialog. It's a multiline editor: type your prompt, press `Enter` for a new line, and press `Ctrl+D` to send (or `Esc` to cancel).
3. If no rows are marked, the prompt is sent to the session under the cursor.

The prompt is delivered by typing it into each agent's multiplexer pane and submitting it (via `send-keys`), so **broadcasting only works for sessions running inside tmux, psmux, or pmux**. Sessions that aren't in a multiplexer are skipped and reported in the status bar, e.g. `Sent to 3/5 agents  |  2 skipped: not in a multiplexer`.

## Windows notifications

On Windows, `agent-watch` sends native notifications when a session transitions to **response complete** (`Idle` after active work), **Done**, or **Error**. Notifications are enabled by default on Windows, can be disabled globally with `--windows-notifications=false`, and can be muted for a selected row during the current run with `m`.

## Platform support

Works on Windows, macOS, and Linux. Process discovery uses:
- **Windows**: PowerShell (`Get-CimInstance Win32_Process`)
- **macOS/Linux**: `ps` with command-line flag parsing

## License

MIT
