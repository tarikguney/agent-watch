// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/tarikguney/agent-watch/internal/session"
	"github.com/tarikguney/agent-watch/internal/tmux"
	"github.com/tarikguney/agent-watch/internal/ui"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	var refresh time.Duration
	var provider string
	var claudeDir string
	var copilotDir string
	var compact bool

	rootCmd := &cobra.Command{
		Use:     "agent-watch",
		Short:   "Monitor Claude Code sessions in real time",
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
		Long: `A zero-setup CLI dashboard for monitoring Claude Code agents.
Discovers sessions automatically from ~/.claude/projects/.

Source: https://github.com/tarikguney/agent-watch`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(provider, claudeDir, copilotDir, refresh, compact)
		},
	}

	rootCmd.Flags().DurationVar(&refresh, "refresh", 1*time.Second, "Dashboard refresh interval")
	rootCmd.Flags().StringVar(&provider, "provider", "all", "Session provider (all|claude|copilot)")
	rootCmd.Flags().StringVar(&claudeDir, "claude-dir", defaultClaudeDir(), "Path to Claude config directory")
	rootCmd.Flags().StringVar(&copilotDir, "copilot-dir", defaultCopilotDir(), "Path to Copilot config directory")
	rootCmd.Flags().BoolVar(&compact, "compact", false, "Compact mode for narrow terminals")

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func run(provider, claudeDir, copilotDir string, refresh time.Duration, compact bool) error {
	// Redirect log output away from stderr so watcher/event-loop warnings
	// don't corrupt the Bubble Tea alt-screen. Failures fall back to discard.
	if closer := redirectLog(); closer != nil {
		defer closer()
	}

	scanner, err := newScanner(provider, claudeDir, copilotDir)
	if err != nil {
		return err
	}

	stopLoading := showLoading("Discovering sessions...")
	if err := scanner.Discover(); err != nil {
		stopLoading()
		return fmt.Errorf("discovery failed: %w", err)
	}
	scanner.LoadAll()
	stopLoading()

	watcher, err := session.NewWatcher(scanner)
	if err != nil {
		return fmt.Errorf("watcher init failed: %w", err)
	}
	if err := watcher.Start(); err != nil {
		return fmt.Errorf("watcher start failed: %w", err)
	}
	defer watcher.Stop()

	stopLoading = showLoading("Checking running processes...")
	refreshProcesses(scanner)
	stopLoading()

	// Background process discovery — keeps session PIDs up to date
	// without blocking the UI loop (PowerShell queries are slow on Windows).
	procDone := make(chan struct{})
	defer close(procDone)
	go func() {
		procTicker := time.NewTicker(2 * time.Second)
		defer procTicker.Stop()
		for {
			select {
			case <-procDone:
				return
			case <-procTicker.C:
				refreshProcesses(scanner)
			}
		}
	}()

	m := ui.NewModel(scanner, compact, refresh)
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err = p.Run()
	return err
}

func newScanner(provider, claudeDir, copilotDir string) (*session.Scanner, error) {
	normalizedProvider, err := normalizeProvider(provider)
	if err != nil {
		return nil, err
	}
	switch normalizedProvider {
	case "all":
		return session.NewScannerWithProvider(session.NewMultiProvider(
			session.NewClaudeProvider(claudeDir),
			session.NewCopilotProvider(copilotDir),
		)), nil
	case "claude":
		return session.NewScannerWithProvider(session.NewClaudeProvider(claudeDir)), nil
	case "copilot":
		return session.NewScannerWithProvider(session.NewCopilotProvider(copilotDir)), nil
	default:
		return nil, fmt.Errorf("invalid --provider %q (must be one of: all, claude, copilot)", provider)
	}
}

func normalizeProvider(provider string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "all":
		return "all", nil
	case "claude":
		return "claude", nil
	case "copilot":
		return "copilot", nil
	default:
		return "", fmt.Errorf("invalid --provider %q (must be one of: all, claude, copilot)", provider)
	}
}

// refreshProcesses discovers running Claude processes and matches them to sessions.
func refreshProcesses(scanner *session.Scanner) {
	if err := scanner.RefreshProcesses(tmux.ListPanes()); err != nil {
		return // silently ignore process discovery errors
	}
	// Load any newly discovered sessions from process matching
	scanner.LoadAll()
}

// showLoading displays an animated spinner with a message on stdout.
// It returns a stop function that halts the animation.
func showLoading(msg string) func() {
	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	style := termenv.Style{}.Foreground(output.Color("#D4A0FF"))
	var once sync.Once
	done := make(chan struct{})
	go func() {
		i := 0
		for {
			select {
			case <-done:
				return
			default:
				frame := style.Styled(frames[i%len(frames)])
				fmt.Fprintf(os.Stdout, "\x1b[H\x1b[2K  %s %s", frame, msg)
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
	}
}

var output = termenv.NewOutput(os.Stdout)

// redirectLog points the standard logger at a file under the user's cache
// directory so internal warnings don't bleed into the Bubble Tea alt-screen.
// Returns a close func, or nil if everything routed to io.Discard.
func redirectLog() func() {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		log.SetOutput(io.Discard)
		return nil
	}
	dir := filepath.Join(cacheDir, "agent-watch")
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.SetOutput(io.Discard)
		return nil
	}
	f, err := os.OpenFile(
		filepath.Join(dir, "agent-watch.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.SetOutput(io.Discard)
		return nil
	}
	log.SetOutput(f)
	return func() { f.Close() }
}

func defaultClaudeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".claude")
	}

	// On Windows, check %APPDATA%\.claude first, fall back to %USERPROFILE%\.claude
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidate := filepath.Join(appData, ".claude")
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
	}

	return filepath.Join(home, ".claude")
}

func defaultCopilotDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("~", ".copilot")
	}

	// On Windows, check %APPDATA%\.copilot first, fall back to %USERPROFILE%\.copilot
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			candidate := filepath.Join(appData, ".copilot")
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate
			}
		}
	}

	return filepath.Join(home, ".copilot")
}
