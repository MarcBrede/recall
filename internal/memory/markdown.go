package memory

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/marc-brede/recall/internal/summarize"
	"github.com/marc-brede/recall/internal/trace"
)

func RenderSessionMarkdown(session *trace.Session, result *summarize.Result, sectionPathByID map[string]string) string {
	var builder strings.Builder

	writeFrontMatterStart(&builder)
	writeYAMLString(&builder, "id", session.ExternalID)
	writeYAMLString(&builder, "source", string(session.Source))
	writeYAMLString(&builder, "source_file", session.SourceFile)
	writeYAMLString(&builder, "forked_from_session_id", forkedFromSessionID(session.Metadata))
	writeYAMLIntValue(&builder, "segment_index", session.SegmentIndex)
	writeYAMLInt(&builder, "source_start_line", session.SourceStartLine)
	writeYAMLInt(&builder, "source_end_line", session.SourceEndLine)
	writeYAMLInt(&builder, "content_start_line", session.ContentStartLine)
	writeYAMLInt(&builder, "compaction_source_line", session.CompactionSourceLine)
	writeYAMLString(&builder, "started_at", formatTime(session.StartedAt))
	writeYAMLString(&builder, "last_event_at", formatTime(session.EndedAt))
	writeYAMLBlock(&builder, "summary", result.SessionSummary)
	writeFrontMatterEnd(&builder)

	builder.WriteString("# Session\n\n")
	if strings.TrimSpace(result.CompactionSummary) != "" {
		builder.WriteString("## Compaction\n\n")
		builder.WriteString(strings.TrimSpace(result.CompactionSummary))
		builder.WriteString("\n\n")
	}
	builder.WriteString("## Sections\n\n")
	for i, section := range session.Sections {
		sectionResult := result.SectionSummaries[section.ID]
		link := filepath.ToSlash(sectionPathByID[section.ID])
		fmt.Fprintf(
			&builder,
			"- [%s](%s): %s\n",
			sectionDisplayID(i+1),
			link,
			oneLine(sectionResult.Summary),
		)
	}

	return builder.String()
}

func RenderSectionMarkdown(session *trace.Session, section *trace.Section, index int, result *summarize.SectionResult) string {
	var builder strings.Builder

	writeFrontMatterStart(&builder)
	writeYAMLString(&builder, "id", section.ID)
	writeYAMLString(&builder, "session_id", session.ExternalID)
	writeYAMLString(&builder, "source_file", session.SourceFile)
	writeYAMLIntValue(&builder, "session_segment_index", session.SegmentIndex)
	writeYAMLInt(&builder, "start_line", section.StartLine)
	writeYAMLInt(&builder, "end_line", section.EndLine)
	writeYAMLString(&builder, "started_at", formatTime(section.StartedAt))
	writeYAMLString(&builder, "last_event_at", formatTime(section.EndedAt))
	writeYAMLBlock(&builder, "summary", result.Summary)
	writeFrontMatterEnd(&builder)

	fmt.Fprintf(&builder, "# %s\n\n", sectionDisplayID(index))
	builder.WriteString("## Steps\n\n")
	for _, step := range section.Steps {
		fmt.Fprintf(
			&builder,
			"- `%s` lines `%s`: %s\n",
			step.ID,
			lineRange(step.StartLine, step.EndLine),
			oneLine(result.StepSummaries[step.ID]),
		)
	}

	return builder.String()
}

func writeFrontMatterStart(builder *strings.Builder) {
	builder.WriteString("---\n")
}

func writeFrontMatterEnd(builder *strings.Builder) {
	builder.WriteString("---\n\n")
}

func writeYAMLString(builder *strings.Builder, key string, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(builder, "%s: %s\n", key, quoteString(value))
}

func writeYAMLInt(builder *strings.Builder, key string, value int) {
	if value == 0 {
		return
	}
	writeYAMLIntValue(builder, key, value)
}

func writeYAMLIntValue(builder *strings.Builder, key string, value int) {
	fmt.Fprintf(builder, "%s: %d\n", key, value)
}

func writeYAMLBlock(builder *strings.Builder, key string, value string) {
	builder.WriteString(key)
	builder.WriteString(": |\n")
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	if value == "" {
		builder.WriteString("  \n")
		return
	}
	for _, line := range strings.Split(value, "\n") {
		builder.WriteString("  ")
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
}

func quoteString(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func lineRange(start int, end int) string {
	if start == 0 && end == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d-%d", start, end)
}
