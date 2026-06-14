package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/marc-brede/recall/internal/config"
	"github.com/marc-brede/recall/internal/discover"
	"github.com/marc-brede/recall/internal/llm"
	"github.com/marc-brede/recall/internal/memory"
	"github.com/marc-brede/recall/internal/obs"
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
	configureLogging(os.Getenv("RECALL_LOG"), os.Getenv("RECALL_LOG_FORMAT"))

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

	result, _, err := summarizeSession(context.Background(), loaded.Config, session)
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
	verbose := flags.Bool("verbose", false, "log per-session progress to stderr")
	logJSON := flags.Bool("log-json", false, "emit logs as JSON instead of text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	applyIngestLogFlags(*verbose, *logJSON)

	remaining := flags.Args()
	if *last > 0 {
		if len(remaining) != 0 {
			return usageError()
		}
		return runIngestLast(*last, *verbose, *logJSON)
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

	log, err := setupIngestLogging(loaded.Dir, *verbose, *logJSON)
	if err != nil {
		return err
	}
	ctx := obs.Into(context.Background(), log)
	log.Info("ingest started", slog.Int("discovered", 1), slog.String("path", remaining[0]))

	output := ingestBatchOutput{
		Discovered:  1,
		Concurrency: 1,
		Results:     ingestPathSegments(ctx, loaded, nil, remaining[0], false),
	}
	countIngestResults(&output)
	logIngestCompleted(log, output)
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

// applyIngestLogFlags refines the default logger from ingest flags. Flags raise
// verbosity on top of any RECALL_LOG setting but never lower it. It runs before
// config is loaded so early failures are still logged to stderr.
func applyIngestLogFlags(verbose bool, logJSON bool) {
	level, json := ingestStderrConfig(verbose, logJSON)
	slog.SetDefault(obs.New(os.Stderr, obs.Options{Level: level, JSON: json}))
}

func ingestStderrConfig(verbose bool, logJSON bool) (slog.Level, bool) {
	level := obs.ParseLevel(os.Getenv("RECALL_LOG"))
	if verbose && level > slog.LevelInfo {
		level = slog.LevelInfo
	}
	json := logJSON || strings.EqualFold(strings.TrimSpace(os.Getenv("RECALL_LOG_FORMAT")), "json")
	return level, json
}

// setupIngestLogging installs the logger used for an ingest run. It tees stderr
// (at the flag/env level) and an append-only NDJSON file under <dir>/logs that
// always captures at least the info-level lifecycle. pid and run_id are bound
// so concurrent runs sharing the day's file stay distinguishable.
func setupIngestLogging(dir string, verbose bool, logJSON bool) (*slog.Logger, error) {
	stderrLevel, json := ingestStderrConfig(verbose, logJSON)

	fileLevel := slog.LevelInfo
	if stderrLevel < fileLevel {
		fileLevel = stderrLevel
	}

	file, err := obs.OpenLogFile(dir)
	if err != nil {
		return nil, err
	}

	logger := obs.New(os.Stderr, obs.Options{
		Level:     stderrLevel,
		JSON:      json,
		File:      file,
		FileLevel: fileLevel,
	}).With(
		slog.Int("pid", os.Getpid()),
		slog.String("run_id", obs.RunID()),
	)
	slog.SetDefault(logger)
	return logger, nil
}

// logIngestCompleted emits a single summary line for a finished ingest run.
func logIngestCompleted(log *slog.Logger, output ingestBatchOutput) {
	log.Info("ingest completed",
		slog.Int("discovered", output.Discovered),
		slog.Int("succeeded", output.Succeeded),
		slog.Int("failed", output.Failed),
		slog.Int("skipped", output.Skipped))
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

func runIngestLast(last int, verbose bool, logJSON bool) error {
	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	log, err := setupIngestLogging(loaded.Dir, verbose, logJSON)
	if err != nil {
		return err
	}
	ctx := obs.Into(context.Background(), log)

	sessions, err := discover.Discover(ctx, discover.Options{
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
	log.Info("ingest started",
		slog.Int("discovered", len(sessions)),
		slog.Int("concurrency", concurrency))

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
					results: ingestPathSegments(ctx, loaded, index, session.Path, true),
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
	logIngestCompleted(log, output)

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

func ingestPathSegments(ctx context.Context, loaded config.Loaded, index *memory.Index, path string, skipIndexed bool) []ingestBatchResult {
	log := obs.From(ctx).With(slog.String("path", path))

	flats, err := provider.ParseFile(ctx, path)
	if err != nil {
		log.Error("ingest parse failed", slog.String("error", err.Error()))
		return []ingestBatchResult{failedIngestResult(path, err)}
	}
	if len(flats) == 0 {
		log.Info("ingest skipped", slog.String("reason", "no_memory_events"))
		return []ingestBatchResult{skippedIngestResult(path, "no_memory_events")}
	}
	// One physical file can split into several segments when the agent compacts
	// context and continues — log how many sub-sessions this file produced.
	log.Info("ingest parsed", slog.Int("sub_sessions", len(flats)))

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

		// Bind the session's identifying attributes once; every log line below
		// (and downstream summarize/llm logs via segCtx) inherits them.
		segLog := log.With(obs.Flat(flat))
		segCtx := obs.Into(ctx, segLog)

		session, err := prepare.FromFlatSession(flat)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			segLog.Error("segment ingest failed", slog.String("stage", "prepare"), slog.String("error", err.Error()))
			results = append(results, result)
			continue
		}
		result.session = session
		if session.EndedAt.IsZero() {
			result.Status = "skipped"
			result.Reason = "missing_last_event_at"
			segLog.Info("segment skipped", slog.String("reason", result.Reason))
			results = append(results, result)
			continue
		}
		if skipIndexed && index != nil && index.IsIndexed(session) {
			result.Status = "skipped"
			result.Reason = "already_indexed"
			segLog.Info("segment skipped", slog.String("reason", result.Reason))
			results = append(results, result)
			continue
		}

		sections, steps, events := sessionCounts(session)
		segLog.Info("segment ingest started",
			slog.Int("sections", sections),
			slog.Int("steps", steps),
			slog.Int("events", events))

		summary, usage, err := summarizeSession(segCtx, loaded.Config, session)
		if err != nil {
			result.Status = "failed"
			result.Error = err.Error()
			segLog.Error("segment ingest failed", slog.String("stage", "summarize"), slog.String("error", err.Error()))
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
			segLog.Error("segment ingest failed", slog.String("stage", "write"), slog.String("error", err.Error()))
			results = append(results, result)
			continue
		}

		result.Status = "succeeded"
		result.Write = writeResult
		segLog.Info("segment ingest succeeded",
			slog.String("memory_dir", writeResult.Dir),
			slog.Int("sections", sections),
			slog.Int("steps", steps),
			slog.Int("events", events),
			slog.Int("input_tokens", usage.InputTokens),
			slog.Int("output_tokens", usage.OutputTokens))
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

// configureLogging installs the process logger. Logs always go to stderr so
// stdout stays a clean JSON data channel for piping. Default level is warn,
// which keeps piped commands quiet while still surfacing failures.
func configureLogging(level string, format string) {
	slog.SetDefault(obs.New(os.Stderr, obs.Options{
		Level: obs.ParseLevel(level),
		JSON:  strings.EqualFold(strings.TrimSpace(format), "json"),
	}))
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

func summarizeSession(ctx context.Context, cfg config.Config, session *trace.Session) (*summarize.Result, llm.Usage, error) {
	return summarize.WithProvider(
		ctx,
		cfg.LLM.Provider,
		cfg.LLM.Model,
		cfg.LLM.Reasoning.Level,
		session,
	)
}

// sessionCounts returns the structural size of a prepared session: how many
// sections, steps, and timeline events it contains.
func sessionCounts(session *trace.Session) (sections int, steps int, events int) {
	sections = len(session.Sections)
	for _, section := range session.Sections {
		steps += len(section.Steps)
		for _, step := range section.Steps {
			events += len(step.Events)
		}
	}
	return sections, steps, events
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
