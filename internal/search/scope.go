package search

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"
)

type sessionScopeEntry struct {
	ID          string
	LastEventAt time.Time
}

func resolveSessionScope(ctx context.Context, db *sql.DB, opts SearchOptions, sessionIDs []string) ([]string, bool, error) {
	if len(sessionIDs) > 0 {
		return sessionIDs, true, nil
	}
	if opts.Since.IsZero() && opts.LastSessions <= 0 {
		return nil, false, nil
	}

	sessions, err := indexedSessions(ctx, db)
	if err != nil {
		return nil, true, err
	}
	filtered := make([]sessionScopeEntry, 0, len(sessions))
	for _, session := range sessions {
		if !opts.Since.IsZero() && session.LastEventAt.Before(opts.Since) {
			continue
		}
		filtered = append(filtered, session)
	}

	sort.SliceStable(filtered, func(i int, j int) bool {
		if !filtered[i].LastEventAt.Equal(filtered[j].LastEventAt) {
			return filtered[i].LastEventAt.After(filtered[j].LastEventAt)
		}
		return filtered[i].ID < filtered[j].ID
	})
	if opts.LastSessions > 0 && len(filtered) > opts.LastSessions {
		filtered = filtered[:opts.LastSessions]
	}

	scopedIDs := make([]string, 0, len(filtered))
	for _, session := range filtered {
		scopedIDs = append(scopedIDs, session.ID)
	}
	return scopedIDs, true, nil
}

func indexedSessions(ctx context.Context, db *sql.DB) ([]sessionScopeEntry, error) {
	rows, err := db.QueryContext(ctx, `
select session_id, memory_path, last_event_at
from nodes
where session_id <> '' and last_event_at <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	latestBySession := make(map[string]sessionScopeEntry)
	for rows.Next() {
		var sessionID string
		var memoryPath string
		var lastEventAt string
		if err := rows.Scan(&sessionID, &memoryPath, &lastEventAt); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, lastEventAt)
		if err != nil {
			return nil, fmt.Errorf("search: parse last_event_at for %s: %w", memoryPath, err)
		}
		parsed = parsed.UTC()
		previous, ok := latestBySession[sessionID]
		if !ok || parsed.After(previous.LastEventAt) {
			latestBySession[sessionID] = sessionScopeEntry{
				ID:          sessionID,
				LastEventAt: parsed,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sessions := make([]sessionScopeEntry, 0, len(latestBySession))
	for _, session := range latestBySession {
		sessions = append(sessions, session)
	}
	return sessions, nil
}
