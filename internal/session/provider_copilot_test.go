package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCopilotProvider_DiscoverLoadUpdate(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "session-state", "sess-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}

	workspace := "" +
		"id: sess-1\n" +
		"cwd: C:\\\\src\\\\watch-target\n" +
		"repository: watch-target\n" +
		"branch: main\n" +
		"summary: investigate flaky test\n" +
		"created_at: 2026-01-02T03:04:05Z\n" +
		"updated_at: 2026-01-02T03:04:08Z\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte(workspace), 0644); err != nil {
		t.Fatal(err)
	}

	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	initialEvents := "" +
		`{"type":"session.start","id":"e1","timestamp":"2026-01-02T03:04:05Z","data":{"sessionId":"sess-1","startTime":"2026-01-02T03:04:05Z"}}` + "\n" +
		`{"type":"user.message","id":"e2","timestamp":"2026-01-02T03:04:06Z","data":{"content":"Investigate flaky test"}}` + "\n" +
		`{"type":"assistant.turn_start","id":"e3","timestamp":"2026-01-02T03:04:07Z","data":{}}` + "\n" +
		`{"type":"assistant.message","id":"e4","timestamp":"2026-01-02T03:04:08Z","data":{"content":"I will inspect main.go","toolRequests":[{"toolName":"Read","arguments":{"file_path":"main.go"}}]}}` + "\n" +
		`{"type":"tool.execution_start","id":"e5","timestamp":"2026-01-02T03:04:09Z","data":{"toolName":"Read","arguments":{"file_path":"main.go"}}}` + "\n"
	if err := os.WriteFile(eventsPath, []byte(initialEvents), 0644); err != nil {
		t.Fatal(err)
	}

	provider := NewCopilotProvider(root)
	discovered, err := provider.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(discovered) != 1 || discovered[0] != eventsPath {
		t.Fatalf("unexpected discover result: %v", discovered)
	}

	state, err := provider.LoadSession(eventsPath, State{})
	if err != nil {
		t.Fatal(err)
	}
	if state.SessionID != "sess-1" {
		t.Fatalf("SessionID: got %q", state.SessionID)
	}
	if state.Cwd != `C:\\src\\watch-target` {
		t.Fatalf("Cwd: got %q", state.Cwd)
	}
	if state.ProjectName != `watch-target` {
		t.Fatalf("ProjectName: got %q", state.ProjectName)
	}
	if state.OriginalTask != "Investigate flaky test" {
		t.Fatalf("OriginalTask: got %q", state.OriginalTask)
	}
	if state.LastPrompt != "Investigate flaky test" {
		t.Fatalf("LastPrompt: got %q", state.LastPrompt)
	}
	if state.LastResponse != "I will inspect main.go" {
		t.Fatalf("LastResponse: got %q", state.LastResponse)
	}
	if state.CurrentAction != "Reading main.go" {
		t.Fatalf("CurrentAction: got %q", state.CurrentAction)
	}
	if state.Status != StatusToolUse {
		t.Fatalf("Status: got %s", state.Status)
	}
	if state.StartTime.IsZero() {
		t.Fatal("expected non-zero start time")
	}
	if state.FileOffset == 0 {
		t.Fatal("expected file offset to be set")
	}

	appendEvents(t, eventsPath,
		`{"type":"tool.execution_complete","id":"e6","timestamp":"2026-01-02T03:04:10Z","data":{"success":true}}`+"\n",
	)
	updated, err := provider.UpdateSession(eventsPath, state)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusResponding {
		t.Fatalf("status after tool.execution_complete: got %s", updated.Status)
	}

	appendEvents(t, eventsPath,
		`{"type":"assistant.turn_end","id":"e7","timestamp":"2026-01-02T03:04:11Z","data":{}}`+"\n",
	)
	updated, err = provider.UpdateSession(eventsPath, updated)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusIdle {
		t.Fatalf("status after assistant.turn_end: got %s", updated.Status)
	}
}

func TestCopilotProvider_MatchProcessesUsesLockFiles(t *testing.T) {
	root := t.TempDir()
	sessionDir := filepath.Join(root, "session-state", "sess-1")
	if err := os.MkdirAll(sessionDir, 0755); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "inuse.321.lock"), []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	state := &State{
		FilePath:            eventsPath,
		SessionID:           "sess-1",
		LastRecordType:      "user.message",
		LastRecordTimestamp: now.Format(time.RFC3339Nano),
		Status:              StatusResponding,
	}
	sessions := map[string]*State{eventsPath: state}

	provider := NewCopilotProvider(root)
	provider.MatchProcesses(sessions, []ProcessInfo{
		{
			PID:        321,
			SessionID:  "sess-1",
			Cwd:        `C:\\src\\watch-target`,
			StartTime:  now,
			ParentPIDs: []int{999},
		},
	}, nil)

	if state.PID != 321 {
		t.Fatalf("PID: got %d", state.PID)
	}
	if state.Cwd != `C:\\src\\watch-target` {
		t.Fatalf("Cwd: got %q", state.Cwd)
	}
	if state.ProjectName != "watch-target" {
		t.Fatalf("ProjectName: got %q", state.ProjectName)
	}
	if state.Status != StatusThinking {
		t.Fatalf("Status: got %s", state.Status)
	}
}

func appendEvents(t *testing.T, path string, events ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if _, err := f.WriteString(event); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}
