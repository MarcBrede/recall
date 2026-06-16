package discover

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/MarcBrede/recall/internal/trace"
)

type Options struct {
	Last int
}

type Session struct {
	Source      trace.Source `json:"source"`
	Path        string       `json:"path"`
	ExternalID  string       `json:"external_id"`
	StartedAt   string       `json:"started_at,omitempty"`
	LastEventAt string       `json:"last_event_at,omitempty"`
	ModifiedAt  string       `json:"modified_at"`
	SizeBytes   int64        `json:"size_bytes"`
}

type inspectedSession struct {
	Session
	startedAt     time.Time
	lastEventAt   time.Time
	sortTime      time.Time
	hasTranscript bool
}

type inspectRecord struct {
	Timestamp   string          `json:"timestamp"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	SessionID   string          `json:"sessionId"`
	IsSidechain bool            `json:"isSidechain"`
}

type codexSessionMeta struct {
	ID        string `json:"id"`
	Timestamp string `json:"timestamp"`
}

func Discover(ctx context.Context, opts Options) ([]Session, error) {
	if opts.Last < 0 {
		return nil, errors.New("discover: --last must be >= 0")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(home) == "" {
		return nil, errors.New("discover: user home directory is empty")
	}

	var sessions []inspectedSession
	roots := []struct {
		source trace.Source
		path   string
	}{
		{source: trace.SourceCodex, path: filepath.Join(home, ".codex", "sessions")},
		{source: trace.SourceClaude, path: filepath.Join(home, ".claude", "projects")},
	}

	for _, root := range roots {
		found, err := discoverRoot(ctx, root.source, root.path)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, found...)
	}

	sort.SliceStable(sessions, func(i, j int) bool {
		if !sessions[i].sortTime.Equal(sessions[j].sortTime) {
			return sessions[i].sortTime.After(sessions[j].sortTime)
		}
		return sessions[i].Path > sessions[j].Path
	})

	if opts.Last > 0 && len(sessions) > opts.Last {
		sessions = sessions[:opts.Last]
	}

	result := make([]Session, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, session.Session)
	}
	return result, nil
}

func discoverRoot(ctx context.Context, source trace.Source, root string) ([]inspectedSession, error) {
	info, err := os.Stat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("discover: %s is not a directory", root)
	}

	var sessions []inspectedSession
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if entry.IsDir() {
			if source == trace.SourceClaude && entry.Name() == "subagents" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".jsonl" {
			return nil
		}

		session, err := inspectFile(source, path)
		if err != nil {
			return err
		}
		if !session.isDiscoverable() {
			return nil
		}
		sessions = append(sessions, session)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

func inspectFile(source trace.Source, path string) (inspectedSession, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return inspectedSession{}, err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	session := inspectedSession{
		Session: Session{
			Source:     source,
			Path:       absPath,
			ModifiedAt: formatTime(stat.ModTime()),
			SizeBytes:  stat.Size(),
		},
		sortTime: stat.ModTime(),
	}

	file, err := os.Open(path)
	if err != nil {
		return inspectedSession{}, err
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			inspectLine(&session, source, strings.TrimSpace(line))
		}
		if err == nil {
			continue
		}
		if err == io.EOF {
			break
		}
		return inspectedSession{}, err
	}

	if session.ExternalID == "" {
		session.ExternalID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	if !session.startedAt.IsZero() {
		session.StartedAt = formatTime(session.startedAt)
	}
	if !session.lastEventAt.IsZero() {
		session.LastEventAt = formatTime(session.lastEventAt)
		session.sortTime = session.lastEventAt
	}

	return session, nil
}

func (session inspectedSession) isDiscoverable() bool {
	switch session.Source {
	case trace.SourceClaude:
		return session.hasTranscript
	default:
		return true
	}
}

func inspectLine(session *inspectedSession, source trace.Source, line string) {
	if line == "" {
		return
	}

	var record inspectRecord
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return
	}

	if timestamp, err := parseTime(record.Timestamp); err == nil {
		noteTimestamp(session, timestamp)
	}

	switch source {
	case trace.SourceCodex:
		inspectCodexLine(session, record)
	case trace.SourceClaude:
		inspectClaudeLine(session, record)
	}
}

func inspectCodexLine(session *inspectedSession, record inspectRecord) {
	if record.Type != "session_meta" || len(record.Payload) == 0 {
		return
	}

	var meta codexSessionMeta
	if err := json.Unmarshal(record.Payload, &meta); err != nil {
		return
	}
	if session.ExternalID == "" {
		session.ExternalID = meta.ID
	}
	if timestamp, err := parseTime(meta.Timestamp); err == nil {
		noteTimestamp(session, timestamp)
	}
}

func inspectClaudeLine(session *inspectedSession, record inspectRecord) {
	if session.ExternalID == "" {
		session.ExternalID = record.SessionID
	}
	switch record.Type {
	case "user", "assistant":
		session.hasTranscript = true
	}
}

func noteTimestamp(session *inspectedSession, timestamp time.Time) {
	if timestamp.IsZero() {
		return
	}
	if session.startedAt.IsZero() || timestamp.Before(session.startedAt) {
		session.startedAt = timestamp
	}
	if session.lastEventAt.IsZero() || timestamp.After(session.lastEventAt) {
		session.lastEventAt = timestamp
	}
}

func parseTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339Nano, value)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
