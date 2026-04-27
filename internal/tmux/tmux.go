// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// PaneInfo holds the tmux/psmux session and window name for a pane.
type PaneInfo struct {
	PanePID     int
	PaneID      string // tmux pane unique ID, e.g. "%5"
	SessionName string
	WindowName  string
}

// tmuxBin returns the first available tmux-compatible binary, or "" if none found.
// Set CLAUDE_WATCH_TMUX_BIN to override (e.g. for a custom build or alternate path).
func tmuxBin() string {
	if bin := os.Getenv("CLAUDE_WATCH_TMUX_BIN"); bin != "" {
		return bin
	}
	for _, name := range []string{"psmux", "tmux", "pmux"} {
		if p, err := exec.LookPath(name); err == nil && p != "" {
			return name
		}
	}
	return ""
}

// Available reports whether a tmux-compatible multiplexer is running.
func Available() bool {
	return tmuxBin() != ""
}

// ListPanes queries all panes across all tmux/psmux sessions.
// Returns a map from pane PID to PaneInfo.
// Iterates sessions individually for broad compatibility (psmux, tmux, pmux).
func ListPanes() map[int]PaneInfo {
	bin := tmuxBin()
	if bin == "" {
		return nil
	}

	sessions := listSessions(bin)
	if len(sessions) == 0 {
		return nil
	}

	result := make(map[int]PaneInfo)
	for _, sess := range sessions {
		panes := listSessionPanes(bin, sess)
		for _, p := range panes {
			result[p.PanePID] = p
		}
	}
	return result
}

// listSessions returns the names of all tmux sessions.
func listSessions(bin string) []string {
	out, err := exec.Command(bin, "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return nil
	}
	var sessions []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions
}

// listSessionPanes returns pane info for all panes in a given session.
// Iterates windows individually because psmux's list-panes -s flag only
// returns panes from one window when -F is used (psmux/psmux#218).
func listSessionPanes(bin, sessionName string) []PaneInfo {
	windows := listSessionWindows(bin, sessionName)
	if len(windows) == 0 {
		return nil
	}
	var panes []PaneInfo
	for _, winIdx := range windows {
		target := sessionName + ":" + winIdx
		cmd := exec.Command(bin, "list-panes", "-t", target,
			"-F", "#{pane_pid} #{pane_id} #{session_name} #{window_name}")
		cmd.Env = envWithSession(sessionName)
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, " ", 4)
			if len(parts) < 4 {
				continue
			}
			pid, err := strconv.Atoi(parts[0])
			if err != nil {
				continue
			}
			panes = append(panes, PaneInfo{
				PanePID:     pid,
				PaneID:      parts[1],
				SessionName: parts[2],
				WindowName:  parts[3],
			})
		}
	}
	return panes
}

// listSessionWindows returns the window indices for a given session.
func listSessionWindows(bin, sessionName string) []string {
	cmd := exec.Command(bin, "list-windows", "-t", sessionName, "-F", "#{window_index}")
	cmd.Env = envWithSession(sessionName)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var windows []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			windows = append(windows, line)
		}
	}
	return windows
}

// Resolve finds the tmux session/window for a process by walking its parent
// PID chain and matching against pane PIDs.
// Returns ("session/window", paneID) or ("", "") if no match.
func Resolve(paneMap map[int]PaneInfo, parentPIDs []int) (string, string) {
	for _, ppid := range parentPIDs {
		if info, ok := paneMap[ppid]; ok {
			return info.SessionName + "/" + info.WindowName, info.PaneID
		}
	}
	return "", ""
}

// SwitchToPane attempts to switch the current tmux/psmux client to the given
// session, window, and optionally pane. target should be in "session/window"
// format; paneID is the tmux pane unique ID (e.g. "%5") for precise targeting.
//
// For psmux: switch-client operates at the session level, so we first switch
// sessions, then select the window and pane separately. PSMUX_SESSION_NAME must
// be set so psmux knows which server to contact for cross-session commands.
func SwitchToPane(target string, paneID string) error {
	bin := tmuxBin()
	if bin == "" {
		return fmt.Errorf("no tmux binary available")
	}

	// Parse "session/window" into session and window components
	parts := strings.SplitN(target, "/", 2)
	targetSession := parts[0]
	targetWindow := ""
	if len(parts) == 2 {
		targetWindow = parts[1]
	}

	// 1. Switch to the target session.
	// For psmux, pass just the session name (it strips :window anyway).
	// PSMUX_SESSION_NAME from the environment tells psmux which server to contact.
	cmd := exec.Command(bin, "switch-client", "-t", targetSession)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("switch-client: %s", strings.TrimSpace(string(out)))
	}

	// 2. Select the window in the target session.
	// For psmux, set PSMUX_SESSION_NAME to the target so we talk to the right server.
	if targetWindow != "" {
		wCmd := exec.Command(bin, "select-window", "-t", targetSession+":"+targetWindow)
		wCmd.Env = envWithSession(targetSession)
		_ = wCmd.Run()
	}

	// 3. Select the specific pane if we have a pane ID
	if paneID != "" {
		pCmd := exec.Command(bin, "select-pane", "-t", paneID)
		pCmd.Env = envWithSession(targetSession)
		_ = pCmd.Run()
	}

	return nil
}

// envWithSession returns a copy of os.Environ with PSMUX_SESSION_NAME set to
// the given session name. This tells psmux which server to route the command to.
func envWithSession(session string) []string {
	env := os.Environ()
	found := false
	for i, e := range env {
		if strings.HasPrefix(e, "PSMUX_SESSION_NAME=") {
			env[i] = "PSMUX_SESSION_NAME=" + session
			found = true
			break
		}
	}
	if !found {
		env = append(env, "PSMUX_SESSION_NAME="+session)
	}
	return env
}
