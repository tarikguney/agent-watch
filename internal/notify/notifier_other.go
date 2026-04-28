// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build !windows

package notify

// NewWindowsNotifier returns a no-op notifier outside Windows.
func NewWindowsNotifier() Notifier {
	return noopNotifier{}
}
