package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marc-brede/recall/internal/embed"
)

type fakeEmbedClient struct{}

func (fakeEmbedClient) Embed(_ context.Context, req embed.Request) (embed.Response, error) {
	input := strings.ToLower(req.Input)
	switch {
	case strings.Contains(input, "alpha"):
		return embed.Response{Model: req.Model, Vector: []float32{1, 0}}, nil
	case strings.Contains(input, "beta"):
		return embed.Response{Model: req.Model, Vector: []float32{0, 1}}, nil
	default:
		return embed.Response{Model: req.Model, Vector: []float32{0.7, 0.7}}, nil
	}
}

func TestReindexAndSearchUsesMarkdownNodes(t *testing.T) {
	recallDir := t.TempDir()
	sessionDir := filepath.Join(recallDir, "sessions", "2026-01-02T030405Z-codex-test-session")
	writeTestFile(t, filepath.Join(sessionDir, "session.md"), `---
id: "test-session"
source: "codex"
last_event_at: "2026-01-02T03:04:05Z"
summary: |
  Alpha project setup and verification.
---

# Session

## Sections

- [S001](sections/S001.md): Alpha project setup.
`)
	writeTestFile(t, filepath.Join(sessionDir, "sections", "S001.md"), `---
id: "S1"
session_id: "test-session"
last_event_at: "2026-01-02T03:04:06Z"
summary: |
  Alpha implementation details.
---

# S001

## Steps

- S1.T1 lines 1-2: Alpha setup was completed.
`)
	writeTestFile(t, filepath.Join(sessionDir, "sections", "S002.md"), `---
id: "S2"
session_id: "test-session"
last_event_at: "2026-01-02T03:04:07Z"
summary: |
  Beta debugging details.
---

# S002

## Steps

- S2.T1 lines 3-4: Beta debugging was completed.
`)
	writeTestFile(t, filepath.Join(sessionDir, "segments", "seg000", "segment.md"), `---
id: "test-session"
segment_index: 0
last_event_at: "2026-01-02T03:04:08Z"
summary: |
  Beta segment overview.
---

# Segment seg000

## Sections

- [S002](sections/S002.md): Beta debugging details.
`)

	result, err := Reindex(context.Background(), Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Nodes, 4; got != want {
		t.Fatalf("nodes = %d, want %d", got, want)
	}
	if got, want := result.Embedded, 4; got != want {
		t.Fatalf("embedded = %d, want %d", got, want)
	}

	results, err := Search(context.Background(), Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}, "beta question", SearchOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if !strings.HasSuffix(results[0].MemoryPath, "sections/S002.md") {
		t.Fatalf("top result path = %q, want S002 section", results[0].MemoryPath)
	}

	results, err = Search(context.Background(), Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}, "beta question", SearchOptions{Limit: 1, NodeTypes: NodeTypeSession})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("session results len = %d, want 1", len(results))
	}
	if got, want := results[0].NodeType, NodeTypeSession; got != want {
		t.Fatalf("session result node type = %q, want %q", got, want)
	}
	if !strings.HasSuffix(results[0].MemoryPath, "session.md") {
		t.Fatalf("session result path = %q, want session.md", results[0].MemoryPath)
	}

	results, err = Search(context.Background(), Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}, "alpha question", SearchOptions{Limit: 10, NodeTypes: NodeTypeSection})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("section results len = %d, want 2", len(results))
	}
	for _, result := range results {
		if got, want := result.NodeType, NodeTypeSection; got != want {
			t.Fatalf("section result node type = %q, want %q", got, want)
		}
	}

	results, err = Search(context.Background(), Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}, "beta question", SearchOptions{Limit: 10, NodeTypes: NodeTypeSession + "," + NodeTypeSegment})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("session-or-segment results len = %d, want 2", len(results))
	}
	for _, result := range results {
		switch result.NodeType {
		case NodeTypeSession, NodeTypeSegment:
		default:
			t.Fatalf("session-or-segment result node type = %q, want session or segment", result.NodeType)
		}
	}

	_, err = Search(context.Background(), Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}, "alpha question", SearchOptions{NodeTypes: "file"})
	if err == nil {
		t.Fatal("Search() invalid node type error = nil, want error")
	}
}

func TestReindexDeletesStaleNodes(t *testing.T) {
	recallDir := t.TempDir()
	sessionDir := filepath.Join(recallDir, "sessions", "2026-01-02T030405Z-codex-test-session")
	stalePath := filepath.Join(sessionDir, "sections", "S001.md")
	writeTestFile(t, stalePath, `---
last_event_at: "2026-01-02T03:04:06Z"
summary: |
  Alpha temporary section.
---

# S001
`)

	opts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}
	if _, err := Reindex(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stalePath); err != nil {
		t.Fatal(err)
	}
	result, err := Reindex(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Deleted, 1; got != want {
		t.Fatalf("deleted = %d, want %d", got, want)
	}
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
