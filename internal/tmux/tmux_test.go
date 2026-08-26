package tmux

import "testing"

func TestPromptCommandTarget(t *testing.T) {
	tests := []struct {
		name       string
		bin        string
		sendTarget string
		paneID     string
		want       string
	}{
		{
			name:       "psmux uses pane ID within routed session",
			bin:        "psmux",
			sendTarget: "agent-watch-coding:1.1",
			paneID:     "%2",
			want:       "%2",
		},
		{
			name:       "psmux executable path",
			bin:        `Q:\tools\PSMUX.EXE`,
			sendTarget: "agent-watch-coding:1.1",
			paneID:     "%2",
			want:       "%2",
		},
		{
			name:       "tmux keeps qualified target",
			bin:        "tmux",
			sendTarget: "agent-watch-coding:1.1",
			paneID:     "%2",
			want:       "agent-watch-coding:1.1",
		},
		{
			name:       "pmux keeps qualified target",
			bin:        "pmux",
			sendTarget: "agent-watch-coding:1.1",
			paneID:     "%2",
			want:       "agent-watch-coding:1.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := promptCommandTarget(tt.bin, tt.sendTarget, tt.paneID); got != tt.want {
				t.Fatalf("promptCommandTarget(%q, %q, %q) = %q, want %q",
					tt.bin, tt.sendTarget, tt.paneID, got, tt.want)
			}
		})
	}
}
