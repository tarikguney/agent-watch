package process

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

func TestListClaude(t *testing.T) {
	procs, err := ListClaude()
	if err != nil {
		t.Fatalf("ListClaude error: %v", err)
	}
	fmt.Printf("Found %d Claude processes:\n", len(procs))
	for _, p := range procs {
		fmt.Printf("  PID=%d  SessionID=%s  Cwd=%s  Start=%v\n", p.PID, p.SessionID, p.Cwd, p.StartTime)
	}
}

func TestParseUnixLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		wantPID  int
		wantSID  string
		wantYear int
	}{
		{
			name:     "standard lstart with double-space day",
			line:     "12345  6789 Sun Apr  6 10:53:38 2026 /home/user/.local/share/claude/versions/2.1.92/claude --session-id 2c8d67fc-e59a-4a9e-af4d-2878b83ffe84 --dangerously-skip-permissions --add-dir /sources/project",
			wantPID:  12345,
			wantSID:  "2c8d67fc-e59a-4a9e-af4d-2878b83ffe84",
			wantYear: 2026,
		},
		{
			name:     "two-digit day",
			line:     "  999   100 Mon Apr 16 09:01:02 2026 /usr/local/bin/claude --session-id abcdef01-2345-6789-abcd-ef0123456789 --add-dir /home/user/myapp",
			wantPID:  999,
			wantSID:  "abcdef01-2345-6789-abcd-ef0123456789",
			wantYear: 2026,
		},
		{
			name:    "no add-dir flag",
			line:    "54321  1111 Tue Jan 10 14:30:00 2026 /home/user/.local/bin/claude --session-id 11111111-2222-3333-4444-555555555555 --mcp-config /tmp/claude-mcp-abc.json",
			wantPID: 54321,
			wantSID: "11111111-2222-3333-4444-555555555555",
		},
		{
			name:    "no session id — not a claude session",
			line:    "11111  2222 Thu Feb 20 12:00:00 2026 /usr/bin/some-other-process",
			wantPID: 11111,
			wantSID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := parseUnixLine(tt.line)
			if info.PID != tt.wantPID {
				t.Errorf("PID: got %d, want %d", info.PID, tt.wantPID)
			}
			if info.SessionID != tt.wantSID {
				t.Errorf("SessionID: got %q, want %q", info.SessionID, tt.wantSID)
			}
			if tt.wantYear > 0 && !info.StartTime.IsZero() && info.StartTime.Year() != tt.wantYear {
				t.Errorf("StartTime year: got %d, want %d", info.StartTime.Year(), tt.wantYear)
			}
			// Note: Cwd is read from OS via GetProcessCwd, not from the command line.
			// Can't test a specific value here since the PID doesn't exist.
		})
	}
}

func TestParsePipedLines(t *testing.T) {
	input := `33416|1234|2026-04-06T10:53:38.3082190-06:00|"C:\Users\user\.claude-cli\2.1.92\claude.exe" --session-id 2c8d67fc-e59a-4a9e-af4d-2878b83ffe84 --add-dir Q:/sources/project
23208|5678|2026-04-06T10:55:03.8656050-06:00|"C:\Users\user\.claude-cli\2.1.92\claude.exe" --session-id ee8e923a-24b3-46d5-a97e-153ca47848cd --add-dir Q:/sources/other
`

	results, err := parsePipedLines(input)
	if err != nil {
		t.Fatalf("parsePipedLines error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].PID != 33416 {
		t.Errorf("PID: got %d, want 33416", results[0].PID)
	}
	if results[0].SessionID != "2c8d67fc-e59a-4a9e-af4d-2878b83ffe84" {
		t.Errorf("SessionID: got %q", results[0].SessionID)
	}
	if results[0].StartTime.IsZero() {
		t.Error("expected non-zero StartTime")
	}

	if results[1].PID != 23208 {
		t.Errorf("PID: got %d, want 23208", results[1].PID)
	}
	if results[1].SessionID != "ee8e923a-24b3-46d5-a97e-153ca47848cd" {
		t.Errorf("SessionID: got %q", results[1].SessionID)
	}
}

func TestExtractFlag(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{"session-id present", "--session-id 2c8d67fc-e59a-4a9e-af4d-2878b83ffe84 --other", "2c8d67fc-e59a-4a9e-af4d-2878b83ffe84"},
		{"no match", "--other-flag value", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFlag(sessionIDRe, tt.cmd)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestExtractCopilotSessionID(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want string
	}{
		{
			name: "session-id flag",
			cmd:  `"C:\Users\me\AppData\Roaming\npm\copilot.cmd" --session-id abc123-xyz`,
			want: "abc123-xyz",
		},
		{
			name: "conversation-id flag",
			cmd:  `/usr/local/bin/copilot --conversation-id 2c8d67fc-e59a-4a9e-af4d-2878b83ffe84`,
			want: "2c8d67fc-e59a-4a9e-af4d-2878b83ffe84",
		},
		{
			name: "camel conversation id marker",
			cmd:  `node /opt/copilot/index.js conversationId=conv_ABC123`,
			want: "conv_ABC123",
		},
		{
			name: "none",
			cmd:  `copilot --help`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractCopilotSessionID(tt.cmd)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsCopilotCommand(t *testing.T) {
	tests := []struct {
		name     string
		procName string
		cmdLine  string
		want     bool
	}{
		{
			name:     "name match",
			procName: "copilot.exe",
			cmdLine:  `"C:\Users\me\AppData\Roaming\npm\copilot.cmd"`,
			want:     true,
		},
		{
			name:     "binary path match",
			procName: "node.exe",
			cmdLine:  `"C:\Users\me\AppData\Roaming\npm\github-copilot-cli.exe" chat`,
			want:     true,
		},
		{
			name:     "npm package marker match",
			procName: "node",
			cmdLine:  `node /home/me/.npm/@github/copilot/dist/index.js chat`,
			want:     true,
		},
		{
			name:     "non copilot process",
			procName: "node",
			cmdLine:  `node /home/me/my-script.js`,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCopilotCommand(tt.procName, tt.cmdLine)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestParseNamedPipedLine(t *testing.T) {
	line := `copilot.exe|23208|5678|2026-04-06T10:55:03.8656050-06:00|"C:\Users\user\AppData\Roaming\npm\copilot.cmd" --conversation-id conv_123`
	info, procName, cmdLine, ok := parseNamedPipedLine(line, map[int]int{5678: 1000, 1000: 100})
	if !ok {
		t.Fatal("expected parseNamedPipedLine to succeed")
	}
	if procName != "copilot.exe" {
		t.Fatalf("procName: got %q", procName)
	}
	if info.PID != 23208 {
		t.Fatalf("PID: got %d, want 23208", info.PID)
	}
	if len(info.ParentPIDs) == 0 || info.ParentPIDs[0] != 5678 {
		t.Fatalf("ParentPIDs: got %v, expected first parent 5678", info.ParentPIDs)
	}
	if cmdLine == "" {
		t.Fatal("expected non-empty cmdLine")
	}
}

func TestParseUnixLineBase_ForCopilot(t *testing.T) {
	line := `999  100 Mon Apr 16 09:01:02 2026 /usr/local/bin/copilot --conversation-id conv_456`
	info, cmdLine, ok := parseUnixLineBase(line, map[int]int{100: 1})
	if !ok {
		t.Fatal("expected parseUnixLineBase to succeed")
	}
	if info.PID != 999 {
		t.Fatalf("PID: got %d, want 999", info.PID)
	}
	if !isCopilotCommand("", cmdLine) {
		t.Fatalf("expected cmdLine to match copilot signature: %q", cmdLine)
	}
	if got := extractCopilotSessionID(cmdLine); got != "conv_456" {
		t.Fatalf("extractCopilotSessionID: got %q, want conv_456", got)
	}
}

func TestGetProcessCwd(t *testing.T) {
	// Test with our own process — should return the current working directory
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" {
		t.Skip("GetProcessCwd test only on windows/linux")
	}
	cwd, err := GetProcessCwd(os.Getpid())
	if err != nil {
		t.Fatalf("GetProcessCwd(%d) error: %v", os.Getpid(), err)
	}
	if cwd == "" {
		t.Error("expected non-empty CWD for own process")
	}
	t.Logf("Own process CWD: %s", cwd)
}

func TestListClaude_Integration(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("unsupported platform")
	}
	// Just verify it doesn't error — may return 0 processes in CI
	_, err := ListClaude()
	if err != nil {
		t.Fatalf("ListClaude should not error: %v", err)
	}
}
