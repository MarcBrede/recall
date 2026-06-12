package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/marc-brede/recall/internal/config"
	"github.com/marc-brede/recall/internal/summarize"
	"github.com/marc-brede/recall/internal/trace"
)

const metadataFileName = "metadata.json"

type WriteOptions struct {
	RecallDir   string
	Config      config.Config
	GeneratedAt time.Time
}

type WriteResult struct {
	Dir          string            `json:"dir"`
	MetadataPath string            `json:"metadata_path"`
	SessionPath  string            `json:"session_path"`
	SectionPaths map[string]string `json:"section_paths"`
}

type Metadata struct {
	SchemaVersion        int            `json:"schema_version"`
	Source               trace.Source   `json:"source"`
	ExternalSessionID    string         `json:"external_session_id"`
	SegmentIndex         int            `json:"segment_index"`
	SourceFile           string         `json:"source_file"`
	SourceStartLine      int            `json:"source_start_line,omitempty"`
	SourceEndLine        int            `json:"source_end_line,omitempty"`
	ContentStartLine     int            `json:"content_start_line,omitempty"`
	CompactionSourceLine int            `json:"compaction_source_line,omitempty"`
	CWD                  string         `json:"cwd,omitempty"`
	GitBranch            string         `json:"git_branch,omitempty"`
	StartedAt            string         `json:"started_at,omitempty"`
	LastEventAt          string         `json:"last_event_at,omitempty"`
	GeneratedAt          string         `json:"generated_at"`
	LLM                  LLMMetadata    `json:"llm"`
	SourceMetadata       trace.Metadata `json:"source_metadata,omitempty"`
}

type LLMMetadata struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	ReasoningLevel string `json:"reasoning_level"`
}

func WriteSession(opts WriteOptions, session *trace.Session, result *summarize.Result) (*WriteResult, error) {
	if session == nil {
		return nil, errors.New("memory: nil session")
	}
	if result == nil {
		return nil, errors.New("memory: nil summary result")
	}
	if opts.RecallDir == "" {
		return nil, errors.New("memory: recall dir is required")
	}
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}
	if err := summarize.ValidateResult(session, result); err != nil {
		return nil, err
	}

	sessionsDir := filepath.Join(opts.RecallDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return nil, err
	}

	dirName := sessionDirName(session)
	targetDir := filepath.Join(sessionsDir, dirName)
	tempDir, err := os.MkdirTemp(sessionsDir, "."+dirName+".tmp-")
	if err != nil {
		return nil, err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()

	writeResult, err := writeSessionDir(tempDir, opts, session, result)
	if err != nil {
		return nil, err
	}

	if err := os.RemoveAll(targetDir); err != nil {
		return nil, err
	}
	if err := os.Rename(tempDir, targetDir); err != nil {
		return nil, err
	}
	keepTemp = true

	writeResult.Dir = targetDir
	writeResult.MetadataPath = filepath.Join(targetDir, metadataFileName)
	writeResult.SessionPath = filepath.Join(targetDir, sessionFileName)
	for id, path := range writeResult.SectionPaths {
		writeResult.SectionPaths[id] = filepath.Join(targetDir, path)
	}
	return writeResult, nil
}

func writeSessionDir(dir string, opts WriteOptions, session *trace.Session, result *summarize.Result) (*WriteResult, error) {
	sectionPathByID := make(map[string]string, len(session.Sections))
	for i, section := range session.Sections {
		sectionPathByID[section.ID] = filepath.Join(sectionsDirName, sectionFileName(i+1))
	}

	metadata := metadataFromSession(opts, session)
	if err := writeJSONFile(filepath.Join(dir, metadataFileName), metadata); err != nil {
		return nil, err
	}

	sessionMarkdown := RenderSessionMarkdown(session, result, sectionPathByID)
	if err := os.WriteFile(filepath.Join(dir, sessionFileName), []byte(sessionMarkdown), 0644); err != nil {
		return nil, err
	}

	sectionsDir := filepath.Join(dir, sectionsDirName)
	if err := os.MkdirAll(sectionsDir, 0755); err != nil {
		return nil, err
	}
	for i, section := range session.Sections {
		sectionResult := result.SectionSummaries[section.ID]
		sectionMarkdown := RenderSectionMarkdown(session, &session.Sections[i], i+1, &sectionResult)
		if err := os.WriteFile(filepath.Join(sectionsDir, sectionFileName(i+1)), []byte(sectionMarkdown), 0644); err != nil {
			return nil, err
		}
	}

	return &WriteResult{
		Dir:          dir,
		MetadataPath: filepath.Join(dir, metadataFileName),
		SessionPath:  filepath.Join(dir, sessionFileName),
		SectionPaths: sectionPathByID,
	}, nil
}

func metadataFromSession(opts WriteOptions, session *trace.Session) Metadata {
	return Metadata{
		SchemaVersion:        1,
		Source:               session.Source,
		ExternalSessionID:    session.ExternalID,
		SegmentIndex:         session.SegmentIndex,
		SourceFile:           session.SourceFile,
		SourceStartLine:      session.SourceStartLine,
		SourceEndLine:        session.SourceEndLine,
		ContentStartLine:     session.ContentStartLine,
		CompactionSourceLine: session.CompactionSourceLine,
		CWD:                  session.CWD,
		GitBranch:            session.GitBranch,
		StartedAt:            formatTime(session.StartedAt),
		LastEventAt:          formatTime(session.EndedAt),
		GeneratedAt:          formatTime(opts.GeneratedAt),
		LLM: LLMMetadata{
			Provider:       opts.Config.LLM.Provider,
			Model:          opts.Config.LLM.Model,
			ReasoningLevel: opts.Config.LLM.Reasoning.Level,
		},
		SourceMetadata: session.Metadata,
	}
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
