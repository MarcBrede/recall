package claude

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marc-brede/recall/internal/trace"
)

func TestParserParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"type":"permission-mode","permissionMode":"default","sessionId":"test-session-001"}
{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"check the status"},"uuid":"event-user-001","timestamp":"2026-01-02T03:04:05.000Z","userType":"external","entrypoint":"cli","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version","gitBranch":"main"}
{"parentUuid":"event-user-001","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"Base directory for this skill: /workspace/example/skills/example\n\nInjected instructions that should not become a human event."}]},"uuid":"event-injected-skill-001","timestamp":"2026-01-02T03:04:05.500Z","userType":"external","entrypoint":"cli","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version","gitBranch":"main"}
{"parentUuid":"event-user-001","isSidechain":false,"type":"assistant","message":{"role":"assistant","model":"test-model","content":[{"type":"thinking","thinking":"","signature":"test-signature"},{"type":"tool_use","id":"tool-001","name":"Shell","input":{"command":"echo hello"}},{"type":"text","text":"I will run a command."}]},"uuid":"event-assistant-001","timestamp":"2026-01-02T03:04:06.000Z","userType":"external","entrypoint":"cli","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version","gitBranch":"main"}
{"parentUuid":"event-assistant-001","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"tool-001","type":"tool_result","content":"hello\n","is_error":false}]},"uuid":"event-tool-result-001","timestamp":"2026-01-02T03:04:07.000Z","userType":"external","entrypoint":"cli","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version","gitBranch":"main"}
{"parentUuid":"event-tool-result-001","isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"<task-notification>\n<task-id>task-001</task-id>\n</task-notification>"}]},"uuid":"event-task-notification-001","timestamp":"2026-01-02T03:04:08.000Z","userType":"external","entrypoint":"cli","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version","gitBranch":"main"}
{"parentUuid":"event-task-notification-001","isSidechain":false,"type":"user","message":{"role":"user","content":"<task-notification>\n<task-id>task-002</task-id>\n</task-notification>"},"uuid":"event-task-notification-002","timestamp":"2026-01-02T03:04:09.000Z","userType":"external","entrypoint":"cli","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version","gitBranch":"main"}
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := Parser{}.Parse(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sessions); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}
	session := sessions[0]

	if session.Source != trace.SourceClaude {
		t.Fatalf("source = %q, want %q", session.Source, trace.SourceClaude)
	}
	if session.ExternalID != "test-session-001" {
		t.Fatalf("external id = %q", session.ExternalID)
	}
	if session.CWD != "/workspace/example" {
		t.Fatalf("cwd = %q", session.CWD)
	}
	if session.GitBranch != "main" {
		t.Fatalf("git branch = %q", session.GitBranch)
	}
	if got := len(session.Events); got != 5 {
		t.Fatalf("events = %d, want 5", got)
	}
	if session.SegmentIndex != 0 {
		t.Fatalf("segment index = %d, want 0", session.SegmentIndex)
	}
	if session.SourceStartLine != 1 {
		t.Fatalf("source start line = %d, want 1", session.SourceStartLine)
	}
	if session.SourceEndLine != 7 {
		t.Fatalf("source end line = %d, want 7", session.SourceEndLine)
	}

	assertEvent(t, session.Events[0], trace.EventHumanUser, "user.text", 2, "check the status", "", "")
	assertEvent(t, session.Events[1], trace.EventReasoningMarker, "assistant.thinking", 4, "", "", "")
	assertEvent(t, session.Events[2], trace.EventToolCall, "assistant.tool_use", 4, `{"command":"echo hello"}`, "Shell", "tool-001")
	assertEvent(t, session.Events[3], trace.EventAssistantText, "assistant.text", 4, "I will run a command.", "", "")
	assertEvent(t, session.Events[4], trace.EventToolOutput, "user.tool_result", 5, "hello\n", "", "tool-001")

	if session.Metadata["model"] != "test-model" {
		t.Fatalf("model metadata = %v", session.Metadata["model"])
	}
	if session.StartedAt.IsZero() {
		t.Fatal("started_at is zero")
	}
	if session.EndedAt.IsZero() {
		t.Fatal("ended_at is zero")
	}
}

func TestParserSplitsCompactedSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"first request"},"uuid":"event-user-001","timestamp":"2026-01-02T03:04:05.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-user-001","isSidechain":false,"type":"assistant","message":{"role":"assistant","model":"test-model","content":[{"type":"text","text":"first answer"}]},"uuid":"event-assistant-001","timestamp":"2026-01-02T03:04:06.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"type":"system","content":"Conversation compacted","timestamp":"2026-01-02T03:05:00.000Z","sessionId":"test-session-001"}
{"parentUuid":"event-assistant-001","isSidechain":false,"type":"user","message":{"role":"user","content":"This session is being continued from a previous conversation that ran out of context. The conversation is summarized below."},"uuid":"event-continuation-001","timestamp":"2026-01-02T03:05:01.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-continuation-001","isSidechain":false,"type":"user","message":{"role":"user","content":"second request"},"uuid":"event-user-002","timestamp":"2026-01-02T03:06:05.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-user-002","isSidechain":false,"type":"assistant","message":{"role":"assistant","model":"test-model","content":[{"type":"text","text":"second answer"}]},"uuid":"event-assistant-002","timestamp":"2026-01-02T03:06:06.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := Parser{}.Parse(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sessions); got != 2 {
		t.Fatalf("sessions = %d, want 2", got)
	}

	if sessions[0].SegmentIndex != 0 || sessions[0].SourceStartLine != 1 || sessions[0].SourceEndLine != 2 {
		t.Fatalf("first segment metadata = index %d lines %d-%d", sessions[0].SegmentIndex, sessions[0].SourceStartLine, sessions[0].SourceEndLine)
	}
	if sessions[1].SegmentIndex != 1 || sessions[1].SourceStartLine != 4 || sessions[1].SourceEndLine != 6 {
		t.Fatalf("second segment metadata = index %d lines %d-%d", sessions[1].SegmentIndex, sessions[1].SourceStartLine, sessions[1].SourceEndLine)
	}
	if sessions[1].ContentStartLine != 5 {
		t.Fatalf("second content start line = %d, want 5", sessions[1].ContentStartLine)
	}
	if sessions[1].CompactionSourceLine != 4 {
		t.Fatalf("second compaction source line = %d, want 4", sessions[1].CompactionSourceLine)
	}
	if sessions[1].CompactionSummary == "" {
		t.Fatal("second compaction summary is empty")
	}
	if sessions[1].ExternalID != "test-session-001" {
		t.Fatalf("second external id = %q, want test-session-001", sessions[1].ExternalID)
	}
	if got := len(sessions[1].Events); got != 2 {
		t.Fatalf("second segment events = %d, want 2", got)
	}
	assertEvent(t, sessions[1].Events[0], trace.EventHumanUser, "user.text", 5, "second request", "", "")
}

func TestParserSkipsMCPBookkeepingButKeepsSkillCommands(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"<command-message>remember</command-message>\n<command-name>/remember</command-name>\n<command-args>what did I do yesterday?</command-args>"},"uuid":"event-remember-001","timestamp":"2026-01-02T03:04:05.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-remember-001","isSidechain":false,"promptId":"prompt-001","type":"user","message":{"role":"user","content":"<local-command-caveat>Caveat: local command output follows.</local-command-caveat>"},"isMeta":true,"uuid":"event-caveat-001","timestamp":"2026-01-02T03:04:06.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-caveat-001","isSidechain":false,"promptId":"prompt-001","type":"user","message":{"role":"user","content":"<command-name>/mcp</command-name>\n<command-message>mcp</command-message>\n<command-args></command-args>"},"uuid":"event-mcp-001","timestamp":"2026-01-02T03:04:07.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-mcp-001","isSidechain":false,"promptId":"prompt-001","type":"user","message":{"role":"user","content":"<local-command-stdout>Authentication successful. Connected to datadog.</local-command-stdout>"},"uuid":"event-stdout-001","timestamp":"2026-01-02T03:04:08.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
{"parentUuid":"event-stdout-001","isSidechain":false,"type":"user","message":{"role":"user","content":"continue the investigation"},"uuid":"event-user-001","timestamp":"2026-01-02T03:04:09.000Z","cwd":"/workspace/example","sessionId":"test-session-001","version":"test-version"}
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := Parser{}.Parse(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sessions); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}

	events := sessions[0].Events
	if got := len(events); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	assertEvent(t, events[0], trace.EventHumanUser, "user.text", 1, "<command-message>remember</command-message>\n<command-name>/remember</command-name>\n<command-args>what did I do yesterday?</command-args>", "", "")
	assertEvent(t, events[1], trace.EventHumanUser, "user.text", 5, "continue the investigation", "", "")
}

func TestParserStoresForkedFromSessionID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"parentUuid":null,"type":"user","message":{"role":"user","content":"start from here"},"uuid":"event-user-001","timestamp":"2026-01-02T03:04:05.000Z","cwd":"/workspace/example","sessionId":"child-session-001","forkedFrom":{"sessionId":"parent-session-001","messageUuid":"event-user-001"}}
{"parentUuid":"event-user-001","type":"assistant","message":{"role":"assistant","model":"test-model","content":[{"type":"text","text":"continuing"}]},"uuid":"event-assistant-001","timestamp":"2026-01-02T03:04:06.000Z","cwd":"/workspace/example","sessionId":"child-session-001","forkedFrom":{"sessionId":"parent-session-001","messageUuid":"event-assistant-001"}}
`
	if err := os.WriteFile(path, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	sessions, err := Parser{}.Parse(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sessions); got != 1 {
		t.Fatalf("sessions = %d, want 1", got)
	}
	if got := sessions[0].Metadata["forked_from_session_id"]; got != "parent-session-001" {
		t.Fatalf("forked_from_session_id = %v, want parent-session-001", got)
	}
}

func assertEvent(t *testing.T, event trace.Event, kind trace.EventKind, rawType string, sourceLine int, text string, toolName string, toolCallID string) {
	t.Helper()
	if event.Kind != kind {
		t.Fatalf("kind = %q, want %q", event.Kind, kind)
	}
	if event.RawType != rawType {
		t.Fatalf("raw type = %q, want %q", event.RawType, rawType)
	}
	if event.SourceLine != sourceLine {
		t.Fatalf("source line = %d, want %d", event.SourceLine, sourceLine)
	}
	if event.Text != text {
		t.Fatalf("text = %q, want %q", event.Text, text)
	}
	if event.ToolName != toolName {
		t.Fatalf("tool name = %q, want %q", event.ToolName, toolName)
	}
	if event.ToolCallID != toolCallID {
		t.Fatalf("tool call id = %q, want %q", event.ToolCallID, toolCallID)
	}
}
