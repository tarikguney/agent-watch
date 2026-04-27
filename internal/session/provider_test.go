package session

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/tarikguney/agent-watch/internal/tmux"
)

type fakeProvider struct {
	discoverPaths []string
	processes     []ProcessInfo
}

func (f *fakeProvider) ID() string                  { return "fake" }
func (f *fakeProvider) BaseDir() string             { return "C:\\fake" }
func (f *fakeProvider) SessionsDir() string         { return filepath.Join(f.BaseDir(), "projects") }
func (f *fakeProvider) Discover() ([]string, error) { return f.discoverPaths, nil }
func (f *fakeProvider) LoadSession(path string, current State) (State, error) {
	current.FilePath = path
	current.OriginalTask = "loaded"
	current.FileOffset = 1
	return current, nil
}
func (f *fakeProvider) UpdateSession(path string, current State) (State, error) {
	current.FilePath = path
	current.CurrentAction = "updated"
	return current, nil
}
func (f *fakeProvider) ListProcesses() ([]ProcessInfo, error) { return f.processes, nil }
func (f *fakeProvider) MatchProcesses(sessions map[string]*State, procs []ProcessInfo, _ map[int]tmux.PaneInfo) {
	for _, proc := range procs {
		for _, st := range sessions {
			st.PID = proc.PID
		}
	}
}

func TestScanner_UsesProviderAbstraction(t *testing.T) {
	provider := &fakeProvider{
		discoverPaths: []string{"C:\\fake\\projects\\p1\\s1.jsonl"},
		processes: []ProcessInfo{
			{PID: 123, SessionID: "s1", StartTime: time.Now()},
		},
	}
	scanner := NewScannerWithProvider(provider)

	if scanner.ProviderID() != "fake" {
		t.Fatalf("expected provider id fake, got %q", scanner.ProviderID())
	}
	if scanner.SessionsDir() != filepath.Join("C:\\fake", "projects") {
		t.Fatalf("unexpected sessions dir: %s", scanner.SessionsDir())
	}

	if err := scanner.Discover(); err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	sessions := scanner.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 discovered session, got %d", len(sessions))
	}

	if err := scanner.LoadSession(provider.discoverPaths[0]); err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if got := scanner.Sessions()[0].OriginalTask; got != "loaded" {
		t.Fatalf("expected loaded task, got %q", got)
	}

	if err := scanner.UpdateSession(provider.discoverPaths[0]); err != nil {
		t.Fatalf("update failed: %v", err)
	}
	if got := scanner.Sessions()[0].CurrentAction; got != "updated" {
		t.Fatalf("expected updated action, got %q", got)
	}

	if err := scanner.RefreshProcesses(nil); err != nil {
		t.Fatalf("refresh processes failed: %v", err)
	}
	if got := scanner.Sessions()[0].PID; got != 123 {
		t.Fatalf("expected PID 123, got %d", got)
	}
}
