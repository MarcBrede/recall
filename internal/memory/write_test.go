package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarcBrede/recall/internal/config"
	"github.com/MarcBrede/recall/internal/summarize"
	"github.com/MarcBrede/recall/internal/trace"
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
		SectionMetadata: map[string]SectionMetadata{
			"S1": {
				InputHash: "sha256:test-section-input",
			},
		},
	}, session, result)
	if err != nil {
		t.Fatal(err)
	}

	wantDir := filepath.Join(recallDir, "sessions", "2026-01-02T030405Z-codex-session-001")
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
	if decoded.ForkedFromSessionID != "parent-session-001" {
		t.Fatalf("forked_from_session_id = %q, want parent-session-001", decoded.ForkedFromSessionID)
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
	if decoded.Summaries.SessionSummary != "Session summary." {
		t.Fatalf("session summary = %q", decoded.Summaries.SessionSummary)
	}
	if decoded.Summaries.SectionSummaries["S1"].StepSummaries["S1.T2"] != "Assistant answers the question." {
		t.Fatalf("missing structured step summary in metadata")
	}
	if decoded.Sections["S1"].InputHash != "sha256:test-section-input" {
		t.Fatalf("section input hash = %q", decoded.Sections["S1"].InputHash)
	}
	if decoded.Sections["S1"].Status != SectionStatusOpen {
		t.Fatalf("section status = %q, want open", decoded.Sections["S1"].Status)
	}
	if decoded.Sections["S1"].Path != "sections/S001.md" {
		t.Fatalf("section path = %q", decoded.Sections["S1"].Path)
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
		`forked_from_session_id: "parent-session-001"`,
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

func TestWriteSessionWritesSegmentDirectory(t *testing.T) {
	recallDir := filepath.Join(t.TempDir(), ".recall")
	session := testSession()
	result := testSummary()
	rootStartedAt := time.Date(2026, 1, 1, 1, 2, 3, 0, time.UTC)
	rootEndedAt := time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC)

	got, err := WriteSession(WriteOptions{
		RecallDir:     recallDir,
		Config:        config.Default(),
		Segmented:     true,
		RootStartedAt: rootStartedAt,
		RootEndedAt:   rootEndedAt,
	}, session, result)
	if err != nil {
		t.Fatal(err)
	}

	wantRoot := filepath.Join(recallDir, "sessions", "2026-01-01T010203Z-codex-session-001")
	wantDir := filepath.Join(wantRoot, "segments", "seg002")
	if got.RootDir != wantRoot {
		t.Fatalf("root dir = %q, want %q", got.RootDir, wantRoot)
	}
	if got.Dir != wantDir {
		t.Fatalf("dir = %q, want %q", got.Dir, wantDir)
	}
	if got.SessionPath != "" {
		t.Fatalf("session path = %q, want empty for segmented write", got.SessionPath)
	}
	if got.SegmentPath != filepath.Join(wantDir, "segment.md") {
		t.Fatalf("segment path = %q", got.SegmentPath)
	}
	segmentMarkdown := readFile(t, filepath.Join(wantDir, "segment.md"))
	for _, want := range []string{
		"# Segment seg002",
		"- [S001](sections/S001.md): Section summary.",
	} {
		if !strings.Contains(segmentMarkdown, want) {
			t.Fatalf("segment markdown missing %q:\n%s", want, segmentMarkdown)
		}
	}
}

func TestWriteSessionPreservesUnchangedSections(t *testing.T) {
	recallDir := filepath.Join(t.TempDir(), ".recall")
	previousDir := filepath.Join(recallDir, "sessions", "previous")
	writeTestMemoryFile(t, filepath.Join(previousDir, "sections", "S001.md"), "old section one")
	writeTestMemoryFile(t, filepath.Join(previousDir, "sections", "S002.md"), "old section two")
	writeTestMemoryFile(t, filepath.Join(previousDir, "sections", "S003.md"), "stale section three")

	session := testSession()
	session.Sections = append(session.Sections, trace.Section{
		ID:        "S2",
		StartLine: 16,
		EndLine:   18,
		StartedAt: session.StartedAt,
		EndedAt:   session.EndedAt,
		Steps: []trace.Step{
			{ID: "S2.T1", StartLine: 16, EndLine: 18},
		},
	})
	result := testSummary()
	result.SectionSummaries["S2"] = summarize.SectionResult{
		Summary: "Changed second section.",
		StepSummaries: map[string]string{
			"S2.T1": "Changed second step.",
		},
	}

	got, err := WriteSession(WriteOptions{
		RecallDir:       recallDir,
		Config:          config.Default(),
		PreviousDir:     previousDir,
		ChangedSections: map[string]bool{"S2": true},
	}, session, result)
	if err != nil {
		t.Fatal(err)
	}

	if gotSection := readFile(t, filepath.Join(got.Dir, "sections", "S001.md")); gotSection != "old section one" {
		t.Fatalf("unchanged section was rewritten:\n%s", gotSection)
	}
	if gotSection := readFile(t, filepath.Join(got.Dir, "sections", "S002.md")); !strings.Contains(gotSection, "Changed second section.") {
		t.Fatalf("changed section was not rewritten:\n%s", gotSection)
	}
	if _, err := os.Stat(filepath.Join(got.Dir, "sections", "S003.md")); !os.IsNotExist(err) {
		t.Fatalf("stale section still exists: %v", err)
	}
}

func TestWriteSessionAggregateWritesRootSummary(t *testing.T) {
	recallDir := filepath.Join(t.TempDir(), ".recall")
	session := testSession()
	rootStartedAt := time.Date(2026, 1, 1, 1, 2, 3, 0, time.UTC)
	rootEndedAt := time.Date(2026, 1, 4, 5, 6, 7, 0, time.UTC)
	rootDir := filepath.Join(recallDir, "sessions", "2026-01-01T010203Z-codex-session-001")
	writeTestMemoryFile(t, filepath.Join(rootDir, "sections", "S001.md"), "stale root section")

	got, err := WriteSessionAggregate(WriteAggregateOptions{
		RecallDir:     recallDir,
		Config:        config.Default(),
		RootStartedAt: rootStartedAt,
		RootEndedAt:   rootEndedAt,
	}, session, "Whole session summary.", []AggregateSegment{
		{
			ID:      "seg000",
			Path:    "segments/seg000/segment.md",
			Summary: "First segment summary.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.RootDir != rootDir {
		t.Fatalf("root dir = %q, want %q", got.RootDir, rootDir)
	}
	if _, err := os.Stat(filepath.Join(rootDir, "sections")); !os.IsNotExist(err) {
		t.Fatalf("root sections still exists: %v", err)
	}

	markdown := readFile(t, filepath.Join(rootDir, "session.md"))
	for _, want := range []string{
		"# Session",
		"summary: |",
		"- [seg000](segments/seg000/segment.md): First segment summary.",
	} {
		if !strings.Contains(markdown, want) {
			t.Fatalf("aggregate markdown missing %q:\n%s", want, markdown)
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
		Metadata: trace.Metadata{
			"forked_from_id": "parent-session-001",
		},
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

func writeTestMemoryFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
