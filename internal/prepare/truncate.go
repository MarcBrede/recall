package prepare

import (
	"fmt"

	"github.com/marc-brede/recall/internal/trace"
)

const maxToolIOChars = 1000

// truncateToolIO returns a copy of the session where large tool inputs and
// outputs are shortened to their first maxToolIOChars characters.
func truncateToolIO(session *trace.FlatSession) *trace.FlatSession {
	if session == nil {
		return nil
	}

	truncated := *session
	truncated.Metadata = cloneMetadata(session.Metadata)
	truncated.Events = make([]trace.Event, len(session.Events))

	for i, event := range session.Events {
		event.Metadata = cloneMetadata(event.Metadata)
		if event.Kind == trace.EventToolCall || event.Kind == trace.EventToolOutput {
			event.Text = truncateText(event.Text)
		}
		truncated.Events[i] = event
	}

	return &truncated
}

func truncateText(text string) string {
	runes := []rune(text)
	if len(runes) <= maxToolIOChars {
		return text
	}

	return fmt.Sprintf(
		"%s\n\n[truncated: original_chars=%d, kept_chars=%d]",
		string(runes[:maxToolIOChars]),
		len(runes),
		maxToolIOChars,
	)
}

func cloneMetadata(metadata trace.Metadata) trace.Metadata {
	if metadata == nil {
		return nil
	}

	clone := make(trace.Metadata, len(metadata))
	for key, value := range metadata {
		clone[key] = value
	}
	return clone
}
