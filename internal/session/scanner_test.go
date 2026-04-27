// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanner_Discover(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "projects", "-Users-tarik-myapp")
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		t.Fatal(err)
	}

	sessionFile := filepath.Join(projectDir, "abc123.jsonl")
	line := `{"type":"user","uuid":"u1","timestamp":"2025-01-01T00:00:00Z","sessionId":"s1","message":{"role":"user","content":"Hello"}}` + "\n"
	if err := os.WriteFile(sessionFile, []byte(line), 0644); err != nil {
		t.Fatal(err)
	}

	scanner := NewScanner(dir)
	if err := scanner.Discover(); err != nil {
		t.Fatal(err)
	}

	sessions := scanner.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].FilePath != sessionFile {
		t.Errorf("expected file path %q, got %q", sessionFile, sessions[0].FilePath)
	}
}

func TestScanner_Discover_NoProjectsDir(t *testing.T) {
	dir := t.TempDir()
	scanner := NewScanner(dir)
	if err := scanner.Discover(); err != nil {
		t.Fatal(err)
	}
	sessions := scanner.Sessions()
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

func TestScanner_LoadSession(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "projects", "-Users-tarik-myapp")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "abc123.jsonl")
	lines := `{"type":"user","uuid":"u1","timestamp":"2025-06-01T10:00:00Z","sessionId":"s1","message":{"role":"user","content":"Add auth to API endpoints"}}
{"type":"assistant","uuid":"a1","timestamp":"2025-06-01T10:01:00Z","sessionId":"s1","message":{"role":"assistant","model":"claude-3.5-sonnet","content":[{"type":"text","text":"I'll help"},{"type":"tool_use","name":"Read","input":{"file_path":"src/auth.ts"}}]}}
`
	os.WriteFile(sessionFile, []byte(lines), 0644)

	scanner := NewScanner(dir)
	scanner.Discover()

	if err := scanner.LoadSession(sessionFile); err != nil {
		t.Fatal(err)
	}

	sessions := scanner.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	s := sessions[0]
	// ProjectName is set by MatchProcesses from OS CWD, not by LoadSession
	if s.OriginalTask != "Add auth to API endpoints" {
		t.Errorf("expected original task 'Add auth to API endpoints', got %q", s.OriginalTask)
	}
	if s.CurrentAction != "Reading auth.ts" {
		t.Errorf("expected action 'Reading auth.ts', got %q", s.CurrentAction)
	}
	if s.Model != "claude-3.5-sonnet" {
		t.Errorf("expected model 'claude-3.5-sonnet', got %q", s.Model)
	}
	if s.SessionID != "s1" {
		t.Errorf("expected session ID 's1', got %q", s.SessionID)
	}
}

func TestScanner_UpdateSession(t *testing.T) {
	dir := t.TempDir()
	projectDir := filepath.Join(dir, "projects", "-Users-tarik-myapp")
	os.MkdirAll(projectDir, 0755)

	sessionFile := filepath.Join(projectDir, "abc123.jsonl")
	line1 := `{"type":"user","uuid":"u1","timestamp":"2025-06-01T10:00:00Z","sessionId":"s1","message":{"role":"user","content":"Fix bug"}}` + "\n"
	os.WriteFile(sessionFile, []byte(line1), 0644)

	scanner := NewScanner(dir)
	scanner.Discover()
	scanner.LoadSession(sessionFile)

	// Append new data
	f, _ := os.OpenFile(sessionFile, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(`{"type":"assistant","uuid":"a1","timestamp":"2025-06-01T10:01:00Z","sessionId":"s1","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit","input":{"file_path":"src/bug.ts"}}]}}` + "\n")
	f.Close()

	if err := scanner.UpdateSession(sessionFile); err != nil {
		t.Fatal(err)
	}

	sessions := scanner.Sessions()
	found := false
	for _, s := range sessions {
		if s.FilePath == sessionFile {
			found = true
			if s.CurrentAction != "Editing bug.ts" {
				t.Errorf("expected action 'Editing bug.ts', got %q", s.CurrentAction)
			}
		}
	}
	if !found {
		t.Error("session not found after update")
	}
}

func TestScanner_MultipleProjects(t *testing.T) {
	dir := t.TempDir()

	for _, proj := range []string{"-Users-tarik-app1", "-Users-tarik-app2"} {
		projDir := filepath.Join(dir, "projects", proj)
		os.MkdirAll(projDir, 0755)
		line := `{"type":"user","uuid":"u1","timestamp":"2025-01-01T00:00:00Z","sessionId":"s1","message":{"role":"user","content":"task"}}` + "\n"
		os.WriteFile(filepath.Join(projDir, "session.jsonl"), []byte(line), 0644)
	}

	scanner := NewScanner(dir)
	scanner.Discover()
	scanner.LoadAll()

	sessions := scanner.Sessions()
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(sessions))
	}

	// ProjectName is set by MatchProcesses from OS CWD, not by LoadSession.
	// Verify sessions were loaded by checking they have non-zero file offsets.
	for _, s := range sessions {
		if s.FileOffset == 0 {
			t.Errorf("expected non-zero FileOffset for session %s", s.FilePath)
		}
	}
}

