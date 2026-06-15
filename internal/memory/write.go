package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marc-brede/recall/internal/config"
	"github.com/marc-brede/recall/internal/summarize"
	"github.com/marc-brede/recall/internal/trace"
)

const metadataFileName = "metadata.json"

type WriteOptions struct {
	RecallDir       string
	Config          config.Config
	GeneratedAt     time.Time
	PreviousDir     string
	SectionMetadata map[string]SectionMetadata
	Segmented       bool
	RootStartedAt   time.Time
	RootEndedAt     time.Time
	ChangedSections map[string]bool
}

type WriteResult struct {
	Dir          string            `json:"dir"`
	RootDir      string            `json:"root_dir,omitempty"`
	MetadataPath string            `json:"metadata_path"`
	SessionPath  string            `json:"session_path,omitempty"`
	SegmentPath  string            `json:"segment_path,omitempty"`
	SectionPaths map[string]string `json:"section_paths"`
}

type WriteAggregateOptions struct {
	RecallDir     string
	Config        config.Config
	GeneratedAt   time.Time
	RootStartedAt time.Time
	RootEndedAt   time.Time
}

type WriteAggregateResult struct {
	RootDir      string `json:"root_dir"`
	MetadataPath string `json:"metadata_path"`
	SessionPath  string `json:"session_path"`
}

type Metadata struct {
	SchemaVersion        int                        `json:"schema_version"`
	Source               trace.Source               `json:"source"`
	ExternalSessionID    string                     `json:"external_session_id"`
	SegmentIndex         int                        `json:"segment_index"`
	SourceFile           string                     `json:"source_file"`
	ForkedFromSessionID  string                     `json:"forked_from_session_id,omitempty"`
	SourceStartLine      int                        `json:"source_start_line,omitempty"`
	SourceEndLine        int                        `json:"source_end_line,omitempty"`
	ContentStartLine     int                        `json:"content_start_line,omitempty"`
	CompactionSourceLine int                        `json:"compaction_source_line,omitempty"`
	CWD                  string                     `json:"cwd,omitempty"`
	GitBranch            string                     `json:"git_branch,omitempty"`
	StartedAt            string                     `json:"started_at,omitempty"`
	LastEventAt          string                     `json:"last_event_at,omitempty"`
	GeneratedAt          string                     `json:"generated_at"`
	LLM                  LLMMetadata                `json:"llm"`
	SourceMetadata       trace.Metadata             `json:"source_metadata,omitempty"`
	Summaries            summarize.Result           `json:"summaries"`
	Sections             map[string]SectionMetadata `json:"sections,omitempty"`
}

type AggregateMetadata struct {
	SchemaVersion       int                `json:"schema_version"`
	Source              trace.Source       `json:"source"`
	ExternalSessionID   string             `json:"external_session_id"`
	SourceFile          string             `json:"source_file"`
	ForkedFromSessionID string             `json:"forked_from_session_id,omitempty"`
	StartedAt           string             `json:"started_at,omitempty"`
	LastEventAt         string             `json:"last_event_at,omitempty"`
	GeneratedAt         string             `json:"generated_at"`
	LLM                 LLMMetadata        `json:"llm"`
	SourceMetadata      trace.Metadata     `json:"source_metadata,omitempty"`
	Summary             string             `json:"summary"`
	Segments            []AggregateSegment `json:"segments,omitempty"`
}

type LLMMetadata struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	ReasoningLevel string `json:"reasoning_level"`
}

const (
	SectionStatusOpen  = "open"
	SectionStatusFinal = "final"
)

type SectionMetadata struct {
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	LastEventAt string `json:"last_event_at,omitempty"`
	Status      string `json:"status"`
	InputHash   string `json:"input_hash,omitempty"`
	Path        string `json:"path"`
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

	rootDir := SessionDirForTimes(opts.RecallDir, session, rootStartedAt(opts, session), rootEndedAt(opts, session))
	targetDir := rootDir
	markdownFileName := sessionFileName
	if opts.Segmented {
		targetDir = filepath.Join(rootDir, segmentsDirName, segmentDirName(session.SegmentIndex))
		markdownFileName = segmentFileName
	}
	parentDir := filepath.Dir(targetDir)
	if err := os.MkdirAll(parentDir, 0755); err != nil {
		return nil, err
	}

	tempDir, err := os.MkdirTemp(parentDir, "."+filepath.Base(targetDir)+".tmp-")
	if err != nil {
		return nil, err
	}
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.RemoveAll(tempDir)
		}
	}()

	copySource := opts.PreviousDir
	copyRootSections := opts.Segmented && samePath(opts.PreviousDir, rootDir)
	if copyRootSections {
		copySource = ""
	}
	copiedPrevious, err := copyPreviousDir(copySource, tempDir)
	if err != nil {
		return nil, err
	}
	if copyRootSections {
		copiedSections, err := copyPreviousSections(rootDir, tempDir)
		if err != nil {
			return nil, err
		}
		copiedPrevious = copiedPrevious || copiedSections
	}

	writeResult, err := writeSessionDir(tempDir, opts, session, result, markdownFileName, copiedPrevious)
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
	if opts.PreviousDir != "" && !samePath(opts.PreviousDir, targetDir) && !pathWithin(opts.PreviousDir, targetDir) {
		if err := os.RemoveAll(opts.PreviousDir); err != nil {
			return nil, err
		}
	}

	writeResult.Dir = targetDir
	writeResult.RootDir = rootDir
	writeResult.MetadataPath = filepath.Join(targetDir, metadataFileName)
	if opts.Segmented {
		writeResult.SegmentPath = filepath.Join(targetDir, segmentFileName)
	} else {
		writeResult.SessionPath = filepath.Join(targetDir, sessionFileName)
	}
	for id, path := range writeResult.SectionPaths {
		writeResult.SectionPaths[id] = filepath.Join(targetDir, path)
	}
	return writeResult, nil
}

func WriteSessionAggregate(opts WriteAggregateOptions, session *trace.Session, summary string, segments []AggregateSegment) (*WriteAggregateResult, error) {
	if session == nil {
		return nil, errors.New("memory: nil session")
	}
	if opts.RecallDir == "" {
		return nil, errors.New("memory: recall dir is required")
	}
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}
	if opts.RootStartedAt.IsZero() {
		opts.RootStartedAt = session.StartedAt
	}
	if opts.RootEndedAt.IsZero() {
		opts.RootEndedAt = session.EndedAt
	}
	rootDir := SessionDirForTimes(opts.RecallDir, session, opts.RootStartedAt, opts.RootEndedAt)
	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(filepath.Join(rootDir, sectionsDirName)); err != nil {
		return nil, err
	}
	if err := os.Remove(filepath.Join(rootDir, segmentFileName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	startedAt := formatTime(opts.RootStartedAt)
	lastEventAt := formatTime(opts.RootEndedAt)
	metadata := AggregateMetadata{
		SchemaVersion:       1,
		Source:              session.Source,
		ExternalSessionID:   session.ExternalID,
		SourceFile:          session.SourceFile,
		ForkedFromSessionID: forkedFromSessionID(session.Metadata),
		StartedAt:           startedAt,
		LastEventAt:         lastEventAt,
		GeneratedAt:         formatTime(opts.GeneratedAt),
		LLM: LLMMetadata{
			Provider:       opts.Config.LLM.Provider,
			Model:          opts.Config.LLM.Model,
			ReasoningLevel: opts.Config.LLM.Reasoning.Level,
		},
		SourceMetadata: session.Metadata,
		Summary:        summary,
		Segments:       segments,
	}
	if err := writeJSONFile(filepath.Join(rootDir, metadataFileName), metadata); err != nil {
		return nil, err
	}

	markdown := RenderAggregateSessionMarkdown(session, summary, startedAt, lastEventAt, segments)
	sessionPath := filepath.Join(rootDir, sessionFileName)
	if err := os.WriteFile(sessionPath, []byte(markdown), 0644); err != nil {
		return nil, err
	}

	return &WriteAggregateResult{
		RootDir:      rootDir,
		MetadataPath: filepath.Join(rootDir, metadataFileName),
		SessionPath:  sessionPath,
	}, nil
}

func writeSessionDir(dir string, opts WriteOptions, session *trace.Session, result *summarize.Result, markdownFileName string, copiedPrevious bool) (*WriteResult, error) {
	sectionPathByID := make(map[string]string, len(session.Sections))
	for i, section := range session.Sections {
		sectionPathByID[section.ID] = filepath.Join(sectionsDirName, sectionFileName(i+1))
	}

	metadata := metadataFromSession(opts, session, result, sectionPathByID)
	if err := writeJSONFile(filepath.Join(dir, metadataFileName), metadata); err != nil {
		return nil, err
	}

	sessionMarkdown := RenderSessionMarkdown(session, result, sectionPathByID)
	if opts.Segmented {
		_ = os.Remove(filepath.Join(dir, sessionFileName))
		sessionMarkdown = RenderSegmentMarkdown(session, result, sectionPathByID)
	} else {
		_ = os.Remove(filepath.Join(dir, segmentFileName))
	}
	if err := os.WriteFile(filepath.Join(dir, markdownFileName), []byte(sessionMarkdown), 0644); err != nil {
		return nil, err
	}

	sectionsDir := filepath.Join(dir, sectionsDirName)
	if err := os.MkdirAll(sectionsDir, 0755); err != nil {
		return nil, err
	}
	if copiedPrevious {
		if err := removeStaleSectionFiles(sectionsDir, sectionPathByID); err != nil {
			return nil, err
		}
	}
	for i, section := range session.Sections {
		if copiedPrevious && opts.ChangedSections != nil && !opts.ChangedSections[section.ID] {
			continue
		}
		sectionResult := result.SectionSummaries[section.ID]
		sectionMarkdown := RenderSectionMarkdown(session, &session.Sections[i], i+1, &sectionResult)
		if err := os.WriteFile(filepath.Join(sectionsDir, sectionFileName(i+1)), []byte(sectionMarkdown), 0644); err != nil {
			return nil, err
		}
	}

	return &WriteResult{
		Dir:          dir,
		MetadataPath: filepath.Join(dir, metadataFileName),
		SectionPaths: sectionPathByID,
	}, nil
}

func removeStaleSectionFiles(sectionsDir string, sectionPathByID map[string]string) error {
	expected := make(map[string]struct{}, len(sectionPathByID))
	for _, path := range sectionPathByID {
		expected[filepath.Base(path)] = struct{}{}
	}
	entries, err := os.ReadDir(sectionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if _, ok := expected[entry.Name()]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(sectionsDir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyPreviousDir(previousDir string, tempDir string) (bool, error) {
	if previousDir == "" {
		return false, nil
	}
	info, err := os.Stat(previousDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("memory: previous path is not a directory: %s", previousDir)
	}
	if err := copyDir(previousDir, tempDir); err != nil {
		return false, err
	}
	return true, nil
}

func copyPreviousSections(previousRoot string, tempDir string) (bool, error) {
	sectionsDir := filepath.Join(previousRoot, sectionsDirName)
	info, err := os.Stat(sectionsDir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("memory: previous sections path is not a directory: %s", sectionsDir)
	}
	if err := copyDir(sectionsDir, filepath.Join(tempDir, sectionsDirName)); err != nil {
		return false, err
	}
	return true, nil
}

func copyDir(src string, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		if entry.Type()&os.ModeType != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	})
}

func samePath(left string, right string) bool {
	if left == "" || right == "" {
		return false
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func pathWithin(parent string, child string) bool {
	if parent == "" || child == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func rootStartedAt(opts WriteOptions, session *trace.Session) time.Time {
	if !opts.RootStartedAt.IsZero() {
		return opts.RootStartedAt
	}
	return session.StartedAt
}

func rootEndedAt(opts WriteOptions, session *trace.Session) time.Time {
	if !opts.RootEndedAt.IsZero() {
		return opts.RootEndedAt
	}
	return session.EndedAt
}

func metadataFromSession(opts WriteOptions, session *trace.Session, result *summarize.Result, sectionPathByID map[string]string) Metadata {
	return Metadata{
		SchemaVersion:        1,
		Source:               session.Source,
		ExternalSessionID:    session.ExternalID,
		SegmentIndex:         session.SegmentIndex,
		SourceFile:           session.SourceFile,
		ForkedFromSessionID:  forkedFromSessionID(session.Metadata),
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
		Summaries:      *result,
		Sections:       sectionMetadataFromSession(opts, session, sectionPathByID),
	}
}

func forkedFromSessionID(metadata trace.Metadata) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata["forked_from_session_id"].(string); ok && value != "" {
		return value
	}
	if value, ok := metadata["forked_from_id"].(string); ok && value != "" {
		return value
	}
	return ""
}

func sectionMetadataFromSession(opts WriteOptions, session *trace.Session, sectionPathByID map[string]string) map[string]SectionMetadata {
	sections := make(map[string]SectionMetadata, len(session.Sections))
	for i, section := range session.Sections {
		status := SectionStatusFinal
		if i == len(session.Sections)-1 {
			status = SectionStatusOpen
		}
		metadata := opts.SectionMetadata[section.ID]
		if metadata.Status == "" {
			metadata.Status = status
		}
		metadata.StartLine = section.StartLine
		metadata.EndLine = section.EndLine
		metadata.LastEventAt = formatTime(section.EndedAt)
		metadata.Path = filepath.ToSlash(sectionPathByID[section.ID])
		sections[section.ID] = metadata
	}
	return sections
}

func LoadMetadata(dir string) (*Metadata, error) {
	if dir == "" {
		return nil, errors.New("memory: metadata dir is required")
	}
	data, err := os.ReadFile(filepath.Join(dir, metadataFileName))
	if err != nil {
		return nil, err
	}
	var metadata Metadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("memory: parse metadata: %w", err)
	}
	if metadata.Sections == nil {
		metadata.Sections = map[string]SectionMetadata{}
	}
	if metadata.Summaries.SectionSummaries == nil {
		metadata.Summaries.SectionSummaries = map[string]summarize.SectionResult{}
	}
	return &metadata, nil
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal %s: %w", path, err)
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}
