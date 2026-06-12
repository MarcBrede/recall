package provider

import (
	"fmt"

	"github.com/marc-brede/recall/internal/provider/claude"
	"github.com/marc-brede/recall/internal/provider/codex"
	"github.com/marc-brede/recall/internal/provider/pi"
	"github.com/marc-brede/recall/internal/trace"
)

// NewParser returns the parser for a detected source provider.
func NewParser(source trace.Source) (Parser, error) {
	switch source {
	case trace.SourceCodex:
		return codex.Parser{}, nil
	case trace.SourceClaude:
		return claude.Parser{}, nil
	case trace.SourcePi:
		return pi.Parser{}, nil
	default:
		return nil, fmt.Errorf("unsupported session provider %q", source)
	}
}
