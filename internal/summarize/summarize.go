package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/MarcBrede/recall/internal/llm"
	"github.com/MarcBrede/recall/internal/prepare"
	"github.com/MarcBrede/recall/internal/trace"
)

const maxSummaryAttempts = 2
const hierarchicalMinSteps = 25
const hierarchicalMinRenderedChars = 60000

func WithProvider(ctx context.Context, provider string, model string, reasoningLevel string, session *trace.Session) (*Result, llm.Usage, error) {
	return WithProviderOptions(ctx, provider, model, reasoningLevel, llm.Options{}, nil, session)
}

func WithProviderLimited(ctx context.Context, provider string, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session) (*Result, llm.Usage, error) {
	return WithProviderOptions(ctx, provider, model, reasoningLevel, llm.Options{}, limiter, session)
}

func WithProviderOptions(ctx context.Context, provider string, model string, reasoningLevel string, options llm.Options, limiter *llm.Limiter, session *trace.Session) (*Result, llm.Usage, error) {
	client, err := llm.NewWithOptions(provider, options)
	if err != nil {
		return nil, llm.Usage{}, err
	}
	return WithClientLimited(ctx, client, model, reasoningLevel, limiter, session)
}

func WithProviderOptionsIncremental(ctx context.Context, provider string, model string, reasoningLevel string, options llm.Options, limiter *llm.Limiter, session *trace.Session, existingSections map[string]SectionResult, sectionIDs []string) (*Result, llm.Usage, error) {
	client, err := llm.NewWithOptions(provider, options)
	if err != nil {
		return nil, llm.Usage{}, err
	}
	return WithClientIncremental(ctx, client, model, reasoningLevel, limiter, session, existingSections, sectionIDs)
}

func WithProviderOptionsAggregateSession(ctx context.Context, provider string, model string, reasoningLevel string, options llm.Options, limiter *llm.Limiter, segments []SegmentSummary) (string, llm.Usage, error) {
	client, err := llm.NewWithOptions(provider, options)
	if err != nil {
		return "", llm.Usage{}, err
	}
	return WithClientAggregateSession(ctx, client, model, reasoningLevel, limiter, segments)
}

// WithClient summarizes the session, retrying on validation failures. The
// returned usage is the sum across every attempt, so it reflects total spend
// even when a retry was needed.
func WithClient(ctx context.Context, client llm.Client, model string, reasoningLevel string, session *trace.Session) (*Result, llm.Usage, error) {
	return WithClientLimited(ctx, client, model, reasoningLevel, nil, session)
}

func WithClientLimited(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session) (*Result, llm.Usage, error) {
	input, err := prepare.RenderForLLM(session)
	if err != nil {
		return nil, llm.Usage{}, err
	}
	if shouldUseHierarchical(session, input) {
		return summarizeHierarchical(ctx, client, model, reasoningLevel, limiter, session)
	}
	return summarizeOneShot(ctx, client, model, reasoningLevel, limiter, session, input)
}

func WithClientIncremental(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session, existingSections map[string]SectionResult, sectionIDs []string) (*Result, llm.Usage, error) {
	sectionSummaries := copySectionResults(existingSections)

	updatedSections, usage, err := summarizeSelectedSections(ctx, client, model, reasoningLevel, limiter, session, sectionIDs)
	if err != nil {
		return nil, usage, err
	}
	for id, result := range updatedSections {
		sectionSummaries[id] = result
	}

	sessionSummary, sessionUsage, err := summarizeSessionFromSections(ctx, client, model, reasoningLevel, limiter, session, sectionSummaries)
	usage.Add(sessionUsage)
	if err != nil {
		return nil, usage, err
	}

	result := &Result{
		SessionSummary:    sessionSummary.SessionSummary,
		CompactionSummary: sessionSummary.CompactionSummary,
		SectionSummaries:  sectionSummaries,
	}
	if err := ValidateResult(session, result); err != nil {
		return nil, usage, err
	}
	return result, usage, nil
}

func WithClientAggregateSession(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, segments []SegmentSummary) (string, llm.Usage, error) {
	var usage llm.Usage
	input := renderAggregateSessionInput(segments)
	schema := buildAggregateSessionSchema()

	var lastErr error
	for attempt := 1; attempt <= maxSummaryAttempts; attempt++ {
		prompt := input
		if lastErr != nil {
			prompt = retryPrompt(input, lastErr, "Return the complete JSON object again. Include session_summary. Do not omit the required summary.")
		}

		response, err := generateStructured(ctx, limiter, client, llm.StructuredRequest{
			Model:          model,
			ReasoningLevel: reasoningLevel,
			SystemPrompt:   aggregateSessionPrompt(),
			UserPrompt:     prompt,
			SchemaName:     aggregateSessionSchemaName,
			Schema:         schema,
		})
		if err != nil {
			return "", usage, err
		}
		usage.Add(response.Usage)
		if err := writeRawOutput(response.Text); err != nil {
			return "", usage, err
		}

		result, err := decodeAndValidateAggregateSessionSummary(response.Text)
		if err == nil {
			return result.SessionSummary, usage, nil
		}
		lastErr = err
	}

	return "", usage, lastErr
}

func summarizeOneShot(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session, input string) (*Result, llm.Usage, error) {
	var usage llm.Usage

	schema, err := buildSchema(session)
	if err != nil {
		return nil, usage, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxSummaryAttempts; attempt++ {
		prompt := input
		if lastErr != nil {
			prompt = retryPrompt(input, lastErr, "Return the complete JSON object again. Include compaction_summary, every section id, and every step id exactly once. Do not omit any required summary.")
		}

		response, err := generateStructured(ctx, limiter, client, llm.StructuredRequest{
			Model:          model,
			ReasoningLevel: reasoningLevel,
			SystemPrompt:   systemPrompt(),
			UserPrompt:     prompt,
			SchemaName:     schemaName,
			Schema:         schema,
		})
		if err != nil {
			return nil, usage, err
		}
		usage.Add(response.Usage)
		if err := writeRawOutput(response.Text); err != nil {
			return nil, usage, err
		}

		result, err := decodeAndValidate(session, response.Text)
		if err == nil {
			return result, usage, nil
		}
		lastErr = err
	}

	return nil, usage, lastErr
}

func summarizeHierarchical(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session) (*Result, llm.Usage, error) {
	sectionSummaries, usage, err := summarizeSections(ctx, client, model, reasoningLevel, limiter, session)
	if err != nil {
		return nil, usage, err
	}

	sessionSummary, sessionUsage, err := summarizeSessionFromSections(ctx, client, model, reasoningLevel, limiter, session, sectionSummaries)
	usage.Add(sessionUsage)
	if err != nil {
		return nil, usage, err
	}

	result := &Result{
		SessionSummary:    sessionSummary.SessionSummary,
		CompactionSummary: sessionSummary.CompactionSummary,
		SectionSummaries:  sectionSummaries,
	}
	if err := ValidateResult(session, result); err != nil {
		return nil, usage, err
	}
	return result, usage, nil
}

func summarizeSections(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session) (map[string]SectionResult, llm.Usage, error) {
	var sectionIDs []string
	for _, section := range session.Sections {
		sectionIDs = append(sectionIDs, section.ID)
	}
	return summarizeSelectedSections(ctx, client, model, reasoningLevel, limiter, session, sectionIDs)
}

func summarizeSelectedSections(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session, sectionIDs []string) (map[string]SectionResult, llm.Usage, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	selected := make(map[string]struct{}, len(sectionIDs))
	for _, id := range sectionIDs {
		selected[id] = struct{}{}
	}

	var mu sync.Mutex
	var usage llm.Usage
	var firstErr error
	results := make(map[string]SectionResult, len(sectionIDs))

	var wg sync.WaitGroup
	for i := range session.Sections {
		section := &session.Sections[i]
		if _, ok := selected[section.ID]; !ok {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()

			result, sectionUsage, err := summarizeSection(ctx, client, model, reasoningLevel, limiter, session, section)
			mu.Lock()
			defer mu.Unlock()
			usage.Add(sectionUsage)
			if err != nil {
				if firstErr == nil {
					firstErr = err
					cancel()
				}
				return
			}
			results[section.ID] = result
		}()
	}
	wg.Wait()

	if firstErr != nil {
		return nil, usage, firstErr
	}
	return results, usage, nil
}

func summarizeSection(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session, section *trace.Section) (SectionResult, llm.Usage, error) {
	var usage llm.Usage

	schema, err := buildSectionSchema(section)
	if err != nil {
		return SectionResult{}, usage, err
	}
	input, err := prepare.RenderSectionForLLM(session, section)
	if err != nil {
		return SectionResult{}, usage, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxSummaryAttempts; attempt++ {
		prompt := input
		if lastErr != nil {
			prompt = retryPrompt(input, lastErr, "Return the complete JSON object again. Include the section id and every step id exactly once. Do not omit any required summary.")
		}

		response, err := generateStructured(ctx, limiter, client, llm.StructuredRequest{
			Model:          model,
			ReasoningLevel: reasoningLevel,
			SystemPrompt:   sectionPrompt(),
			UserPrompt:     prompt,
			SchemaName:     sectionSchemaName,
			Schema:         schema,
		})
		if err != nil {
			return SectionResult{}, usage, err
		}
		usage.Add(response.Usage)
		if err := writeRawOutput(response.Text); err != nil {
			return SectionResult{}, usage, err
		}

		result, err := decodeAndValidateSection(section, response.Text)
		if err == nil {
			return result, usage, nil
		}
		lastErr = err
	}

	return SectionResult{}, usage, lastErr
}

func summarizeSessionFromSections(ctx context.Context, client llm.Client, model string, reasoningLevel string, limiter *llm.Limiter, session *trace.Session, sections map[string]SectionResult) (wireSessionResult, llm.Usage, error) {
	var usage llm.Usage

	schema, err := buildSessionSchema(session)
	if err != nil {
		return wireSessionResult{}, usage, err
	}
	input := renderSessionSummaryInput(session, sections)

	var lastErr error
	for attempt := 1; attempt <= maxSummaryAttempts; attempt++ {
		prompt := input
		if lastErr != nil {
			prompt = retryPrompt(input, lastErr, "Return the complete JSON object again. Include session_summary and compaction_summary. Do not omit any required summary.")
		}

		response, err := generateStructured(ctx, limiter, client, llm.StructuredRequest{
			Model:          model,
			ReasoningLevel: reasoningLevel,
			SystemPrompt:   sessionPrompt(),
			UserPrompt:     prompt,
			SchemaName:     sessionSchemaName,
			Schema:         schema,
		})
		if err != nil {
			return wireSessionResult{}, usage, err
		}
		usage.Add(response.Usage)
		if err := writeRawOutput(response.Text); err != nil {
			return wireSessionResult{}, usage, err
		}

		result, err := decodeAndValidateSessionSummary(session, response.Text)
		if err == nil {
			return result, usage, nil
		}
		lastErr = err
	}

	return wireSessionResult{}, usage, lastErr
}

func generateStructured(ctx context.Context, limiter *llm.Limiter, client llm.Client, req llm.StructuredRequest) (llm.Response, error) {
	return limiter.Do(ctx, func(ctx context.Context) (llm.Response, error) {
		return client.GenerateStructured(ctx, req)
	})
}

func decodeAndValidate(session *trace.Session, raw string) (*Result, error) {
	var wire wireResult
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return nil, err
	}
	result, err := resultFromWire(wire)
	if err != nil {
		return nil, err
	}
	if err := ValidateResult(session, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func decodeAndValidateSection(section *trace.Section, raw string) (SectionResult, error) {
	var wire wireSectionResult
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return SectionResult{}, err
	}
	return sectionResultFromWire(section, wire)
}

func decodeAndValidateSessionSummary(session *trace.Session, raw string) (wireSessionResult, error) {
	var result wireSessionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return wireSessionResult{}, err
	}
	if strings.TrimSpace(result.SessionSummary) == "" {
		return wireSessionResult{}, fmt.Errorf("summarize: session_summary is missing")
	}
	if strings.TrimSpace(session.CompactionSummary) != "" && strings.TrimSpace(result.CompactionSummary) == "" {
		return wireSessionResult{}, fmt.Errorf("summarize: compaction_summary is missing")
	}
	return result, nil
}

func decodeAndValidateAggregateSessionSummary(raw string) (wireAggregateSessionResult, error) {
	var result wireAggregateSessionResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return wireAggregateSessionResult{}, err
	}
	if strings.TrimSpace(result.SessionSummary) == "" {
		return wireAggregateSessionResult{}, fmt.Errorf("summarize: session_summary is missing")
	}
	return result, nil
}

func retryPrompt(input string, err error, instruction string) string {
	return input + "\n\n<retry_instructions>\nThe previous JSON response failed validation: " + err.Error() + "\n" + instruction + "\n</retry_instructions>\n"
}

func writeRawOutput(raw string) error {
	if path := os.Getenv("RECALL_LLM_RAW_OUTPUT_PATH"); path != "" {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			return fmt.Errorf("summarize: write raw LLM output: %w", err)
		}
	}
	return nil
}

func resultFromWire(wire wireResult) (Result, error) {
	result := Result{
		SessionSummary:    wire.SessionSummary,
		CompactionSummary: wire.CompactionSummary,
		SectionSummaries:  map[string]SectionResult{},
	}

	for _, section := range wire.SectionSummaries {
		if section.ID == "" {
			return Result{}, fmt.Errorf("summarize: section_summaries contains an empty id")
		}
		if _, exists := result.SectionSummaries[section.ID]; exists {
			return Result{}, fmt.Errorf("summarize: duplicate section summary for %s", section.ID)
		}

		sectionResult := SectionResult{
			Summary:       section.Summary,
			StepSummaries: map[string]string{},
		}
		for _, step := range section.StepSummaries {
			if step.ID == "" {
				return Result{}, fmt.Errorf("summarize: step_summaries contains an empty id for %s", section.ID)
			}
			if _, exists := sectionResult.StepSummaries[step.ID]; exists {
				return Result{}, fmt.Errorf("summarize: duplicate step summary for %s", step.ID)
			}
			sectionResult.StepSummaries[step.ID] = step.Summary
		}
		result.SectionSummaries[section.ID] = sectionResult
	}

	return result, nil
}

func sectionResultFromWire(section *trace.Section, wire wireSectionResult) (SectionResult, error) {
	if section == nil {
		return SectionResult{}, fmt.Errorf("summarize: nil section")
	}
	if wire.ID == "" {
		return SectionResult{}, fmt.Errorf("summarize: section summary id is empty")
	}
	if wire.ID != section.ID {
		return SectionResult{}, fmt.Errorf("summarize: expected section summary for %s, got %s", section.ID, wire.ID)
	}

	result := SectionResult{
		Summary:       wire.Summary,
		StepSummaries: map[string]string{},
	}
	for _, step := range wire.StepSummaries {
		if step.ID == "" {
			return SectionResult{}, fmt.Errorf("summarize: step_summaries contains an empty id for %s", section.ID)
		}
		if _, exists := result.StepSummaries[step.ID]; exists {
			return SectionResult{}, fmt.Errorf("summarize: duplicate step summary for %s", step.ID)
		}
		result.StepSummaries[step.ID] = step.Summary
	}

	expected := Result{
		SessionSummary:    "section-only validation",
		CompactionSummary: "section-only validation",
		SectionSummaries: map[string]SectionResult{
			section.ID: result,
		},
	}
	session := &trace.Session{Sections: []trace.Section{*section}}
	if err := ValidateResult(session, &expected); err != nil {
		return SectionResult{}, err
	}
	return result, nil
}

func shouldUseHierarchical(session *trace.Session, renderedInput string) bool {
	return countSteps(session) >= hierarchicalMinSteps || len(renderedInput) >= hierarchicalMinRenderedChars
}

func countSteps(session *trace.Session) int {
	if session == nil {
		return 0
	}
	var steps int
	for _, section := range session.Sections {
		steps += len(section.Steps)
	}
	return steps
}

func copySectionResults(sections map[string]SectionResult) map[string]SectionResult {
	copied := make(map[string]SectionResult, len(sections))
	for id, result := range sections {
		stepSummaries := make(map[string]string, len(result.StepSummaries))
		for stepID, summary := range result.StepSummaries {
			stepSummaries[stepID] = summary
		}
		copied[id] = SectionResult{
			Summary:       result.Summary,
			StepSummaries: stepSummaries,
		}
	}
	return copied
}

func renderSessionSummaryInput(session *trace.Session, sections map[string]SectionResult) string {
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
		summary := sections[section.ID]
		builder.WriteString("\n<section_summary id=\"")
		builder.WriteString(section.ID)
		builder.WriteString("\">\n")
		builder.WriteString(strings.TrimSpace(summary.Summary))
		builder.WriteString("\n</section_summary:")
		builder.WriteString(section.ID)
		builder.WriteString(">\n")
	}
	builder.WriteString("</session>\n")
	return builder.String()
}

func renderAggregateSessionInput(segments []SegmentSummary) string {
	var builder strings.Builder
	builder.WriteString("<session_segments>\n")
	for _, segment := range segments {
		builder.WriteString("\n<segment_summary id=\"")
		builder.WriteString(segment.ID)
		builder.WriteString("\">\n")
		builder.WriteString(strings.TrimSpace(segment.Summary))
		builder.WriteString("\n</segment_summary:")
		builder.WriteString(segment.ID)
		builder.WriteString(">\n")
	}
	builder.WriteString("</session_segments>\n")
	return builder.String()
}
