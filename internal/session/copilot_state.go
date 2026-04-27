// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarikguney/agent-watch/internal/parser"
)

func applyCopilotEvent(state *State, event parser.CopilotEvent, processRunning bool, now time.Time) {
	if state == nil {
		return
	}

	state.LastRecordType = event.Type
	state.LastRecordSubtype = ""
	state.LastRecordTimestamp = event.Timestamp
	state.LastUpdate = now
	state.LastBlockTypes = nil
	state.LastIsSystemInjectedUser = false
	state.LastHasToolResult = false
	state.LastIsInterrupt = false
	state.LastAssistantIsWorking = false
	state.LastStopReason = ""

	if eventTime := event.EventTime(); !eventTime.IsZero() {
		state.LastUpdate = eventTime
	}
	if sessionID := event.SessionID(); sessionID != "" {
		state.SessionID = sessionID
	}
	if cwd := event.Cwd(); cwd != "" && state.Cwd == "" {
		state.Cwd = cwd
	}
	if state.ProjectName == "" && state.Cwd != "" {
		state.ProjectName = filepath.Base(state.Cwd)
	}

	switch event.Type {
	case "session.start":
		if start := event.StartTime(); !start.IsZero() && state.StartTime.IsZero() {
			state.StartTime = start
		}
		state.CurrentAction = ""
		state.LastToolResultError = false
		state.LastTurnOpen = false
		state.Status = StatusWaiting
	case "session.resume":
		state.CurrentAction = ""
		state.LastToolResultError = false
		state.LastTurnOpen = false
		state.Status = StatusWaiting
	case "user.message":
		state.CurrentAction = ""
		state.LastToolResultError = false
		state.LastTurnOpen = false
		if text := truncate(event.MessageText(), 200); text != "" {
			if state.OriginalTask == "" {
				state.OriginalTask = text
			}
			state.LastPrompt = text
		}
		state.Status = deriveCopilotStatus(*state, processRunning, now)
	case "assistant.turn_start":
		state.LastTurnOpen = true
		state.LastToolResultError = false
		state.Status = StatusThinking
	case "assistant.message":
		text := truncate(event.MessageText(), 200)
		if text != "" {
			state.LastResponse = text
			state.LastBlockTypes = append(state.LastBlockTypes, "text")
		}
		if requests := event.ToolRequests(); len(requests) > 0 {
			state.LastBlockTypes = append(state.LastBlockTypes, "tool_request")
			state.LastAssistantIsWorking = true
		}
		state.Status = deriveCopilotStatus(*state, processRunning, now)
	case "assistant.turn_end":
		state.CurrentAction = ""
		state.LastTurnOpen = false
		state.Status = StatusIdle
	case "tool.execution_start":
		name, args := event.ToolCall()
		state.CurrentAction = formatCopilotToolAction(name, args)
		state.LastToolResultError = false
		state.LastAssistantIsWorking = true
		state.LastBlockTypes = []string{"tool_execution"}
		state.Status = StatusToolUse
	case "tool.execution_complete":
		state.LastAssistantIsWorking = false
		if success, ok := event.Success(); ok {
			if success {
				state.LastToolResultError = false
				state.LastStopReason = "success"
				state.Status = StatusResponding
			} else {
				state.LastToolResultError = true
				state.LastStopReason = "failure"
				state.Status = StatusError
			}
			break
		}
		state.LastToolResultError = false
		state.LastStopReason = ""
		state.Status = StatusResponding
	case "session.error":
		state.LastToolResultError = true
		state.LastStopReason = "error"
		state.Status = StatusError
	case "abort":
		state.CurrentAction = ""
		state.LastTurnOpen = false
		state.LastStopReason = "abort"
		state.Status = StatusInterrupted
	case "session.task_complete":
		state.CurrentAction = ""
		state.LastTurnOpen = false
		if success, ok := event.Success(); ok && success {
			state.LastToolResultError = false
			state.LastStopReason = "success"
			state.Status = StatusDone
		} else if ok {
			state.LastToolResultError = true
			state.LastStopReason = "failure"
			state.Status = StatusError
		}
	case "session.shutdown":
		state.CurrentAction = ""
		state.LastTurnOpen = false
		if event.IsRoutineShutdown() {
			state.LastToolResultError = false
			state.LastStopReason = "routine"
			state.Status = StatusDone
		} else {
			state.LastStopReason = "shutdown"
			state.Status = StatusIdle
		}
	case "subagent.started":
		state.LastAssistantIsWorking = true
		state.Status = StatusToolUse
	case "subagent.completed":
		state.LastAssistantIsWorking = false
		state.Status = StatusResponding
	default:
		state.Status = deriveCopilotStatus(*state, processRunning, now)
	}
}

func deriveCopilotStatus(state State, processRunning bool, now time.Time) Status {
	if state.LastToolResultError || state.LastRecordType == "session.error" {
		return StatusError
	}

	switch state.LastRecordType {
	case "session.start", "session.resume":
		return StatusWaiting
	case "user.message":
		if isRecentTimestamp(state.LastRecordTimestamp, now, activeThreshold) {
			if processRunning {
				return StatusThinking
			}
			return StatusResponding
		}
		return StatusIdle
	case "assistant.turn_start":
		return StatusThinking
	case "assistant.message":
		if hasCopilotMarker(state.LastBlockTypes, "tool_request") {
			return StatusToolUse
		}
		if hasCopilotMarker(state.LastBlockTypes, "text") {
			if state.LastTurnOpen {
				return StatusStreaming
			}
			return StatusResponding
		}
		if state.LastTurnOpen {
			return StatusThinking
		}
		return StatusIdle
	case "tool.execution_start":
		return StatusToolUse
	case "tool.execution_complete":
		if state.LastStopReason == "failure" {
			return StatusError
		}
		return StatusResponding
	case "abort":
		return StatusInterrupted
	case "assistant.turn_end":
		return StatusIdle
	case "session.task_complete":
		if state.LastStopReason == "success" {
			return StatusDone
		}
		if state.LastStopReason == "failure" {
			return StatusError
		}
		return StatusIdle
	case "session.shutdown":
		if state.LastStopReason == "routine" {
			return StatusDone
		}
		return StatusIdle
	case "subagent.started":
		return StatusToolUse
	case "subagent.completed":
		return StatusResponding
	default:
		return state.Status
	}
}

func formatCopilotToolAction(toolName string, args json.RawMessage) string {
	normalized := normalizeCopilotToolName(toolName)
	if input, err := parser.ParseToolInput(args); err == nil {
		action := FormatToolAction(normalized, input)
		if action != fmt.Sprintf("Using %s", normalized) {
			return action
		}
	}

	if value := copilotArgumentValue(args, "file_path", "filePath", "path", "command", "pattern", "query", "url", "description"); value != "" {
		return fmt.Sprintf("%s: %s", normalized, truncate(value, 35))
	}
	return fmt.Sprintf("Using %s", normalized)
}

func normalizeCopilotToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "tool"
	}

	normalized := strings.ToLower(trimmed)
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, "_", "")
	normalized = strings.ReplaceAll(normalized, ".", "")

	switch normalized {
	case "read", "readfile":
		return "Read"
	case "edit", "applypatch":
		return "Edit"
	case "write", "writefile":
		return "Write"
	case "bash", "shell", "powershell", "terminal":
		return "Bash"
	case "grep", "search":
		return "Grep"
	case "glob":
		return "Glob"
	case "task", "subagent":
		return "Task"
	case "websearch":
		return "WebSearch"
	case "webfetch", "fetch":
		return "WebFetch"
	default:
		return trimmed
	}
}

func copilotArgumentValue(raw json.RawMessage, keys ...string) string {
	if len(raw) == 0 {
		return ""
	}

	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		var asString string
		if err := json.Unmarshal(raw, &asString); err == nil {
			return strings.TrimSpace(asString)
		}
		return ""
	}

	for _, key := range keys {
		if value, ok := values[key]; ok {
			if text := parserTextValue(value); text != "" {
				return text
			}
		}
	}

	return ""
}

func parserTextValue(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	return ""
}

func hasCopilotMarker(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func isRecentTimestamp(ts string, now time.Time, threshold time.Duration) bool {
	if ts == "" {
		return false
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return false
	}
	return now.Sub(parsed) < threshold
}
