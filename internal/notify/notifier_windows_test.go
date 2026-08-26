// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build windows

package notify

import (
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildToastXML_StructuredContent(t *testing.T) {
	content, err := buildToastXML(Notification{
		Title:    "CLAUDE completed: demo",
		Project:  "demo & tools",
		Source:   "CLAUDE",
		Prompt:   "fix <notification>",
		Response: "done",
	}, "file:///C:/agent-watch.png", "")
	if err != nil {
		t.Fatalf("buildToastXML returned an error: %v", err)
	}

	var document toastDocument
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("generated invalid XML: %v", err)
	}

	if document.Visual.Binding.Template != "ToastGeneric" {
		t.Fatalf("unexpected template: %q", document.Visual.Binding.Template)
	}
	if len(document.Visual.Binding.Text) != 5 {
		t.Fatalf("expected 5 text elements, got %d", len(document.Visual.Binding.Text))
	}
	if document.Visual.Binding.Text[1].Value != "CLAUDE" {
		t.Fatalf("unexpected source text: %q", document.Visual.Binding.Text[1].Value)
	}
	if strings.Contains(string(content), "fix <notification>") {
		t.Fatal("expected XML-sensitive prompt content to be escaped")
	}
	if document.Visual.Binding.Image == nil ||
		document.Visual.Binding.Image.Placement != "appLogoOverride" ||
		document.Visual.Binding.Image.Crop != "circle" {
		t.Fatalf("unexpected toast image: %#v", document.Visual.Binding.Image)
	}
	if document.Visual.Binding.Text[4].Value != "Agent Watch" {
		t.Fatalf("unexpected attribution: %q", document.Visual.Binding.Text[4].Value)
	}
}

func TestBuildToastXML_MessageFallback(t *testing.T) {
	content, err := buildToastXML(Notification{
		Title:   "agent-watch test notification",
		Message: "Windows notifications are enabled.",
	}, "", "")
	if err != nil {
		t.Fatalf("buildToastXML returned an error: %v", err)
	}

	var document toastDocument
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("generated invalid XML: %v", err)
	}

	if len(document.Visual.Binding.Text) != 3 {
		t.Fatalf("expected title, message, and attribution elements, got %d", len(document.Visual.Binding.Text))
	}
	if document.Visual.Binding.Text[1].Value != "Windows notifications are enabled." {
		t.Fatalf("unexpected fallback message: %q", document.Visual.Binding.Text[1].Value)
	}
}

func TestBuildToastXML_ProtocolActivation(t *testing.T) {
	launchURI := "agent-watch://activate?port=1234&token=0123456789abcdef0123456789abcdef0123456789abcdef"
	content, err := buildToastXML(Notification{
		Title: "Session complete",
	}, "", launchURI)
	if err != nil {
		t.Fatalf("buildToastXML returned an error: %v", err)
	}

	var document toastDocument
	if err := xml.Unmarshal(content, &document); err != nil {
		t.Fatalf("generated invalid XML: %v", err)
	}
	if document.Launch != launchURI {
		t.Fatalf("expected launch URI %q, got %q", launchURI, document.Launch)
	}
	if document.ActivationType != "protocol" {
		t.Fatalf("expected protocol activation, got %q", document.ActivationType)
	}
}

func TestWindowsNotifier_ActivationBrokerRoutesTargetOnce(t *testing.T) {
	notifier := &windowsNotifier{
		activations: make(chan Activation, 1),
		targets:     make(map[string]activationTarget),
	}
	if err := notifier.startActivationBroker(); err != nil {
		t.Fatalf("startActivationBroker returned an error: %v", err)
	}
	defer notifier.Close()

	activationURI, err := notifier.activationURI(`c:\work\demo`)
	if err != nil {
		t.Fatalf("activationURI returned an error: %v", err)
	}
	parsed, err := url.Parse(activationURI)
	if err != nil {
		t.Fatalf("parse activation URI: %v", err)
	}
	port := parsed.Query().Get("port")
	token := parsed.Query().Get("token")
	if port == "" || token == "" {
		t.Fatal("activation URI did not include a port and token")
	}
	callback := "http://127.0.0.1:" + port + "/activate/" + token

	response, err := http.Post(callback, "text/plain", nil)
	if err != nil {
		t.Fatalf("post activation callback: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("expected status 204, got %d", response.StatusCode)
	}

	select {
	case activation := <-notifier.Activations():
		if activation.Target != `c:\work\demo` {
			t.Fatalf("unexpected activation target: %q", activation.Target)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activation")
	}

	response, err = http.Post(callback, "text/plain", nil)
	if err != nil {
		t.Fatalf("post consumed activation callback: %v", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("expected consumed callback status 404, got %d", response.StatusCode)
	}
}

func TestActivationScriptPostsValidatedCallback(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef0123456789abcdef"
	activated := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", request.Method)
		}
		if request.URL.Path != "/activate/"+token {
			t.Errorf("unexpected activation path: %s", request.URL.Path)
		}
		activated <- struct{}{}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	_, port, ok := strings.Cut(serverURL.Host, ":")
	if !ok {
		t.Fatalf("test server URL did not include a port: %s", server.URL)
	}

	scriptPath := filepath.Join(t.TempDir(), activationScript)
	if err := ensureActivationScript(scriptPath); err != nil {
		t.Fatalf("ensureActivationScript returned an error: %v", err)
	}
	activationURI := "agent-watch://activate?port=" + port + "&token=" + token
	output, err := exec.Command(
		"powershell.exe",
		"-NoProfile",
		"-NonInteractive",
		"-ExecutionPolicy",
		"Bypass",
		"-File",
		scriptPath,
		activationURI,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("activation script failed: %v: %s", err, strings.TrimSpace(string(output)))
	}

	select {
	case <-activated:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for activation script callback")
	}
}

func TestActivationProtocolCommandIsHiddenAndForwardsURI(t *testing.T) {
	t.Setenv("SystemRoot", `C:\Windows`)
	command := activationProtocolCommand(`C:\Users\demo\AppData\Local\agent-watch\activate-notification.ps1`)

	for _, expected := range []string{
		`"C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe"`,
		"-WindowStyle Hidden",
		`-File "C:\Users\demo\AppData\Local\agent-watch\activate-notification.ps1"`,
		`"%1"`,
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("expected command to contain %q, got %q", expected, command)
		}
	}
	if strings.Contains(command, `"%%1"`) {
		t.Fatalf("protocol command retained escaped placeholder: %q", command)
	}
}
