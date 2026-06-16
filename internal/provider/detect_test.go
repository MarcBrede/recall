package provider

import (
	"testing"

	"github.com/MarcBrede/recall/internal/trace"
)

func TestSourceFromPath(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		source trace.Source
		ok     bool
	}{
		{
			name:   "codex",
			path:   "/Users/example/.codex/sessions/2026/06/session.jsonl",
			source: trace.SourceCodex,
			ok:     true,
		},
		{
			name:   "claude",
			path:   "/Users/example/.claude/projects/-Users-example/session.jsonl",
			source: trace.SourceClaude,
			ok:     true,
		},
		{
			name: "unknown",
			path: "/tmp/session.jsonl",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, ok := SourceFromPath(tt.path)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if source != tt.source {
				t.Fatalf("source = %q, want %q", source, tt.source)
			}
		})
	}
}
