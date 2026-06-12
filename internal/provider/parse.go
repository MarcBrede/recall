package provider

import (
	"context"

	"github.com/marc-brede/recall/internal/trace"
)

// ParseFile detects the session provider from the path and dispatches to the
// matching parser.
func ParseFile(ctx context.Context, path string) ([]*trace.FlatSession, error) {
	source, err := DetectSource(path)
	if err != nil {
		return nil, err
	}

	parser, err := NewParser(source)
	if err != nil {
		return nil, err
	}

	return parser.Parse(ctx, path)
}
