# End-to-End Test Plan

Run these checks after significant changes. Build first:

```bash
go build -o agent-watch-test.exe .
```

## Provider matrix

Run the dashboard in both supported providers:

```bash
# Claude provider (default)
./agent-watch-test.exe --provider claude

# Copilot provider
./agent-watch-test.exe --provider copilot
```

## Claude provider checks

1. Start one or more active Claude sessions and leave at least one idle session.
2. Confirm rows appear for running Claude sessions only.
3. Validate:
   - [ ] **PID** maps to a real `claude` process
   - [ ] **PROJECT** matches process CWD basename
   - [ ] **STATUS** transitions look correct (`Thinking/Tool Use/Streaming/Responding/Idle/Done/Error/Interrupted/Waiting`)
   - [ ] **CURRENT ACTION** updates when tools run
   - [ ] **TMUX SESSION/WINDOW** appears only when in tmux/psmux/pmux
   - [ ] No duplicate rows per cwd

## Copilot provider checks

1. Start one or more Copilot CLI sessions (`copilot chat` or equivalent) and keep at least one session active.
2. Confirm `events.jsonl` exists under `~/.copilot/session-state/<sessionId>/`.
3. Validate:
   - [ ] **PID** is resolved from `inuse.<pid>.lock` and live process match
   - [ ] **PROJECT** matches workspace cwd basename
   - [ ] **STATUS** follows Copilot event transitions (start/resume -> `Waiting`, turn start -> `Thinking`, tool start -> `Tool Use`, tool complete -> `Responding`, abort -> `Interrupted`, shutdown/task complete -> `Done`)
   - [ ] **Last prompt/response** (expanded row) reflect recent user/assistant events
   - [ ] No stale lock-only sessions appear as running after process exits

## Cross-provider checks

- [ ] `g` navigation works for sessions running in tmux/psmux/pmux
- [ ] `--compact` renders without wrapping artifacts
- [ ] Invalid provider shows clear error:
  - `agent-watch --provider invalid` -> `invalid --provider "invalid" (must be one of: claude, copilot)`
