package obs

import (
	"log/slog"

	"github.com/marc-brede/recall/internal/trace"
)

// trace groups the identifying fields shared by flat and prepared sessions into
// a single nested "session" attribute. Defining it once here is what keeps
// agent type, external id, and segment consistent across every log line — call
// sites bind the group rather than restating individual keys.
func traceAttr(source trace.Source, externalID string, segment int) slog.Attr {
	return slog.Group("session",
		slog.String("agent", string(source)),
		slog.String("external_id", externalID),
		slog.Int("segment", segment),
	)
}

// Session describes a prepared session for logging.
func Session(s *trace.Session) slog.Attr {
	return traceAttr(s.Source, s.ExternalID, s.SegmentIndex)
}

// Flat describes a provider-normalized session before section splitting. Use it
// when a session fails to prepare and only the flat form is available.
func Flat(f *trace.FlatSession) slog.Attr {
	return traceAttr(f.Source, f.ExternalID, f.SegmentIndex)
}
