package summarize

import (
	"errors"
	"fmt"

	"github.com/marc-brede/recall/internal/trace"
)

const schemaName = "recall_memory_summaries"

func buildSchema(session *trace.Session) (map[string]any, error) {
	if session == nil {
		return nil, errors.New("summarize: nil session")
	}

	for _, section := range session.Sections {
		if section.ID == "" {
			return nil, errors.New("summarize: section id is empty")
		}
		for _, step := range section.Steps {
			if step.ID == "" {
				return nil, fmt.Errorf("summarize: step id is empty in section %s", section.ID)
			}
		}
	}

	return objectSchema(
		map[string]any{
			"session_summary":    map[string]any{"type": "string"},
			"compaction_summary": map[string]any{"type": "string"},
			"section_summaries": arraySchema(
				objectSchema(
					map[string]any{
						"id":      map[string]any{"type": "string"},
						"summary": map[string]any{"type": "string"},
						"step_summaries": arraySchema(
							objectSchema(
								map[string]any{
									"id":      map[string]any{"type": "string"},
									"summary": map[string]any{"type": "string"},
								},
								[]string{"id", "summary"},
							),
						),
					},
					[]string{"id", "summary", "step_summaries"},
				),
			),
		},
		[]string{"session_summary", "compaction_summary", "section_summaries"},
	), nil
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"properties":           properties,
		"required":             required,
		"additionalProperties": false,
	}
}

func arraySchema(items map[string]any) map[string]any {
	return map[string]any{
		"type":  "array",
		"items": items,
	}
}
