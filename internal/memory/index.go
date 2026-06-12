package memory

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marc-brede/recall/internal/trace"
)

const indexFileName = ".index.json"

type Index struct {
	SchemaVersion int                   `json:"schema_version"`
	Entries       map[string]IndexEntry `json:"entries"`
}

type IndexEntry struct {
	Source               trace.Source `json:"source"`
	ExternalID           string       `json:"external_id"`
	SegmentIndex         int          `json:"segment_index"`
	SourceFile           string       `json:"source_file"`
	SourceStartLine      int          `json:"source_start_line,omitempty"`
	SourceEndLine        int          `json:"source_end_line,omitempty"`
	ContentStartLine     int          `json:"content_start_line,omitempty"`
	CompactionSourceLine int          `json:"compaction_source_line,omitempty"`
	SessionStartedAt     string       `json:"session_started_at,omitempty"`
	SessionLastEventAt   string       `json:"session_last_event_at"`
	MemoryDir            string       `json:"memory_dir"`
	IndexedAt            string       `json:"indexed_at"`
}

func LoadIndex(recallDir string) (*Index, error) {
	if strings.TrimSpace(recallDir) == "" {
		return nil, errors.New("memory: recall dir is required")
	}

	data, err := os.ReadFile(IndexPath(recallDir))
	if errors.Is(err, os.ErrNotExist) {
		return newIndex(), nil
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return newIndex(), nil
	}

	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("memory: parse index: %w", err)
	}
	normalizeIndex(&index)
	return &index, nil
}

func (index *Index) Save(recallDir string) error {
	if strings.TrimSpace(recallDir) == "" {
		return errors.New("memory: recall dir is required")
	}
	if index == nil {
		index = newIndex()
	}
	normalizeIndex(index)

	sessionsDir := filepath.Join(recallDir, "sessions")
	if err := os.MkdirAll(sessionsDir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("memory: marshal index: %w", err)
	}
	data = append(data, '\n')

	tempFile, err := os.CreateTemp(sessionsDir, "."+indexFileName+".tmp-")
	if err != nil {
		return err
	}
	tempPath := tempFile.Name()
	keepTemp := false
	defer func() {
		if !keepTemp {
			_ = os.Remove(tempPath)
		}
	}()

	if _, err := tempFile.Write(data); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tempPath, 0644); err != nil {
		return err
	}
	if err := os.Rename(tempPath, IndexPath(recallDir)); err != nil {
		return err
	}
	keepTemp = true
	return nil
}

func IndexPath(recallDir string) string {
	return filepath.Join(recallDir, "sessions", indexFileName)
}

func IndexKey(source trace.Source, externalID string, segmentIndex int) string {
	return string(source) + ":" + externalID + ":" + strconv.Itoa(segmentIndex)
}

func (index *Index) IsIndexed(session *trace.Session) bool {
	if index == nil || session == nil || session.EndedAt.IsZero() {
		return false
	}
	entry, ok := index.Entries[IndexKey(session.Source, session.ExternalID, session.SegmentIndex)]
	return ok &&
		entry.SessionLastEventAt == formatTime(session.EndedAt) &&
		entry.SourceStartLine == session.SourceStartLine &&
		entry.SourceEndLine == session.SourceEndLine &&
		entry.ContentStartLine == session.ContentStartLine &&
		entry.CompactionSourceLine == session.CompactionSourceLine
}

func (index *Index) Upsert(recallDir string, session *trace.Session, writeResult *WriteResult, indexedAt time.Time) bool {
	if index == nil || session == nil || writeResult == nil {
		return false
	}
	if strings.TrimSpace(session.ExternalID) == "" || session.EndedAt.IsZero() {
		return false
	}
	normalizeIndex(index)
	if indexedAt.IsZero() {
		indexedAt = time.Now().UTC()
	}

	memoryDir := writeResult.Dir
	if rel, err := filepath.Rel(recallDir, writeResult.Dir); err == nil {
		memoryDir = filepath.ToSlash(rel)
	}

	index.Entries[IndexKey(session.Source, session.ExternalID, session.SegmentIndex)] = IndexEntry{
		Source:               session.Source,
		ExternalID:           session.ExternalID,
		SegmentIndex:         session.SegmentIndex,
		SourceFile:           session.SourceFile,
		SourceStartLine:      session.SourceStartLine,
		SourceEndLine:        session.SourceEndLine,
		ContentStartLine:     session.ContentStartLine,
		CompactionSourceLine: session.CompactionSourceLine,
		SessionStartedAt:     formatTime(session.StartedAt),
		SessionLastEventAt:   formatTime(session.EndedAt),
		MemoryDir:            memoryDir,
		IndexedAt:            formatTime(indexedAt),
	}
	return true
}

func newIndex() *Index {
	return &Index{
		SchemaVersion: 1,
		Entries:       make(map[string]IndexEntry),
	}
}

func normalizeIndex(index *Index) {
	if index.SchemaVersion == 0 {
		index.SchemaVersion = 1
	}
	if index.Entries == nil {
		index.Entries = make(map[string]IndexEntry)
	}
}
