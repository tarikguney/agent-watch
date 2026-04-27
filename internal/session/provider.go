// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"time"

	"github.com/tarikguney/agent-watch/internal/tmux"
)

// ProcessInfo is provider-neutral process metadata for session matching.
type ProcessInfo struct {
	PID        int
	SessionID  string
	Cwd        string
	StartTime  time.Time
	ParentPIDs []int
}

// Provider encapsulates provider-specific session discovery, parsing and process matching.
type Provider interface {
	ID() string
	BaseDir() string
	SessionsDir() string
	Discover() ([]string, error)
	LoadSession(path string, current State) (State, error)
	UpdateSession(path string, current State) (State, error)
	ListProcesses() ([]ProcessInfo, error)
	MatchProcesses(sessions map[string]*State, procs []ProcessInfo, paneMap map[int]tmux.PaneInfo)
}
