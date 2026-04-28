// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package ui

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tarikguney/agent-watch/internal/notify"
	"github.com/tarikguney/agent-watch/internal/session"
)

type fakeNotifier struct {
	mu            sync.Mutex
	supported     bool
	notifications []notify.Notification
}

func (f *fakeNotifier) Notify(n notify.Notification) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notifications = append(f.notifications, n)
	return nil
}

func (f *fakeNotifier) Supported() bool {
	return f.supported
}

func (f *fakeNotifier) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.notifications)
}

func (f *fakeNotifier) titleAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notifications[i].Title
}

func (f *fakeNotifier) messageAt(i int) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.notifications[i].Message
}

// waitForCount waits up to 1s for the notifier to receive `want` notifications.
func waitForCount(f *fakeNotifier, want int) bool {
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if f.count() >= want {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return f.count() >= want
}

func TestRender_EmptySessions(t *testing.T) {
	output := Render(nil, false)
	if !strings.Contains(output, "AGENT WATCH") {
		t.Error("expected header 'AGENT WATCH'")
	}
	if !strings.Contains(output, "No active sessions found") {
		t.Error("expected empty state message")
	}
}

func TestRender_WithSessions(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			ProjectName:   "myapp",
			OriginalTask:  "Add auth to API endpoints",
			CurrentAction: "Editing src/middleware.ts",
			Status:        session.StatusResponding,
			StartTime:     now.Add(-12 * time.Minute),
			LastUpdate:    now,
		},
		{
			ProjectName:   "webapp",
			OriginalTask:  "Fix login page CSS",
			CurrentAction: "Running npm test",
			Status:        session.StatusResponding,
			StartTime:     now.Add(-5 * time.Minute),
			LastUpdate:    now,
		},
		{
			ProjectName:   "cli-tool",
			OriginalTask:  "Add --verbose flag",
			CurrentAction: "",
			Status:        session.StatusDone,
			StartTime:     now.Add(-8 * time.Minute),
			LastUpdate:    now.Add(-2 * time.Minute),
		},
	}

	output := Render(sessions, false)

	if !strings.Contains(output, "AGENT WATCH") {
		t.Error("expected header")
	}
	if !strings.Contains(output, "PROJECT") {
		t.Error("expected column header PROJECT")
	}
	if !strings.Contains(output, "PROVIDER") {
		t.Error("expected column header PROVIDER")
	}
	if !strings.Contains(output, "myapp") {
		t.Error("expected project name 'myapp'")
	}
	if !strings.Contains(output, "webapp") {
		t.Error("expected project name 'webapp'")
	}
	if !strings.Contains(output, "cli-tool") {
		t.Error("expected project name 'cli-tool'")
	}
	if !strings.Contains(output, "Completed") {
		t.Error("expected 'Completed' for done session")
	}
}

func TestRender_SortOrder_ByProviderThenPID(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			Provider:    "copilot",
			ProjectName: "copilot-low",
			PID:         100,
			Status:      session.StatusIdle,
			LastUpdate:  now,
			StartTime:   now.Add(-10 * time.Minute),
		},
		{
			Provider:    "claude",
			ProjectName: "claude-high",
			PID:         300,
			Status:      session.StatusResponding,
			LastUpdate:  now,
			StartTime:   now.Add(-5 * time.Minute),
		},
		{
			Provider:    "claude",
			ProjectName: "claude-low",
			PID:         200,
			Status:      session.StatusDone,
			LastUpdate:  now,
			StartTime:   now.Add(-20 * time.Minute),
		},
	}

	output := Render(sessions, false)

	claudeLowIdx := strings.Index(output, "claude-low")
	claudeHighIdx := strings.Index(output, "claude-high")
	copilotLowIdx := strings.Index(output, "copilot-low")

	if claudeLowIdx > claudeHighIdx || claudeHighIdx > copilotLowIdx {
		t.Error("sessions should be sorted by provider first, then PID")
	}
}

func TestRender_Compact(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			ProjectName:   "myapp",
			OriginalTask:  "Add auth",
			CurrentAction: "Editing main.go",
			Status:        session.StatusResponding,
			StartTime:     now.Add(-5 * time.Minute),
			LastUpdate:    now,
		},
	}

	output := Render(sessions, true)
	if !strings.Contains(output, "AGENT WATCH") {
		t.Error("expected header in compact mode")
	}
	// Compact mode should NOT have TASK column
	if strings.Contains(output, "TASK") {
		t.Error("compact mode should not show TASK column")
	}
	if !strings.Contains(output, "myapp") {
		t.Error("expected project name in compact mode")
	}
}

func TestRender_SortOrder(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			ProjectName: "high-pid",
			PID:         300,
			Status:      session.StatusResponding,
			LastUpdate:  now,
			StartTime:   now.Add(-5 * time.Minute),
		},
		{
			ProjectName: "low-pid",
			PID:         100,
			Status:      session.StatusIdle,
			LastUpdate:  now,
			StartTime:   now.Add(-10 * time.Minute),
		},
		{
			ProjectName: "mid-pid",
			PID:         200,
			Status:      session.StatusDone,
			LastUpdate:  now,
			StartTime:   now.Add(-20 * time.Minute),
		},
	}

	output := Render(sessions, false)

	lowIdx := strings.Index(output, "low-pid")
	midIdx := strings.Index(output, "mid-pid")
	highIdx := strings.Index(output, "high-pid")

	if lowIdx > midIdx || midIdx > highIdx {
		t.Error("sessions should be sorted by PID ascending")
	}
}

// TestRenderRow_NeverWraps guards against the "long value pushes a column onto
// a second line" regression. No matter how long the tmux session, project, or
// action text is — and no matter how narrow the terminal — a rendered row must
// be exactly one physical line.
func TestRenderRow_NeverWraps(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			PID:           12345,
			ProjectName:   "sync_calling_concore-commoninfra-very-long-name",
			TmuxSession:   "common-infra/client-enrichment-potential-bug-with-extra-suffix",
			TmuxPaneID:    "%1",
			CurrentAction: "SHARE_FILE=$(mktemp /tmp/copilot-session-$$)\nand a second line that must be flattened",
			Status:        session.StatusToolUse,
			StartTime:     now.Add(-20 * time.Minute),
			LastUpdate:    now,
		},
	}

	// Exercise a range of terminal widths: narrow, typical, wide.
	for _, termW := range []int{60, 80, 100, 140, 200} {
		c := computeCols(sessions, now, termW)
		row := renderRow(sessions[0], now, c, false)
		if strings.Contains(row, "\n") {
			t.Errorf("termW=%d: row contains newline; must be a single line. got:\n%q", termW, row)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		in    string
		width int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hell…"},
		{"", 5, ""},
		{"x", 0, ""},
		{"abcdef", 1, "…"},
		{"line1\nline2", 20, "line1 line2"},
		{"a\r\nb", 20, "a b"},
		{"long enough to cut\nwith newline", 10, "long enou…"},
	}
	for _, tt := range tests {
		got := truncate(tt.in, tt.width)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
		}
	}
}

func TestComputeCols_CapsFlexColumns(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			PID:         100,
			ProjectName: strings.Repeat("p", 80),
			TmuxSession: strings.Repeat("t", 80),
			StartTime:   now.Add(-5 * time.Minute),
			LastUpdate:  now,
		},
	}
	c := computeCols(sessions, now, 200)
	if c.tmux > tmuxColCap {
		t.Errorf("tmux column should be capped at %d, got %d", tmuxColCap, c.tmux)
	}
	if c.project > projectColCap {
		t.Errorf("project column should be capped at %d, got %d", projectColCap, c.project)
	}
	if c.action < len("CURRENT ACTION")+2 {
		t.Errorf("action column should get remaining space, got %d", c.action)
	}
}

func TestStatusPriority(t *testing.T) {
	if statusPriority(session.StatusResponding) >= statusPriority(session.StatusError) {
		t.Error("Responding should have higher priority than Error")
	}
	if statusPriority(session.StatusError) >= statusPriority(session.StatusIdle) {
		t.Error("Error should have higher priority than Idle")
	}
	if statusPriority(session.StatusIdle) >= statusPriority(session.StatusDone) {
		t.Error("Idle should have higher priority than Done")
	}
	// New statuses should have same priority as Responding
	if statusPriority(session.StatusThinking) != statusPriority(session.StatusResponding) {
		t.Error("Thinking should have same priority as Responding")
	}
	if statusPriority(session.StatusToolUse) != statusPriority(session.StatusResponding) {
		t.Error("ToolUse should have same priority as Responding")
	}
	if statusPriority(session.StatusStreaming) != statusPriority(session.StatusResponding) {
		t.Error("Streaming should have same priority as Responding")
	}
}

func TestActionForStatus_NewStatuses(t *testing.T) {
	tests := []struct {
		name     string
		state    session.State
		expected string
	}{
		{
			name:     "Thinking shows Thinking...",
			state:    session.State{Status: session.StatusThinking},
			expected: "Thinking...",
		},
		{
			name:     "ToolUse with action shows action",
			state:    session.State{Status: session.StatusToolUse, CurrentAction: "Reading main.go"},
			expected: "Reading main.go",
		},
		{
			name:     "ToolUse without action shows default",
			state:    session.State{Status: session.StatusToolUse},
			expected: "Executing tool...",
		},
		{
			name:     "Streaming shows Streaming response...",
			state:    session.State{Status: session.StatusStreaming},
			expected: "Streaming response...",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := actionForStatus(tt.state)
			if got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestRender_NewStatuses(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			PID:         101,
			ProjectName: "thinking-project",
			Status:      session.StatusThinking,
			StartTime:   now.Add(-1 * time.Minute),
			LastUpdate:  now,
		},
		{
			PID:           102,
			ProjectName:   "tool-project",
			CurrentAction: "Editing auth.go",
			Status:        session.StatusToolUse,
			StartTime:     now.Add(-2 * time.Minute),
			LastUpdate:    now,
		},
		{
			PID:         103,
			ProjectName: "stream-project",
			Status:      session.StatusStreaming,
			StartTime:   now.Add(-3 * time.Minute),
			LastUpdate:  now,
		},
	}

	output := Render(sessions, false)

	if !strings.Contains(output, "Thinking") {
		t.Error("expected 'Thinking' status in output")
	}
	if !strings.Contains(output, "Tool Use") {
		t.Error("expected 'Tool Use' status in output")
	}
	if !strings.Contains(output, "Streaming") {
		t.Error("expected 'Streaming' status in output")
	}
	if !strings.Contains(output, "Thinking...") {
		t.Error("expected 'Thinking...' action text")
	}
	if !strings.Contains(output, "Editing auth.go") {
		t.Error("expected tool action text")
	}
	if !strings.Contains(output, "Streaming response...") {
		t.Error("expected 'Streaming response...' action text")
	}
}

func TestProcessNotifications_TerminalTransitionSendsNotification(t *testing.T) {
	notifier := &fakeNotifier{supported: true}
	now := time.Now()
	m := Model{
		notifier:             notifier,
		notificationsEnabled: true,
		mutedNotifications:   make(map[string]bool),
		notificationStates: map[string]session.Status{
			`c:\work\demo`: session.StatusResponding,
		},
		notificationStartedAt: now.Add(-1 * time.Minute),
	}

	m.processNotifications([]session.State{
		{
			Provider:     "claude",
			PID:          12345,
			Cwd:          `C:\work\demo`,
			ProjectName:  "demo",
			Status:       session.StatusDone,
			LastPrompt:   "add Windows notifications",
			LastResponse: "Windows notifications are enabled by default on Windows.",
			LastUpdate:   now,
		},
	})

	if !waitForCount(notifier, 1) {
		t.Fatalf("expected 1 notification, got %d", notifier.count())
	}
	if notifier.titleAt(0) != "CLAUDE completed: demo" {
		t.Fatalf("unexpected notification title: %q", notifier.titleAt(0))
	}
	wantMessage := "Project: demo\nPrompt: add Windows notifications\nResponse: Windows notifications are enabled by default on Windows."
	if notifier.messageAt(0) != wantMessage {
		t.Fatalf("unexpected notification message: %q", notifier.messageAt(0))
	}
}

func TestProcessNotifications_GlobalDisableSuppressesNotification(t *testing.T) {
	notifier := &fakeNotifier{supported: true}
	now := time.Now()
	m := Model{
		notifier:             notifier,
		notificationsEnabled: false,
		mutedNotifications:   make(map[string]bool),
		notificationStates: map[string]session.Status{
			`c:\work\demo`: session.StatusResponding,
		},
		notificationStartedAt: now.Add(-1 * time.Minute),
	}

	m.processNotifications([]session.State{
		{
			Provider:    "claude",
			PID:         12345,
			Cwd:         `C:\work\demo`,
			ProjectName: "demo",
			Status:      session.StatusDone,
			LastUpdate:  now,
		},
	})

	// Give async dispatch a moment, then verify suppression.
	time.Sleep(50 * time.Millisecond)
	if notifier.count() != 0 {
		t.Fatalf("expected no notifications when globally disabled, got %d", notifier.count())
	}
}

func TestProcessNotifications_MutedRowSuppressesNotification(t *testing.T) {
	notifier := &fakeNotifier{supported: true}
	now := time.Now()
	key := sessionNotificationKey(session.State{Cwd: `C:\work\demo`})
	m := Model{
		notifier:             notifier,
		notificationsEnabled: true,
		mutedNotifications: map[string]bool{
			key: true,
		},
		notificationStates: map[string]session.Status{
			key: session.StatusResponding,
		},
		notificationStartedAt: now.Add(-1 * time.Minute),
	}

	m.processNotifications([]session.State{
		{
			Provider:    "claude",
			PID:         12345,
			Cwd:         `C:\work\demo`,
			ProjectName: "demo",
			Status:      session.StatusError,
			LastUpdate:  now,
		},
	})

	time.Sleep(50 * time.Millisecond)
	if notifier.count() != 0 {
		t.Fatalf("expected no notifications for muted row, got %d", notifier.count())
	}
}

func TestProcessNotifications_IgnoresSessionsWithoutPID(t *testing.T) {
	notifier := &fakeNotifier{supported: true}
	now := time.Now()
	m := Model{
		notifier:             notifier,
		notificationsEnabled: true,
		mutedNotifications:   make(map[string]bool),
		notificationStates: map[string]session.Status{
			`c:\work\demo`: session.StatusResponding,
		},
		notificationStartedAt: now.Add(-1 * time.Minute),
	}

	m.processNotifications([]session.State{
		{
			Provider:    "claude",
			PID:         0,
			Cwd:         `C:\work\demo`,
			ProjectName: "demo",
			Status:      session.StatusDone,
			LastUpdate:  now,
		},
	})

	time.Sleep(50 * time.Millisecond)
	if notifier.count() != 0 {
		t.Fatalf("expected no notifications for non-running session, got %d", notifier.count())
	}
}

func TestNotificationSessionCandidates_DedupesByKey(t *testing.T) {
	now := time.Now()
	sessions := []session.State{
		{
			Provider:    "claude",
			PID:         0,
			Cwd:         `C:\work\demo`,
			ProjectName: "demo-old",
			Status:      session.StatusDone,
			LastUpdate:  now.Add(-5 * time.Minute),
		},
		{
			Provider:    "claude",
			PID:         999,
			Cwd:         `C:\work\demo`,
			ProjectName: "demo-live",
			Status:      session.StatusError,
			LastUpdate:  now,
		},
	}

	got := notificationSessionCandidates(sessions)
	if len(got) != 1 {
		t.Fatalf("expected 1 deduped session, got %d", len(got))
	}
	if got[0].PID != 999 {
		t.Fatalf("expected live session to win dedupe, got PID %d", got[0].PID)
	}
}

func TestProcessNotifications_IdleAfterWorkSendsNotification(t *testing.T) {
	notifier := &fakeNotifier{supported: true}
	now := time.Now()
	m := Model{
		notifier:             notifier,
		notificationsEnabled: true,
		mutedNotifications:   make(map[string]bool),
		notificationStates: map[string]session.Status{
			`c:\work\demo`: session.StatusResponding,
		},
		notificationStartedAt: now.Add(-1 * time.Minute),
	}

	m.processNotifications([]session.State{
		{
			Provider:     "claude",
			PID:          12345,
			Cwd:          `C:\work\demo`,
			ProjectName:  "demo",
			Status:       session.StatusIdle,
			LastPrompt:   "summarize current changes",
			LastResponse: "Done. I summarized the latest code updates.",
			LastUpdate:   now,
		},
	})

	if !waitForCount(notifier, 1) {
		t.Fatalf("expected 1 notification, got %d", notifier.count())
	}
	if notifier.titleAt(0) != "CLAUDE response complete: demo" {
		t.Fatalf("unexpected notification title: %q", notifier.titleAt(0))
	}
}

func TestProcessNotifications_CooldownSuppressesRapidDuplicate(t *testing.T) {
	notifier := &fakeNotifier{supported: true}
	now := time.Now()
	key := sessionNotificationKey(session.State{Cwd: `C:\work\demo`})
	m := Model{
		notifier:             notifier,
		notificationsEnabled: true,
		mutedNotifications:   make(map[string]bool),
		notificationStates: map[string]session.Status{
			key: session.StatusResponding,
		},
		notificationLastSent: map[string]time.Time{
			key: now,
		},
		notificationStartedAt: now.Add(-1 * time.Minute),
	}

	m.processNotifications([]session.State{
		{
			Provider:    "claude",
			PID:         12345,
			Cwd:         `C:\work\demo`,
			ProjectName: "demo",
			Status:      session.StatusIdle,
			LastUpdate:  now,
		},
	})

	time.Sleep(50 * time.Millisecond)
	if notifier.count() != 0 {
		t.Fatalf("expected cooldown to suppress notification, got %d", notifier.count())
	}
}
