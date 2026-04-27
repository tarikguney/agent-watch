// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package parser

import (
	"encoding/json"
	"strings"
	"time"
)

// CopilotEvent is a single JSONL event emitted by GitHub Copilot CLI.
type CopilotEvent struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	ParentID  string          `json:"parentId"`
	Timestamp string          `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// CopilotToolRequest is the assistant-declared tool request payload.
type CopilotToolRequest struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	ToolName  string          `json:"toolName,omitempty"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type copilotEventData struct {
	SessionID    string               `json:"sessionId"`
	StartTime    string               `json:"startTime"`
	Content      json.RawMessage      `json:"content"`
	Message      json.RawMessage      `json:"message"`
	Text         string               `json:"text"`
	ToolRequests []CopilotToolRequest `json:"toolRequests"`
	ToolName     string               `json:"toolName"`
	Name         string               `json:"name"`
	Tool         string               `json:"tool"`
	Arguments    json.RawMessage      `json:"arguments"`
	Input        json.RawMessage      `json:"input"`
	Success      *bool                `json:"success"`
	Reason       string               `json:"reason"`
	Status       string               `json:"status"`
	Cwd          string               `json:"cwd"`
	Context      struct {
		Cwd string `json:"cwd"`
	} `json:"context"`
	Error json.RawMessage `json:"error"`
}

// ReadCopilotHead reads the first chunk of a Copilot events file.
func ReadCopilotHead(path string) ([]CopilotEvent, error) {
	return readChunkWithParser(path, headReadSize, true, parseCopilotEventLine)
}

// ReadCopilotTail reads the last chunk of a Copilot events file.
func ReadCopilotTail(path string) ([]CopilotEvent, error) {
	return readChunkWithParser(path, tailReadSize, false, parseCopilotEventLine)
}

// ReadNewCopilotBytes reads newly appended Copilot JSONL events.
func ReadNewCopilotBytes(path string, offset int64) ([]CopilotEvent, int64, error) {
	return readNewBytesWithParser(path, offset, parseCopilotEventLine)
}

// ParseCopilotLines parses raw JSONL Copilot events.
func ParseCopilotLines(data []byte) []CopilotEvent {
	return parseJSONLLines(data, parseCopilotEventLine)
}

// EventTime parses the event timestamp.
func (e CopilotEvent) EventTime() time.Time {
	if e.Timestamp == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, e.Timestamp)
	if err != nil {
		return time.Time{}
	}
	return t
}

// SessionID extracts the payload session id, if present.
func (e CopilotEvent) SessionID() string {
	return e.payload().SessionID
}

// StartTime extracts the payload start time, if present.
func (e CopilotEvent) StartTime() time.Time {
	start := e.payload().StartTime
	if start == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Cwd extracts cwd from the payload or resume context.
func (e CopilotEvent) Cwd() string {
	data := e.payload()
	if data.Cwd != "" {
		return data.Cwd
	}
	return data.Context.Cwd
}

// MessageText extracts user/assistant text content from common payload shapes.
func (e CopilotEvent) MessageText() string {
	data := e.payload()
	if text := strings.TrimSpace(data.Text); text != "" {
		return text
	}
	if text := extractCopilotText(data.Content); text != "" {
		return text
	}
	return extractCopilotText(data.Message)
}

// ToolRequests returns assistant tool requests, if any.
func (e CopilotEvent) ToolRequests() []CopilotToolRequest {
	data := e.payload()
	if len(data.ToolRequests) == 0 {
		return nil
	}
	requests := make([]CopilotToolRequest, 0, len(data.ToolRequests))
	for _, req := range data.ToolRequests {
		if req.Input == nil && req.Arguments != nil {
			req.Input = req.Arguments
		}
		if req.Arguments == nil && req.Input != nil {
			req.Arguments = req.Input
		}
		requests = append(requests, req)
	}
	return requests
}

// ToolCall extracts a tool execution payload.
func (e CopilotEvent) ToolCall() (string, json.RawMessage) {
	data := e.payload()
	name := firstNonEmpty(data.ToolName, data.Name, data.Tool)
	if data.Arguments != nil {
		return name, data.Arguments
	}
	return name, data.Input
}

// Success returns a success value when explicitly present.
func (e CopilotEvent) Success() (bool, bool) {
	data := e.payload()
	if data.Success != nil {
		return *data.Success, true
	}
	if strings.EqualFold(data.Status, "success") || strings.EqualFold(data.Reason, "success") {
		return true, true
	}
	if strings.EqualFold(data.Status, "failed") || strings.EqualFold(data.Status, "error") || strings.EqualFold(data.Reason, "failed") || strings.EqualFold(data.Reason, "error") {
		return false, true
	}
	return false, false
}

// IsRoutineShutdown reports whether a shutdown event was a routine completion.
func (e CopilotEvent) IsRoutineShutdown() bool {
	data := e.payload()
	return strings.EqualFold(data.Reason, "routine") || strings.EqualFold(data.Status, "routine")
}

func parseCopilotEventLine(line []byte) (CopilotEvent, error) {
	var event CopilotEvent
	err := json.Unmarshal(line, &event)
	return event, err
}

func (e CopilotEvent) payload() copilotEventData {
	var data copilotEventData
	if len(e.Data) == 0 {
		return data
	}
	_ = json.Unmarshal(e.Data, &data)
	return data
}

func extractCopilotText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return strings.TrimSpace(asString)
	}

	var asArray []json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err == nil {
		parts := make([]string, 0, len(asArray))
		for _, item := range asArray {
			if text := extractCopilotText(item); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n"))
	}

	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err == nil {
		for _, key := range []string{"text", "content", "message", "body", "prompt", "value"} {
			if nested, ok := asObject[key]; ok {
				if text := extractCopilotText(nested); text != "" {
					return text
				}
			}
		}
	}

	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
