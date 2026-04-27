// Copyright 2026 Tarik Guney
// Licensed under the MIT License.
// https://github.com/tarikguney/agent-watch

package parser

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

// Record represents a single JSONL record from a Claude Code session transcript.
type Record struct {
	Type       string          `json:"type"`
	Subtype    string          `json:"subtype,omitempty"`
	UUID       string          `json:"uuid"`
	ParentUUID string          `json:"parentUuid"`
	Timestamp  string          `json:"timestamp"`
	SessionID  string          `json:"sessionId"`
	Cwd        string          `json:"cwd"`
	Message    json.RawMessage `json:"message"`
	IsMeta     bool            `json:"isMeta"`
}

// IsStatusRelevant returns true if this record type is meaningful for status
// derivation. Records like file-history-snapshot, tool_reference, and
// queue-operation are ambiguous (appear during both active and idle phases)
// and should be skipped to preserve the previous status.
// Attachment records ARE relevant — they only appear between user prompts
// and assistant responses, signaling active context loading.
func (r Record) IsStatusRelevant() bool {
	switch r.Type {
	case "user", "assistant", "result", "system", "attachment":
		return true
	default:
		return false
	}
}

// IsSystemInjectedUser returns true if this is a user-type record that was
// injected by the system (bash output, local commands, /clear, etc.) rather
// than a real user prompt that Claude needs to respond to.
func (r Record) IsSystemInjectedUser() bool {
	if r.Type != "user" {
		return false
	}
	if r.IsMeta {
		return true
	}
	// Check message content for system-injected patterns
	mc, err := ParseMessageContent(r)
	if err != nil {
		return false
	}
	blocks, err := ParseContentBlocks(mc)
	if err != nil {
		return false
	}
	for _, b := range blocks {
		// Tool results are system-injected (output from tool execution)
		if b.Type == "tool_result" {
			return true
		}
		if b.Type == "text" && b.Text != "" {
			trimmed := strings.TrimSpace(b.Text)
			if strings.HasPrefix(trimmed, "<") {
				return true
			}
			if strings.HasPrefix(trimmed, "[Request interrupted by user") {
				return true
			}
		}
	}
	return false
}

// IsInterruptRecord returns true if this is a "[Request interrupted by user]" record.
func (r Record) IsInterruptRecord() bool {
	if r.Type != "user" {
		return false
	}
	mc, err := ParseMessageContent(r)
	if err != nil {
		return false
	}
	blocks, err := ParseContentBlocks(mc)
	if err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "text" && strings.HasPrefix(strings.TrimSpace(b.Text), "[Request interrupted by user") {
			return true
		}
	}
	return false
}

// HasToolResult returns true if this user record contains a tool_result content block.
func (r Record) HasToolResult() bool {
	if r.Type != "user" {
		return false
	}
	mc, err := ParseMessageContent(r)
	if err != nil {
		return false
	}
	blocks, err := ParseContentBlocks(mc)
	if err != nil {
		return false
	}
	for _, b := range blocks {
		if b.Type == "tool_result" {
			return true
		}
	}
	return false
}

// MessageContent represents the message field of a record.
type MessageContent struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"`
	Model      string          `json:"model"`
	StopReason *string         `json:"stop_reason,omitempty"`
}

// ContentBlock represents a single block in the content array.
type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ToolInput holds common fields from tool_use input.
type ToolInput struct {
	FilePath    string `json:"file_path"`
	Command     string `json:"command"`
	Pattern     string `json:"pattern"`
	Query       string `json:"query"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

// ToolResultContent represents a tool_result content block.
type ToolResultContent struct {
	Type    string `json:"type"`
	IsError bool   `json:"is_error"`
}

const (
	tailReadSize = 16 * 1024 // 16KB for tail reads
	headReadSize = 4 * 1024  // 4KB for head reads
)

// ReadTail reads the last ~16KB of a file and parses JSONL records from it.
func ReadTail(path string) ([]Record, error) {
	return readChunk(path, tailReadSize, false)
}

// ReadHead reads the first ~4KB of a file and parses JSONL records from it.
func ReadHead(path string) ([]Record, error) {
	return readChunk(path, headReadSize, true)
}

// ReadNewBytes reads bytes appended since the given offset and returns parsed records
// along with the new offset. Only advances offset to the last complete newline so that
// partial lines are not lost between reads.
func ReadNewBytes(path string, offset int64) ([]Record, int64, error) {
	return readNewBytesWithParser(path, offset, parseRecordLine)
}

// ParseLines splits raw bytes on newlines and parses each line as a JSONL Record.
func ParseLines(data []byte) []Record {
	return parseJSONLLines(data, parseRecordLine)
}

// ParseMessageContent unmarshals the Message field of a Record.
func ParseMessageContent(rec Record) (MessageContent, error) {
	var mc MessageContent
	if rec.Message == nil {
		return mc, errors.New("nil message")
	}
	err := json.Unmarshal(rec.Message, &mc)
	return mc, err
}

// ParseContentBlocks parses the content field of a MessageContent.
// Content can be a string or an array of ContentBlock.
func ParseContentBlocks(mc MessageContent) ([]ContentBlock, error) {
	if mc.Content == nil {
		return nil, nil
	}

	// Try as string first
	var s string
	if err := json.Unmarshal(mc.Content, &s); err == nil {
		return []ContentBlock{{Type: "text", Text: s}}, nil
	}

	// Try as array
	var blocks []ContentBlock
	if err := json.Unmarshal(mc.Content, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

// ParseToolInput extracts common fields from a tool_use input block.
func ParseToolInput(raw json.RawMessage) (ToolInput, error) {
	var ti ToolInput
	err := json.Unmarshal(raw, &ti)
	return ti, err
}

func readChunk(path string, size int, fromHead bool) ([]Record, error) {
	return readChunkWithParser(path, size, fromHead, parseRecordLine)
}

func parseRecordLine(line []byte) (Record, error) {
	var rec Record
	err := json.Unmarshal(line, &rec)
	return rec, err
}

func parseJSONLLines[T any](data []byte, parseLine func([]byte) (T, error)) []T {
	var records []T
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rec, err := parseLine([]byte(line))
		if err != nil {
			continue
		}
		records = append(records, rec)
	}
	return records
}

func readNewBytesWithParser[T any](path string, offset int64, parseLine func([]byte) (T, error)) ([]T, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, offset, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, offset, err
	}

	if info.Size() <= offset {
		return nil, offset, nil
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, offset, err
	}

	data := make([]byte, info.Size()-offset)
	n, err := io.ReadFull(f, data)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, offset, err
	}
	data = data[:n]

	lastNL := bytes.LastIndexByte(data, '\n')
	if lastNL < 0 {
		return nil, offset, nil
	}

	complete := data[:lastNL+1]
	records := parseJSONLLines(complete, parseLine)
	return records, offset + int64(lastNL+1), nil
}

func readChunkWithParser[T any](path string, size int, fromHead bool, parseLine func([]byte) (T, error)) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	readSize := int64(size)
	if info.Size() < readSize {
		readSize = info.Size()
	}

	var data []byte
	if fromHead {
		data = make([]byte, readSize)
		n, err := f.Read(data)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, err
		}
		data = data[:n]
	} else {
		offset := info.Size() - readSize
		if offset < 0 {
			offset = 0
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		data = make([]byte, readSize)
		n, err := io.ReadFull(f, data)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		data = data[:n]
	}

	return parseJSONLLines(data, parseLine), nil
}
