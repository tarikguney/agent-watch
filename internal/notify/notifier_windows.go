// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build windows

package notify

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

const (
	appID            = "agent-watch"
	notifySettingKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Notifications\Settings\` + appID
)

type toastDocument struct {
	XMLName xml.Name    `xml:"toast"`
	Visual  toastVisual `xml:"visual"`
	Audio   toastAudio  `xml:"audio"`
}

type toastVisual struct {
	Binding toastBinding `xml:"binding"`
}

type toastBinding struct {
	Template string      `xml:"template,attr"`
	Image    *toastImage `xml:"image,omitempty"`
	Text     []toastText `xml:"text"`
}

type toastText struct {
	Style     string `xml:"hint-style,attr,omitempty"`
	Wrap      string `xml:"hint-wrap,attr,omitempty"`
	Placement string `xml:"placement,attr,omitempty"`
	Value     string `xml:",chardata"`
}

type toastImage struct {
	Placement string `xml:"placement,attr"`
	Crop      string `xml:"hint-crop,attr,omitempty"`
	Source    string `xml:"src,attr"`
}

type toastAudio struct {
	Source string `xml:"src,attr"`
}

// NewWindowsNotifier returns the native Windows notifier implementation.
// It also ensures Windows is configured to show banners for our app, since
// unpackaged CLI apps default to delivering toasts silently to Action Center.
func NewWindowsNotifier() Notifier {
	iconPath, registrationErr := ensureAppRegistration()
	ensureBannerEnabled()
	return windowsNotifier{
		registrationErr: registrationErr,
		iconURI:         fileURI(iconPath),
	}
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

func (w windowsNotifier) Notify(n Notification) error {
	if w.registrationErr != nil {
		return fmt.Errorf("register Windows notification app: %w", w.registrationErr)
	}

	content, err := buildToastXML(n, w.iconURI)
	if err != nil {
		return err
	}

	xmlBase64 := base64.StdEncoding.EncodeToString(content)
	script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.UI.Notifications.ToastNotification, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] | Out-Null

$content = [Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('%s'))
$document = New-Object Windows.Data.Xml.Dom.XmlDocument
$document.LoadXml($content)
$toast = New-Object Windows.UI.Notifications.ToastNotification $document
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('%s').Show($toast)
`, xmlBase64, appID)

	cmd := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-EncodedCommand",
		encodePowerShellCommand(script),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func (windowsNotifier) Supported() bool {
	return true
}

func buildToastXML(n Notification, iconURI string) ([]byte, error) {
	text := []toastText{
		{Style: "title", Wrap: "true", Value: n.Title},
	}

	if n.Source != "" {
		text = append(text, toastText{
			Style: "captionSubtle",
			Wrap:  "true",
			Value: n.Source,
		})
	}
	if n.Prompt != "" {
		text = append(text, toastText{
			Style: "body",
			Wrap:  "true",
			Value: "Prompt: " + n.Prompt,
		})
	}
	if n.Response != "" {
		text = append(text, toastText{
			Style: "caption",
			Wrap:  "true",
			Value: "Response: " + n.Response,
		})
	}
	if n.Source == "" && n.Prompt == "" && n.Response == "" && n.Message != "" {
		text = append(text, toastText{
			Style: "body",
			Wrap:  "true",
			Value: n.Message,
		})
	}
	text = append(text, toastText{
		Style:     "captionSubtle",
		Placement: "attribution",
		Value:     "Agent Watch",
	})

	document := toastDocument{
		Visual: toastVisual{
			Binding: toastBinding{
				Template: "ToastGeneric",
				Text:     text,
			},
		},
		Audio: toastAudio{
			Source: "ms-winsoundevent:Notification.Default",
		},
	}
	if iconURI != "" {
		document.Visual.Binding.Image = &toastImage{
			Placement: "appLogoOverride",
			Crop:      "circle",
			Source:    iconURI,
		}
	}

	content, err := xml.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("marshal Windows toast XML: %w", err)
	}
	return append([]byte(xml.Header), content...), nil
}

func fileURI(path string) string {
	if path == "" {
		return ""
	}
	slashed := filepath.ToSlash(path)
	if len(slashed) >= 2 && slashed[1] == ':' {
		slashed = "/" + slashed
	}
	return (&url.URL{Scheme: "file", Path: slashed}).String()
}

func encodePowerShellCommand(script string) string {
	encoded := utf16.Encode([]rune(script))
	data := make([]byte, len(encoded)*2)
	for i, value := range encoded {
		binary.LittleEndian.PutUint16(data[i*2:], value)
	}
	return base64.StdEncoding.EncodeToString(data)
}
