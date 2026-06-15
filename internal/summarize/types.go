package summarize

type Result struct {
	SessionSummary    string                   `json:"session_summary"`
	CompactionSummary string                   `json:"compaction_summary"`
	SectionSummaries  map[string]SectionResult `json:"section_summaries"`
}

type SectionResult struct {
	Summary       string            `json:"summary"`
	StepSummaries map[string]string `json:"step_summaries"`
}

type SegmentSummary struct {
	ID      string
	Summary string
}

type wireResult struct {
	SessionSummary    string              `json:"session_summary"`
	CompactionSummary string              `json:"compaction_summary"`
	SectionSummaries  []wireSectionResult `json:"section_summaries"`
}

type wireSessionResult struct {
	SessionSummary    string `json:"session_summary"`
	CompactionSummary string `json:"compaction_summary"`
}

type wireAggregateSessionResult struct {
	SessionSummary string `json:"session_summary"`
}

type wireSectionResult struct {
	ID            string           `json:"id"`
	Summary       string           `json:"summary"`
	StepSummaries []wireStepResult `json:"step_summaries"`
}

type wireStepResult struct {
	ID      string `json:"id"`
	Summary string `json:"summary"`
}
