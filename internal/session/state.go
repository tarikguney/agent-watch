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

type Status string

const (
	StatusThinking     Status = "Thinking"
	StatusToolUse      Status = "Tool Use"
	StatusStreaming    Status = "Streaming"
	StatusResponding   Status = "Responding"
	StatusCompletedAgo Status = "Completed"
	StatusIdle         Status = "Idle"
	StatusDone         Status = "Done"
	StatusError        Status = "Error"
	StatusInterrupted  Status = "Interrupted"
	StatusWaiting      Status = "Waiting"
)

// State holds the derived state for a single Claude Code session.
type State struct {
	SessionID      string
	Provider       string
	PID            int // OS process ID if the session has a running claude process
	FilePath       string
	ProjectName    string
	Cwd            string
	TmuxSession    string // "session/window" from tmux/psmux, or ""
	TmuxPaneID     string // tmux pane unique ID for navigation, e.g. "%5"
	TmuxSendTarget string // fully-qualified "session:window.pane" for send-keys, or ""
	OriginalTask   string
	LastPrompt     string
	LastResponse   string
	CurrentAction  string
	Status         Status
	CompletedAt    time.Time
	Model          string
	StartTime      time.Time
	LastUpdate     time.Time
	FileOffset     int64
	FileModTime    time.Time

	// Cached from last record for re-deriving status after PID changes
	LastRecordType           string
	LastRecordSubtype        string // e.g. "turn_duration" for system records
	LastRecordTimestamp      string
	LastToolResultError      bool
	LastAssistantIsWorking   bool // true if last assistant record had tool_use or thinking (no text-only)
	LastIsSystemInjectedUser bool
	LastHasToolResult        bool
	LastIsInterrupt          bool
	LastStopReason           string   // "tool_use", "end_turn", "stop_sequence", or "" (null/absent)
	LastBlockTypes           []string // content block types from the last assistant record
	LastTurnOpen             bool
}

// idleThreshold is the duration after which a session with no result is considered Idle.
const idleThreshold = 5 * time.Minute

// activeThreshold is the max age of the last record to still be considered Responding.
const activeThreshold = 2 * time.Minute

// completedAgoThreshold is how long "Completed X ago" is shown before reverting to Idle.
const completedAgoThreshold = 8 * time.Hour

// IsActiveWorkStatus returns true when the agent is actively working on a turn.
func IsActiveWorkStatus(status Status) bool {
	return status == StatusThinking ||
		status == StatusToolUse ||
		status == StatusStreaming ||
		status == StatusResponding
}

// ParseStatusTimestamp parses an RFC3339Nano timestamp and returns zero on failure.
func ParseStatusTimestamp(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return parsed
}

// ApplyCompletedAgoStatus converts terminal Idle/Done states to the transient
// Completed status after active work and preserves it until new work starts.
func ApplyCompletedAgoStatus(previous, current Status, existingCompletedAt, completionTime, now time.Time) (Status, time.Time) {
	if IsActiveWorkStatus(current) || current == StatusError || current == StatusInterrupted || current == StatusWaiting {
		return current, time.Time{}
	}

	if current == StatusCompletedAgo {
		if !existingCompletedAt.IsZero() {
			if completedAgoExpired(existingCompletedAt, now) {
				return StatusIdle, time.Time{}
			}
			return current, existingCompletedAt
		}
		if completionTime.IsZero() {
			completionTime = now
		}
		if completedAgoExpired(completionTime, now) {
			return StatusIdle, time.Time{}
		}
		return current, completionTime
	}

	if current == StatusIdle || current == StatusDone {
		if IsActiveWorkStatus(previous) {
			if completionTime.IsZero() {
				completionTime = now
			}
			if completedAgoExpired(completionTime, now) {
				return StatusIdle, time.Time{}
			}
			return StatusCompletedAgo, completionTime
		}
		if previous == StatusCompletedAgo && !existingCompletedAt.IsZero() {
			if completedAgoExpired(existingCompletedAt, now) {
				return StatusIdle, time.Time{}
			}
			return StatusCompletedAgo, existingCompletedAt
		}
	}

	return current, time.Time{}
}

func completedAgoExpired(completedAt, now time.Time) bool {
	if completedAt.IsZero() {
		return false
	}
	return now.Sub(completedAt) >= completedAgoThreshold
}

// DeriveStatus computes the session status from the last record and its timestamp.
// processRunning indicates whether the session has a live OS process (PID > 0).
func DeriveStatus(rec parser.Record, lastToolResultIsError bool, now time.Time, processRunning bool) Status {
	recTime, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
	if err != nil {
		recTime = time.Now()
	}
	age := now.Sub(recTime)

	if rec.Type == "result" {
		return StatusDone
	}

	// System record with turn_duration subtype means the turn ended — idle.
	if rec.Type == "system" && rec.Subtype == "turn_duration" {
		return StatusIdle
	}

	// Only mark Idle from timestamp age if there's no running process.
	// A running process means Claude is alive — it just hasn't written to the file recently.
	if age > idleThreshold && !processRunning {
		return StatusIdle
	}

	if lastToolResultIsError {
		return StatusError
	}

	switch rec.Type {
	case "attachment":
		// Attachment records only appear between user prompt and assistant response,
		// indicating Claude Code is actively loading context before responding.
		if processRunning {
			return StatusThinking
		}
		return StatusIdle

	case "assistant":
		return deriveAssistantStatus(rec)

	case "user":
		if rec.IsInterruptRecord() {
			return StatusInterrupted
		}
		// tool_result means a tool just finished and Claude is about to process
		// the result — this is an active state.
		if rec.HasToolResult() {
			return StatusResponding
		}
		if rec.IsSystemInjectedUser() {
			return StatusIdle
		}
		// Real user prompt with a running process — Claude is thinking
		// (the gap between user prompt and first assistant record).
		if processRunning && age < activeThreshold {
			return StatusThinking
		}
		// Real user prompt without a running process — grace period.
		if age < activeThreshold {
			return StatusResponding
		}
		return StatusIdle
	}

	return StatusIdle
}

// deriveAssistantStatus determines the fine-grained status for an assistant record
// using stop_reason and content block types.
func deriveAssistantStatus(rec parser.Record) Status {
	mc, err := parser.ParseMessageContent(rec)
	if err != nil {
		return StatusResponding // safe fallback
	}
	blocks, err := parser.ParseContentBlocks(mc)
	if err != nil {
		return StatusResponding
	}

	stopReason := ""
	if mc.StopReason != nil {
		stopReason = *mc.StopReason
	}

	hasToolUse := false
	hasThinking := false
	hasText := false
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			hasToolUse = true
		case "thinking":
			hasThinking = true
		case "text":
			hasText = true
		}
	}

	switch stopReason {
	case "tool_use":
		// API call completed and Claude wants to invoke a tool.
		return StatusToolUse
	case "end_turn", "stop_sequence":
		// Claude finished responding — waiting for user input.
		return StatusIdle
	case "":
		// stop_reason is null or absent — Claude is still generating (streaming).
		if hasToolUse {
			return StatusToolUse
		}
		if hasThinking {
			return StatusThinking
		}
		if hasText {
			return StatusStreaming
		}
		// Fallback for records with no stop_reason and no recognizable blocks
		// (e.g. older Claude Code versions that didn't log stop_reason).
		return StatusResponding
	default:
		return StatusResponding
	}
}

// rederiveAssistantStatus recomputes assistant status from cached fields,
// used when PID attaches and we need to re-derive without re-parsing the record.
func rederiveAssistantStatus(stopReason string, blockTypes []string, assistantIsWorking bool) Status {
	hasToolUse := false
	hasThinking := false
	hasText := false
	for _, bt := range blockTypes {
		switch bt {
		case "tool_use":
			hasToolUse = true
		case "thinking":
			hasThinking = true
		case "text":
			hasText = true
		}
	}

	switch stopReason {
	case "tool_use":
		return StatusToolUse
	case "end_turn", "stop_sequence":
		return StatusIdle
	case "":
		if hasToolUse {
			return StatusToolUse
		}
		if hasThinking {
			return StatusThinking
		}
		if hasText {
			return StatusStreaming
		}
		// Fallback for older records without stop_reason
		if assistantIsWorking {
			return StatusResponding
		}
		return StatusIdle
	default:
		return StatusResponding
	}
}

// isAssistantWorking returns true if the assistant record indicates active work
// (tool calls or thinking), as opposed to a final text response.
func isAssistantWorking(rec parser.Record) bool {
	mc, err := parser.ParseMessageContent(rec)
	if err != nil {
		return false
	}
	blocks, err := parser.ParseContentBlocks(mc)
	if err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_use" || b.Type == "thinking" {
			return true
		}
	}
	return false
}

// extractBlockTypes returns the content block types from a record.
func extractBlockTypes(rec parser.Record) []string {
	mc, err := parser.ParseMessageContent(rec)
	if err != nil {
		return nil
	}
	blocks, err := parser.ParseContentBlocks(mc)
	if err != nil {
		return nil
	}
	types := make([]string, 0, len(blocks))
	for _, b := range blocks {
		types = append(types, b.Type)
	}
	return types
}

// extractStopReason returns the stop_reason from a record's message, or "".
func extractStopReason(rec parser.Record) string {
	mc, err := parser.ParseMessageContent(rec)
	if err != nil {
		return ""
	}
	if mc.StopReason != nil {
		return *mc.StopReason
	}
	return ""
}

// FormatToolAction produces a human-readable one-liner from a tool_use content block.
func FormatToolAction(toolName string, input parser.ToolInput) string {
	switch toolName {
	case "Read":
		return fmt.Sprintf("Reading %s", shortenPath(input.FilePath))
	case "Edit":
		return fmt.Sprintf("Editing %s", shortenPath(input.FilePath))
	case "Write":
		return fmt.Sprintf("Writing %s", shortenPath(input.FilePath))
	case "Bash":
		return fmt.Sprintf("Running %s", truncate(input.Command, 40))
	case "Grep":
		return fmt.Sprintf("Searching for %s", truncate(input.Pattern, 30))
	case "Glob":
		return fmt.Sprintf("Finding %s", truncate(input.Pattern, 30))
	case "Task":
		return fmt.Sprintf("Subagent: %s", truncate(input.Description, 35))
	case "WebSearch":
		return fmt.Sprintf("Searching: %s", truncate(input.Query, 35))
	case "WebFetch":
		return fmt.Sprintf("Fetching %s", truncate(input.URL, 40))
	default:
		return fmt.Sprintf("Using %s", toolName)
	}
}

// ExtractLastToolAction finds the last tool_use block in a record and returns a display string.
func ExtractLastToolAction(rec parser.Record) string {
	mc, err := parser.ParseMessageContent(rec)
	if err != nil {
		return ""
	}
	blocks, err := parser.ParseContentBlocks(mc)
	if err != nil {
		return ""
	}

	var lastToolName string
	var lastToolInput parser.ToolInput
	for _, b := range blocks {
		if b.Type == "tool_use" {
			lastToolName = b.Name
			ti, err := parser.ParseToolInput(b.Input)
			if err == nil {
				lastToolInput = ti
			}
		}
	}

	if lastToolName == "" {
		return ""
	}
	return FormatToolAction(lastToolName, lastToolInput)
}

// EncodeProjectDir converts a filesystem path to the encoded directory name
// used by Claude Code under ~/.claude/projects/.
// e.g., "Q:\sources\ci-seal-phase-2" -> "Q--sources-ci-seal-phase-2"
func EncodeProjectDir(path string) string {
	path = filepath.Clean(path)
	r := strings.NewReplacer(":", "-", `\`, "-", "/", "-")
	return r.Replace(path)
}

// ExtractOriginalTask finds the first real user message text, truncated.
// Skips system-injected messages (XML tags, command wrappers, etc.).
func ExtractOriginalTask(records []parser.Record) string {
	for _, rec := range records {
		if rec.Type != "user" {
			continue
		}
		mc, err := parser.ParseMessageContent(rec)
		if err != nil {
			continue
		}
		blocks, err := parser.ParseContentBlocks(mc)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				trimmed := strings.TrimSpace(b.Text)
				// Skip system-injected content (XML tags, command wrappers, interrupts)
				if strings.HasPrefix(trimmed, "<") {
					continue
				}
				if strings.HasPrefix(trimmed, "[Request interrupted by user") {
					continue
				}
				return truncate(trimmed, 200)
			}
		}
	}
	return ""
}

// ExtractLastResponse finds the last assistant text response (not tool_use).
func ExtractLastResponse(records []parser.Record) string {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type != "assistant" {
			continue
		}
		mc, err := parser.ParseMessageContent(records[i])
		if err != nil {
			continue
		}
		blocks, err := parser.ParseContentBlocks(mc)
		if err != nil {
			continue
		}
		// Find the last text block in this assistant message
		lastText := ""
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				lastText = b.Text
			}
		}
		if lastText != "" {
			return truncate(strings.TrimSpace(lastText), 200)
		}
	}
	return ""
}

// ExtractLastPrompt finds the last real user message, returning the first few words.
func ExtractLastPrompt(records []parser.Record) string {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type != "user" {
			continue
		}
		mc, err := parser.ParseMessageContent(records[i])
		if err != nil {
			continue
		}
		blocks, err := parser.ParseContentBlocks(mc)
		if err != nil {
			continue
		}
		for _, b := range blocks {
			if b.Type == "text" && b.Text != "" {
				trimmed := strings.TrimSpace(b.Text)
				if strings.HasPrefix(trimmed, "<") {
					continue
				}
				if strings.HasPrefix(trimmed, "[Request interrupted by user") {
					continue
				}
				return truncate(trimmed, 200)
			}
		}
	}
	return ""
}

// ExtractModel returns the model string from assistant records.
func ExtractModel(records []parser.Record) string {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].Type != "assistant" {
			continue
		}
		mc, err := parser.ParseMessageContent(records[i])
		if err != nil {
			continue
		}
		if mc.Model != "" {
			return mc.Model
		}
	}
	return ""
}

// FormatDuration returns a human-friendly short duration string (e.g., "12m", "1h05m").
func FormatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%02dm", h, m)
}

// CheckLastToolResultError scans recent records for a tool_result with is_error: true.
// In Claude JSONL, tool_result blocks appear in content arrays and have an is_error field.
func CheckLastToolResultError(records []parser.Record) bool {
	for i := len(records) - 1; i >= 0; i-- {
		rec := records[i]
		mc, err := parser.ParseMessageContent(rec)
		if err != nil {
			continue
		}

		// Parse content as raw JSON array to check for tool_result with is_error
		var rawBlocks []json.RawMessage
		if err := json.Unmarshal(mc.Content, &rawBlocks); err != nil {
			continue
		}

		for _, raw := range rawBlocks {
			var block struct {
				Type    string `json:"type"`
				IsError bool   `json:"is_error"`
			}
			if err := json.Unmarshal(raw, &block); err != nil {
				continue
			}
			if block.Type == "tool_result" && block.IsError {
				return true
			}
		}

		// Only check the last record with parseable content
		if len(rawBlocks) > 0 {
			break
		}
	}
	return false
}

func shortenPath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

func truncate(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}
