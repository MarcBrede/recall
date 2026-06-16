package codex

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MarcBrede/recall/internal/trace"
)

func TestParserParse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"timestamp":"2026-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"test-session-001","timestamp":"2026-01-02T03:04:00.000Z","cwd":"/workspace/example","originator":"test-client","cli_version":"test-version","source":"test-source","thread_source":"test-thread","model_provider":"test-provider","base_instructions":{"text":"ignored"}}}
{"timestamp":"2026-01-02T03:04:06.100Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>ignored</environment_context>"}]}}
{"timestamp":"2026-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"/workspace/example","workspace_roots":["/workspace/example"],"current_date":"2026-01-02","timezone":"UTC","approval_policy":"never","model":"test-model"}}
{"timestamp":"2026-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"user_message","message":"check the status","images":[],"local_images":[],"text_elements":[]}}
{"timestamp":"2026-01-02T03:04:08.000Z","type":"response_item","payload":{"type":"reasoning","summary":[],"encrypted_content":"encrypted"}}
{"timestamp":"2026-01-02T03:04:09.000Z","type":"event_msg","payload":{"type":"agent_message","message":"I will inspect it.","phase":"commentary","memory_citation":null}}
{"timestamp":"2026-01-02T03:04:10.000Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"git status --short\"}","call_id":"call_123"}}
{"timestamp":"2026-01-02T03:04:11.000Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_123","output":"M file.go\n"}}
{"timestamp":"2026-01-02T03:04:11.100Z","type":"response_item","payload":{"type":"custom_tool_call","name":"apply_patch","input":"*** Begin Patch\n*** Add File: example.txt\n+hello\n*** End Patch\n","call_id":"call_patch"}}
{"timestamp":"2026-01-02T03:04:11.200Z","type":"response_item","payload":{"type":"custom_tool_call_output","call_id":"call_patch","output":"Success\n"}}
{"timestamp":"2026-01-02T03:04:11.300Z","type":"response_item","payload":{"type":"tool_search_call","call_id":"call_search","arguments":{"query":"sample docs","limit":2}}}
{"timestamp":"2026-01-02T03:04:11.400Z","type":"response_item","payload":{"type":"tool_search_output","call_id":"call_search","tools":[{"name":"sample_tool"}]}}
{"timestamp":"2026-01-02T03:04:11.500Z","type":"response_item","payload":{"type":"web_search_call","action":{"type":"search","query":"example search"}}}
{"timestamp":"2026-01-02T03:04:11.600Z","type":"response_item","payload":{"type":"shell_tool_call","call_id":"call_shell","input":{"cmd":"echo hi"}}}
{"timestamp":"2026-01-02T03:04:11.700Z","type":"response_item","payload":{"type":"shell_tool_result","call_id":"call_shell","result":{"exit_code":0,"output":"hi"}}}
{"timestamp":"2026-01-02T03:04:12.000Z","type":"event_msg","payload":{"type":"task_complete","last_agent_message":"done"}}
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

	if session.Source != trace.SourceCodex {
		t.Fatalf("source = %q, want %q", session.Source, trace.SourceCodex)
	}
	if session.ExternalID != "test-session-001" {
		t.Fatalf("external id = %q", session.ExternalID)
	}
	if session.CWD != "/workspace/example" {
		t.Fatalf("cwd = %q", session.CWD)
	}
	if got := len(session.Events); got != 12 {
		t.Fatalf("events = %d, want 12", got)
	}
	if session.SegmentIndex != 0 {
		t.Fatalf("segment index = %d, want 0", session.SegmentIndex)
	}
	if session.SourceStartLine != 1 {
		t.Fatalf("source start line = %d, want 1", session.SourceStartLine)
	}
	if session.SourceEndLine != 16 {
		t.Fatalf("source end line = %d, want 16", session.SourceEndLine)
	}

	assertEvent(t, session.Events[0], trace.EventHumanUser, "check the status", "", "")
	assertEvent(t, session.Events[1], trace.EventReasoningMarker, "", "", "")
	assertEvent(t, session.Events[2], trace.EventAssistantText, "I will inspect it.", "", "")
	assertEvent(t, session.Events[3], trace.EventToolCall, "{\"cmd\":\"git status --short\"}", "exec_command", "call_123")
	assertEvent(t, session.Events[4], trace.EventToolOutput, "M file.go\n", "", "call_123")
	assertEvent(t, session.Events[5], trace.EventToolCall, "*** Begin Patch\n*** Add File: example.txt\n+hello\n*** End Patch\n", "apply_patch", "call_patch")
	assertEvent(t, session.Events[6], trace.EventToolOutput, "Success\n", "", "call_patch")
	assertEvent(t, session.Events[7], trace.EventToolCall, "{\"query\":\"sample docs\",\"limit\":2}", "tool_search", "call_search")
	assertEvent(t, session.Events[8], trace.EventToolOutput, "[{\"name\":\"sample_tool\"}]", "", "call_search")
	assertEvent(t, session.Events[9], trace.EventToolCall, "{\"type\":\"search\",\"query\":\"example search\"}", "web_search", "")
	assertEvent(t, session.Events[10], trace.EventToolCall, "{\"cmd\":\"echo hi\"}", "shell", "call_shell")
	assertEvent(t, session.Events[11], trace.EventToolOutput, "{\"exit_code\":0,\"output\":\"hi\"}", "", "call_shell")

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

func TestParserKeepsFirstSessionMetaID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"timestamp":"2026-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"test-session-001","forked_from_id":"test-parent-001","timestamp":"2026-01-02T03:04:00.000Z","cwd":"/workspace/current"}}
{"timestamp":"2026-01-02T03:04:06.000Z","type":"session_meta","payload":{"id":"test-parent-001","timestamp":"2026-01-01T03:04:00.000Z","cwd":"/workspace/parent"}}
{"timestamp":"2026-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"user_message","message":"remember this"}}
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

	if session.ExternalID != "test-session-001" {
		t.Fatalf("external id = %q, want test-session-001", session.ExternalID)
	}
	if session.Metadata["forked_from_id"] != "test-parent-001" {
		t.Fatalf("forked_from_id metadata = %v", session.Metadata["forked_from_id"])
	}
	if session.CWD != "/workspace/current" {
		t.Fatalf("cwd = %q, want /workspace/current", session.CWD)
	}
	if got := session.StartedAt.Format(time.RFC3339); got != "2026-01-02T03:04:00Z" {
		t.Fatalf("started_at = %q, want 2026-01-02T03:04:00Z", got)
	}
}

func TestParserSplitsCompactedSessions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	input := `{"timestamp":"2026-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"test-session-001","timestamp":"2026-01-02T03:04:00.000Z","cwd":"/workspace/example"}}
{"timestamp":"2026-01-02T03:04:07.000Z","type":"event_msg","payload":{"type":"user_message","message":"first request"}}
{"timestamp":"2026-01-02T03:04:08.000Z","type":"event_msg","payload":{"type":"agent_message","message":"first answer"}}
{"timestamp":"2026-01-02T03:05:00.000Z","type":"compacted","payload":{"ignored":true}}
{"timestamp":"2026-01-02T03:05:01.000Z","type":"event_msg","payload":{"type":"agent_message","message":"late final answer"}}
{"timestamp":"2026-01-02T03:06:07.000Z","type":"event_msg","payload":{"type":"user_message","message":"second request"}}
{"timestamp":"2026-01-02T03:06:08.000Z","type":"event_msg","payload":{"type":"agent_message","message":"second answer"}}
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

	if sessions[0].SegmentIndex != 0 || sessions[0].SourceStartLine != 1 || sessions[0].SourceEndLine != 5 {
		t.Fatalf("first segment metadata = index %d lines %d-%d", sessions[0].SegmentIndex, sessions[0].SourceStartLine, sessions[0].SourceEndLine)
	}
	if sessions[1].SegmentIndex != 1 || sessions[1].SourceStartLine != 6 || sessions[1].SourceEndLine != 7 {
		t.Fatalf("second segment metadata = index %d lines %d-%d", sessions[1].SegmentIndex, sessions[1].SourceStartLine, sessions[1].SourceEndLine)
	}
	if sessions[1].ContentStartLine != 6 {
		t.Fatalf("second content start line = %d, want 6", sessions[1].ContentStartLine)
	}
	if sessions[1].CompactionSummary != "" {
		t.Fatalf("second compaction summary = %q, want empty", sessions[1].CompactionSummary)
	}
	if sessions[1].ExternalID != "test-session-001" {
		t.Fatalf("second external id = %q, want test-session-001", sessions[1].ExternalID)
	}
	assertEvent(t, sessions[0].Events[0], trace.EventHumanUser, "first request", "", "")
	assertEvent(t, sessions[0].Events[len(sessions[0].Events)-1], trace.EventAssistantText, "late final answer", "", "")
	assertEvent(t, sessions[1].Events[0], trace.EventHumanUser, "second request", "", "")
}

func assertEvent(t *testing.T, event trace.Event, kind trace.EventKind, text string, toolName string, toolCallID string) {
	t.Helper()
	if event.Kind != kind {
		t.Fatalf("kind = %q, want %q", event.Kind, kind)
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
