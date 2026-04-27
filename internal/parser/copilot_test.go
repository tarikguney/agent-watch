package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseCopilotLinesAndHelpers(t *testing.T) {
	data := []byte(`{"type":"session.start","id":"e1","timestamp":"2026-01-02T03:04:05Z","data":{"sessionId":"sess-1","startTime":"2026-01-02T03:04:05Z"}}
{"type":"user.message","id":"e2","timestamp":"2026-01-02T03:04:06Z","data":{"content":"Investigate flaky test"}}
{"type":"assistant.message","id":"e3","timestamp":"2026-01-02T03:04:07Z","data":{"content":[{"text":"I will inspect the failing file."}],"toolRequests":[{"toolName":"Read","arguments":{"file_path":"main.go"}}]}}
{"type":"tool.execution_start","id":"e4","timestamp":"2026-01-02T03:04:08Z","data":{"toolName":"Read","arguments":{"file_path":"main.go"}}}
`)

	events := ParseCopilotLines(data)
	if len(events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(events))
	}
	if got := events[0].SessionID(); got != "sess-1" {
		t.Fatalf("SessionID: got %q, want sess-1", got)
	}
	if events[0].StartTime().IsZero() {
		t.Fatal("expected non-zero start time")
	}
	if got := events[1].MessageText(); got != "Investigate flaky test" {
		t.Fatalf("MessageText: got %q", got)
	}

	requests := events[2].ToolRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 tool request, got %d", len(requests))
	}
	if got := events[2].MessageText(); got != "I will inspect the failing file." {
		t.Fatalf("assistant message text: got %q", got)
	}

	toolName, args := events[3].ToolCall()
	if toolName != "Read" {
		t.Fatalf("ToolCall name: got %q, want Read", toolName)
	}
	if len(args) == 0 {
		t.Fatal("expected tool arguments")
	}
}

func TestReadNewCopilotBytes_PartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")

	complete := `{"type":"session.start","id":"e1","timestamp":"2026-01-02T03:04:05Z","data":{"sessionId":"sess-1","startTime":"2026-01-02T03:04:05Z"}}` + "\n"
	partial := `{"type":"user.message","id":"e2","timestamp":"2026-01-02T03`
	if err := os.WriteFile(path, []byte(complete+partial), 0644); err != nil {
		t.Fatal(err)
	}

	events, offset, err := ReadNewCopilotBytes(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 parsed event, got %d", len(events))
	}
	if offset != int64(len(complete)) {
		t.Fatalf("expected offset %d, got %d", len(complete), offset)
	}

	rest := `:04:06Z","data":{"content":"Investigate flaky test"}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(rest); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	events, newOffset, err := ReadNewCopilotBytes(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 deferred event, got %d", len(events))
	}
	if events[0].Type != "user.message" {
		t.Fatalf("expected user.message, got %q", events[0].Type)
	}
	if newOffset <= offset {
		t.Fatal("expected offset to advance")
	}
}

func TestCopilotEventSuccessAndShutdown(t *testing.T) {
	successEvent := ParseCopilotLines([]byte(`{"type":"tool.execution_complete","id":"e1","timestamp":"2026-01-02T03:04:08Z","data":{"success":true}}`))
	if ok, known := successEvent[0].Success(); !known || !ok {
		t.Fatalf("expected explicit success, got ok=%v known=%v", ok, known)
	}

	shutdownEvent := ParseCopilotLines([]byte(`{"type":"session.shutdown","id":"e2","timestamp":"2026-01-02T03:04:09Z","data":{"reason":"routine"}}`))
	if !shutdownEvent[0].IsRoutineShutdown() {
		t.Fatal("expected routine shutdown to be recognized")
	}
}
