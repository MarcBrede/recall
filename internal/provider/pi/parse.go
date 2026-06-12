package pi

import (
	"context"
	"errors"

	"github.com/marc-brede/recall/internal/trace"
)

var errNotImplemented = errors.New("pi parser not implemented")

// Parser translates Pi JSONL sessions into flat normalized traces.
type Parser struct{}

func (Parser) Parse(context.Context, string) ([]*trace.FlatSession, error) {
	return nil, errNotImplemented
}
