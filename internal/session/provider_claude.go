// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarikguney/agent-watch/internal/parser"
	"github.com/tarikguney/agent-watch/internal/process"
	"github.com/tarikguney/agent-watch/internal/tmux"
)

type claudeProvider struct {
	claudeDir string
}

// NewClaudeProvider creates a provider for the existing Claude transcript/process model.
func NewClaudeProvider(claudeDir string) Provider {
	return &claudeProvider{claudeDir: claudeDir}
}

func (p *claudeProvider) ID() string {
	return "claude"
}

func (p *claudeProvider) BaseDir() string {
	return p.claudeDir
}

func (p *claudeProvider) SessionsDir() string {
	return filepath.Join(p.claudeDir, "projects")
}

func (p *claudeProvider) Discover() ([]string, error) {
	projectsDir := p.SessionsDir()
	if _, err := os.Stat(projectsDir); os.IsNotExist(err) {
		return nil, nil
	}

	paths := make([]string, 0)
	err := filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip inaccessible paths
		}
		if info.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		if strings.Contains(path, "subagents") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	return paths, err
}

func (p *claudeProvider) LoadSession(path string, current State) (State, error) {
	state := current
	state.FilePath = path
	state.Provider = "claude"
	processRunning := state.PID > 0

	headRecords, err := parser.ReadHead(path)
	if err != nil {
		return current, err
	}
	originalTask := ExtractOriginalTask(headRecords)

	tailRecords, err := parser.ReadTail(path)
	if err != nil {
		return current, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return current, err
	}

	now := time.Now()
	lastRec := lastStatusRelevantRecord(tailRecords)

	model := ExtractModel(tailRecords)
	action := ""
	for i := len(tailRecords) - 1; i >= 0; i-- {
		if tailRecords[i].Type == "assistant" {
			a := ExtractLastToolAction(tailRecords[i])
			if a != "" {
				action = a
				break
			}
		}
	}

	isError := CheckLastToolResultError(tailRecords)
	lastAssistantWorking := isAssistantWorking(lastRec)
	lastIsSystemInjected := lastRec.IsSystemInjectedUser()
	lastHasToolResult := lastRec.HasToolResult()
	lastIsInterrupt := lastRec.IsInterruptRecord()
	status := StatusIdle
	if len(tailRecords) > 0 {
		status = DeriveStatus(lastRec, isError, now, processRunning)
	}

	var startTime time.Time
	for _, rec := range headRecords {
		if rec.Timestamp != "" {
			if t, err := time.Parse(time.RFC3339Nano, rec.Timestamp); err == nil {
				startTime = t
				break
			}
		}
	}

	cwd := extractCwd(headRecords)
	lastPrompt := ExtractLastPrompt(tailRecords)
	lastResponse := ExtractLastResponse(tailRecords)

	state.Cwd = cwd
	state.OriginalTask = originalTask
	state.LastPrompt = lastPrompt
	state.LastResponse = lastResponse
	state.CurrentAction = action
	state.Status = status
	state.Model = model
	state.StartTime = startTime
	state.LastUpdate = now
	state.FileOffset = info.Size()
	state.FileModTime = info.ModTime()
	state.LastRecordType = lastRec.Type
	state.LastRecordSubtype = lastRec.Subtype
	state.LastRecordTimestamp = lastRec.Timestamp
	state.LastToolResultError = isError
	state.LastAssistantIsWorking = lastAssistantWorking
	state.LastIsSystemInjectedUser = lastIsSystemInjected
	state.LastHasToolResult = lastHasToolResult
	state.LastIsInterrupt = lastIsInterrupt
	state.LastStopReason = extractStopReason(lastRec)
	state.LastBlockTypes = extractBlockTypes(lastRec)
	if lastRec.SessionID != "" {
		state.SessionID = lastRec.SessionID
	}

	return state, nil
}

func (p *claudeProvider) UpdateSession(path string, current State) (State, error) {
	state := current
	state.FilePath = path
	state.Provider = "claude"
	offset := state.FileOffset
	processRunning := state.PID > 0
	prevStatus := state.Status

	newRecords, newOffset, err := parser.ReadNewBytes(path, offset)
	if err != nil {
		return current, err
	}
	if len(newRecords) == 0 {
		return state, nil
	}

	now := time.Now()
	lastRec := lastStatusRelevantRecord(newRecords)
	hasRelevant := lastRec.IsStatusRelevant()

	action := ""
	for i := len(newRecords) - 1; i >= 0; i-- {
		if newRecords[i].Type == "assistant" {
			a := ExtractLastToolAction(newRecords[i])
			if a != "" {
				action = a
			}
			break
		}
	}

	model := ExtractModel(newRecords)
	lastPrompt := ExtractLastPrompt(newRecords)
	lastResponse := ExtractLastResponse(newRecords)
	isError := CheckLastToolResultError(newRecords)
	lastAssistantWorking := isAssistantWorking(lastRec)

	var status Status
	if hasRelevant {
		status = DeriveStatus(lastRec, isError, now, processRunning)
	} else {
		status = prevStatus
	}

	newCwd := ""
	for _, rec := range newRecords {
		if rec.Cwd != "" {
			newCwd = rec.Cwd
			break
		}
	}

	state.FileOffset = newOffset
	state.LastUpdate = now
	state.FileModTime = now
	state.Status = status
	if hasRelevant {
		state.LastRecordType = lastRec.Type
		state.LastRecordSubtype = lastRec.Subtype
		state.LastRecordTimestamp = lastRec.Timestamp
		state.LastToolResultError = isError
		state.LastAssistantIsWorking = lastAssistantWorking
		state.LastIsSystemInjectedUser = lastRec.IsSystemInjectedUser()
		state.LastHasToolResult = lastRec.HasToolResult()
		state.LastIsInterrupt = lastRec.IsInterruptRecord()
		state.LastStopReason = extractStopReason(lastRec)
		state.LastBlockTypes = extractBlockTypes(lastRec)
	}
	if action != "" {
		state.CurrentAction = action
	}
	if model != "" {
		state.Model = model
	}
	if lastPrompt != "" {
		state.LastPrompt = lastPrompt
	}
	if lastResponse != "" {
		state.LastResponse = lastResponse
	}
	if lastRec.SessionID != "" {
		state.SessionID = lastRec.SessionID
	}
	if state.Cwd == "" && newCwd != "" {
		state.Cwd = newCwd
	}

	return state, nil
}

func (p *claudeProvider) ListProcesses() ([]ProcessInfo, error) {
	procs, err := process.ListClaude()
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

func (p *claudeProvider) MatchProcesses(sessions map[string]*State, procs []ProcessInfo, paneMap map[int]tmux.PaneInfo) {
	for key, state := range sessions {
		state.PID = 0
		if strings.HasPrefix(key, "placeholder:") {
			delete(sessions, key)
		}
	}

	fileSessionToPath := make(map[string]string)
	for path := range sessions {
		base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		if base != "" {
			fileSessionToPath[base] = path
		}
	}

	for _, proc := range procs {
		if proc.SessionID == "" {
			continue
		}

		projectDir := ""
		if proc.Cwd != "" {
			encoded := EncodeProjectDir(proc.Cwd)
			candidate := filepath.Join(p.claudeDir, "projects", encoded)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				projectDir = candidate
				entries, _ := os.ReadDir(candidate)
				for _, e := range entries {
					if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
						continue
					}
					if strings.Contains(e.Name(), "subagents") {
						continue
					}
					fullPath := filepath.Join(candidate, e.Name())
					if _, exists := sessions[fullPath]; !exists {
						sessions[fullPath] = &State{FilePath: fullPath}
					}
				}
			}
		}

		if projectDir == "" {
			if origPath, ok := fileSessionToPath[proc.SessionID]; ok {
				projectDir = filepath.Dir(origPath)
			} else {
				targetFile := proc.SessionID + ".jsonl"
				projectsDir := filepath.Join(p.claudeDir, "projects")
				filepath.Walk(projectsDir, func(path string, info os.FileInfo, err error) error {
					if projectDir != "" || err != nil || info == nil || info.IsDir() {
						return nil
					}
					if filepath.Base(path) == targetFile {
						projectDir = filepath.Dir(path)
						if _, exists := sessions[path]; !exists {
							sessions[path] = &State{FilePath: path}
						}
					}
					return nil
				})
			}
		}

		tmuxSession, tmuxPaneID := tmux.Resolve(paneMap, proc.ParentPIDs)

		if projectDir == "" {
			createWaitingSession(sessions, proc, tmuxSession, tmuxPaneID)
			continue
		}

		bestPath := ""
		var bestMod time.Time
		for path, state := range sessions {
			if filepath.Dir(path) == projectDir {
				if state.FileModTime.After(bestMod) || (bestPath == "" && !state.FileModTime.After(bestMod)) {
					bestPath = path
					bestMod = state.FileModTime
				}
			}
		}
		if bestPath != "" && !proc.StartTime.IsZero() && !bestMod.IsZero() && bestMod.Before(proc.StartTime) {
			bestPath = ""
		}

		if bestPath != "" {
			delete(sessions, "placeholder:"+proc.SessionID)
			st := sessions[bestPath]
			st.Provider = "claude"
			st.PID = proc.PID
			if tmuxSession != "" {
				st.TmuxSession = tmuxSession
				st.TmuxPaneID = tmuxPaneID
			}
			if st.StartTime.IsZero() && !proc.StartTime.IsZero() {
				st.StartTime = proc.StartTime
			}
			if proc.Cwd != "" {
				st.Cwd = proc.Cwd
				st.ProjectName = filepath.Base(proc.Cwd)
			}
		} else {
			createWaitingSession(sessions, proc, tmuxSession, tmuxPaneID)
		}
	}

	for _, state := range sessions {
		if state.PID <= 0 || state.LastRecordType == "" {
			continue
		}
		if state.Status == StatusDone {
			continue
		}
		if state.LastToolResultError {
			state.Status = StatusError
			continue
		}
		if state.LastRecordType == "system" && state.LastRecordSubtype == "turn_duration" {
			state.Status = StatusIdle
			continue
		}
		switch state.LastRecordType {
		case "attachment":
			state.Status = StatusThinking
		case "assistant":
			state.Status = rederiveAssistantStatus(state.LastStopReason, state.LastBlockTypes, state.LastAssistantIsWorking)
		case "user":
			if state.LastIsInterrupt {
				state.Status = StatusInterrupted
			} else if state.LastHasToolResult {
				state.Status = StatusResponding
			} else if state.LastIsSystemInjectedUser {
				state.Status = StatusIdle
			} else {
				recTime, err := time.Parse(time.RFC3339Nano, state.LastRecordTimestamp)
				if err == nil && time.Since(recTime) < activeThreshold {
					state.Status = StatusThinking
				} else {
					state.Status = StatusIdle
				}
			}
		}
	}
}

func createWaitingSession(sessions map[string]*State, proc ProcessInfo, tmuxSession, tmuxPaneID string) {
	if proc.Cwd == "" {
		return
	}
	sessions["placeholder:"+proc.SessionID] = &State{
		SessionID:   proc.SessionID,
		Provider:    "claude",
		PID:         proc.PID,
		Cwd:         proc.Cwd,
		ProjectName: filepath.Base(proc.Cwd),
		TmuxSession: tmuxSession,
		TmuxPaneID:  tmuxPaneID,
		Status:      StatusWaiting,
		StartTime:   proc.StartTime,
		LastUpdate:  time.Now(),
		FileModTime: time.Now(),
	}
}

func extractCwd(records []parser.Record) string {
	for _, rec := range records {
		if rec.Cwd != "" {
			return rec.Cwd
		}
	}
	return ""
}

func lastStatusRelevantRecord(records []parser.Record) parser.Record {
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].IsStatusRelevant() {
			return records[i]
		}
	}
	if len(records) > 0 {
		return records[len(records)-1]
	}
	return parser.Record{}
}
