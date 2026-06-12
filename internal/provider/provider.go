package provider

import (
	"context"

	"github.com/marc-brede/recall/internal/trace"
)

// Parser translates one source JSONL file into one or more flat normalized
// traces. A physical file can contain multiple memory sessions when the agent
// compacts context and continues in the same file.
type Parser interface {
	Parse(ctx context.Context, path string) ([]*trace.FlatSession, error)
}
