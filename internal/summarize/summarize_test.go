package summarize

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/MarcBrede/recall/internal/llm"
	"github.com/MarcBrede/recall/internal/trace"
)

func TestWithClientBuildsSchemaAndValidatesResult(t *testing.T) {
	client := &fakeClient{
		raws: []string{`{
			"session_summary": "session",
			"compaction_summary": "",
			"section_summaries": [
				{
					"id": "S1",
					"summary": "section",
					"step_summaries": [
						{"id": "S1.T1", "summary": "user"},
						{"id": "S1.T2", "summary": "work"}
					]
				}
			]
		}`},
	}
	session := testSession()

	result, usage, err := WithClient(context.Background(), client, "test-model", "low", session)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionSummary != "session" {
		t.Fatalf("session summary = %q", result.SessionSummary)
	}
	if usage != (llm.Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Fatalf("usage = %+v, want {10 5}", usage)
	}
	if client.reqs[0].Model != "test-model" {
		t.Fatalf("model = %q", client.reqs[0].Model)
	}
	if client.reqs[0].ReasoningLevel != "low" {
		t.Fatalf("reasoning level = %q, want low", client.reqs[0].ReasoningLevel)
	}
	if client.reqs[0].SystemPrompt != summarySystemPrompt {
		t.Fatalf("system prompt = %q, want summary prompt", client.reqs[0].SystemPrompt)
	}
	if !strings.Contains(client.reqs[0].UserPrompt, `<section id="S1">`) {
		t.Fatalf("user prompt does not contain rendered session:\n%s", client.reqs[0].UserPrompt)
	}
	if client.reqs[0].SchemaName != schemaName {
		t.Fatalf("schema name = %q, want %q", client.reqs[0].SchemaName, schemaName)
	}

	sectionSummaries := client.reqs[0].Schema["properties"].(map[string]any)["section_summaries"].(map[string]any)
	if sectionSummaries["type"] != "array" {
		t.Fatalf("section_summaries schema type = %v, want array", sectionSummaries["type"])
	}
}

func TestWithClientRejectsMissingStepSummary(t *testing.T) {
	client := &fakeClient{
		raws: []string{`{
			"session_summary": "session",
			"compaction_summary": "",
			"section_summaries": [
				{
					"id": "S1",
					"summary": "section",
					"step_summaries": [
						{"id": "S1.T1", "summary": "user"}
					]
				}
			]
		}`, `{
			"session_summary": "session",
			"compaction_summary": "",
			"section_summaries": [
				{
					"id": "S1",
					"summary": "section",
					"step_summaries": [
						{"id": "S1.T1", "summary": "user"}
					]
				}
			]
		}`},
	}

	_, _, err := WithClient(context.Background(), client, "test-model", "off", testSession())
	if err == nil {
		t.Fatal("err is nil")
	}
	if !strings.Contains(err.Error(), "expected 2 step summaries") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(client.reqs); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

func TestWithClientRetriesValidationFailure(t *testing.T) {
	client := &fakeClient{
		raws: []string{`{
			"session_summary": "session",
			"compaction_summary": "",
			"section_summaries": [
				{
					"id": "S1",
					"summary": "section",
					"step_summaries": [
						{"id": "S1.T1", "summary": "user"}
					]
				}
			]
		}`, `{
			"session_summary": "session",
			"compaction_summary": "",
			"section_summaries": [
				{
					"id": "S1",
					"summary": "section",
					"step_summaries": [
						{"id": "S1.T1", "summary": "user"},
						{"id": "S1.T2", "summary": "work"}
					]
				}
			]
		}`},
	}

	result, usage, err := WithClient(context.Background(), client, "test-model", "off", testSession())
	if err != nil {
		t.Fatal(err)
	}
	if result.SectionSummaries["S1"].StepSummaries["S1.T2"] != "work" {
		t.Fatalf("retry result did not include S1.T2")
	}
	if usage != (llm.Usage{InputTokens: 20, OutputTokens: 10}) {
		t.Fatalf("usage = %+v, want {20 10} (summed across 2 attempts)", usage)
	}
	if got := len(client.reqs); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if !strings.Contains(client.reqs[1].UserPrompt, "previous JSON response failed validation") {
		t.Fatalf("retry prompt did not include validation feedback:\n%s", client.reqs[1].UserPrompt)
	}
}

func TestWithClientUsesHierarchicalSummariesForLargeSession(t *testing.T) {
	client := &fakeClient{
		respond: func(req llm.StructuredRequest) (string, llm.Usage, error) {
			switch req.SchemaName {
			case sectionSchemaName:
				switch {
				case strings.Contains(req.UserPrompt, `<section id="S1">`):
					return sectionJSON("S1", 13), llm.Usage{InputTokens: 3, OutputTokens: 2}, nil
				case strings.Contains(req.UserPrompt, `<section id="S2">`):
					return sectionJSON("S2", 13), llm.Usage{InputTokens: 3, OutputTokens: 2}, nil
				default:
					return "", llm.Usage{}, fmt.Errorf("section prompt did not contain expected section: %s", req.UserPrompt)
				}
			case sessionSchemaName:
				if !strings.Contains(req.UserPrompt, `<section_summary id="S1">`) ||
					!strings.Contains(req.UserPrompt, `<section_summary id="S2">`) {
					return "", llm.Usage{}, fmt.Errorf("session prompt missing section summaries: %s", req.UserPrompt)
				}
				return `{"session_summary":"large session","compaction_summary":""}`, llm.Usage{InputTokens: 4, OutputTokens: 3}, nil
			default:
				return "", llm.Usage{}, fmt.Errorf("unexpected schema %q", req.SchemaName)
			}
		},
	}

	result, usage, err := WithClientLimited(context.Background(), client, "test-model", "off", llm.NewLimiter(2), largeTestSession())
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionSummary != "large session" {
		t.Fatalf("session summary = %q", result.SessionSummary)
	}
	if got := len(result.SectionSummaries); got != 2 {
		t.Fatalf("section summaries = %d, want 2", got)
	}
	if got := len(result.SectionSummaries["S1"].StepSummaries); got != 13 {
		t.Fatalf("S1 step summaries = %d, want 13", got)
	}
	if usage != (llm.Usage{InputTokens: 10, OutputTokens: 7}) {
		t.Fatalf("usage = %+v, want {10 7}", usage)
	}

	var sectionCalls int
	var sessionCalls int
	for _, req := range client.reqs {
		switch req.SchemaName {
		case sectionSchemaName:
			sectionCalls++
		case sessionSchemaName:
			sessionCalls++
		case schemaName:
			t.Fatalf("large session used one-shot schema")
		}
	}
	if sectionCalls != 2 || sessionCalls != 1 {
		t.Fatalf("calls = section:%d session:%d, want section:2 session:1", sectionCalls, sessionCalls)
	}
}

func TestWithClientIncrementalReusesUnchangedSections(t *testing.T) {
	client := &fakeClient{
		respond: func(req llm.StructuredRequest) (string, llm.Usage, error) {
			switch req.SchemaName {
			case sectionSchemaName:
				if strings.Contains(req.UserPrompt, `<section id="S2">`) {
					return sectionJSON("S2", 1), llm.Usage{InputTokens: 3, OutputTokens: 2}, nil
				}
				return "", llm.Usage{}, fmt.Errorf("unexpected section prompt: %s", req.UserPrompt)
			case sessionSchemaName:
				if !strings.Contains(req.UserPrompt, `<section_summary id="S1">`) ||
					!strings.Contains(req.UserPrompt, `<section_summary id="S2">`) {
					return "", llm.Usage{}, fmt.Errorf("session prompt missing section summaries: %s", req.UserPrompt)
				}
				return `{"session_summary":"incremental session","compaction_summary":""}`, llm.Usage{InputTokens: 4, OutputTokens: 3}, nil
			default:
				return "", llm.Usage{}, fmt.Errorf("unexpected schema %q", req.SchemaName)
			}
		},
	}

	result, usage, err := WithClientIncremental(
		context.Background(),
		client,
		"test-model",
		"off",
		llm.NewLimiter(2),
		twoSectionSession(),
		map[string]SectionResult{
			"S1": {
				Summary: "cached S1",
				StepSummaries: map[string]string{
					"S1.T1": "cached S1.T1",
				},
			},
		},
		[]string{"S2"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionSummary != "incremental session" {
		t.Fatalf("session summary = %q", result.SessionSummary)
	}
	if result.SectionSummaries["S1"].Summary != "cached S1" {
		t.Fatalf("S1 summary was not reused")
	}
	if result.SectionSummaries["S2"].Summary != "summary for S2" {
		t.Fatalf("S2 summary = %q", result.SectionSummaries["S2"].Summary)
	}
	if usage != (llm.Usage{InputTokens: 7, OutputTokens: 5}) {
		t.Fatalf("usage = %+v, want {7 5}", usage)
	}

	var sectionCalls int
	var sessionCalls int
	for _, req := range client.reqs {
		switch req.SchemaName {
		case sectionSchemaName:
			sectionCalls++
			if strings.Contains(req.UserPrompt, `<section id="S1">`) {
				t.Fatalf("unchanged section S1 was summarized again")
			}
		case sessionSchemaName:
			sessionCalls++
		}
	}
	if sectionCalls != 1 || sessionCalls != 1 {
		t.Fatalf("calls = section:%d session:%d, want section:1 session:1", sectionCalls, sessionCalls)
	}
}

type fakeClient struct {
	mu      sync.Mutex
	reqs    []llm.StructuredRequest
	raws    []string
	respond func(llm.StructuredRequest) (string, llm.Usage, error)
}

func (client *fakeClient) GenerateStructured(_ context.Context, req llm.StructuredRequest) (llm.Response, error) {
	client.mu.Lock()
	client.reqs = append(client.reqs, req)
	respond := client.respond
	if len(client.raws) == 0 {
		client.mu.Unlock()
		if respond != nil {
			text, usage, err := respond(req)
			return llm.Response{Text: text, Usage: usage}, err
		}
		return llm.Response{}, nil
	}
	index := len(client.reqs) - 1
	if index >= len(client.raws) {
		index = len(client.raws) - 1
	}
	raw := client.raws[index]
	client.mu.Unlock()
	return llm.Response{
		Text:  raw,
		Usage: llm.Usage{InputTokens: 10, OutputTokens: 5},
	}, nil
}

func testSession() *trace.Session {
	return &trace.Session{
		Source: trace.SourceCodex,
		Sections: []trace.Section{
			{
				ID: "S1",
				Steps: []trace.Step{
					{
						ID: "S1.T1",
						Events: []trace.Event{
							{Kind: trace.EventHumanUser, Text: "request"},
						},
					},
					{
						ID: "S1.T2",
						Events: []trace.Event{
							{Kind: trace.EventAssistantText, Text: "work"},
						},
					},
				},
			},
		},
	}
}

func twoSectionSession() *trace.Session {
	return &trace.Session{
		Source: trace.SourceCodex,
		Sections: []trace.Section{
			{
				ID: "S1",
				Steps: []trace.Step{
					{
						ID: "S1.T1",
						Events: []trace.Event{
							{Kind: trace.EventHumanUser, Text: "first request"},
						},
					},
				},
			},
			{
				ID: "S2",
				Steps: []trace.Step{
					{
						ID: "S2.T1",
						Events: []trace.Event{
							{Kind: trace.EventHumanUser, Text: "second request"},
						},
					},
				},
			},
		},
	}
}

func largeTestSession() *trace.Session {
	session := &trace.Session{Source: trace.SourceCodex}
	for sectionIndex := 1; sectionIndex <= 2; sectionIndex++ {
		sectionID := fmt.Sprintf("S%d", sectionIndex)
		section := trace.Section{ID: sectionID}
		for stepIndex := 1; stepIndex <= 13; stepIndex++ {
			stepID := fmt.Sprintf("%s.T%d", sectionID, stepIndex)
			section.Steps = append(section.Steps, trace.Step{
				ID: stepID,
				Events: []trace.Event{
					{Kind: trace.EventAssistantText, Text: "work " + stepID},
				},
			})
		}
		session.Sections = append(session.Sections, section)
	}
	return session
}

func sectionJSON(sectionID string, steps int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, `{"id":%q,"summary":"summary for %s","step_summaries":[`, sectionID, sectionID)
	for i := 1; i <= steps; i++ {
		if i > 1 {
			builder.WriteByte(',')
		}
		stepID := fmt.Sprintf("%s.T%d", sectionID, i)
		fmt.Fprintf(&builder, `{"id":%q,"summary":"summary for %s"}`, stepID, stepID)
	}
	builder.WriteString(`]}`)
	return builder.String()
}
