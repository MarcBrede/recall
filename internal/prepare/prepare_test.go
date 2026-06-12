package prepare

import (
	"strings"
	"testing"
	"time"

	"github.com/marc-brede/recall/internal/trace"
)

func TestFromFlatSessionRendersSessionRepresentation(t *testing.T) {
	longOutput := strings.Repeat("x", maxToolIOChars+1)
	session := &trace.FlatSession{
		Source:               trace.SourceCodex,
		SegmentIndex:         3,
		SourceStartLine:      9,
		SourceEndLine:        20,
		ContentStartLine:     10,
		CompactionSummary:    "Earlier work established the project shape.",
		CompactionSourceLine: 9,
		Events: []trace.Event{
			event(trace.EventHumanUser, 1, "first request"),
			event(trace.EventAssistantText, 2, "I will inspect it."),
			toolCall(3, "exec_command", `{"cmd":"rg query"}`),
			event(trace.EventToolOutput, 4, longOutput),
			event(trace.EventReasoningMarker, 5, ""),
			event(trace.EventAssistantText, 6, "I found the answer."),
			event(trace.EventHumanUser, 7, "second request"),
			event(trace.EventAssistantText, 8, "Second answer."),
		},
	}

	prepared, err := FromFlatSession(session)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.SegmentIndex != 3 || prepared.SourceStartLine != 9 || prepared.SourceEndLine != 20 {
		t.Fatalf("segment metadata = index %d lines %d-%d", prepared.SegmentIndex, prepared.SourceStartLine, prepared.SourceEndLine)
	}
	got, err := RenderForLLM(prepared)
	if err != nil {
		t.Fatal(err)
	}

	wantContains := []string{
		"<session source=\"codex\">",
		"<compaction source_line=\"9\">\nEarlier work established the project shape.\n</compaction>",
		"<section id=\"S1\">",
		"<step id=\"S1.T1\">\nUSER\nfirst request",
		"<step id=\"S1.T2\">\nASSISTANT\nI will inspect it.\n\nTOOL exec_command\n{\"cmd\":\"rg query\"}\n\nTOOL_RESULT",
		"[truncated: original_chars=1001, kept_chars=1000]",
		"<step id=\"S1.T3\">\nASSISTANT\nI found the answer.",
		"</step:S1.T3>",
		"</section:S1>",
		"<section id=\"S2\">",
		"<step id=\"S2.T1\">\nUSER\nsecond request",
		"<step id=\"S2.T2\">\nASSISTANT\nSecond answer.",
		"</session>",
	}
	for _, want := range wantContains {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered input missing %q:\n%s", want, got)
		}
	}

	if strings.Contains(got, "REASONING\n\n") {
		t.Fatalf("empty reasoning marker was rendered:\n%s", got)
	}
}

func TestTruncateToolIO(t *testing.T) {
	longText := strings.Repeat("a", maxToolIOChars+1)
	session := &trace.FlatSession{
		Events: []trace.Event{
			{Kind: trace.EventHumanUser, Text: longText},
			{Kind: trace.EventToolCall, Text: longText},
			{Kind: trace.EventToolOutput, Text: "small output"},
		},
	}

	truncated := truncateToolIO(session)
	if truncated == session {
		t.Fatal("expected a copied session")
	}
	if truncated.Events[0].Text != longText {
		t.Fatal("non-tool event was truncated")
	}
	if !strings.HasPrefix(truncated.Events[1].Text, strings.Repeat("a", maxToolIOChars)) {
		t.Fatal("tool call did not keep the text prefix")
	}
	if !strings.Contains(truncated.Events[1].Text, "\n\n[truncated: original_chars=") {
		t.Fatalf("tool call was not marked as truncated at the end: %q", truncated.Events[1].Text)
	}
	if truncated.Events[2].Text != "small output" {
		t.Fatal("small tool output changed")
	}
	if session.Events[1].Text != longText {
		t.Fatal("original session was mutated")
	}
}

func TestFromFlatSessionKeepsToolBurstsTogether(t *testing.T) {
	session := &trace.FlatSession{
		Source: trace.SourceCodex,
		Events: []trace.Event{
			event(trace.EventHumanUser, 1, "request"),
			toolCall(2, "exec_command", `{"cmd":"first"}`),
			event(trace.EventToolOutput, 3, "first output"),
			event(trace.EventReasoningMarker, 4, ""),
			toolCall(5, "exec_command", `{"cmd":"second"}`),
			event(trace.EventToolOutput, 6, "second output"),
			event(trace.EventAssistantText, 7, "done"),
		},
	}

	prepared, err := FromFlatSession(session)
	if err != nil {
		t.Fatal(err)
	}
	got, err := RenderForLLM(prepared)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "<step id=\"S1.T2\">\nTOOL exec_command\n{\"cmd\":\"first\"}") {
		t.Fatalf("first tool call missing from second step:\n%s", got)
	}
	if !strings.Contains(got, "first output\n\nTOOL exec_command\n{\"cmd\":\"second\"}") {
		t.Fatalf("second tool call was not kept in the same step:\n%s", got)
	}
	if !strings.Contains(got, "<step id=\"S1.T3\">\nASSISTANT\ndone") {
		t.Fatalf("assistant after tool burst did not start a new step:\n%s", got)
	}
}

func TestFromFlatSessionNil(t *testing.T) {
	_, err := FromFlatSession(nil)
	if err == nil {
		t.Fatal("err is nil")
	}
}

func TestRenderForLLMNil(t *testing.T) {
	_, err := RenderForLLM(nil)
	if err == nil {
		t.Fatal("err is nil")
	}
}

func event(kind trace.EventKind, sourceLine int, text string) trace.Event {
	return trace.Event{
		Kind:       kind,
		SourceLine: sourceLine,
		Timestamp:  time.Date(2026, 1, 2, 3, 4, sourceLine, 0, time.UTC),
		Text:       text,
	}
}

func toolCall(sourceLine int, name string, text string) trace.Event {
	event := event(trace.EventToolCall, sourceLine, text)
	event.ToolName = name
	return event
}
