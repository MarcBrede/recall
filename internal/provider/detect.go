package provider

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/MarcBrede/recall/internal/trace"
)

// DetectSource infers the source provider from the session file path.
func DetectSource(path string) (trace.Source, error) {
	if source, ok := SourceFromPath(path); ok {
		return source, nil
	}
	return "", fmt.Errorf("could not detect session provider from path %q", path)
}

// SourceFromPath returns a source hint when the path contains a known local
// agent data directory.
func SourceFromPath(path string) (trace.Source, bool) {
	parts := splitPath(path)
	for _, part := range parts {
		switch part {
		case ".codex":
			return trace.SourceCodex, true
		case ".claude":
			return trace.SourceClaude, true
		case ".pi":
			return trace.SourcePi, true
		}
	}
	return "", false
}

func splitPath(path string) []string {
	cleaned := filepath.Clean(path)
	return strings.FieldsFunc(cleaned, func(r rune) bool {
		return r == '/' || r == '\\'
	})
}
