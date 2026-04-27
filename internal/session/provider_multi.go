// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/tarikguney/agent-watch/internal/tmux"
)

// multiProvider combines multiple providers into a single view.
type multiProvider struct {
	providers []Provider

	mu           sync.RWMutex
	pathProvider map[string]Provider // absolute session file path -> provider
	pidProvider  map[int]Provider    // process pid -> provider
}

// NewMultiProvider creates a provider that aggregates multiple providers.
func NewMultiProvider(providers ...Provider) Provider {
	filtered := make([]Provider, 0, len(providers))
	for _, provider := range providers {
		if provider != nil {
			filtered = append(filtered, provider)
		}
	}
	return &multiProvider{
		providers:     filtered,
		pathProvider:  make(map[string]Provider),
		pidProvider:   make(map[int]Provider),
	}
}

func (m *multiProvider) ID() string {
	return "all"
}

func (m *multiProvider) BaseDir() string {
	return ""
}

func (m *multiProvider) SessionsDir() string {
	if len(m.providers) == 0 {
		return ""
	}
	return m.providers[0].SessionsDir()
}

// SessionsDirs returns all underlying provider session roots.
func (m *multiProvider) SessionsDirs() []string {
	seen := make(map[string]struct{})
	dirs := make([]string, 0, len(m.providers))
	for _, provider := range m.providers {
		dir := provider.SessionsDir()
		if dir == "" {
			continue
		}
		clean := filepath.Clean(dir)
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		dirs = append(dirs, clean)
	}
	return dirs
}

func (m *multiProvider) Discover() ([]string, error) {
	paths := make([]string, 0)
	for _, provider := range m.providers {
		discovered, err := provider.Discover()
		if err != nil {
			return nil, err
		}
		for _, path := range discovered {
			m.registerPath(path, provider)
			paths = append(paths, path)
		}
	}
	return paths, nil
}

func (m *multiProvider) LoadSession(path string, current State) (State, error) {
	provider := m.providerForPath(path)
	if provider == nil {
		return current, fmt.Errorf("no provider for session path %q", path)
	}
	return provider.LoadSession(path, current)
}

func (m *multiProvider) UpdateSession(path string, current State) (State, error) {
	provider := m.providerForPath(path)
	if provider == nil {
		return current, fmt.Errorf("no provider for session path %q", path)
	}
	return provider.UpdateSession(path, current)
}

func (m *multiProvider) ListProcesses() ([]ProcessInfo, error) {
	all := make([]ProcessInfo, 0)
	pidOwner := make(map[int]Provider)
	for _, provider := range m.providers {
		procs, err := provider.ListProcesses()
		if err != nil {
			return nil, err
		}
		for _, proc := range procs {
			pidOwner[proc.PID] = provider
			all = append(all, proc)
		}
	}

	m.mu.Lock()
	m.pidProvider = pidOwner
	m.mu.Unlock()

	return all, nil
}

func (m *multiProvider) MatchProcesses(sessions map[string]*State, procs []ProcessInfo, paneMap map[int]tmux.PaneInfo) {
	for _, provider := range m.providers {
		subSessions := make(map[string]*State)
		for path, state := range sessions {
			if m.providerForPath(path) == provider {
				subSessions[path] = state
			}
		}
		if len(subSessions) == 0 {
			continue
		}

		subProcs := m.procsForProvider(procs, provider)
		provider.MatchProcesses(subSessions, subProcs, paneMap)
	}
}

func (m *multiProvider) registerPath(path string, provider Provider) {
	m.mu.Lock()
	m.pathProvider[filepath.Clean(path)] = provider
	m.mu.Unlock()
}

func (m *multiProvider) providerForPath(path string) Provider {
	cleanPath := filepath.Clean(path)

	m.mu.RLock()
	if provider, ok := m.pathProvider[cleanPath]; ok {
		m.mu.RUnlock()
		return provider
	}
	m.mu.RUnlock()

	var selected Provider
	longest := -1
	for _, provider := range m.providers {
		root := filepath.Clean(provider.SessionsDir())
		if !hasPathPrefix(cleanPath, root) {
			continue
		}
		if len(root) > longest {
			selected = provider
			longest = len(root)
		}
	}

	if selected != nil {
		m.registerPath(cleanPath, selected)
	}
	return selected
}

func (m *multiProvider) procsForProvider(procs []ProcessInfo, provider Provider) []ProcessInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	filtered := make([]ProcessInfo, 0, len(procs))
	for _, proc := range procs {
		owner, ok := m.pidProvider[proc.PID]
		if ok && owner == provider {
			filtered = append(filtered, proc)
		}
	}
	return filtered
}

func hasPathPrefix(path, prefix string) bool {
	if path == "" || prefix == "" {
		return false
	}
	cleanPath := filepath.Clean(path)
	cleanPrefix := filepath.Clean(prefix)

	if runtime.GOOS == "windows" {
		cleanPath = strings.ToLower(cleanPath)
		cleanPrefix = strings.ToLower(cleanPrefix)
	}
	if cleanPath == cleanPrefix {
		return true
	}
	return strings.HasPrefix(cleanPath, cleanPrefix+string(os.PathSeparator))
}
