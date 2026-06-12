package codex

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

	"github.com/marc-brede/recall/internal/trace"
)

// Parser translates Codex JSONL sessions into flat normalized traces.
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

	return state.finish(lineNumber), nil
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
		Source:          trace.SourceCodex,
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

	if rec.Type == "compacted" {
		summary := compactionSummaryText(rec.Payload)
		state.split(lineNumber)
		if summary != "" {
			state.setCompactionSummary(summary, lineNumber, timestamp)
		}
		return nil
	}

	switch rec.Type {
	case "session_meta":
		return parseSessionMeta(state.current, rec.Payload, timestamp)
	case "turn_context":
		return parseTurnContext(state.current, rec.Payload)
	case "event_msg":
		return parseEventMessage(state.current, rec.Payload, lineNumber, timestamp)
	case "response_item":
		return parseResponseItem(state.current, rec.Payload, lineNumber, timestamp)
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

func (state *parseState) setCompactionSummary(summary string, lineNumber int, timestamp time.Time) {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return
	}
	state.current.CompactionSummary = summary
	state.current.CompactionSourceLine = lineNumber
	if state.current.SourceStartLine == 0 || lineNumber < state.current.SourceStartLine {
		state.current.SourceStartLine = lineNumber
	}
	noteTimestamp(state.current, timestamp)
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
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type sessionMeta struct {
	ID            string `json:"id"`
	ForkedFromID  string `json:"forked_from_id"`
	Timestamp     string `json:"timestamp"`
	CWD           string `json:"cwd"`
	Originator    string `json:"originator"`
	CLIVersion    string `json:"cli_version"`
	Source        string `json:"source"`
	ThreadSource  string `json:"thread_source"`
	ModelProvider string `json:"model_provider"`
}

type turnContext struct {
	CWD            string   `json:"cwd"`
	WorkspaceRoots []string `json:"workspace_roots"`
	CurrentDate    string   `json:"current_date"`
	Timezone       string   `json:"timezone"`
	ApprovalPolicy string   `json:"approval_policy"`
	Model          string   `json:"model"`
}

type eventMessage struct {
	Type           string `json:"type"`
	Message        string `json:"message"`
	Phase          string `json:"phase"`
	MemoryCitation any    `json:"memory_citation"`
}

type responseKind struct {
	Type string `json:"type"`
}

type reasoningItem struct {
	Type             string          `json:"type"`
	Summary          json.RawMessage `json:"summary"`
	EncryptedContent string          `json:"encrypted_content"`
}

type responseToolItem struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	CallID string `json:"call_id"`
	Name   string `json:"name"`

	Arguments json.RawMessage `json:"arguments"`
	Input     json.RawMessage `json:"input"`
	Action    json.RawMessage `json:"action"`
	Output    json.RawMessage `json:"output"`
	Result    json.RawMessage `json:"result"`
	Tools     json.RawMessage `json:"tools"`
}

type compactedPayload struct {
	Message string `json:"message"`
	Summary string `json:"summary"`
}

func parseSessionMeta(session *trace.FlatSession, payload json.RawMessage, timestamp time.Time) error {
	var meta sessionMeta
	if err := json.Unmarshal(payload, &meta); err != nil {
		return err
	}

	if session.ExternalID == "" {
		session.ExternalID = meta.ID
	}
	if session.CWD == "" {
		session.CWD = meta.CWD
	}
	if session.StartedAt.IsZero() {
		startedAt, err := parseTimestamp(meta.Timestamp)
		if err == nil && !startedAt.IsZero() {
			session.StartedAt = startedAt
		} else if !timestamp.IsZero() {
			session.StartedAt = timestamp
		}
	}

	setMetadata(session.Metadata, "forked_from_id", meta.ForkedFromID)
	setMetadata(session.Metadata, "originator", meta.Originator)
	setMetadata(session.Metadata, "cli_version", meta.CLIVersion)
	setMetadata(session.Metadata, "source", meta.Source)
	setMetadata(session.Metadata, "thread_source", meta.ThreadSource)
	setMetadata(session.Metadata, "model_provider", meta.ModelProvider)

	return nil
}

func parseTurnContext(session *trace.FlatSession, payload json.RawMessage) error {
	var context turnContext
	if err := json.Unmarshal(payload, &context); err != nil {
		return err
	}

	if session.CWD == "" {
		session.CWD = context.CWD
	}
	setMetadata(session.Metadata, "model", context.Model)
	setMetadata(session.Metadata, "current_date", context.CurrentDate)
	setMetadata(session.Metadata, "timezone", context.Timezone)
	setMetadata(session.Metadata, "approval_policy", context.ApprovalPolicy)
	if len(context.WorkspaceRoots) > 0 {
		session.Metadata["workspace_roots"] = context.WorkspaceRoots
	}

	return nil
}

func parseEventMessage(session *trace.FlatSession, payload json.RawMessage, lineNumber int, timestamp time.Time) error {
	var msg eventMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		return err
	}

	switch msg.Type {
	case "user_message":
		appendEvent(session, trace.Event{
			Kind:       trace.EventHumanUser,
			SourceLine: lineNumber,
			Timestamp:  timestamp,
			Text:       msg.Message,
			RawType:    "event_msg.user_message",
		})
	case "agent_message":
		event := trace.Event{
			Kind:       trace.EventAssistantText,
			SourceLine: lineNumber,
			Timestamp:  timestamp,
			Text:       msg.Message,
			RawType:    "event_msg.agent_message",
			Metadata:   trace.Metadata{},
		}
		setMetadata(event.Metadata, "phase", msg.Phase)
		if msg.MemoryCitation != nil {
			event.Metadata["memory_citation"] = msg.MemoryCitation
		}
		appendEvent(session, event)
	case "task_started", "task_complete":
		noteTimestamp(session, timestamp)
	}

	return nil
}

func parseResponseItem(session *trace.FlatSession, payload json.RawMessage, lineNumber int, timestamp time.Time) error {
	var kind responseKind
	if err := json.Unmarshal(payload, &kind); err != nil {
		return err
	}

	switch kind.Type {
	case "reasoning":
		return parseReasoning(session, payload, lineNumber, timestamp)
	case "message":
		return nil
	default:
		return parseToolShape(session, payload, lineNumber, timestamp)
	}
}

func parseReasoning(session *trace.FlatSession, payload json.RawMessage, lineNumber int, timestamp time.Time) error {
	var item reasoningItem
	if err := json.Unmarshal(payload, &item); err != nil {
		return err
	}

	summaryText := reasoningSummaryText(item.Summary)
	event := trace.Event{
		Kind:       trace.EventReasoningMarker,
		SourceLine: lineNumber,
		Timestamp:  timestamp,
		Text:       summaryText,
		RawType:    "response_item.reasoning",
		Metadata:   trace.Metadata{},
	}
	if summaryText != "" {
		event.Kind = trace.EventReasoning
	}
	if item.EncryptedContent != "" {
		event.Metadata["has_encrypted_content"] = true
	}
	appendEvent(session, event)

	return nil
}

func parseToolShape(session *trace.FlatSession, payload json.RawMessage, lineNumber int, timestamp time.Time) error {
	var item responseToolItem
	if err := json.Unmarshal(payload, &item); err != nil {
		return err
	}

	rawType := "response_item." + item.Type
	if output, ok := toolOutputText(item); ok {
		appendEvent(session, trace.Event{
			Kind:       trace.EventToolOutput,
			SourceLine: lineNumber,
			Timestamp:  timestamp,
			Text:       output,
			RawType:    rawType,
			ToolCallID: item.CallID,
		})
		return nil
	}

	if input, ok := toolCallText(item); ok {
		appendEvent(session, trace.Event{
			Kind:       trace.EventToolCall,
			SourceLine: lineNumber,
			Timestamp:  timestamp,
			Text:       input,
			ToolName:   toolName(item),
			RawType:    rawType,
			EventID:    item.ID,
			ToolCallID: item.CallID,
		})
		return nil
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

func reasoningSummaryText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.TrimSpace(part.Text) != "" {
				texts = append(texts, strings.TrimSpace(part.Text))
			}
		}
		return strings.Join(texts, "\n")
	}

	var stringsOnly []string
	if err := json.Unmarshal(raw, &stringsOnly); err == nil {
		texts := make([]string, 0, len(stringsOnly))
		for _, text := range stringsOnly {
			if strings.TrimSpace(text) != "" {
				texts = append(texts, strings.TrimSpace(text))
			}
		}
		return strings.Join(texts, "\n")
	}

	return ""
}

func rawMessageText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err == nil {
		return buf.String()
	}

	return string(raw)
}

func compactionSummaryText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}

	var payload compactedPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.Summary) != "" {
		return strings.TrimSpace(payload.Summary)
	}
	return strings.TrimSpace(payload.Message)
}

func toolCallText(item responseToolItem) (string, bool) {
	if item.CallID == "" && !strings.HasSuffix(item.Type, "_call") {
		return "", false
	}
	for _, raw := range []json.RawMessage{item.Arguments, item.Input, item.Action} {
		if rawMessagePresent(raw) {
			return rawMessageText(raw), true
		}
	}
	return "", false
}

func toolOutputText(item responseToolItem) (string, bool) {
	if item.CallID == "" {
		return "", false
	}
	for _, raw := range []json.RawMessage{item.Output, item.Result, item.Tools} {
		if rawMessagePresent(raw) {
			return rawMessageText(raw), true
		}
	}
	return "", false
}

func rawMessagePresent(raw json.RawMessage) bool {
	return len(raw) > 0 && string(raw) != "null"
}

func toolName(item responseToolItem) string {
	if item.Name != "" {
		return item.Name
	}
	name := strings.TrimSuffix(item.Type, "_call")
	name = strings.TrimSuffix(name, "_tool")
	if name == item.Type {
		return ""
	}
	return name
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
