package main

import "testing"

func TestNormalizeProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{name: "default empty", input: "", want: "all"},
		{name: "all", input: "all", want: "all"},
		{name: "claude", input: "claude", want: "claude"},
		{name: "copilot", input: "copilot", want: "copilot"},
		{name: "trim and normalize", input: "  CoPiLoT  ", want: "copilot"},
		{name: "invalid", input: "other", wantError: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeProvider(tc.input)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %q, got %q", tc.want, got)
			}
		})
	}
}

func TestNewScannerSelectsProvider(t *testing.T) {
	t.Parallel()

	scanner, err := newScanner("claude", "C:\\tmp\\claude", "C:\\tmp\\copilot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scanner.ProviderID() != "claude" {
		t.Fatalf("expected claude provider, got %q", scanner.ProviderID())
	}
	if scanner.ClaudeDir() != "C:\\tmp\\claude" {
		t.Fatalf("expected claude dir, got %q", scanner.ClaudeDir())
	}

	allScanner, err := newScanner("all", "C:\\tmp\\claude", "C:\\tmp\\copilot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if allScanner.ProviderID() != "all" {
		t.Fatalf("expected all provider, got %q", allScanner.ProviderID())
	}

	copilotScanner, err := newScanner("copilot", "C:\\tmp\\claude", "C:\\tmp\\copilot")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if copilotScanner.ProviderID() != "copilot" {
		t.Fatalf("expected copilot provider, got %q", copilotScanner.ProviderID())
	}
	if copilotScanner.ClaudeDir() != "C:\\tmp\\copilot" {
		t.Fatalf("expected copilot dir, got %q", copilotScanner.ClaudeDir())
	}
}

func TestNewScannerInvalidProvider(t *testing.T) {
	t.Parallel()

	_, err := newScanner("unknown", "claude", "copilot")
	if err == nil {
		t.Fatalf("expected error for invalid provider")
	}
	want := `invalid --provider "unknown" (must be one of: all, claude, copilot)`
	if err.Error() != want {
		t.Fatalf("expected %q, got %q", want, err.Error())
	}
}
