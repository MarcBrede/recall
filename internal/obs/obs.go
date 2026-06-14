// Package obs holds all of recall's observability wiring: logger construction,
// context propagation, and the canonical attribute helpers. Keeping it in one
// place lets the rest of the codebase stay free of logging boilerplate — call
// sites bind an attribute group once and let child loggers carry it.
package obs

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

// LevelSilent is above slog.LevelError, so a handler set to it drops everything.
const LevelSilent = slog.Level(12)

type ctxKey struct{}

// Options configures the process logger.
type Options struct {
	// Level is the minimum level emitted to the primary stream (stderr).
	// Use LevelSilent to mute.
	Level slog.Level
	// JSON emits machine-readable JSON on the primary stream instead of text.
	JSON bool
	// File, when non-nil, is a second sink that always receives NDJSON. Open it
	// with O_APPEND so concurrent processes can share one file without their
	// lines interleaving.
	File io.Writer
	// FileLevel is the minimum level written to File.
	FileLevel slog.Level
}

// New builds a logger. The primary stream is always stderr (callers pass
// os.Stderr) because stdout is reserved for the pipeline's JSON data contract.
// When opts.File is set, records fan out to both sinks.
func New(w io.Writer, opts Options) *slog.Logger {
	handlers := []slog.Handler{streamHandler(w, opts.Level, opts.JSON)}
	if opts.File != nil {
		handlers = append(handlers, slog.NewJSONHandler(opts.File, &slog.HandlerOptions{Level: opts.FileLevel}))
	}
	if len(handlers) == 1 {
		return slog.New(handlers[0])
	}
	return slog.New(&fanoutHandler{handlers: handlers})
}

func streamHandler(w io.Writer, level slog.Level, json bool) slog.Handler {
	handlerOpts := &slog.HandlerOptions{Level: level}
	if json {
		return slog.NewJSONHandler(w, handlerOpts)
	}
	return slog.NewTextHandler(w, handlerOpts)
}

// ParseLevel maps a string to a slog.Level. Unknown and empty values default to
// warn, which keeps piped commands quiet while still surfacing problems.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off", "silent", "none":
		return LevelSilent
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

// Into returns a context carrying the logger so deep code can retrieve it
// without threading a *slog.Logger parameter through every signature.
func Into(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// From returns the logger bound to ctx, or slog.Default() when none is set.
func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && logger != nil {
		return logger
	}
	return slog.Default()
}
