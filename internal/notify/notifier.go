// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package notify

// Notification is the platform-neutral payload for a user-facing alert.
type Notification struct {
	Title   string
	Message string
}

// Notifier sends user-facing notifications for agent-watch events.
type Notifier interface {
	Notify(Notification) error
	Supported() bool
}

type noopNotifier struct{}

func (noopNotifier) Notify(Notification) error {
	return nil
}

func (noopNotifier) Supported() bool {
	return false
}
