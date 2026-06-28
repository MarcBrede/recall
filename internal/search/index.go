package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/MarcBrede/recall/internal/embed"
	_ "modernc.org/sqlite"
)

const DatabaseFileName = "search.sqlite"

type Options struct {
	RecallDir   string
	Model       string
	Client      embed.Client
	Concurrency int
	Now         time.Time
}

type IndexResult struct {
	DatabasePath string `json:"database_path"`
	MetadataPath string `json:"metadata_path"`
	Nodes        int    `json:"nodes"`
	Embedded     int    `json:"embedded"`
	Skipped      int    `json:"skipped"`
	Deleted      int    `json:"deleted"`
}

type Result struct {
	NodeType    string  `json:"node_type"`
	SessionID   string  `json:"session_id"`
	MemoryPath  string  `json:"memory_path"`
	LastEventAt string  `json:"last_event_at"`
	Score       float64 `json:"score"`
	Snippet     string  `json:"snippet"`
}

type SearchOptions struct {
	Limit        int
	NodeTypes    string
	SessionIDs   []string
	Since        time.Time
	LastSessions int
}

func Reindex(ctx context.Context, opts Options) (IndexResult, error) {
	scope := filepath.Join(opts.RecallDir, "sessions")
	return indexScope(ctx, opts, scope, true)
}

func IndexMemoryDir(ctx context.Context, opts Options, memoryDir string) (IndexResult, error) {
	if strings.TrimSpace(memoryDir) == "" {
		return IndexResult{}, errors.New("search: memory dir is required")
	}
	return indexScope(ctx, opts, memoryDir, false)
}

func Search(ctx context.Context, opts Options, query string, searchOpts SearchOptions) ([]Result, error) {
	if err := validateOptions(opts); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search: query is required")
	}
	limit := searchOpts.Limit
	if limit <= 0 {
		limit = 10
	}
	nodeTypes, err := parseNodeTypeFilter(searchOpts.NodeTypes)
	if err != nil {
		return nil, err
	}
	sessionIDs, err := normalizeSessionIDs(searchOpts.SessionIDs)
	if err != nil {
		return nil, err
	}
	if err := validateSearchScope(searchOpts, sessionIDs); err != nil {
		return nil, err
	}

	metadata, err := loadMetadata(opts.RecallDir)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(metadata.EmbeddingModel) == "" {
		return nil, errors.New("search: search index metadata is missing; run recall reindex")
	}
	if metadata.EmbeddingModel != opts.Model {
		return nil, fmt.Errorf("search: index was built with model %q, config uses %q; run recall reindex", metadata.EmbeddingModel, opts.Model)
	}

	db, err := openDB(opts.RecallDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		return nil, err
	}

	sessionIDs, scoped, err := resolveSessionScope(ctx, db, searchOpts, sessionIDs)
	if err != nil {
		return nil, err
	}
	if scoped && len(sessionIDs) == 0 {
		return []Result{}, nil
	}

	embedding, err := opts.Client.Embed(ctx, embed.Request{
		Model: opts.Model,
		Input: embeddingInput(query),
	})
	if err != nil {
		return nil, err
	}
	if len(embedding.Vector) != metadata.EmbeddingDim {
		return nil, fmt.Errorf("search: query embedding dimension %d does not match index dimension %d", len(embedding.Vector), metadata.EmbeddingDim)
	}

	querySQL := `
		select n.node_type, n.session_id, n.memory_path, n.content, n.last_event_at, e.vector
		from nodes n
		join embeddings e on e.node_id = n.id`
	conditions := make([]string, 0, 2)
	args := make([]any, 0, len(nodeTypes)+len(sessionIDs))
	if len(nodeTypes) > 0 {
		placeholders := placeholders(len(nodeTypes))
		conditions = append(conditions, "n.node_type in ("+strings.Join(placeholders, ", ")+")")
		for _, nodeType := range nodeTypes {
			args = append(args, nodeType)
		}
	}
	if len(sessionIDs) > 0 {
		placeholders := placeholders(len(sessionIDs))
		conditions = append(conditions, "n.session_id in ("+strings.Join(placeholders, ", ")+")")
		for _, sessionID := range sessionIDs {
			args = append(args, sessionID)
		}
	}
	if len(conditions) > 0 {
		querySQL += "\nwhere " + strings.Join(conditions, " and ")
	}
	rows, err := db.QueryContext(ctx, querySQL, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	results := make([]Result, 0)
	for rows.Next() {
		var nodeType string
		var sessionID string
		var memoryPath string
		var content string
		var lastEventAt string
		var encoded []byte
		if err := rows.Scan(&nodeType, &sessionID, &memoryPath, &content, &lastEventAt, &encoded); err != nil {
			return nil, err
		}
		vector, err := decodeVector(encoded)
		if err != nil {
			return nil, err
		}
		score := cosineSimilarity(embedding.Vector, vector)
		results = append(results, Result{
			NodeType:    nodeType,
			SessionID:   sessionID,
			MemoryPath:  memoryPath,
			LastEventAt: lastEventAt,
			Score:       score,
			Snippet:     snippet(content),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.SliceStable(results, func(i int, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].MemoryPath < results[j].MemoryPath
		}
		return results[i].Score > results[j].Score
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

func validateSearchScope(searchOpts SearchOptions, sessionIDs []string) error {
	if searchOpts.LastSessions < 0 {
		return errors.New("search: last sessions must be >= 0")
	}
	scopeModes := 0
	if len(sessionIDs) > 0 {
		scopeModes++
	}
	if !searchOpts.Since.IsZero() {
		scopeModes++
	}
	if searchOpts.LastSessions > 0 {
		scopeModes++
	}
	if scopeModes > 1 {
		return errors.New("search: use only one of session ids, since, or last sessions")
	}
	return nil
}

func placeholders(count int) []string {
	values := make([]string, 0, count)
	for i := 0; i < count; i++ {
		values = append(values, "?")
	}
	return values
}

func parseNodeTypeFilter(filter string) ([]string, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}
	seen := make(map[string]struct{})
	var nodeTypes []string
	for _, raw := range strings.Split(filter, ",") {
		nodeType := strings.TrimSpace(raw)
		if nodeType == "" {
			continue
		}
		switch nodeType {
		case NodeTypeSession, NodeTypeSegment, NodeTypeSection:
			if _, ok := seen[nodeType]; ok {
				continue
			}
			seen[nodeType] = struct{}{}
			nodeTypes = append(nodeTypes, nodeType)
		default:
			return nil, fmt.Errorf("search: unsupported node type %q; expected comma-separated values from: %s, %s, %s", nodeType, NodeTypeSession, NodeTypeSegment, NodeTypeSection)
		}
	}
	if len(nodeTypes) == 0 {
		return nil, fmt.Errorf("search: node type filter %q did not contain a valid node type", filter)
	}
	return nodeTypes, nil
}

func normalizeSessionIDs(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{})
	sessionIDs := make([]string, 0, len(values))
	for _, value := range values {
		for _, raw := range strings.Split(value, ",") {
			sessionID := strings.TrimSpace(raw)
			if sessionID == "" {
				continue
			}
			if _, ok := seen[sessionID]; ok {
				continue
			}
			seen[sessionID] = struct{}{}
			sessionIDs = append(sessionIDs, sessionID)
		}
	}
	if len(sessionIDs) == 0 {
		return nil, errors.New("search: session filter did not contain a valid session id")
	}
	return sessionIDs, nil
}

func indexScope(ctx context.Context, opts Options, scopeDir string, allowModelChange bool) (IndexResult, error) {
	if err := validateOptions(opts); err != nil {
		return IndexResult{}, err
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}

	nodes, err := scanMemoryNodes(opts.RecallDir, scopeDir)
	if err != nil {
		return IndexResult{}, err
	}

	db, err := openDB(opts.RecallDir)
	if err != nil {
		return IndexResult{}, err
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		return IndexResult{}, err
	}

	metadata, err := loadMetadata(opts.RecallDir)
	if err != nil {
		return IndexResult{}, err
	}
	modelChanged := metadata.EmbeddingModel != "" && metadata.EmbeddingModel != opts.Model
	if modelChanged && !allowModelChange {
		return IndexResult{}, fmt.Errorf("search: index was built with model %q, config uses %q; run recall reindex", metadata.EmbeddingModel, opts.Model)
	}
	if modelChanged {
		if _, err := db.ExecContext(ctx, `delete from embeddings`); err != nil {
			return IndexResult{}, err
		}
	}

	result := IndexResult{
		DatabasePath: DatabasePath(opts.RecallDir),
		MetadataPath: MetadataPath(opts.RecallDir),
		Nodes:        len(nodes),
	}
	var jobs []embeddingJob
	for _, node := range nodes {
		previous, err := getExistingNode(ctx, db, node.MemoryPath)
		if err != nil {
			return IndexResult{}, err
		}
		nodeID, err := upsertNode(ctx, db, node)
		if err != nil {
			return IndexResult{}, err
		}
		if !modelChanged && previous.Found && previous.ContentHash == node.ContentHash && previous.HasEmbedding {
			result.Skipped++
			continue
		}
		jobs = append(jobs, embeddingJob{
			NodeID: nodeID,
			Node:   node,
		})
	}

	embeddingResults, err := embedNodes(ctx, opts, jobs)
	if err != nil {
		return IndexResult{}, err
	}
	for _, embeddingResult := range embeddingResults {
		if len(embeddingResult.Vector) == 0 {
			return IndexResult{}, fmt.Errorf("search: empty embedding for %s", embeddingResult.MemoryPath)
		}
		if metadata.EmbeddingDim != 0 && len(embeddingResult.Vector) != metadata.EmbeddingDim {
			return IndexResult{}, fmt.Errorf("search: embedding dimension changed from %d to %d", metadata.EmbeddingDim, len(embeddingResult.Vector))
		}
		metadata.EmbeddingDim = len(embeddingResult.Vector)
		if err := upsertEmbedding(ctx, db, embeddingResult.NodeID, embeddingResult.Vector); err != nil {
			return IndexResult{}, err
		}
		result.Embedded++
	}

	deleted, err := deleteStaleNodes(ctx, db, opts.RecallDir, scopeDir, nodes)
	if err != nil {
		return IndexResult{}, err
	}
	result.Deleted = deleted

	if metadata.EmbeddingDim == 0 {
		dim, err := firstEmbeddingDim(ctx, db)
		if err != nil {
			return IndexResult{}, err
		}
		metadata.EmbeddingDim = dim
	}
	metadata.SchemaVersion = 1
	metadata.EmbeddingModel = opts.Model
	metadata.BuiltAt = now.Format(time.RFC3339Nano)
	if err := saveMetadata(opts.RecallDir, metadata); err != nil {
		return IndexResult{}, err
	}
	return result, nil
}

func validateOptions(opts Options) error {
	if strings.TrimSpace(opts.RecallDir) == "" {
		return errors.New("search: recall dir is required")
	}
	if strings.TrimSpace(opts.Model) == "" {
		return errors.New("search: embedding model is required")
	}
	if opts.Client == nil {
		return errors.New("search: embedding client is required")
	}
	return nil
}

type embeddingJob struct {
	NodeID int64
	Node   nodeInput
}

type embeddingResult struct {
	NodeID     int64
	MemoryPath string
	Vector     []float32
}

func embedNodes(ctx context.Context, opts Options, jobs []embeddingJob) ([]embeddingResult, error) {
	if len(jobs) == 0 {
		return nil, nil
	}
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	if concurrency > len(jobs) {
		concurrency = len(jobs)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobCh := make(chan embeddingJob)
	resultCh := make(chan embeddingResult, len(jobs))
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				response, err := opts.Client.Embed(ctx, embed.Request{
					Model: opts.Model,
					Input: embeddingInput(job.Node.Content),
				})
				if err != nil {
					select {
					case errCh <- fmt.Errorf("search: embed %s: %w", job.Node.MemoryPath, err):
						cancel()
					default:
					}
					return
				}
				select {
				case resultCh <- embeddingResult{
					NodeID:     job.NodeID,
					MemoryPath: job.Node.MemoryPath,
					Vector:     response.Vector,
				}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

sendLoop:
	for _, job := range jobs {
		select {
		case jobCh <- job:
		case <-ctx.Done():
			break sendLoop
		}
	}
	close(jobCh)
	wg.Wait()
	close(resultCh)

	select {
	case err := <-errCh:
		return nil, err
	default:
	}
	if err := ctx.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}

	results := make([]embeddingResult, 0, len(jobs))
	for result := range resultCh {
		results = append(results, result)
	}
	if len(results) != len(jobs) {
		return nil, fmt.Errorf("search: embedded %d of %d nodes", len(results), len(jobs))
	}
	sort.Slice(results, func(i int, j int) bool {
		return results[i].MemoryPath < results[j].MemoryPath
	})
	return results, nil
}

func openDB(recallDir string) (*sql.DB, error) {
	if err := os.MkdirAll(recallDir, 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", DatabasePath(recallDir))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`pragma foreign_keys = on`); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func DatabasePath(recallDir string) string {
	return filepath.Join(recallDir, DatabaseFileName)
}

func ensureSchema(db *sql.DB) error {
	statements := []string{
		`create table if not exists nodes (
			id integer primary key,
			node_type text not null,
			session_id text not null default '',
			memory_path text not null unique,
			content text not null,
			content_hash text not null,
			last_event_at text not null
		)`,
		`create table if not exists embeddings (
			node_id integer primary key references nodes(id) on delete cascade,
			vector blob not null
		)`,
		`create index if not exists nodes_last_event_at_idx on nodes(last_event_at)`,
		`create index if not exists nodes_node_type_idx on nodes(node_type)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	if err := ensureNodeSessionIDColumn(db); err != nil {
		return err
	}
	if _, err := db.Exec(`create index if not exists nodes_session_id_idx on nodes(session_id)`); err != nil {
		return err
	}
	return nil
}

func ensureNodeSessionIDColumn(db *sql.DB) error {
	rows, err := db.Query(`pragma table_info(nodes)`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var dataType string
		var notNull int
		var defaultValue sql.NullString
		var primaryKey int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "session_id" {
			return rows.Err()
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`alter table nodes add column session_id text not null default ''`)
	return err
}

type existingNode struct {
	Found        bool
	ID           int64
	ContentHash  string
	HasEmbedding bool
}

func getExistingNode(ctx context.Context, db *sql.DB, memoryPath string) (existingNode, error) {
	var node existingNode
	var hasEmbedding int
	err := db.QueryRowContext(ctx, `
select n.id, n.content_hash, case when e.node_id is null then 0 else 1 end
from nodes n
left join embeddings e on e.node_id = n.id
where n.memory_path = ?`, memoryPath).Scan(&node.ID, &node.ContentHash, &hasEmbedding)
	if errors.Is(err, sql.ErrNoRows) {
		return existingNode{}, nil
	}
	if err != nil {
		return existingNode{}, err
	}
	node.Found = true
	node.HasEmbedding = hasEmbedding == 1
	return node, nil
}

func upsertNode(ctx context.Context, db *sql.DB, node nodeInput) (int64, error) {
	_, err := db.ExecContext(ctx, `
insert into nodes(node_type, session_id, memory_path, content, content_hash, last_event_at)
values (?, ?, ?, ?, ?, ?)
on conflict(memory_path) do update set
	node_type = excluded.node_type,
	session_id = excluded.session_id,
	content = excluded.content,
	content_hash = excluded.content_hash,
	last_event_at = excluded.last_event_at`,
		node.NodeType,
		node.SessionID,
		node.MemoryPath,
		node.Content,
		node.ContentHash,
		node.LastEventAt,
	)
	if err != nil {
		return 0, err
	}
	var id int64
	if err := db.QueryRowContext(ctx, `select id from nodes where memory_path = ?`, node.MemoryPath).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func upsertEmbedding(ctx context.Context, db *sql.DB, nodeID int64, vector []float32) error {
	_, err := db.ExecContext(ctx, `
insert into embeddings(node_id, vector)
values (?, ?)
on conflict(node_id) do update set vector = excluded.vector`,
		nodeID,
		encodeVector(vector),
	)
	return err
}

func deleteStaleNodes(ctx context.Context, db *sql.DB, recallDir string, scopeDir string, nodes []nodeInput) (int, error) {
	seen := make(map[string]struct{}, len(nodes))
	for _, node := range nodes {
		seen[node.MemoryPath] = struct{}{}
	}
	scopePrefix, err := filepath.Rel(recallDir, filepath.Clean(scopeDir))
	if err != nil {
		return 0, err
	}
	scopePrefix = filepath.ToSlash(scopePrefix)
	if scopePrefix != "." && !strings.HasSuffix(scopePrefix, "/") {
		scopePrefix += "/"
	}

	rows, err := db.QueryContext(ctx, `select id, memory_path from nodes`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		var memoryPath string
		if err := rows.Scan(&id, &memoryPath); err != nil {
			return 0, err
		}
		if scopePrefix != "." && !strings.HasPrefix(memoryPath, scopePrefix) {
			continue
		}
		if _, ok := seen[memoryPath]; !ok {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := db.ExecContext(ctx, `delete from nodes where id = ?`, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func firstEmbeddingDim(ctx context.Context, db *sql.DB) (int, error) {
	var data []byte
	err := db.QueryRowContext(ctx, `select vector from embeddings limit 1`).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	vector, err := decodeVector(data)
	if err != nil {
		return 0, err
	}
	return len(vector), nil
}

func snippet(content string) string {
	fields := strings.Fields(content)
	text := strings.Join(fields, " ")
	const max = 240
	if len(text) <= max {
		return text
	}
	return strings.TrimSpace(text[:max]) + "..."
}
