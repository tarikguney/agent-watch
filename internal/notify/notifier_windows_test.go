// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

//go:build windows

package notify

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestBuildToastXML_StructuredContent(t *testing.T) {
	content, err := buildToastXML(Notification{
		Title:    "CLAUDE completed: demo",
		Project:  "demo & tools",
		Source:   "CLAUDE",
		Prompt:   "fix <notification>",
		Response: "done",
	}, "file:///C:/agent-watch.png")
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
	}, "")
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
