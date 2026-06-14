package summarize

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/marc-brede/recall/internal/llm"
	"github.com/marc-brede/recall/internal/prepare"
	"github.com/marc-brede/recall/internal/trace"
)

const maxSummaryAttempts = 2

func WithProvider(ctx context.Context, provider string, model string, reasoningLevel string, session *trace.Session) (*Result, llm.Usage, error) {
	client, err := llm.New(provider)
	if err != nil {
		return nil, llm.Usage{}, err
	}
	return WithClient(ctx, client, model, reasoningLevel, session)
}

// WithClient summarizes the session, retrying on validation failures. The
// returned usage is the sum across every attempt, so it reflects total spend
// even when a retry was needed.
func WithClient(ctx context.Context, client llm.Client, model string, reasoningLevel string, session *trace.Session) (*Result, llm.Usage, error) {
	var usage llm.Usage

	schema, err := buildSchema(session)
	if err != nil {
		return nil, usage, err
	}

	input, err := prepare.RenderForLLM(session)
	if err != nil {
		return nil, usage, err
	}

	var lastErr error
	for attempt := 1; attempt <= maxSummaryAttempts; attempt++ {
		prompt := input
		if lastErr != nil {
			prompt = retryPrompt(input, lastErr)
		}

		response, err := client.GenerateStructured(ctx, llm.StructuredRequest{
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

func retryPrompt(input string, err error) string {
	return input + "\n\n<retry_instructions>\nThe previous JSON response failed validation: " + err.Error() + "\nReturn the complete JSON object again. Include compaction_summary, every section id, and every step id exactly once. Do not omit any required summary.\n</retry_instructions>\n"
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
