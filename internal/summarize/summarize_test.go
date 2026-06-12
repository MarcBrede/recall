package summarize

import (
	"context"
	"strings"
	"testing"

	"github.com/marc-brede/recall/internal/llm"
	"github.com/marc-brede/recall/internal/trace"
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

	result, err := WithClient(context.Background(), client, "test-model", "low", session)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionSummary != "session" {
		t.Fatalf("session summary = %q", result.SessionSummary)
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

	_, err := WithClient(context.Background(), client, "test-model", "off", testSession())
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

	result, err := WithClient(context.Background(), client, "test-model", "off", testSession())
	if err != nil {
		t.Fatal(err)
	}
	if result.SectionSummaries["S1"].StepSummaries["S1.T2"] != "work" {
		t.Fatalf("retry result did not include S1.T2")
	}
	if got := len(client.reqs); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
	if !strings.Contains(client.reqs[1].UserPrompt, "previous JSON response failed validation") {
		t.Fatalf("retry prompt did not include validation feedback:\n%s", client.reqs[1].UserPrompt)
	}
}

type fakeClient struct {
	reqs []llm.StructuredRequest
	raws []string
}

func (client *fakeClient) GenerateStructured(_ context.Context, req llm.StructuredRequest) (string, error) {
	client.reqs = append(client.reqs, req)
	if len(client.raws) == 0 {
		return "", nil
	}
	index := len(client.reqs) - 1
	if index >= len(client.raws) {
		index = len(client.raws) - 1
	}
	return client.raws[index], nil
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
