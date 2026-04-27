package session

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/tarikguney/agent-watch/internal/parser"
)

func TestDeriveStatus_AllScenarios(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name    string
		rec     parser.Record
		isError bool
		running bool
		want    Status
	}{
		{
			name: "system-injected + running",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Add(-30 * time.Minute).Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":"<local-command-stdout>Copied to clipboard</local-command-stdout>"}`),
			},
			running: true,
			want:    StatusIdle,
		},
		{
			name: "fresh real user prompt + no process",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":"fix the bug"}`),
			},
			running: false,
			want:    StatusResponding,
		},
		{
			name: "fresh real user prompt + running = Thinking (pre-assistant gap)",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":"fix the bug"}`),
			},
			running: true,
			want:    StatusThinking,
		},
		{
			name: "old real user prompt + running",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Add(-5 * time.Minute).Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":"fix the bug"}`),
			},
			running: true,
			want:    StatusIdle,
		},
		{
			name: "tool result + running = Responding (Claude processing result)",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}`),
			},
			running: true,
			want:    StatusResponding,
		},
		{
			name: "tool result + no process = Responding",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}`),
			},
			running: false,
			want:    StatusResponding,
		},
		// --- stop_reason-aware assistant scenarios ---
		{
			name: "assistant tool_use with stop_reason=tool_use => ToolUse",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Read","input":{}}]}`),
			},
			running: true,
			want:    StatusToolUse,
		},
		{
			name: "assistant thinking with stop_reason=tool_use => ToolUse (earlier block in tool_use response)",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"let me check"}]}`),
			},
			running: true,
			want:    StatusToolUse,
		},
		{
			name: "assistant thinking with stop_reason=null => Thinking (actively reasoning)",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":null,"content":[{"type":"thinking","thinking":""}]}`),
			},
			running: true,
			want:    StatusThinking,
		},
		{
			name: "assistant text with stop_reason=null => Streaming (mid-response)",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":null,"content":[{"type":"text","text":"Here is my an"}]}`),
			},
			running: true,
			want:    StatusStreaming,
		},
		{
			name: "assistant text with stop_reason=end_turn => Idle (finished)",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":"end_turn","content":[{"type":"text","text":"Here is my answer"}]}`),
			},
			running: true,
			want:    StatusIdle,
		},
		{
			name: "assistant thinking with stop_reason=end_turn => Idle",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":"end_turn","content":[{"type":"thinking","thinking":"done"}]}`),
			},
			running: true,
			want:    StatusIdle,
		},
		{
			name: "assistant text with stop_reason=stop_sequence => Idle",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","stop_reason":"stop_sequence","content":[{"type":"text","text":"ok"}]}`),
			},
			running: true,
			want:    StatusIdle,
		},
		// --- Backwards compatibility: no stop_reason ---
		{
			name: "assistant tool_use without stop_reason => fallback Responding (has tool_use block)",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","content":[{"type":"tool_use","name":"Read","input":{}}]}`),
			},
			running: true,
			want:    StatusToolUse,
		},
		{
			name: "assistant text-only without stop_reason => fallback Responding (no stop_reason, text block)",
			rec: parser.Record{
				Type:      "assistant",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"done"}]}`),
			},
			running: true,
			want:    StatusStreaming,
		},
		// --- Other terminal states ---
		{
			name: "interrupt + running",
			rec: parser.Record{
				Type:      "user",
				Timestamp: now.Format(time.RFC3339Nano),
				Message:   json.RawMessage(`{"role":"user","content":"[Request interrupted by user]"}`),
			},
			running: true,
			want:    StatusInterrupted,
		},
		{
			name: "result type always Done",
			rec: parser.Record{
				Type:      "result",
				Timestamp: now.Format(time.RFC3339Nano),
			},
			running: true,
			want:    StatusDone,
		},
		// --- Attachment records ---
		{
			name: "attachment + running => Thinking (context loading before response)",
			rec: parser.Record{
				Type:      "attachment",
				Timestamp: now.Format(time.RFC3339Nano),
			},
			running: true,
			want:    StatusThinking,
		},
		{
			name: "attachment + no process => Idle (stale)",
			rec: parser.Record{
				Type:      "attachment",
				Timestamp: now.Format(time.RFC3339Nano),
			},
			running: false,
			want:    StatusIdle,
		},
		// --- System records ---
		{
			name: "system turn_duration => Idle",
			rec: parser.Record{
				Type:      "system",
				Subtype:   "turn_duration",
				Timestamp: now.Format(time.RFC3339Nano),
			},
			running: true,
			want:    StatusIdle,
		},
		{
			name: "system local_command => Idle (default)",
			rec: parser.Record{
				Type:      "system",
				Subtype:   "local_command",
				Timestamp: now.Format(time.RFC3339Nano),
			},
			running: true,
			want:    StatusIdle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DeriveStatus(tt.rec, tt.isError, now, tt.running)
			if got != tt.want {
				fmt.Printf("  FAIL: got %s, want %s\n", got, tt.want)
			}
			if got != tt.want {
				t.Errorf("got %s, want %s", got, tt.want)
			}
		})
	}
}
