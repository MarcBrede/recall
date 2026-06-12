package trace

import "time"

// Source identifies the agent/runtime that produced a session file.
type Source string

const (
	SourceCodex  Source = "codex"
	SourceClaude Source = "claude"
	SourcePi     Source = "pi"
)

// EventKind identifies a normalized timeline event inside a step.
type EventKind string

const (
	EventHumanUser       EventKind = "human_user"
	EventAssistantText   EventKind = "assistant_text"
	EventReasoning       EventKind = "reasoning"
	EventReasoningMarker EventKind = "reasoning_marker"
	EventToolCall        EventKind = "tool_call"
	EventToolOutput      EventKind = "tool_output"
)

// FlatSession is the provider-normalized form before section/step splitting.
type FlatSession struct {
	Source               Source    `json:"source"`
	ExternalID           string    `json:"external_id"`
	SegmentIndex         int       `json:"segment_index"`
	SourceFile           string    `json:"source_file"`
	SourceStartLine      int       `json:"source_start_line,omitempty"`
	SourceEndLine        int       `json:"source_end_line,omitempty"`
	ContentStartLine     int       `json:"content_start_line,omitempty"`
	CompactionSummary    string    `json:"compaction_summary,omitempty"`
	CompactionSourceLine int       `json:"compaction_source_line,omitempty"`
	CWD                  string    `json:"cwd,omitempty"`
	GitBranch            string    `json:"git_branch,omitempty"`
	StartedAt            time.Time `json:"started_at,omitempty"`
	EndedAt              time.Time `json:"ended_at,omitempty"`
	Events               []Event   `json:"events"`
	Metadata             Metadata  `json:"metadata,omitempty"`
}

// Session is one complete normalized agent session.
type Session struct {
	Source               Source    `json:"source"`
	ExternalID           string    `json:"external_id"`
	SegmentIndex         int       `json:"segment_index"`
	SourceFile           string    `json:"source_file"`
	SourceStartLine      int       `json:"source_start_line,omitempty"`
	SourceEndLine        int       `json:"source_end_line,omitempty"`
	ContentStartLine     int       `json:"content_start_line,omitempty"`
	CompactionSummary    string    `json:"compaction_summary,omitempty"`
	CompactionSourceLine int       `json:"compaction_source_line,omitempty"`
	CWD                  string    `json:"cwd,omitempty"`
	GitBranch            string    `json:"git_branch,omitempty"`
	StartedAt            time.Time `json:"started_at,omitempty"`
	EndedAt              time.Time `json:"ended_at,omitempty"`
	Sections             []Section `json:"sections"`
	Metadata             Metadata  `json:"metadata,omitempty"`
}

type FlatSessionBatch struct {
	Sessions []FlatSession `json:"sessions"`
}

type SessionBatch struct {
	Sessions []Session `json:"sessions"`
}

// Section starts at a human user message and ends before the next one.
type Section struct {
	ID        string    `json:"id"`
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Steps     []Step    `json:"steps"`
}

// Step is one logical unit of work inside a section.
type Step struct {
	ID        string    `json:"id"`
	StartLine int       `json:"start_line"`
	EndLine   int       `json:"end_line"`
	StartedAt time.Time `json:"started_at,omitempty"`
	EndedAt   time.Time `json:"ended_at,omitempty"`
	Events    []Event   `json:"events"`
}

// Event is one normalized timeline item inside a step.
type Event struct {
	Kind       EventKind `json:"kind"`
	SourceLine int       `json:"source_line"`
	Timestamp  time.Time `json:"timestamp,omitempty"`
	Text       string    `json:"text,omitempty"`
	ToolName   string    `json:"tool_name,omitempty"`
	RawType    string    `json:"raw_type,omitempty"`
	EventID    string    `json:"event_id,omitempty"`
	ToolCallID string    `json:"tool_call_id,omitempty"`
	Metadata   Metadata  `json:"metadata,omitempty"`
}

// Metadata stores provider-specific details that are useful to keep but not
// important enough to make part of the shared trace model.
type Metadata map[string]any
