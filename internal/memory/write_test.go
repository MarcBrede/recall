package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marc-brede/recall/internal/config"
	"github.com/marc-brede/recall/internal/summarize"
	"github.com/marc-brede/recall/internal/trace"
)

func TestWriteSessionWritesMemoryDirectory(t *testing.T) {
	recallDir := filepath.Join(t.TempDir(), ".recall")
	session := testSession()
	result := testSummary()
	generatedAt := time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)

	got, err := WriteSession(WriteOptions{
		RecallDir: recallDir,
		Config: config.Config{
			ConfigVersion: 1,
			LLM: config.LLMConfig{
				Provider: "anthropic",
				Model:    "claude-opus-4-8",
				Reasoning: config.ReasoningConfig{
					Level: "high",
				},
			},
		},
		GeneratedAt: generatedAt,
	}, session, result)
	if err != nil {
		t.Fatal(err)
	}

	wantDir := filepath.Join(recallDir, "sessions", "2026-01-02T030630Z-codex-session-001-seg002")
	if got.Dir != wantDir {
		t.Fatalf("dir = %q, want %q", got.Dir, wantDir)
	}

	metadata := readFile(t, filepath.Join(wantDir, "metadata.json"))
	if strings.Contains(metadata, "ended_at") {
		t.Fatalf("metadata contains ended_at:\n%s", metadata)
	}
	var decoded Metadata
	if err := json.Unmarshal([]byte(metadata), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.LastEventAt != "2026-01-02T03:06:30Z" {
		t.Fatalf("last_event_at = %q", decoded.LastEventAt)
	}
	if decoded.SegmentIndex != 2 {
		t.Fatalf("segment_index = %d, want 2", decoded.SegmentIndex)
	}
	if decoded.SourceStartLine != 3 || decoded.SourceEndLine != 18 {
		t.Fatalf("source lines = %d-%d, want 3-18", decoded.SourceStartLine, decoded.SourceEndLine)
	}
	if decoded.ContentStartLine != 7 {
		t.Fatalf("content_start_line = %d, want 7", decoded.ContentStartLine)
	}
	if decoded.CompactionSourceLine != 3 {
		t.Fatalf("compaction_source_line = %d, want 3", decoded.CompactionSourceLine)
	}
	if decoded.LLM.Model != "claude-opus-4-8" {
		t.Fatalf("llm model = %q", decoded.LLM.Model)
	}

	sessionMarkdown := readFile(t, filepath.Join(wantDir, "session.md"))
	for _, unwanted := range []string{"schema_version", "node_type", "llm:"} {
		if strings.Contains(sessionMarkdown, unwanted) {
			t.Fatalf("session markdown contains %q:\n%s", unwanted, sessionMarkdown)
		}
	}
	for _, want := range []string{
		`id: "session-001"`,
		`segment_index: 2`,
		`source_start_line: 3`,
		`source_end_line: 18`,
		`content_start_line: 7`,
		`compaction_source_line: 3`,
		`last_event_at: "2026-01-02T03:06:30Z"`,
		"summary: |",
		"## Compaction\n\nPrior context summary.",
		"- [S001](sections/S001.md): Section summary.",
	} {
		if !strings.Contains(sessionMarkdown, want) {
			t.Fatalf("session markdown missing %q:\n%s", want, sessionMarkdown)
		}
	}

	sectionMarkdown := readFile(t, filepath.Join(wantDir, "sections", "S001.md"))
	for _, want := range []string{
		`id: "S1"`,
		`session_id: "session-001"`,
		`session_segment_index: 2`,
		`start_line: 7`,
		`end_line: 15`,
		`last_event_at: "2026-01-02T03:06:30Z"`,
		"- `S1.T1` lines `7-7`: User asks the question.",
		"- `S1.T2` lines `9-15`: Assistant answers the question.",
	} {
		if !strings.Contains(sectionMarkdown, want) {
			t.Fatalf("section markdown missing %q:\n%s", want, sectionMarkdown)
		}
	}
}

func TestWriteSessionRejectsMissingStepSummary(t *testing.T) {
	result := testSummary()
	delete(result.SectionSummaries["S1"].StepSummaries, "S1.T2")

	_, err := WriteSession(WriteOptions{
		RecallDir:   filepath.Join(t.TempDir(), ".recall"),
		Config:      config.Default(),
		GeneratedAt: time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC),
	}, testSession(), result)
	if err == nil {
		t.Fatal("err is nil")
	}
	if !strings.Contains(err.Error(), "missing: S1.T2") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func testSession() *trace.Session {
	startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	lastEventAt := time.Date(2026, 1, 2, 3, 6, 30, 0, time.UTC)
	return &trace.Session{
		Source:               trace.SourceCodex,
		ExternalID:           "session-001",
		SegmentIndex:         2,
		SourceFile:           "/tmp/session.jsonl",
		SourceStartLine:      3,
		SourceEndLine:        18,
		ContentStartLine:     7,
		CompactionSummary:    "Raw compaction context.",
		CompactionSourceLine: 3,
		CWD:                  "/workspace/project",
		StartedAt:            startedAt,
		EndedAt:              lastEventAt,
		Sections: []trace.Section{
			{
				ID:        "S1",
				StartLine: 7,
				EndLine:   15,
				StartedAt: startedAt,
				EndedAt:   lastEventAt,
				Steps: []trace.Step{
					{
						ID:        "S1.T1",
						StartLine: 7,
						EndLine:   7,
					},
					{
						ID:        "S1.T2",
						StartLine: 9,
						EndLine:   15,
					},
				},
			},
		},
	}
}

func testSummary() *summarize.Result {
	return &summarize.Result{
		SessionSummary:    "Session summary.",
		CompactionSummary: "Prior context summary.",
		SectionSummaries: map[string]summarize.SectionResult{
			"S1": {
				Summary: "Section summary.",
				StepSummaries: map[string]string{
					"S1.T1": "User asks the question.",
					"S1.T2": "Assistant answers the question.",
				},
			},
		},
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
