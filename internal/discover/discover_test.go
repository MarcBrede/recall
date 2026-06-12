package discover

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiscoverReturnsNewestSessionsAcrossCodexAndClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".codex", "sessions", "2026", "01", "02", "codex-old.jsonl"), `{"timestamp":"2026-01-02T03:04:05Z","type":"session_meta","payload":{"id":"codex-old","timestamp":"2026-01-02T03:04:00Z"}}
{"timestamp":"2026-01-02T03:05:00Z","type":"event_msg","payload":{"type":"user_message","message":"old"}}
`)
	writeFile(t, filepath.Join(home, ".claude", "projects", "project-a", "claude-new.jsonl"), `{"type":"user","sessionId":"claude-new","timestamp":"2026-01-03T03:04:05Z","message":{"role":"user","content":"new"}}
{"type":"assistant","sessionId":"claude-new","timestamp":"2026-01-03T03:06:00Z","message":{"role":"assistant","content":[]}}
`)
	writeFile(t, filepath.Join(home, ".claude", "projects", "project-a", "subagents", "agent.jsonl"), `{"type":"user","sessionId":"claude-subagent","timestamp":"2026-01-04T03:04:05Z","message":{"role":"user","content":"skip"}}
`)

	got, err := Discover(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2: %#v", len(got), got)
	}

	if got[0].ExternalID != "claude-new" {
		t.Fatalf("first external id = %q, want claude-new", got[0].ExternalID)
	}
	if got[0].Source != "claude" {
		t.Fatalf("first source = %q, want claude", got[0].Source)
	}
	if got[0].LastEventAt != "2026-01-03T03:06:00Z" {
		t.Fatalf("first last_event_at = %q", got[0].LastEventAt)
	}
	if got[1].ExternalID != "codex-old" {
		t.Fatalf("second external id = %q, want codex-old", got[1].ExternalID)
	}
	if got[1].StartedAt != "2026-01-02T03:04:00Z" {
		t.Fatalf("second started_at = %q", got[1].StartedAt)
	}
}

func TestDiscoverLastLimitsResults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".codex", "sessions", "a.jsonl"), `{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"a"}}`)
	writeFile(t, filepath.Join(home, ".codex", "sessions", "b.jsonl"), `{"timestamp":"2026-01-02T00:00:00Z","type":"session_meta","payload":{"id":"b"}}`)

	got, err := Discover(context.Background(), Options{Last: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ExternalID != "b" {
		t.Fatalf("external id = %q, want b", got[0].ExternalID)
	}
}

func TestDiscoverSortsSessionsWithoutTimestampsByModifiedAt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	validPath := filepath.Join(home, ".codex", "sessions", "valid.jsonl")
	noTimestampPath := filepath.Join(home, ".claude", "projects", "project-a", "no-timestamp.jsonl")
	writeFile(t, validPath, `{"timestamp":"2026-01-01T00:00:00Z","type":"session_meta","payload":{"id":"valid"}}`)
	writeFile(t, noTimestampPath, `{"type":"user","sessionId":"no-timestamp","message":{"role":"user","content":"no timestamp"}}`)
	setMTime(t, validPath, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	setMTime(t, noTimestampPath, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))

	got, err := Discover(context.Background(), Options{Last: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ExternalID != "no-timestamp" {
		t.Fatalf("external id = %q, want no-timestamp", got[0].ExternalID)
	}
	if got[0].LastEventAt != "" {
		t.Fatalf("last_event_at = %q, want empty", got[0].LastEventAt)
	}
	if got[0].ModifiedAt != "2026-01-02T00:00:00Z" {
		t.Fatalf("modified_at = %q", got[0].ModifiedAt)
	}
}

func TestDiscoverIgnoresClaudeTitleOnlyFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	realPath := filepath.Join(home, ".claude", "projects", "project-a", "real.jsonl")
	titlePath := filepath.Join(home, ".claude", "projects", "project-a", "title-only.jsonl")
	writeFile(t, realPath, `{"type":"user","sessionId":"real","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"real session"}}
`)
	writeFile(t, titlePath, `{"type":"ai-title","aiTitle":"Generated title","sessionId":"title-only"}
`)
	setMTime(t, realPath, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	setMTime(t, titlePath, time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC))

	got, err := Discover(context.Background(), Options{Last: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %#v", len(got), got)
	}
	if got[0].ExternalID != "real" {
		t.Fatalf("external id = %q, want real", got[0].ExternalID)
	}
}

func TestDiscoverIgnoresMalformedLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	writeFile(t, filepath.Join(home, ".claude", "projects", "project-a", "fallback.jsonl"), `not-json
{"type":"user","timestamp":"2026-01-02T00:00:00Z","message":{"role":"user","content":"missing session id"}}
`)

	got, err := Discover(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ExternalID != "fallback" {
		t.Fatalf("external id = %q, want fallback", got[0].ExternalID)
	}
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func setMTime(t *testing.T, path string, value time.Time) {
	t.Helper()
	if err := os.Chtimes(path, value, value); err != nil {
		t.Fatal(err)
	}
}
