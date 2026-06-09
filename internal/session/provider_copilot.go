// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tarikguney/agent-watch/internal/parser"
	"github.com/tarikguney/agent-watch/internal/process"
	"github.com/tarikguney/agent-watch/internal/tmux"
)

type copilotProvider struct {
	copilotDir string
}

type copilotWorkspace struct {
	ID         string
	Cwd        string
	Repository string
	Branch     string
	Summary    string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// NewCopilotProvider creates a provider for GitHub Copilot CLI sessions.
func NewCopilotProvider(copilotDir string) Provider {
	return &copilotProvider{copilotDir: copilotDir}
}

func (p *copilotProvider) ID() string {
	return "copilot"
}

func (p *copilotProvider) BaseDir() string {
	return p.copilotDir
}

func (p *copilotProvider) SessionsDir() string {
	return filepath.Join(p.copilotDir, "session-state")
}

func (p *copilotProvider) Discover() ([]string, error) {
	sessionsDir := p.SessionsDir()
	if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
		return nil, nil
	}

	paths := make([]string, 0)
	err := filepath.Walk(sessionsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.EqualFold(info.Name(), "events.jsonl") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

func (p *copilotProvider) LoadSession(path string, current State) (State, error) {
	state := current
	state.FilePath = path
	state.Provider = "copilot"
	processRunning := state.PID > 0
	sessionDir := filepath.Dir(path)

	workspace, err := readCopilotWorkspace(filepath.Join(sessionDir, "workspace.yaml"))
	if err != nil {
		return current, err
	}

	headEvents, err := parser.ReadCopilotHead(path)
	if err != nil {
		return current, err
	}
	tailEvents, err := parser.ReadCopilotTail(path)
	if err != nil {
		return current, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return current, err
	}

	state.SessionID = filepath.Base(sessionDir)
	if workspace.Cwd != "" {
		state.Cwd = workspace.Cwd
	}
	if state.Cwd == "" {
		for _, event := range headEvents {
			if cwd := event.Cwd(); cwd != "" {
				state.Cwd = cwd
				break
			}
		}
	}
	if state.ProjectName == "" {
		if state.Cwd != "" {
			state.ProjectName = projectNameFromCwd(state.Cwd)
		} else if workspace.Repository != "" {
			state.ProjectName = workspace.Repository
		}
	}
	if task := extractFirstCopilotPrompt(headEvents); task != "" {
		state.OriginalTask = task
	} else if workspace.Summary != "" {
		state.OriginalTask = truncate(workspace.Summary, 200)
	}
	if !workspace.CreatedAt.IsZero() {
		state.StartTime = workspace.CreatedAt
	}
	if !workspace.UpdatedAt.IsZero() {
		state.LastUpdate = workspace.UpdatedAt
	}

	now := time.Now()
	for _, event := range tailEvents {
		applyCopilotEvent(&state, event, processRunning, now)
	}

	if state.StartTime.IsZero() {
		for _, event := range headEvents {
			if event.Type == "session.start" {
				if start := event.StartTime(); !start.IsZero() {
					state.StartTime = start
					break
				}
			}
		}
	}
	if state.LastPrompt == "" {
		state.LastPrompt = extractLastCopilotPrompt(tailEvents)
	}
	if state.LastResponse == "" {
		state.LastResponse = extractLastCopilotResponse(tailEvents)
	}
	if state.SessionID == "" {
		state.SessionID = filepath.Base(sessionDir)
	}
	if state.OriginalTask == "" && workspace.Summary != "" {
		state.OriginalTask = truncate(workspace.Summary, 200)
	}
	if state.ProjectName == "" && workspace.Repository != "" {
		state.ProjectName = workspace.Repository
	}
	if state.LastUpdate.IsZero() {
		state.LastUpdate = info.ModTime()
	}
	state.FileOffset = info.Size()
	state.FileModTime = info.ModTime()

	return state, nil
}

func (p *copilotProvider) UpdateSession(path string, current State) (State, error) {
	state := current
	state.FilePath = path
	state.Provider = "copilot"
	processRunning := state.PID > 0

	newEvents, newOffset, err := parser.ReadNewCopilotBytes(path, state.FileOffset)
	if err != nil {
		return current, err
	}
	if len(newEvents) == 0 {
		return state, nil
	}

	now := time.Now()
	for _, event := range newEvents {
		applyCopilotEvent(&state, event, processRunning, now)
	}

	if state.OriginalTask == "" {
		if task := extractFirstCopilotPrompt(newEvents); task != "" {
			state.OriginalTask = task
		}
	}

	info, err := os.Stat(path)
	if err == nil {
		state.FileModTime = info.ModTime()
	} else {
		state.FileModTime = now
	}
	state.FileOffset = newOffset

	if state.ProjectName == "" {
		if state.Cwd != "" {
			state.ProjectName = projectNameFromCwd(state.Cwd)
		}
	}
	if state.SessionID == "" {
		state.SessionID = filepath.Base(filepath.Dir(path))
	}

	return state, nil
}

func (p *copilotProvider) ListProcesses() ([]ProcessInfo, error) {
	procs, err := process.ListCopilot()
	if err != nil {
		return nil, err
	}

	converted := make([]ProcessInfo, 0, len(procs))
	for _, proc := range procs {
		converted = append(converted, ProcessInfo{
			PID:        proc.PID,
			SessionID:  proc.SessionID,
			Cwd:        proc.Cwd,
			StartTime:  proc.StartTime,
			ParentPIDs: proc.ParentPIDs,
		})
	}
	return converted, nil
}

func (p *copilotProvider) MatchProcesses(sessions map[string]*State, procs []ProcessInfo, paneMap map[int]tmux.PaneInfo) {
	live := make(map[int]ProcessInfo, len(procs))
	for _, proc := range procs {
		live[proc.PID] = proc
	}

	for _, state := range sessions {
		state.PID = 0
		state.TmuxSession = ""
		state.TmuxPaneID = ""
		state.TmuxSendTarget = ""
	}

	for path, state := range sessions {
		sessionDir := filepath.Dir(path)
		for _, lockPID := range findCopilotLockPIDs(sessionDir) {
			proc, ok := live[lockPID]
			if !ok {
				continue
			}

			state.PID = proc.PID
			state.Provider = "copilot"
			if proc.Cwd != "" {
				state.Cwd = proc.Cwd
				state.ProjectName = projectNameFromCwd(proc.Cwd)
			}
			if proc.SessionID != "" {
				state.SessionID = proc.SessionID
			} else if state.SessionID == "" {
				state.SessionID = filepath.Base(sessionDir)
			}
			if state.StartTime.IsZero() && !proc.StartTime.IsZero() {
				state.StartTime = proc.StartTime
			}

			tmuxSession, tmuxPaneID, tmuxSendTarget := tmux.Resolve(paneMap, proc.ParentPIDs)
			state.TmuxSession = tmuxSession
			state.TmuxPaneID = tmuxPaneID
			state.TmuxSendTarget = tmuxSendTarget
			break
		}
	}

	now := time.Now()
	for _, state := range sessions {
		if state.LastRecordType == "" {
			continue
		}
		state.Status = deriveCopilotStatus(*state, state.PID > 0, now)
	}
}

func readCopilotWorkspace(path string) (copilotWorkspace, error) {
	var workspace copilotWorkspace

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return workspace, nil
		}
		return workspace, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = trimYAMLScalar(value)
		switch key {
		case "id":
			workspace.ID = value
		case "cwd":
			workspace.Cwd = value
		case "repository":
			workspace.Repository = value
		case "branch":
			workspace.Branch = value
		case "summary":
			workspace.Summary = value
		case "created_at":
			workspace.CreatedAt = parseWorkspaceTime(value)
		case "updated_at":
			workspace.UpdatedAt = parseWorkspaceTime(value)
		}
	}

	if err := scanner.Err(); err != nil {
		return workspace, err
	}
	return workspace, nil
}

func extractFirstCopilotPrompt(events []parser.CopilotEvent) string {
	for _, event := range events {
		if event.Type != "user.message" {
			continue
		}
		if text := truncate(event.MessageText(), 200); text != "" {
			return text
		}
	}
	return ""
}

func extractLastCopilotPrompt(events []parser.CopilotEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "user.message" {
			continue
		}
		if text := truncate(events[i].MessageText(), 200); text != "" {
			return text
		}
	}
	return ""
}

func extractLastCopilotResponse(events []parser.CopilotEvent) string {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != "assistant.message" {
			continue
		}
		if text := truncate(events[i].MessageText(), 200); text != "" {
			return text
		}
	}
	return ""
}

func findCopilotLockPIDs(sessionDir string) []int {
	entries, err := os.ReadDir(sessionDir)
	if err != nil {
		return nil
	}

	pids := make([]int, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "inuse.") || !strings.HasSuffix(name, ".lock") {
			continue
		}
		pidText := strings.TrimSuffix(strings.TrimPrefix(name, "inuse."), ".lock")
		pid, err := strconv.Atoi(pidText)
		if err != nil {
			continue
		}
		pids = append(pids, pid)
	}
	return pids
}

func trimYAMLScalar(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, `'`)
	value = strings.Trim(value, `"`)
	return value
}

func parseWorkspaceTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

func projectNameFromCwd(cwd string) string {
	cleaned := strings.TrimSpace(cwd)
	cleaned = strings.TrimRight(cleaned, "/\\")
	if cleaned == "" {
		return ""
	}

	lastSep := strings.LastIndexAny(cleaned, "/\\")
	if lastSep == -1 || lastSep == len(cleaned)-1 {
		return cleaned
	}
	return cleaned[lastSep+1:]
}
