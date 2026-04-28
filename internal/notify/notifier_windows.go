// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build windows

package notify

import (
	"github.com/go-toast/toast"
	"golang.org/x/sys/windows/registry"
)

const (
	appID            = "agent-watch"
	notifySettingKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Notifications\Settings\` + appID
)

type windowsNotifier struct{}

// NewWindowsNotifier returns the native Windows notifier implementation.
// It also ensures Windows is configured to show banners for our app, since
// unpackaged CLI apps default to delivering toasts silently to Action Center.
func NewWindowsNotifier() Notifier {
	ensureBannerEnabled()
	return windowsNotifier{}
}

// ensureBannerEnabled sets the per-app notification keys so the toast banner
// actually appears (not just plays a sound and lands in Action Center).
// Failures are silently ignored — the user can still toggle settings manually.
func ensureBannerEnabled() {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, notifySettingKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer key.Close()

	for _, name := range []string{"ShowBanner", "ShowInActionCenter", "Enabled"} {
		if v, _, err := key.GetIntegerValue(name); err == nil && v == 1 {
			continue
		}
		_ = key.SetDWordValue(name, 1)
	}
}

func (windowsNotifier) Notify(n Notification) error {
	notification := toast.Notification{
		AppID:   appID,
		Title:   n.Title,
		Message: n.Message,
		Audio:   toast.Default,
	}
	return notification.Push()
}

func (windowsNotifier) Supported() bool {
	return true
}
