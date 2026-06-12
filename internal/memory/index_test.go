package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marc-brede/recall/internal/trace"
)

func TestIndexUpsertSaveLoadAndSkip(t *testing.T) {
	recallDir := filepath.Join(t.TempDir(), ".recall")
	index, err := LoadIndex(recallDir)
	if err != nil {
		t.Fatal(err)
	}

	session := &trace.Session{
		Source:          trace.SourceCodex,
		ExternalID:      "session-001",
		SegmentIndex:    2,
		SourceFile:      "/tmp/session.jsonl",
		SourceStartLine: 20,
		SourceEndLine:   40,
		StartedAt:       time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		EndedAt:         time.Date(2026, 1, 2, 3, 6, 30, 0, time.UTC),
	}
	writeResult := &WriteResult{
		Dir: filepath.Join(recallDir, "sessions", "2026-01-02T030630Z-codex-session-001-seg002"),
	}

	if !index.Upsert(recallDir, session, writeResult, time.Date(2026, 1, 3, 4, 5, 6, 0, time.UTC)) {
		t.Fatal("upsert returned false")
	}
	if !index.IsIndexed(session) {
		t.Fatal("session was not marked indexed")
	}
	changedSession := *session
	changedSession.SourceEndLine = 41
	if index.IsIndexed(&changedSession) {
		t.Fatal("session with newer last event was marked indexed")
	}
	if err := index.Save(recallDir); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadIndex(recallDir)
	if err != nil {
		t.Fatal(err)
	}
	entry := loaded.Entries[IndexKey(trace.SourceCodex, "session-001", 2)]
	if entry.MemoryDir != "sessions/2026-01-02T030630Z-codex-session-001-seg002" {
		t.Fatalf("memory dir = %q", entry.MemoryDir)
	}
	if entry.SegmentIndex != 2 {
		t.Fatalf("segment index = %d, want 2", entry.SegmentIndex)
	}
	if entry.SourceFile != "/tmp/session.jsonl" {
		t.Fatalf("source file = %q", entry.SourceFile)
	}
	if entry.SourceStartLine != 20 || entry.SourceEndLine != 40 {
		t.Fatalf("source lines = %d-%d, want 20-40", entry.SourceStartLine, entry.SourceEndLine)
	}
	if entry.SessionStartedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("session started at = %q", entry.SessionStartedAt)
	}
	if entry.SessionLastEventAt != "2026-01-02T03:06:30Z" {
		t.Fatalf("session last event at = %q", entry.SessionLastEventAt)
	}
	if entry.IndexedAt != "2026-01-03T04:05:06Z" {
		t.Fatalf("indexed at = %q", entry.IndexedAt)
	}
}

func TestLoadIndexTreatsEmptyFileAsFreshIndex(t *testing.T) {
	recallDir := filepath.Join(t.TempDir(), ".recall")
	if err := os.MkdirAll(filepath.Join(recallDir, "sessions"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(IndexPath(recallDir), nil, 0644); err != nil {
		t.Fatal(err)
	}

	index, err := LoadIndex(recallDir)
	if err != nil {
		t.Fatal(err)
	}
	if index.SchemaVersion != 1 {
		t.Fatalf("schema version = %d, want 1", index.SchemaVersion)
	}
	if len(index.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(index.Entries))
	}
}
