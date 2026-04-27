// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package session

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors provider session directories for file changes using fsnotify.
type Watcher struct {
	scanner  *Scanner
	watcher  *fsnotify.Watcher
	done     chan struct{}
	stopOnce sync.Once
}

// NewWatcher creates a new file watcher attached to the given scanner.
func NewWatcher(scanner *Scanner) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		scanner: scanner,
		watcher: fw,
		done:    make(chan struct{}),
	}, nil
}

// Start begins watching for file system events. It adds watches on all discovered
// session directories and processes events in a goroutine.
func (w *Watcher) Start() error {
	for _, sessionsDir := range w.scanner.SessionsDirs() {
		if sessionsDir == "" {
			continue
		}
		if _, err := os.Stat(sessionsDir); os.IsNotExist(err) {
			continue
		}
		if err := w.addWatchesRecursive(sessionsDir); err != nil {
			log.Printf("warning: could not add watches for %s: %v", sessionsDir, err)
		}
	}

	go w.eventLoop()
	return nil
}

// Stop shuts down the watcher. Safe to call multiple times.
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		w.watcher.Close()
	})
}

func (w *Watcher) eventLoop() {
	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			log.Printf("watcher error: %v", err)
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	path := event.Name

	if event.Has(fsnotify.Create) {
		info, err := os.Stat(path)
		if err != nil {
			return
		}
		if info.IsDir() {
			w.watcher.Add(path)
			return
		}
	}

	if !strings.HasSuffix(path, ".jsonl") {
		return
	}
	if strings.Contains(path, "subagents") {
		return
	}

	if event.Has(fsnotify.Create) {
		// New session file — register and do a full load
		if err := w.scanner.LoadSession(path); err != nil {
			log.Printf("error loading new session %s: %v", path, err)
		}
	} else if event.Has(fsnotify.Write) {
		if err := w.scanner.UpdateSession(path); err != nil {
			log.Printf("error updating session %s: %v", path, err)
		}
	}
}

func (w *Watcher) addWatchesRecursive(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	if err := w.watcher.Add(dir); err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.IsDir() {
			subdir := filepath.Join(dir, entry.Name())
			if err := w.addWatchesRecursive(subdir); err != nil {
				log.Printf("warning: could not watch %s: %v", subdir, err)
			}
		}
	}

	return nil
}
