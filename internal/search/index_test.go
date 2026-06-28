package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MarcBrede/recall/internal/embed"
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

type countingEmbedClient struct {
	calls int
}

func (client *countingEmbedClient) Embed(ctx context.Context, req embed.Request) (embed.Response, error) {
	client.calls++
	return fakeEmbedClient{}.Embed(ctx, req)
}

type maxInputEmbedClient struct {
	maxRunes int
}

func (client maxInputEmbedClient) Embed(_ context.Context, req embed.Request) (embed.Response, error) {
	if got := len([]rune(req.Input)); got > client.maxRunes {
		return embed.Response{}, errors.New("input too large")
	}
	return embed.Response{Model: req.Model, Vector: []float32{1, 0}}, nil
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
	if got, want := results[0].SessionID, "test-session"; got != want {
		t.Fatalf("top result session id = %q, want %q", got, want)
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

func TestSearchFiltersBySessionID(t *testing.T) {
	recallDir := t.TempDir()
	writeTestFile(t, filepath.Join(recallDir, "sessions", "2026-01-02T030405Z-codex-session-a", "sections", "S001.md"), `---
id: "S1"
session_id: "session-a"
last_event_at: "2026-01-02T03:04:06Z"
summary: |
  Alpha implementation details for session A.
---

# S001
`)
	writeTestFile(t, filepath.Join(recallDir, "sessions", "2026-01-02T040506Z-codex-session-b", "sections", "S001.md"), `---
id: "S1"
session_id: "session-b"
last_event_at: "2026-01-02T04:05:06Z"
summary: |
  Alpha implementation details for session B.
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

	results, err := Search(context.Background(), opts, "alpha question", SearchOptions{
		Limit:      10,
		NodeTypes:  NodeTypeSection,
		SessionIDs: []string{"session-b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("results len = %d, want 1", len(results))
	}
	if got, want := results[0].SessionID, "session-b"; got != want {
		t.Fatalf("result session id = %q, want %q", got, want)
	}
	if !strings.Contains(results[0].MemoryPath, "session-b") {
		t.Fatalf("result path = %q, want session-b path", results[0].MemoryPath)
	}

	results, err = Search(context.Background(), opts, "alpha question", SearchOptions{
		Limit:      10,
		NodeTypes:  NodeTypeSection,
		SessionIDs: []string{"session-a,session-b", "session-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("comma-separated results len = %d, want 2", len(results))
	}
}

func TestReindexTruncatesEmbeddingInput(t *testing.T) {
	recallDir := t.TempDir()
	writeScopedSection(t, recallDir, "session-a", "2026-01-02T03:04:06Z", strings.Repeat("Alpha ", maxEmbeddingInputChars))

	opts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    maxInputEmbedClient{maxRunes: maxEmbeddingInputChars},
	}
	if _, err := Reindex(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
}

func TestSearchFiltersBySinceAndTypeBeforeSimilarity(t *testing.T) {
	recallDir := t.TempDir()
	writeScopedSection(t, recallDir, "session-a", "2026-01-02T03:04:06Z", "Alpha implementation details for old session.")
	writeScopedSection(t, recallDir, "session-b", "2026-01-03T03:04:06Z", "Alpha implementation details for middle session.")
	writeScopedSection(t, recallDir, "session-c", "2026-01-04T03:04:06Z", "Alpha implementation details for newest session.")

	opts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}
	if _, err := Reindex(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	results, err := Search(context.Background(), opts, "alpha question", SearchOptions{
		Limit:     10,
		NodeTypes: NodeTypeSection,
		Since:     time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	for _, result := range results {
		if result.NodeType != NodeTypeSection {
			t.Fatalf("result node type = %q, want section", result.NodeType)
		}
		if result.SessionID == "session-a" {
			t.Fatalf("old session was included in since-filtered results: %+v", result)
		}
	}
}

func TestSearchFiltersByLastSessionsBeforeSimilarity(t *testing.T) {
	recallDir := t.TempDir()
	writeScopedSection(t, recallDir, "session-a", "2026-01-02T03:04:06Z", "Alpha implementation details for old session.")
	writeScopedSection(t, recallDir, "session-b", "2026-01-03T03:04:06Z", "Alpha implementation details for middle session.")
	writeScopedSection(t, recallDir, "session-c", "2026-01-04T03:04:06Z", "Alpha implementation details for newest session.")

	opts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}
	if _, err := Reindex(context.Background(), opts); err != nil {
		t.Fatal(err)
	}

	results, err := Search(context.Background(), opts, "alpha question", SearchOptions{
		Limit:        10,
		NodeTypes:    NodeTypeSection,
		LastSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("results len = %d, want 2", len(results))
	}
	seen := make(map[string]bool)
	for _, result := range results {
		seen[result.SessionID] = true
	}
	if seen["session-a"] {
		t.Fatal("oldest session was included in last-sessions-filtered results")
	}
	if !seen["session-b"] || !seen["session-c"] {
		t.Fatalf("seen sessions = %+v, want session-b and session-c", seen)
	}
}

func TestSearchRejectsCombinedSessionScopes(t *testing.T) {
	recallDir := t.TempDir()
	opts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}

	tests := []SearchOptions{
		{SessionIDs: []string{"session-a"}, Since: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)},
		{SessionIDs: []string{"session-a"}, LastSessions: 1},
		{Since: time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC), LastSessions: 1},
	}
	for _, searchOpts := range tests {
		_, err := Search(context.Background(), opts, "alpha question", searchOpts)
		if err == nil {
			t.Fatalf("Search() error = nil for options %+v, want error", searchOpts)
		}
		if !strings.Contains(err.Error(), "use only one") {
			t.Fatalf("Search() error = %q, want only-one scope error", err)
		}
	}
}

func TestSearchEmptySessionScopeSkipsQueryEmbedding(t *testing.T) {
	recallDir := t.TempDir()
	writeScopedSection(t, recallDir, "session-a", "2026-01-02T03:04:06Z", "Alpha implementation details for old session.")

	indexOpts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    fakeEmbedClient{},
	}
	if _, err := Reindex(context.Background(), indexOpts); err != nil {
		t.Fatal(err)
	}

	counter := &countingEmbedClient{}
	searchOpts := Options{
		RecallDir: recallDir,
		Model:     "fake-embedding-model",
		Client:    counter,
	}
	results, err := Search(context.Background(), searchOpts, "alpha question", SearchOptions{
		Limit: 10,
		Since: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("results len = %d, want 0", len(results))
	}
	if counter.calls != 0 {
		t.Fatalf("query embeddings = %d, want 0", counter.calls)
	}
}

func TestEnsureSchemaAddsSessionIDColumn(t *testing.T) {
	recallDir := t.TempDir()
	db, err := openDB(recallDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`create table nodes (
		id integer primary key,
		node_type text not null,
		memory_path text not null unique,
		content text not null,
		content_hash text not null,
		last_event_at text not null
	)`); err != nil {
		t.Fatal(err)
	}
	if err := ensureSchema(db); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Query(`pragma table_info(nodes)`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue any
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		if name == "session_id" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("session_id column was not added")
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

func writeScopedSection(t *testing.T, recallDir string, sessionID string, lastEventAt string, summary string) {
	t.Helper()
	writeTestFile(t, filepath.Join(recallDir, "sessions", lastEventAt[:10]+"-codex-"+sessionID, "sections", "S001.md"), `---
id: "S1"
session_id: "`+sessionID+`"
last_event_at: "`+lastEventAt+`"
summary: |
  `+summary+`
---

# S001
`)
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
