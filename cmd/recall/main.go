package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/marc-brede/recall/internal/config"
	"github.com/marc-brede/recall/internal/discover"
	"github.com/marc-brede/recall/internal/memory"
	"github.com/marc-brede/recall/internal/prepare"
	"github.com/marc-brede/recall/internal/provider"
	"github.com/marc-brede/recall/internal/summarize"
	"github.com/marc-brede/recall/internal/trace"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return usageError()
	}

	switch args[0] {
	case "parse":
		return runParse(args[1:])
	case "prepare":
		return runPrepare(args[1:])
	case "render":
		return runRender(args[1:])
	case "summarize":
		return runSummarize(args[1:])
	case "write-memory":
		return runWriteMemory(args[1:])
	case "ingest":
		return runIngest(args[1:])
	case "discover":
		return runDiscover(args[1:])
	default:
		return usageError()
	}
}

func runParse(args []string) error {
	if len(args) != 1 {
		return usageError()
	}

	sessions, err := provider.ParseFile(context.Background(), args[0])
	if err != nil {
		return err
	}

	return writeJSON(os.Stdout, flatSessionBatch(sessions))
}

func runPrepare(args []string) error {
	if len(args) > 1 {
		return usageError()
	}

	path := "-"
	if len(args) == 1 {
		path = args[0]
	}

	sessions, err := readFlatSessions(path)
	if err != nil {
		return err
	}

	prepared := make([]*trace.Session, 0, len(sessions))
	for _, session := range sessions {
		preparedSession, err := prepare.FromFlatSession(session)
		if err != nil {
			return err
		}
		prepared = append(prepared, preparedSession)
	}

	return writeJSON(os.Stdout, sessionBatch(prepared))
}

func runRender(args []string) error {
	flags := flag.NewFlagSet("render", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	segment := flags.Int("segment", -1, "render one segment by segment index")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return usageError()
	}

	path := "-"
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}

	sessions, err := readPreparedSessions(path)
	if err != nil {
		return err
	}
	session, err := selectPreparedSession(sessions, *segment)
	if err != nil {
		return err
	}

	input, err := prepare.RenderForLLM(session)
	if err != nil {
		return err
	}

	_, err = fmt.Fprint(os.Stdout, input)
	return err
}

func runSummarize(args []string) error {
	flags := flag.NewFlagSet("summarize", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	segment := flags.Int("segment", -1, "summarize one segment by segment index")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 1 {
		return usageError()
	}

	path := "-"
	if flags.NArg() == 1 {
		path = flags.Arg(0)
	}

	sessions, err := readPreparedSessions(path)
	if err != nil {
		return err
	}
	session, err := selectPreparedSession(sessions, *segment)
	if err != nil {
		return err
	}

	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	result, err := summarizeSession(loaded.Config, session)
	if err != nil {
		return err
	}

	return writeJSON(os.Stdout, result)
}

func runWriteMemory(args []string) error {
	flags := flag.NewFlagSet("write-memory", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	segment := flags.Int("segment", -1, "write one segment by segment index")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 2 {
		return usageError()
	}

	sessions, err := readPreparedSessions(flags.Arg(0))
	if err != nil {
		return err
	}
	session, err := selectPreparedSession(sessions, *segment)
	if err != nil {
		return err
	}

	summary, err := readSummaryResult(flags.Arg(1))
	if err != nil {
		return err
	}

	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	writeResult, err := memory.WriteSession(memory.WriteOptions{
		RecallDir: loaded.Dir,
		Config:    loaded.Config,
	}, session, summary)
	if err != nil {
		return err
	}

	return writeJSON(os.Stdout, writeResult)
}

func runIngest(args []string) error {
	flags := flag.NewFlagSet("ingest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	last := flags.Int("last", 0, "discover and ingest the last N local sessions")
	if err := flags.Parse(args); err != nil {
		return err
	}

	remaining := flags.Args()
	if *last > 0 {
		if len(remaining) != 0 {
			return usageError()
		}
		return runIngestLast(*last)
	}

	if len(remaining) != 1 {
		return usageError()
	}

	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	index, err := memory.LoadIndex(loaded.Dir)
	if err != nil {
		return err
	}

	output := ingestBatchOutput{
		Discovered:  1,
		Concurrency: 1,
		Results:     ingestPathSegments(loaded, nil, remaining[0], false),
	}
	countIngestResults(&output)
	indexChanged := upsertIngestResults(index, loaded.Dir, output.Results, time.Now().UTC())
	if indexChanged {
		if err := index.Save(loaded.Dir); err != nil {
			return err
		}
	}

	if err := writeJSON(os.Stdout, output); err != nil {
		return err
	}
	if output.Failed > 0 {
		return fmt.Errorf("ingest: %d of %d segments failed", output.Failed, len(output.Results))
	}
	return nil
}

type ingestBatchOutput struct {
	Discovered  int                 `json:"discovered"`
	Queued      int                 `json:"queued"`
	Skipped     int                 `json:"skipped"`
	Succeeded   int                 `json:"succeeded"`
	Failed      int                 `json:"failed"`
	Concurrency int                 `json:"concurrency"`
	Results     []ingestBatchResult `json:"results"`
}

type ingestBatchResult struct {
	Source          trace.Source        `json:"source"`
	Path            string              `json:"path"`
	ExternalID      string              `json:"external_id"`
	SegmentIndex    int                 `json:"segment_index"`
	SourceStartLine int                 `json:"source_start_line,omitempty"`
	SourceEndLine   int                 `json:"source_end_line,omitempty"`
	Status          string              `json:"status"`
	Reason          string              `json:"reason,omitempty"`
	Write           *memory.WriteResult `json:"write,omitempty"`
	Error           string              `json:"error,omitempty"`

	session *trace.Session
}

type ingestJobResult struct {
	index   int
	results []ingestBatchResult
}

func runIngestLast(last int) error {
	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	sessions, err := discover.Discover(context.Background(), discover.Options{
		Last: last,
	})
	if err != nil {
		return err
	}

	index, err := memory.LoadIndex(loaded.Dir)
	if err != nil {
		return err
	}

	output := ingestBatchOutput{
		Discovered: len(sessions),
	}
	concurrency := loaded.Config.Ingest.Concurrency
	if concurrency > len(sessions) {
		concurrency = len(sessions)
	}
	output.Concurrency = concurrency

	jobs := make(chan int)
	results := make(chan ingestJobResult)
	var wg sync.WaitGroup
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for jobIndex := range jobs {
				session := sessions[jobIndex]
				results <- ingestJobResult{
					index:   jobIndex,
					results: ingestPathSegments(loaded, index, session.Path, true),
				}
			}
		}()
	}

	go func() {
		for index := range sessions {
			jobs <- index
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	fileResults := make([][]ingestBatchResult, len(sessions))
	for result := range results {
		fileResults[result.index] = result.results
	}

	for _, result := range fileResults {
		output.Results = append(output.Results, result...)
	}
	countIngestResults(&output)

	indexedAt := time.Now().UTC()
	indexChanged := upsertIngestResults(index, loaded.Dir, output.Results, indexedAt)
	if indexChanged {
		if err := index.Save(loaded.Dir); err != nil {
			return err
		}
	}

	if err := writeJSON(os.Stdout, output); err != nil {
		return err
	}
	if output.Failed > 0 {
		return fmt.Errorf("ingest: %d of %d segments failed", output.Failed, len(output.Results))
	}
	return nil
}

func ingestPathSegments(loaded config.Loaded, index *memory.Index, path string, skipIndexed bool) []ingestBatchResult {
	flats, err := provider.ParseFile(context.Background(), path)
	if err != nil {
		return []ingestBatchResult{failedIngestResult(path, err)}
	}
	if len(flats) == 0 {
		return []ingestBatchResult{skippedIngestResult(path, "no_memory_events")}
	}

	results := make([]ingestBatchResult, 0, len(flats))
	for _, flat := range flats {
		result := ingestBatchResult{
			Source:          flat.Source,
			Path:            path,
			ExternalID:      flat.ExternalID,
			SegmentIndex:    flat.SegmentIndex,
			SourceStartLine: flat.SourceStartLine,
			SourceEndLine:   flat.SourceEndLine,
		}

		session, err := prepare.FromFlatSession(flat)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		result.session = session
		if session.EndedAt.IsZero() {
			result.Status = "skipped"
			result.Reason = "missing_last_event_at"
			results = append(results, result)
			continue
		}
		if skipIndexed && index != nil && index.IsIndexed(session) {
			result.Status = "skipped"
			result.Reason = "already_indexed"
			results = append(results, result)
			continue
		}

		summary, err := summarizeSession(loaded.Config, session)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		writeResult, err := memory.WriteSession(memory.WriteOptions{
			RecallDir: loaded.Dir,
			Config:    loaded.Config,
		}, session, summary)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			results = append(results, result)
			continue
		}

		result.Status = "succeeded"
		result.Write = writeResult
		results = append(results, result)
	}

	return results
}

func failedIngestResult(path string, err error) ingestBatchResult {
	source, _ := provider.DetectSource(path)
	return ingestBatchResult{
		Source: source,
		Path:   path,
		Status: "failed",
		Error:  err.Error(),
	}
}

func skippedIngestResult(path string, reason string) ingestBatchResult {
	source, _ := provider.DetectSource(path)
	return ingestBatchResult{
		Source: source,
		Path:   path,
		Status: "skipped",
		Reason: reason,
	}
}

func countIngestResults(output *ingestBatchOutput) {
	output.Queued = 0
	output.Skipped = 0
	output.Succeeded = 0
	output.Failed = 0
	for _, result := range output.Results {
		switch result.Status {
		case "succeeded":
			output.Queued++
			output.Succeeded++
		case "failed":
			output.Queued++
			output.Failed++
		case "skipped":
			output.Skipped++
		}
	}
}

func upsertIngestResults(index *memory.Index, recallDir string, results []ingestBatchResult, indexedAt time.Time) bool {
	indexChanged := false
	for _, result := range results {
		if result.Status == "succeeded" && index.Upsert(recallDir, result.session, result.Write, indexedAt) {
			indexChanged = true
		}
	}
	return indexChanged
}

func runDiscover(args []string) error {
	flags := flag.NewFlagSet("discover", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	last := flags.Int("last", 0, "return the last N sessions by last event time")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return usageError()
	}

	sessions, err := discover.Discover(context.Background(), discover.Options{
		Last: *last,
	})
	if err != nil {
		return err
	}
	return writeJSON(os.Stdout, sessions)
}

func loadConfig() (config.Loaded, error) {
	loaded, err := config.Load(".")
	if err != nil {
		return config.Loaded{}, err
	}
	if err := loaded.Config.ValidateLLM(loaded.Path); err != nil {
		return config.Loaded{}, err
	}
	if err := loaded.Config.ValidateIngest(loaded.Path); err != nil {
		return config.Loaded{}, err
	}
	return loaded, nil
}

func summarizeSession(cfg config.Config, session *trace.Session) (*summarize.Result, error) {
	return summarize.WithProvider(
		context.Background(),
		cfg.LLM.Provider,
		cfg.LLM.Model,
		cfg.LLM.Reasoning.Level,
		session,
	)
}

func readFlatSessions(path string) ([]*trace.FlatSession, error) {
	data, err := readInput(path)
	if err != nil {
		return nil, err
	}

	var batch trace.FlatSessionBatch
	if err := json.Unmarshal(data, &batch); err == nil && batch.Sessions != nil {
		sessions := make([]*trace.FlatSession, 0, len(batch.Sessions))
		for i := range batch.Sessions {
			sessions = append(sessions, &batch.Sessions[i])
		}
		return sessions, nil
	}

	var session trace.FlatSession
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return []*trace.FlatSession{&session}, nil
}

func readPreparedSessions(path string) ([]*trace.Session, error) {
	data, err := readInput(path)
	if err != nil {
		return nil, err
	}

	var batch trace.SessionBatch
	if err := json.Unmarshal(data, &batch); err == nil && batch.Sessions != nil {
		sessions := make([]*trace.Session, 0, len(batch.Sessions))
		for i := range batch.Sessions {
			sessions = append(sessions, &batch.Sessions[i])
		}
		return sessions, nil
	}

	var session trace.Session
	if err := json.Unmarshal(data, &session); err != nil {
		return nil, err
	}
	return []*trace.Session{&session}, nil
}

func selectPreparedSession(sessions []*trace.Session, segmentIndex int) (*trace.Session, error) {
	if len(sessions) == 0 {
		return nil, fmt.Errorf("no prepared sessions in input")
	}
	if segmentIndex >= 0 {
		for _, session := range sessions {
			if session.SegmentIndex == segmentIndex {
				return session, nil
			}
		}
		return nil, fmt.Errorf("prepared input does not contain segment %d", segmentIndex)
	}
	if len(sessions) == 1 {
		return sessions[0], nil
	}
	return nil, fmt.Errorf("prepared input contains %d sessions; pass --segment", len(sessions))
}

func readSummaryResult(path string) (*summarize.Result, error) {
	var result summarize.Result
	if err := readJSONInput(path, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(path)
}

func readJSONInput(path string, value any) error {
	data, err := readInput(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, value)
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func flatSessionBatch(sessions []*trace.FlatSession) trace.FlatSessionBatch {
	batch := trace.FlatSessionBatch{Sessions: make([]trace.FlatSession, 0, len(sessions))}
	for _, session := range sessions {
		if session != nil {
			batch.Sessions = append(batch.Sessions, *session)
		}
	}
	return batch
}

func sessionBatch(sessions []*trace.Session) trace.SessionBatch {
	batch := trace.SessionBatch{Sessions: make([]trace.Session, 0, len(sessions))}
	for _, session := range sessions {
		if session != nil {
			batch.Sessions = append(batch.Sessions, *session)
		}
	}
	return batch
}

func usageError() error {
	return fmt.Errorf("usage:\n  recall parse <session.jsonl>\n  recall prepare [parsed-session.json|-]\n  recall render [prepared-session.json|-]\n  recall summarize [prepared-session.json|-]\n  recall write-memory <prepared-session.json> <summary.json>\n  recall ingest <session.jsonl>\n  recall ingest --last N\n  recall discover [--last N]")
}
