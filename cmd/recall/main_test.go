package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marc-brede/recall/internal/memory"
)

func TestIngestLastSkipsAlreadyIndexedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeTestFile(t, filepath.Join(home, ".codex", "sessions", "session.jsonl"), `{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"session-001","timestamp":"2026-01-02T03:04:05Z"}}
{"timestamp":"2026-01-02T03:06:30Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}
`)
	writeTestFile(t, filepath.Join(home, ".recall", "sessions", ".index.json"), `{
  "schema_version": 1,
  "entries": {
    "codex:session-001:0": {
      "source": "codex",
      "external_id": "session-001",
      "segment_index": 0,
      "source_file": "/tmp/session.jsonl",
      "source_start_line": 1,
      "source_end_line": 2,
      "session_started_at": "2026-01-02T03:04:05Z",
      "session_last_event_at": "2026-01-02T03:06:30Z",
      "memory_dir": "sessions/2026-01-02T030630Z-codex-session-001-seg000",
      "indexed_at": "2026-01-03T04:05:06Z"
    }
  }
}`)

	output := captureStdout(t, func() {
		if err := runIngestLast(1, false, false, false); err != nil {
			t.Fatal(err)
		}
	})

	var decoded ingestBatchOutput
	if err := json.Unmarshal([]byte(output), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Discovered != 1 {
		t.Fatalf("discovered = %d, want 1", decoded.Discovered)
	}
	if decoded.Queued != 0 {
		t.Fatalf("queued = %d, want 0", decoded.Queued)
	}
	if decoded.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", decoded.Skipped)
	}
	if decoded.Results[0].Status != "skipped" {
		t.Fatalf("status = %q, want skipped", decoded.Results[0].Status)
	}
	if decoded.Results[0].Reason != "already_indexed" {
		t.Fatalf("reason = %q, want already_indexed", decoded.Results[0].Reason)
	}
}

func TestIngestDryRunLastPlansAlreadyIndexedSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeTestFile(t, filepath.Join(home, ".codex", "sessions", "session.jsonl"), `{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"session-001","timestamp":"2026-01-02T03:04:05Z"}}
{"timestamp":"2026-01-02T03:06:30Z","type":"event_msg","payload":{"type":"user_message","message":"hello"}}
`)
	writeTestFile(t, filepath.Join(home, ".recall", "sessions", ".index.json"), `{
  "schema_version": 1,
  "entries": {
    "codex:session-001:0": {
      "source": "codex",
      "external_id": "session-001",
      "segment_index": 0,
      "source_file": "/tmp/session.jsonl",
      "source_start_line": 1,
      "source_end_line": 2,
      "session_started_at": "2026-01-02T03:04:05Z",
      "session_last_event_at": "2026-01-02T03:06:30Z",
      "memory_dir": "sessions/2026-01-02T030630Z-codex-session-001-seg000",
      "indexed_at": "2026-01-03T04:05:06Z"
    }
  }
}`)

	output := captureStdout(t, func() {
		if err := runIngestLast(1, false, false, true); err != nil {
			t.Fatal(err)
		}
	})

	if want := "No sessions would be ingested. discovered=1 segments=1 skipped=1"; !strings.Contains(output, want) {
		t.Fatalf("output missing %q:\n%s", want, output)
	}
}

func TestSectionListSummaryCollapsesNewSegment(t *testing.T) {
	got := sectionListSummary([]sectionPlanEntry{
		{ID: "S1", Reason: "new_segment"},
		{ID: "S2", Reason: "new_segment"},
		{ID: "S3", Reason: "new_segment", Status: memory.SectionStatusOpen},
	})
	want := "all 3 sections (S1-S3, open S3)"
	if got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
