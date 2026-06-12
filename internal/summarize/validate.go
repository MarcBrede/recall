package summarize

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/marc-brede/recall/internal/trace"
)

func ValidateResult(session *trace.Session, result *Result) error {
	if session == nil {
		return errors.New("summarize: nil session")
	}
	if result == nil {
		return errors.New("summarize: nil result")
	}
	if result.SectionSummaries == nil {
		return errors.New("summarize: section_summaries is missing")
	}
	if strings.TrimSpace(session.CompactionSummary) != "" && strings.TrimSpace(result.CompactionSummary) == "" {
		return errors.New("summarize: compaction_summary is missing")
	}
	if len(result.SectionSummaries) != len(session.Sections) {
		expected := make([]string, 0, len(session.Sections))
		for _, section := range session.Sections {
			expected = append(expected, section.ID)
		}
		actual := make([]string, 0, len(result.SectionSummaries))
		for id := range result.SectionSummaries {
			actual = append(actual, id)
		}
		return fmt.Errorf(
			"summarize: expected %d section summaries, got %d (missing: %s; unexpected: %s)",
			len(session.Sections),
			len(result.SectionSummaries),
			formatIDList(missingIDs(expected, actual)),
			formatIDList(missingIDs(actual, expected)),
		)
	}

	for _, section := range session.Sections {
		sectionResult, ok := result.SectionSummaries[section.ID]
		if !ok {
			return fmt.Errorf("summarize: missing section summary for %s", section.ID)
		}
		if sectionResult.StepSummaries == nil {
			return fmt.Errorf("summarize: step_summaries is missing for %s", section.ID)
		}
		if len(sectionResult.StepSummaries) != len(section.Steps) {
			expected := make([]string, 0, len(section.Steps))
			for _, step := range section.Steps {
				expected = append(expected, step.ID)
			}
			actual := make([]string, 0, len(sectionResult.StepSummaries))
			for id := range sectionResult.StepSummaries {
				actual = append(actual, id)
			}
			return fmt.Errorf(
				"summarize: expected %d step summaries for %s, got %d (missing: %s; unexpected: %s)",
				len(section.Steps),
				section.ID,
				len(sectionResult.StepSummaries),
				formatIDList(missingIDs(expected, actual)),
				formatIDList(missingIDs(actual, expected)),
			)
		}
		for _, step := range section.Steps {
			if _, ok := sectionResult.StepSummaries[step.ID]; !ok {
				return fmt.Errorf("summarize: missing step summary for %s", step.ID)
			}
		}
	}

	return nil
}

func missingIDs(expected []string, actual []string) []string {
	seen := make(map[string]struct{}, len(actual))
	for _, id := range actual {
		seen[id] = struct{}{}
	}

	var missing []string
	for _, id := range expected {
		if _, ok := seen[id]; !ok {
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}

func formatIDList(ids []string) string {
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}
