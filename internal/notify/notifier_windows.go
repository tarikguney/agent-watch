// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build windows

package notify

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows/registry"
)

const (
	appID            = "agent-watch"
	protocolScheme   = "agent-watch"
	notifySettingKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Notifications\Settings\` + appID
	activationTTL    = 24 * time.Hour
)

type toastDocument struct {
	XMLName        xml.Name    `xml:"toast"`
	Launch         string      `xml:"launch,attr,omitempty"`
	ActivationType string      `xml:"activationType,attr,omitempty"`
	Visual         toastVisual `xml:"visual"`
	Audio          toastAudio  `xml:"audio"`
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

type activationTarget struct {
	target    string
	expiresAt time.Time
}

type windowsNotifier struct {
	registrationErr   error
	iconURI           string
	activationPort    string
	activationProcess uint32
	activations       chan Activation
	server            *http.Server
	listener          net.Listener
	mu                sync.Mutex
	targets           map[string]activationTarget
}

// NewWindowsNotifier returns the native Windows notifier implementation.
// It also ensures Windows is configured to show banners for our app, since
// unpackaged CLI apps default to delivering toasts silently to Action Center.
func NewWindowsNotifier() Notifier {
	iconPath, registrationErr := ensureAppRegistration()
	ensureBannerEnabled()
	window := foregroundWindow()
	notifier := &windowsNotifier{
		registrationErr:   registrationErr,
		iconURI:           fileURI(iconPath),
		activationProcess: windowProcessID(window),
		activations:       make(chan Activation, 16),
		targets:           make(map[string]activationTarget),
	}
	if err := notifier.startActivationBroker(); err != nil && notifier.registrationErr == nil {
		notifier.registrationErr = fmt.Errorf("start notification activation broker: %w", err)
	}
	return notifier
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

func (w *windowsNotifier) Notify(n Notification) error {
	if w.registrationErr != nil {
		return fmt.Errorf("register Windows notification app: %w", w.registrationErr)
	}

	launchURI := ""
	if n.ActivationTarget != "" {
		var err error
		launchURI, err = w.activationURI(n.ActivationTarget)
		if err != nil {
			return fmt.Errorf("create notification activation: %w", err)
		}
	}

	content, err := buildToastXML(n, w.iconURI, launchURI)
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

func (*windowsNotifier) Supported() bool {
	return true
}

func (w *windowsNotifier) Activations() <-chan Activation {
	return w.activations
}

func (w *windowsNotifier) Close() error {
	if w.server == nil {
		return nil
	}
	return w.server.Close()
}

func (w *windowsNotifier) startActivationBroker() error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/activate/", w.handleActivation)
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		listener.Close()
		return fmt.Errorf("resolve activation broker port: %w", err)
	}
	w.listener = listener
	w.activationPort = port
	w.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	go func() {
		_ = w.server.Serve(listener)
	}()
	return nil
}

func (w *windowsNotifier) activationURI(target string) (string, error) {
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)

	now := time.Now()
	w.mu.Lock()
	for existingToken, existingTarget := range w.targets {
		if now.After(existingTarget.expiresAt) {
			delete(w.targets, existingToken)
		}
	}
	w.targets[token] = activationTarget{
		target:    target,
		expiresAt: now.Add(activationTTL),
	}
	w.mu.Unlock()

	activationURL := &url.URL{Scheme: protocolScheme, Host: "activate"}
	query := activationURL.Query()
	query.Set("port", w.activationPort)
	query.Set("token", token)
	if w.activationProcess != 0 {
		query.Set("process", strconv.FormatUint(uint64(w.activationProcess), 10))
	}
	activationURL.RawQuery = query.Encode()
	return activationURL.String(), nil
}

func (w *windowsNotifier) handleActivation(response http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	token := strings.TrimPrefix(request.URL.Path, "/activate/")
	if token == "" || strings.Contains(token, "/") {
		http.NotFound(response, request)
		return
	}

	now := time.Now()
	w.mu.Lock()
	target, ok := w.targets[token]
	if ok && now.After(target.expiresAt) {
		delete(w.targets, token)
		ok = false
	}
	w.mu.Unlock()
	if !ok {
		http.NotFound(response, request)
		return
	}

	select {
	case w.activations <- Activation{Target: target.target}:
		w.mu.Lock()
		delete(w.targets, token)
		w.mu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	case <-time.After(2 * time.Second):
		http.Error(response, "activation queue unavailable", http.StatusServiceUnavailable)
	}
}

func buildToastXML(n Notification, iconURI, launchURI string) ([]byte, error) {
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
		Launch: launchURI,
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
	if launchURI != "" {
		document.ActivationType = "protocol"
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
