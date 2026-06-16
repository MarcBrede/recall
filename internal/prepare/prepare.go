package prepare

import (
	"errors"
	"fmt"
	"strings"

	"github.com/MarcBrede/recall/internal/trace"
)

func FromFlatSession(session *trace.FlatSession) (*trace.Session, error) {
	if session == nil {
		return nil, errors.New("prepare: nil session")
	}

	prepared := truncateToolIO(session)
	structured := splitSession(prepared)
	return &structured, nil
}

func RenderForLLM(session *trace.Session) (string, error) {
	if session == nil {
		return "", errors.New("prepare: nil session")
	}

	return renderSession(*session), nil
}

func RenderSectionForLLM(session *trace.Session, section *trace.Section) (string, error) {
	if session == nil {
		return "", errors.New("prepare: nil session")
	}
	if section == nil {
		return "", errors.New("prepare: nil section")
	}

	singleSection := trace.Session{
		Source:               session.Source,
		ExternalID:           session.ExternalID,
		SegmentIndex:         session.SegmentIndex,
		SourceFile:           session.SourceFile,
		SourceStartLine:      session.SourceStartLine,
		SourceEndLine:        session.SourceEndLine,
		ContentStartLine:     session.ContentStartLine,
		CompactionSummary:    session.CompactionSummary,
		CompactionSourceLine: session.CompactionSourceLine,
		CWD:                  session.CWD,
		GitBranch:            session.GitBranch,
		StartedAt:            session.StartedAt,
		EndedAt:              session.EndedAt,
		Sections:             []trace.Section{*section},
		Metadata:             session.Metadata,
	}
	return renderSession(singleSection), nil
}

func splitSession(flat *trace.FlatSession) trace.Session {
	session := trace.Session{
		Source:               flat.Source,
		ExternalID:           flat.ExternalID,
		SegmentIndex:         flat.SegmentIndex,
		SourceFile:           flat.SourceFile,
		SourceStartLine:      flat.SourceStartLine,
		SourceEndLine:        flat.SourceEndLine,
		ContentStartLine:     flat.ContentStartLine,
		CompactionSummary:    flat.CompactionSummary,
		CompactionSourceLine: flat.CompactionSourceLine,
		CWD:                  flat.CWD,
		GitBranch:            flat.GitBranch,
		StartedAt:            flat.StartedAt,
		EndedAt:              flat.EndedAt,
		Metadata:             flat.Metadata,
	}

	var currentSection *trace.Section
	var currentStep *trace.Step

	flushStep := func() {
		if currentSection == nil || currentStep == nil || len(currentStep.Events) == 0 {
			currentStep = nil
			return
		}
		finalizeStep(currentStep)
		currentSection.Steps = append(currentSection.Steps, *currentStep)
		currentStep = nil
	}

	flushSection := func() {
		if currentSection == nil {
			return
		}
		flushStep()
		finalizeSection(currentSection)
		session.Sections = append(session.Sections, *currentSection)
		currentSection = nil
	}

	startSection := func(event trace.Event) {
		flushSection()
		id := fmt.Sprintf("S%d", len(session.Sections)+1)
		currentSection = &trace.Section{
			ID:        id,
			StartLine: event.SourceLine,
			EndLine:   event.SourceLine,
			StartedAt: event.Timestamp,
			EndedAt:   event.Timestamp,
		}
	}

	startStep := func() {
		if currentSection == nil {
			currentSection = &trace.Section{ID: fmt.Sprintf("S%d", len(session.Sections)+1)}
		}
		id := fmt.Sprintf("%s.T%d", currentSection.ID, len(currentSection.Steps)+1)
		currentStep = &trace.Step{ID: id}
	}

	for _, event := range flat.Events {
		if event.Kind == trace.EventHumanUser {
			startSection(event)
			startStep()
			addEvent(currentStep, event)
			flushStep()
			continue
		}

		if currentSection == nil {
			currentSection = &trace.Section{ID: fmt.Sprintf("S%d", len(session.Sections)+1)}
		}
		if currentStep == nil || shouldStartStep(*currentStep, event) {
			flushStep()
			startStep()
		}
		addEvent(currentStep, event)
	}

	flushSection()
	return session
}

func shouldStartStep(step trace.Step, event trace.Event) bool {
	if len(step.Events) == 0 {
		return false
	}
	if isUserOnlyStep(step) {
		return true
	}

	switch event.Kind {
	case trace.EventAssistantText:
		return stepHasKind(step, trace.EventToolCall) ||
			stepHasKind(step, trace.EventToolOutput) ||
			stepHasKind(step, trace.EventAssistantText)
	default:
		return false
	}
}

func addEvent(step *trace.Step, event trace.Event) {
	if step.StartLine == 0 || event.SourceLine < step.StartLine {
		step.StartLine = event.SourceLine
	}
	if event.SourceLine > step.EndLine {
		step.EndLine = event.SourceLine
	}
	if step.StartedAt.IsZero() || (!event.Timestamp.IsZero() && event.Timestamp.Before(step.StartedAt)) {
		step.StartedAt = event.Timestamp
	}
	if step.EndedAt.IsZero() || event.Timestamp.After(step.EndedAt) {
		step.EndedAt = event.Timestamp
	}
	step.Events = append(step.Events, event)
}

func finalizeStep(step *trace.Step) {
	for _, event := range step.Events {
		if step.StartLine == 0 || event.SourceLine < step.StartLine {
			step.StartLine = event.SourceLine
		}
		if event.SourceLine > step.EndLine {
			step.EndLine = event.SourceLine
		}
		if step.StartedAt.IsZero() || (!event.Timestamp.IsZero() && event.Timestamp.Before(step.StartedAt)) {
			step.StartedAt = event.Timestamp
		}
		if step.EndedAt.IsZero() || event.Timestamp.After(step.EndedAt) {
			step.EndedAt = event.Timestamp
		}
	}
}

func finalizeSection(section *trace.Section) {
	for _, step := range section.Steps {
		if section.StartLine == 0 || step.StartLine < section.StartLine {
			section.StartLine = step.StartLine
		}
		if step.EndLine > section.EndLine {
			section.EndLine = step.EndLine
		}
		if section.StartedAt.IsZero() || (!step.StartedAt.IsZero() && step.StartedAt.Before(section.StartedAt)) {
			section.StartedAt = step.StartedAt
		}
		if section.EndedAt.IsZero() || step.EndedAt.After(section.EndedAt) {
			section.EndedAt = step.EndedAt
		}
	}
}

func isUserOnlyStep(step trace.Step) bool {
	return len(step.Events) == 1 && step.Events[0].Kind == trace.EventHumanUser
}

func stepHasKind(step trace.Step, kind trace.EventKind) bool {
	for _, event := range step.Events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func renderSession(session trace.Session) string {
	var builder strings.Builder
	builder.WriteString(`<session`)
	if session.Source != "" {
		builder.WriteString(` source="`)
		builder.WriteString(string(session.Source))
		builder.WriteString(`"`)
	}
	builder.WriteString(">\n")

	if strings.TrimSpace(session.CompactionSummary) != "" {
		builder.WriteString("\n<compaction")
		if session.CompactionSourceLine != 0 {
			builder.WriteString(` source_line="`)
			builder.WriteString(fmt.Sprintf("%d", session.CompactionSourceLine))
			builder.WriteString(`"`)
		}
		builder.WriteString(">\n")
		builder.WriteString(strings.TrimSpace(session.CompactionSummary))
		builder.WriteString("\n</compaction>\n")
	}

	for _, section := range session.Sections {
		builder.WriteString("\n<section id=\"")
		builder.WriteString(section.ID)
		builder.WriteString("\">\n")

		for _, step := range section.Steps {
			builder.WriteString("\n<step id=\"")
			builder.WriteString(step.ID)
			builder.WriteString("\">\n")
			renderEvents(&builder, step.Events)
			builder.WriteString("</step:")
			builder.WriteString(step.ID)
			builder.WriteString(">\n")
		}
		builder.WriteString("</section:")
		builder.WriteString(section.ID)
		builder.WriteString(">\n")
	}
	builder.WriteString("</session>\n")

	return builder.String()
}

func renderEvents(builder *strings.Builder, events []trace.Event) {
	for _, event := range events {
		label, ok := eventLabel(event)
		if !ok {
			continue
		}

		text := strings.TrimSpace(event.Text)
		builder.WriteString(label)
		builder.WriteString("\n")
		if text != "" {
			builder.WriteString(text)
			builder.WriteString("\n")
		}
		builder.WriteString("\n")
	}
}

func eventLabel(event trace.Event) (string, bool) {
	switch event.Kind {
	case trace.EventHumanUser:
		return "USER", true
	case trace.EventAssistantText:
		return "ASSISTANT", true
	case trace.EventReasoning:
		return "REASONING", strings.TrimSpace(event.Text) != ""
	case trace.EventReasoningMarker:
		return "", false
	case trace.EventToolCall:
		if event.ToolName == "" {
			return "TOOL", true
		}
		return "TOOL " + event.ToolName, true
	case trace.EventToolOutput:
		return "TOOL_RESULT", true
	default:
		return strings.ToUpper(string(event.Kind)), true
	}
}
