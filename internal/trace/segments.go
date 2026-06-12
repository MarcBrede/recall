package trace

import "time"

// NormalizeSegmentBoundaries keeps compaction splits aligned to user-message
// boundaries. Some agents write final assistant/tool events after a compaction
// marker; those events still belong to the previous user-request section.
func NormalizeSegmentBoundaries(sessions []*FlatSession) []*FlatSession {
	normalized := make([]*FlatSession, 0, len(sessions))
	for _, session := range sessions {
		if session == nil || len(session.Events) == 0 {
			continue
		}
		if len(normalized) == 0 {
			normalized = append(normalized, session)
			continue
		}

		firstUser := firstEventIndex(session.Events, EventHumanUser)
		previous := normalized[len(normalized)-1]
		switch {
		case firstUser < 0:
			absorbSegment(previous, session)
			continue
		case firstUser > 0:
			absorbLeadingEvents(previous, session, firstUser)
		default:
			setContentStartLine(session, session.Events[0].SourceLine)
		}
		normalized = append(normalized, session)
	}

	for i, session := range normalized {
		session.SegmentIndex = i
	}
	return normalized
}

func firstEventIndex(events []Event, kind EventKind) int {
	for i, event := range events {
		if event.Kind == kind {
			return i
		}
	}
	return -1
}

func absorbSegment(previous *FlatSession, current *FlatSession) {
	previous.Events = append(previous.Events, current.Events...)
	previous.SourceEndLine = current.SourceEndLine
	for _, event := range current.Events {
		noteFlatTimestamp(previous, event.Timestamp)
	}
}

func absorbLeadingEvents(previous *FlatSession, current *FlatSession, count int) {
	previous.Events = append(previous.Events, current.Events[:count]...)
	current.Events = append([]Event(nil), current.Events[count:]...)
	setContentStartLine(current, current.Events[0].SourceLine)
	previous.SourceEndLine = current.ContentStartLine - 1

	for _, event := range previous.Events[len(previous.Events)-count:] {
		noteFlatTimestamp(previous, event.Timestamp)
	}
	recomputeFlatTimes(current)
}

func recomputeFlatTimes(session *FlatSession) {
	session.StartedAt = time.Time{}
	session.EndedAt = time.Time{}
	for _, event := range session.Events {
		noteFlatTimestamp(session, event.Timestamp)
	}
}

func setContentStartLine(session *FlatSession, line int) {
	session.ContentStartLine = line
	if session.CompactionSummary == "" {
		session.SourceStartLine = line
	}
}

func noteFlatTimestamp(session *FlatSession, timestamp time.Time) {
	if timestamp.IsZero() {
		return
	}
	if session.StartedAt.IsZero() || timestamp.Before(session.StartedAt) {
		session.StartedAt = timestamp
	}
	if session.EndedAt.IsZero() || timestamp.After(session.EndedAt) {
		session.EndedAt = timestamp
	}
}
