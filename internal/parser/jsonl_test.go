// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package parser

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLines_ValidRecords(t *testing.T) {
	data := []byte(`{"type":"user","uuid":"u1","timestamp":"2025-02-20T09:14:32.441Z","sessionId":"s1","message":{"role":"user","content":"Hello"}}
{"type":"assistant","uuid":"a1","timestamp":"2025-02-20T09:14:33.441Z","sessionId":"s1","message":{"role":"assistant","content":[{"type":"text","text":"Hi there"}]}}
`)
	records := ParseLines(data)
	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}
	if records[0].Type != "user" {
		t.Errorf("expected type 'user', got %q", records[0].Type)
	}
	if records[1].Type != "assistant" {
		t.Errorf("expected type 'assistant', got %q", records[1].Type)
	}
	if records[0].SessionID != "s1" {
		t.Errorf("expected sessionId 's1', got %q", records[0].SessionID)
	}
}

func TestParseLines_SkipsInvalidLines(t *testing.T) {
	data := []byte(`not json
{"type":"user","uuid":"u1","timestamp":"2025-01-01T00:00:00Z","sessionId":"s1","message":{}}
also not json
`)
	records := ParseLines(data)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
}

func TestParseLines_EmptyInput(t *testing.T) {
	records := ParseLines([]byte(""))
	if len(records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(records))
	}
}

func TestParseMessageContent_StringContent(t *testing.T) {
	rec := Record{
		Type:    "user",
		Message: json.RawMessage(`{"role":"user","content":"Hello world"}`),
	}
	mc, err := ParseMessageContent(rec)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := ParseContentBlocks(mc)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 1 || blocks[0].Text != "Hello world" {
		t.Errorf("unexpected blocks: %+v", blocks)
	}
}

func TestParseMessageContent_ArrayContent(t *testing.T) {
	rec := Record{
		Type: "assistant",
		Message: json.RawMessage(`{"role":"assistant","content":[
			{"type":"text","text":"Let me help"},
			{"type":"tool_use","name":"Read","input":{"file_path":"main.go"}}
		]}`),
	}
	mc, err := ParseMessageContent(rec)
	if err != nil {
		t.Fatal(err)
	}
	blocks, err := ParseContentBlocks(mc)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(blocks))
	}
	if blocks[0].Type != "text" || blocks[0].Text != "Let me help" {
		t.Errorf("unexpected first block: %+v", blocks[0])
	}
	if blocks[1].Type != "tool_use" || blocks[1].Name != "Read" {
		t.Errorf("unexpected second block: %+v", blocks[1])
	}
}

func TestParseToolInput(t *testing.T) {
	raw := json.RawMessage(`{"file_path":"src/auth.ts","command":"npm test","pattern":"*.go","query":"JWT","url":"https://example.com","description":"explore auth"}`)
	ti, err := ParseToolInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ti.FilePath != "src/auth.ts" {
		t.Errorf("unexpected file_path: %q", ti.FilePath)
	}
	if ti.Command != "npm test" {
		t.Errorf("unexpected command: %q", ti.Command)
	}
	if ti.Pattern != "*.go" {
		t.Errorf("unexpected pattern: %q", ti.Pattern)
	}
	if ti.Query != "JWT" {
		t.Errorf("unexpected query: %q", ti.Query)
	}
	if ti.URL != "https://example.com" {
		t.Errorf("unexpected url: %q", ti.URL)
	}
	if ti.Description != "explore auth" {
		t.Errorf("unexpected description: %q", ti.Description)
	}
}

func TestIsSystemInjectedUser_ToolResult(t *testing.T) {
	rec := Record{
		Type:    "user",
		Message: json.RawMessage(`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"output"}]}`),
	}
	if !rec.IsSystemInjectedUser() {
		t.Error("expected tool_result user record to be system-injected")
	}
}

func TestIsSystemInjectedUser_RealPrompt(t *testing.T) {
	rec := Record{
		Type:    "user",
		Message: json.RawMessage(`{"role":"user","content":"fix the bug"}`),
	}
	if rec.IsSystemInjectedUser() {
		t.Error("expected real user prompt to NOT be system-injected")
	}
}

func TestIsSystemInjectedUser_IsMeta(t *testing.T) {
	rec := Record{
		Type:    "user",
		IsMeta:  true,
		Message: json.RawMessage(`{"role":"user","content":"some meta content"}`),
	}
	if !rec.IsSystemInjectedUser() {
		t.Error("expected isMeta=true user record to be system-injected")
	}
}

func TestParseMessageContent_NilMessage(t *testing.T) {
	rec := Record{Type: "user"}
	_, err := ParseMessageContent(rec)
	if err == nil {
		t.Error("expected error for nil message")
	}
}

func TestReadHead_And_ReadTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"type":"assistant","uuid":"a","timestamp":"2025-01-01T00:00:00Z","sessionId":"s","message":{"role":"assistant","content":"line"}}`)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0644); err != nil {
		t.Fatal(err)
	}

	headRecords, err := ReadHead(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(headRecords) == 0 {
		t.Error("expected head records")
	}

	tailRecords, err := ReadTail(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(tailRecords) == 0 {
		t.Error("expected tail records")
	}
}

func TestReadNewBytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	line1 := `{"type":"user","uuid":"u1","timestamp":"2025-01-01T00:00:00Z","sessionId":"s","message":{"role":"user","content":"hello"}}` + "\n"
	if err := os.WriteFile(path, []byte(line1), 0644); err != nil {
		t.Fatal(err)
	}

	records, offset, err := ReadNewBytes(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	line2 := `{"type":"assistant","uuid":"a1","timestamp":"2025-01-01T00:00:01Z","sessionId":"s","message":{"role":"assistant","content":"hi"}}` + "\n"
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(line2)
	f.Close()

	records2, newOffset, err := ReadNewBytes(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(records2) != 1 {
		t.Fatalf("expected 1 new record, got %d", len(records2))
	}
	if records2[0].Type != "assistant" {
		t.Errorf("expected 'assistant', got %q", records2[0].Type)
	}
	if newOffset <= offset {
		t.Error("offset should have advanced")
	}
}

func TestReadNewBytes_NoNewData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	line := `{"type":"user","uuid":"u1","timestamp":"2025-01-01T00:00:00Z","sessionId":"s","message":{}}` + "\n"
	os.WriteFile(path, []byte(line), 0644)

	_, offset, _ := ReadNewBytes(path, 0)
	records, newOffset, err := ReadNewBytes(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 0 {
		t.Errorf("expected no new records, got %d", len(records))
	}
	if newOffset != offset {
		t.Error("offset should not change when no new data")
	}
}

func TestReadNewBytes_PartialLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	// Write a complete line followed by a partial line (no trailing newline)
	complete := `{"type":"user","uuid":"u1","timestamp":"2025-01-01T00:00:00Z","sessionId":"s","message":{}}` + "\n"
	partial := `{"type":"assistant","uuid":"a1","timestamp":"2025-01-01T`
	os.WriteFile(path, []byte(complete+partial), 0644)

	records, offset, err := ReadNewBytes(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	// Should only get the complete line — partial line is not committed
	if len(records) != 1 {
		t.Fatalf("expected 1 record (partial line deferred), got %d", len(records))
	}
	if records[0].Type != "user" {
		t.Errorf("expected 'user', got %q", records[0].Type)
	}
	// Offset should point just past the first newline, not EOF
	if offset != int64(len(complete)) {
		t.Errorf("expected offset %d, got %d", len(complete), offset)
	}

	// Now append the rest of the partial line
	rest := `00:00:01Z","sessionId":"s","message":{"role":"assistant","content":"hi"}}` + "\n"
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	f.WriteString(rest)
	f.Close()

	// Second read should pick up the now-complete line
	records2, _, err := ReadNewBytes(path, offset)
	if err != nil {
		t.Fatal(err)
	}
	if len(records2) != 1 {
		t.Fatalf("expected 1 record from completed partial, got %d", len(records2))
	}
	if records2[0].Type != "assistant" {
		t.Errorf("expected 'assistant', got %q", records2[0].Type)
	}
}

func TestParseMessageContent_StopReason(t *testing.T) {
	rec := Record{
		Type:    "assistant",
		Message: json.RawMessage(`{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Read","input":{}}]}`),
	}
	mc, err := ParseMessageContent(rec)
	if err != nil {
		t.Fatal(err)
	}
	if mc.StopReason == nil {
		t.Fatal("expected non-nil stop_reason")
	}
	if *mc.StopReason != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %q", *mc.StopReason)
	}
}

func TestParseMessageContent_StopReasonNull(t *testing.T) {
	rec := Record{
		Type:    "assistant",
		Message: json.RawMessage(`{"role":"assistant","stop_reason":null,"content":[{"type":"thinking","thinking":""}]}`),
	}
	mc, err := ParseMessageContent(rec)
	if err != nil {
		t.Fatal(err)
	}
	// JSON null should result in nil pointer
	if mc.StopReason != nil {
		t.Errorf("expected nil stop_reason for null, got %q", *mc.StopReason)
	}
}

func TestParseMessageContent_StopReasonAbsent(t *testing.T) {
	rec := Record{
		Type:    "assistant",
		Message: json.RawMessage(`{"role":"assistant","content":[{"type":"text","text":"hi"}]}`),
	}
	mc, err := ParseMessageContent(rec)
	if err != nil {
		t.Fatal(err)
	}
	if mc.StopReason != nil {
		t.Errorf("expected nil stop_reason when absent, got %q", *mc.StopReason)
	}
}

func TestRecordSubtype(t *testing.T) {
	data := []byte(`{"type":"system","subtype":"turn_duration","timestamp":"2025-01-01T00:00:00Z"}`)
	records := ParseLines(data)
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Subtype != "turn_duration" {
		t.Errorf("expected subtype 'turn_duration', got %q", records[0].Subtype)
	}
}

func TestIsStatusRelevant(t *testing.T) {
	relevant := []string{"user", "assistant", "result", "system", "attachment"}
	for _, typ := range relevant {
		rec := Record{Type: typ}
		if !rec.IsStatusRelevant() {
			t.Errorf("expected %q to be status-relevant", typ)
		}
	}

	irrelevant := []string{"file-history-snapshot", "tool_reference", "queue-operation"}
	for _, typ := range irrelevant {
		rec := Record{Type: typ}
		if rec.IsStatusRelevant() {
			t.Errorf("expected %q to NOT be status-relevant", typ)
		}
	}
}
