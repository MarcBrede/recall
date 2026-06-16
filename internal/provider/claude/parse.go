package claude

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MarcBrede/recall/internal/trace"
)

// Parser translates Claude JSONL sessions into flat normalized traces.
type Parser struct{}

func (Parser) Parse(ctx context.Context, path string) ([]*trace.FlatSession, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	sourceFile, err := filepath.Abs(path)
	if err != nil {
		sourceFile = path
	}

	state := newParseState(sourceFile)

	reader := bufio.NewReader(file)
	lineNumber := 0
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			lineNumber++
			if parseErr := state.parseLine(lineNumber, strings.TrimSpace(line)); parseErr != nil {
				return nil, fmt.Errorf("%s:%d: %w", path, lineNumber, parseErr)
			}
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return nil, err
	}

	sessions := state.finish(lineNumber)
	for _, session := range sessions {
		if session.ExternalID == "" {
			session.ExternalID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		}
	}

	return sessions, nil
}

type parseState struct {
	sourceFile   string
	segmentIndex int
	current      *trace.FlatSession
	segments     []*trace.FlatSession
}

func newParseState(sourceFile string) *parseState {
	state := &parseState{sourceFile: sourceFile}
	state.current = state.newSession(0, 1, nil)
	return state
}

func (state *parseState) newSession(index int, startLine int, previous *trace.FlatSession) *trace.FlatSession {
	session := &trace.FlatSession{
		Source:          trace.SourceClaude,
		SegmentIndex:    index,
		SourceFile:      state.sourceFile,
		SourceStartLine: startLine,
		Metadata:        trace.Metadata{},
	}
	if previous != nil {
		session.ExternalID = previous.ExternalID
		session.CWD = previous.CWD
		session.GitBranch = previous.GitBranch
		session.Metadata = cloneMetadata(previous.Metadata)
	}
	return session
}

func (state *parseState) parseLine(lineNumber int, line string) error {
	if line == "" {
		return nil
	}

	var rec record
	if err := json.Unmarshal([]byte(line), &rec); err != nil {
		return err
	}

	timestamp, err := parseTimestamp(rec.Timestamp)
	if err != nil {
		return err
	}

	if isCompactionRecord(rec) {
		state.split(lineNumber)
		return nil
	}

	noteRecordMetadata(state.current, rec, timestamp)

	switch rec.Type {
	case "user":
		if summary := continuationSummaryText(rec); summary != "" {
			state.setCompactionSummary(summary, lineNumber)
			return nil
		}
		return parseUserRecord(state.current, rec, lineNumber, timestamp)
	case "assistant":
		return parseAssistantRecord(state.current, rec, lineNumber, timestamp)
	default:
		return nil
	}
}

func (state *parseState) split(lineNumber int) {
	previous := state.current
	state.flush(lineNumber - 1)
	state.segmentIndex++
	state.current = state.newSession(state.segmentIndex, lineNumber+1, previous)
}

func (state *parseState) setCompactionSummary(summary string, lineNumber int) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	state.current.CompactionSummary = summary
	state.current.CompactionSourceLine = lineNumber
	if state.current.SourceStartLine == 0 || lineNumber < state.current.SourceStartLine {
		state.current.SourceStartLine = lineNumber
	}
}

func (state *parseState) finish(lastLine int) []*trace.FlatSession {
	state.flush(lastLine)
	return trace.NormalizeSegmentBoundaries(state.segments)
}

func (state *parseState) flush(endLine int) {
	if state.current == nil {
		return
	}
	if len(state.current.Events) == 0 {
		return
	}
	state.current.SourceEndLine = endLine
	state.segments = append(state.segments, state.current)
}

type record struct {
	Type        string        `json:"type"`
	UUID        string        `json:"uuid"`
	ParentUUID  string        `json:"parentUuid"`
	ForkedFrom  *forkedFrom   `json:"forkedFrom"`
	Timestamp   string        `json:"timestamp"`
	CWD         string        `json:"cwd"`
	SessionID   string        `json:"sessionId"`
	Version     string        `json:"version"`
	GitBranch   string        `json:"gitBranch"`
	UserType    string        `json:"userType"`
	Entrypoint  string        `json:"entrypoint"`
	IsSidechain bool          `json:"isSidechain"`
	Content     string        `json:"content"`
	Message     claudeMessage `json:"message"`
}

type forkedFrom struct {
	SessionID string `json:"sessionId"`
}

type claudeMessage struct {
	ID         string          `json:"id"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    json.RawMessage `json:"content"`
	StopReason string          `json:"stop_reason"`
}

type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Signature string          `json:"signature"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   *bool           `json:"is_error"`
}

func noteRecordMetadata(session *trace.FlatSession, rec record, timestamp time.Time) {
	if session.ExternalID == "" {
		session.ExternalID = rec.SessionID
	}
	if session.CWD == "" {
		session.CWD = rec.CWD
	}
	if session.GitBranch == "" {
		session.GitBranch = rec.GitBranch
	}

	setMetadata(session.Metadata, "version", rec.Version)
	setMetadata(session.Metadata, "user_type", rec.UserType)
	setMetadata(session.Metadata, "entrypoint", rec.Entrypoint)
	if rec.IsSidechain {
		session.Metadata["is_sidechain"] = true
	}
	if rec.ForkedFrom != nil {
		setMetadataIfAbsent(session.Metadata, "forked_from_session_id", rec.ForkedFrom.SessionID)
	}

	noteTimestamp(session, timestamp)
}

func parseUserRecord(session *trace.FlatSession, rec record, lineNumber int, timestamp time.Time) error {
	if len(rec.Message.Content) == 0 {
		return nil
	}

	if text, ok := contentString(rec.Message.Content); ok {
		if shouldSkipUserText(text) {
			return nil
		}
		appendEvent(session, trace.Event{
			Kind:       trace.EventHumanUser,
			SourceLine: lineNumber,
			Timestamp:  timestamp,
			Text:       text,
			RawType:    "user.text",
			EventID:    rec.UUID,
		})
		return nil
	}

	blocks, err := contentBlocks(rec.Message.Content)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if shouldSkipUserText(block.Text) {
				continue
			}
			appendEvent(session, trace.Event{
				Kind:       trace.EventHumanUser,
				SourceLine: lineNumber,
				Timestamp:  timestamp,
				Text:       block.Text,
				RawType:    "user.text",
				EventID:    rec.UUID,
			})
		case "tool_result":
			event := trace.Event{
				Kind:       trace.EventToolOutput,
				SourceLine: lineNumber,
				Timestamp:  timestamp,
				Text:       blockContentText(block.Content),
				RawType:    "user.tool_result",
				EventID:    rec.UUID,
				ToolCallID: block.ToolUseID,
				Metadata:   trace.Metadata{},
			}
			if block.IsError != nil {
				event.Metadata["is_error"] = *block.IsError
			}
			appendEvent(session, event)
		}
	}

	return nil
}

func parseAssistantRecord(session *trace.FlatSession, rec record, lineNumber int, timestamp time.Time) error {
	setMetadata(session.Metadata, "model", rec.Message.Model)

	blocks, err := contentBlocks(rec.Message.Content)
	if err != nil {
		return err
	}
	for _, block := range blocks {
		switch block.Type {
		case "text":
			appendEvent(session, trace.Event{
				Kind:       trace.EventAssistantText,
				SourceLine: lineNumber,
				Timestamp:  timestamp,
				Text:       block.Text,
				RawType:    "assistant.text",
				EventID:    rec.UUID,
			})
		case "thinking":
			kind := trace.EventReasoningMarker
			if strings.TrimSpace(block.Thinking) != "" {
				kind = trace.EventReasoning
			}
			event := trace.Event{
				Kind:       kind,
				SourceLine: lineNumber,
				Timestamp:  timestamp,
				Text:       block.Thinking,
				RawType:    "assistant.thinking",
				EventID:    rec.UUID,
				Metadata:   trace.Metadata{},
			}
			if block.Signature != "" {
				event.Metadata["has_signature"] = true
			}
			appendEvent(session, event)
		case "tool_use":
			appendEvent(session, trace.Event{
				Kind:       trace.EventToolCall,
				SourceLine: lineNumber,
				Timestamp:  timestamp,
				Text:       compactJSON(block.Input),
				ToolName:   block.Name,
				RawType:    "assistant.tool_use",
				EventID:    rec.UUID,
				ToolCallID: block.ID,
			})
		}
	}

	return nil
}

func appendEvent(session *trace.FlatSession, event trace.Event) {
	session.Events = append(session.Events, event)
	noteTimestamp(session, event.Timestamp)
}

func noteTimestamp(session *trace.FlatSession, timestamp time.Time) {
	if timestamp.IsZero() {
		return
	}
	if session.StartedAt.IsZero() || timestamp.Before(session.StartedAt) {
		session.StartedAt = timestamp
	}
	if session.EndedAt.IsZero() || timestamp.After(session.EndedAt) {
		session.EndedAt = timestamp
	}
}

func parseTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func setMetadata(metadata trace.Metadata, key string, value string) {
	if value == "" {
		return
	}
	metadata[key] = value
}

func setMetadataIfAbsent(metadata trace.Metadata, key string, value string) {
	if value == "" {
		return
	}
	if _, exists := metadata[key]; exists {
		return
	}
	metadata[key] = value
}

func contentString(raw json.RawMessage) (string, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return "", false
	}
	return text, true
}

func contentBlocks(raw json.RawMessage) ([]contentBlock, error) {
	var blocks []contentBlock
	if len(raw) == 0 {
		return nil, nil
	}
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return nil, err
	}
	return blocks, nil
}

func shouldSkipUserText(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "Base directory for this skill:") ||
		strings.HasPrefix(trimmed, "<task-notification>") ||
		isLocalCommandBookkeeping(trimmed) ||
		strings.HasPrefix(trimmed, "This session is being continued from a previous conversation that ran out of context.")
}

func isLocalCommandBookkeeping(text string) bool {
	return strings.HasPrefix(text, "<local-command-caveat>") ||
		strings.HasPrefix(text, "<local-command-stdout>") ||
		strings.HasPrefix(text, "<local-command-stderr>") ||
		strings.Contains(text, "<command-name>/mcp</command-name>")
}

func continuationSummaryText(rec record) string {
	if rec.Type != "user" || len(rec.Message.Content) == 0 {
		return ""
	}

	if text, ok := contentString(rec.Message.Content); ok {
		if isContinuationSummaryText(text) {
			return strings.TrimSpace(text)
		}
		return ""
	}

	blocks, err := contentBlocks(rec.Message.Content)
	if err != nil {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			texts = append(texts, strings.TrimSpace(block.Text))
		}
	}
	text := strings.Join(texts, "\n")
	if isContinuationSummaryText(text) {
		return text
	}
	return ""
}

func isContinuationSummaryText(text string) bool {
	return strings.HasPrefix(
		strings.TrimSpace(text),
		"This session is being continued from a previous conversation that ran out of context.",
	)
}

func blockContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	if text, ok := contentString(raw); ok {
		return text
	}

	blocks, err := contentBlocks(raw)
	if err == nil {
		var texts []string
		for _, block := range blocks {
			if strings.TrimSpace(block.Text) != "" {
				texts = append(texts, block.Text)
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}

	return compactJSON(raw)
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var buffer bytes.Buffer
	if err := json.Compact(&buffer, raw); err != nil {
		return string(raw)
	}
	return buffer.String()
}

func isCompactionRecord(rec record) bool {
	if rec.Type != "system" {
		return false
	}
	content := strings.ToLower(strings.TrimSpace(rec.Content))
	return strings.Contains(content, "conversation compacted")
}

func cloneMetadata(metadata trace.Metadata) trace.Metadata {
	if metadata == nil {
		return nil
	}
	clone := make(trace.Metadata, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}
