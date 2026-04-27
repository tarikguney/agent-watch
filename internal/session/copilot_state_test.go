package session

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/tarikguney/agent-watch/internal/parser"
)

func TestApplyCopilotEvent_StatusMappings(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		initial        State
		event          parser.CopilotEvent
		processRunning bool
		want           Status
		wantAction     string
	}{
		{
			name:  "session start waits",
			event: copilotEvent("session.start", now, `{"sessionId":"sess-1","startTime":"`+now.Format(time.RFC3339Nano)+`"}`),
			want:  StatusWaiting,
		},
		{
			name:           "user message with running process thinks",
			event:          copilotEvent("user.message", now, `{"content":"fix flaky test"}`),
			processRunning: true,
			want:           StatusThinking,
		},
		{
			name:  "assistant turn start thinks",
			event: copilotEvent("assistant.turn_start", now, `{}`),
			want:  StatusThinking,
		},
		{
			name:    "assistant message with tool requests uses tools",
			initial: State{LastTurnOpen: true},
			event:   copilotEvent("assistant.message", now, `{"content":"checking files","toolRequests":[{"toolName":"Read","arguments":{"file_path":"main.go"}}]}`),
			want:    StatusToolUse,
		},
		{
			name:    "assistant content while turn open streams",
			initial: State{LastTurnOpen: true},
			event:   copilotEvent("assistant.message", now, `{"content":"streaming partial response"}`),
			want:    StatusStreaming,
		},
		{
			name:       "tool execution start sets tool action",
			event:      copilotEvent("tool.execution_start", now, `{"toolName":"Read","arguments":{"file_path":"main.go"}}`),
			want:       StatusToolUse,
			wantAction: "Reading main.go",
		},
		{
			name:    "tool execution success responds",
			initial: State{CurrentAction: "Reading main.go"},
			event:   copilotEvent("tool.execution_complete", now, `{"success":true}`),
			want:    StatusResponding,
		},
		{
			name:    "tool execution failure errors",
			initial: State{CurrentAction: "Reading main.go"},
			event:   copilotEvent("tool.execution_complete", now, `{"success":false}`),
			want:    StatusError,
		},
		{
			name:  "session error errors",
			event: copilotEvent("session.error", now, `{"message":"boom"}`),
			want:  StatusError,
		},
		{
			name:  "abort interrupts",
			event: copilotEvent("abort", now, `{}`),
			want:  StatusInterrupted,
		},
		{
			name:  "task complete succeeds",
			event: copilotEvent("session.task_complete", now, `{"success":true}`),
			want:  StatusDone,
		},
		{
			name:  "routine shutdown completes",
			event: copilotEvent("session.shutdown", now, `{"reason":"routine"}`),
			want:  StatusDone,
		},
		{
			name:    "turn end idles",
			initial: State{LastTurnOpen: true, CurrentAction: "Reading main.go"},
			event:   copilotEvent("assistant.turn_end", now, `{}`),
			want:    StatusIdle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.initial
			applyCopilotEvent(&state, tt.event, tt.processRunning, now)
			if state.Status != tt.want {
				t.Fatalf("got %s, want %s", state.Status, tt.want)
			}
			if tt.wantAction != "" && state.CurrentAction != tt.wantAction {
				t.Fatalf("action: got %q, want %q", state.CurrentAction, tt.wantAction)
			}
		})
	}
}

func TestDeriveCopilotStatus_RecomputesForAttachedProcess(t *testing.T) {
	now := time.Now()
	state := State{
		LastRecordType:      "user.message",
		LastRecordTimestamp: now.Format(time.RFC3339Nano),
		Status:              StatusResponding,
	}

	if got := deriveCopilotStatus(state, true, now); got != StatusThinking {
		t.Fatalf("got %s, want %s", got, StatusThinking)
	}
}

func copilotEvent(eventType string, ts time.Time, data string) parser.CopilotEvent {
	return parser.CopilotEvent{
		Type:      eventType,
		Timestamp: ts.Format(time.RFC3339Nano),
		Data:      json.RawMessage(data),
	}
}
