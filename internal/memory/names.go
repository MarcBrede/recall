package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/marc-brede/recall/internal/trace"
)

const (
	sessionFileName = "session.md"
	sectionsDirName = "sections"
)

func sessionDirName(session *trace.Session) string {
	timestamp := session.EndedAt
	if timestamp.IsZero() {
		timestamp = session.StartedAt
	}

	timePart := "unknown-time"
	if !timestamp.IsZero() {
		timePart = timestamp.UTC().Format("2006-01-02T150405Z")
	}

	source := sanitizeName(string(session.Source))
	if source == "" {
		source = "unknown-source"
	}

	id := sanitizeName(session.ExternalID)
	if id == "" {
		id = sanitizeName(strings.TrimSuffix(filepath.Base(session.SourceFile), filepath.Ext(session.SourceFile)))
	}
	if id == "" {
		id = "unknown-id"
	}

	return fmt.Sprintf("%s-%s-%s-seg%03d", timePart, source, id, session.SegmentIndex)
}

func sectionFileName(index int) string {
	return sectionDisplayID(index) + ".md"
}

func sectionDisplayID(index int) string {
	return fmt.Sprintf("S%03d", index)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	lastDash := false
	for _, r := range value {
		keep := unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '_' || r == '-'
		if keep {
			builder.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			builder.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
