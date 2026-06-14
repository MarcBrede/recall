package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
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
	obs.Configure()

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

	limiter := llm.NewLimiter(loaded.Config.Ingest.Concurrency)
	result, _, err := summarizeSession(context.Background(), loaded.Config, limiter, session)
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
	dryRun := flags.Bool("dry-run", false, "show what would be ingested without LLM calls or writes")
	verbose := flags.Bool("verbose", false, "log per-session progress to stderr")
	logJSON := flags.Bool("log-json", false, "emit logs as JSON instead of text")
	if err := flags.Parse(args); err != nil {
		return err
	}

	remaining := flags.Args()
	if *last > 0 {
		if len(remaining) != 0 {
			return usageError()
		}
		return runIngestLast(*last, *verbose, *logJSON, *dryRun)
	}

	if len(remaining) != 1 {
		return usageError()
	}

	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	if *dryRun {
		index, err := memory.LoadIndex(loaded.Dir)
		if err != nil {
			return err
		}
		output := ingestPlanOutput{
			Discovered: 1,
			Results:    planPathSegments(context.Background(), loaded, index, remaining[0], true),
		}
		countPlanResults(&output)
		compactPlanOutput(&output)
		return writePlanSummary(os.Stdout, output)
	}

	index, err := memory.LoadIndex(loaded.Dir)
	if err != nil {
		return err
	}

	log, err := obs.SetupIngest(loaded.Dir, *verbose, *logJSON)
	if err != nil {
		return err
	}
	ctx := obs.Into(context.Background(), log)
	log.Info("ingest started", slog.Int("discovered", 1), slog.String("path", remaining[0]))
	progress := newProgressReporter(!*verbose && !*logJSON)
	progress.Printf("ingest: parsing %s\n", remaining[0])

	output := ingestBatchOutput{
		Discovered:     1,
		LLMConcurrency: loaded.Config.Ingest.Concurrency,
		Results:        ingestPathSegments(ctx, loaded, index, remaining[0], true, llm.NewLimiter(loaded.Config.Ingest.Concurrency), progress),
	}
	countIngestResults(&output)
	logIngestCompleted(log, output)
	progress.Printf("ingest: complete succeeded=%d skipped=%d failed=%d\n", output.Succeeded, output.Skipped, output.Failed)
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

// logIngestCompleted emits a single summary line for a finished ingest run.
func logIngestCompleted(log *slog.Logger, output ingestBatchOutput) {
	log.Info("ingest completed",
		slog.Int("discovered", output.Discovered),
		slog.Int("succeeded", output.Succeeded),
		slog.Int("failed", output.Failed),
		slog.Int("skipped", output.Skipped))
}

type ingestBatchOutput struct {
	Discovered     int                 `json:"discovered"`
	Queued         int                 `json:"queued"`
	Skipped        int                 `json:"skipped"`
	Succeeded      int                 `json:"succeeded"`
	Failed         int                 `json:"failed"`
	LLMConcurrency int                 `json:"llm_concurrency"`
	Results        []ingestBatchResult `json:"results"`
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

type ingestPlanOutput struct {
	Discovered int                `json:"discovered"`
	Segments   int                `json:"segments"`
	Skipped    int                `json:"skipped"`
	WouldRun   int                `json:"would_run"`
	WouldFail  int                `json:"would_fail"`
	Failed     int                `json:"failed"`
	Results    []ingestPlanResult `json:"results"`
}

type ingestPlanResult struct {
	Source           trace.Source       `json:"source"`
	Path             string             `json:"path"`
	ExternalID       string             `json:"external_id,omitempty"`
	SegmentIndex     int                `json:"segment_index"`
	SourceStartLine  int                `json:"source_start_line,omitempty"`
	SourceEndLine    int                `json:"source_end_line,omitempty"`
	Status           string             `json:"status"`
	Reason           string             `json:"reason,omitempty"`
	CurrentMemoryDir string             `json:"current_memory_dir,omitempty"`
	TargetMemoryDir  string             `json:"target_memory_dir,omitempty"`
	SectionCount     int                `json:"section_count,omitempty"`
	StepCount        int                `json:"step_count,omitempty"`
	EventCount       int                `json:"event_count,omitempty"`
	Sections         []sectionPlanEntry `json:"sections,omitempty"`
	Error            string             `json:"error,omitempty"`
}

type sectionPlanEntry struct {
	ID        string `json:"id"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Status    string `json:"status,omitempty"`
	Action    string `json:"action"`
	Reason    string `json:"reason,omitempty"`
}

func runIngestLast(last int, verbose bool, logJSON bool, dryRun bool) error {
	loaded, err := loadConfig()
	if err != nil {
		return err
	}

	if dryRun {
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
		output := ingestPlanOutput{
			Discovered: len(sessions),
		}
		for _, session := range sessions {
			output.Results = append(output.Results, planPathSegments(context.Background(), loaded, index, session.Path, true)...)
		}
		countPlanResults(&output)
		compactPlanOutput(&output)
		return writePlanSummary(os.Stdout, output)
	}

	progress := newProgressReporter(!verbose && !logJSON)
	progress.Printf("ingest: discovering last %d session file(s)\n", last)

	log, err := obs.SetupIngest(loaded.Dir, verbose, logJSON)
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
	limiter := llm.NewLimiter(loaded.Config.Ingest.Concurrency)
	output.LLMConcurrency = loaded.Config.Ingest.Concurrency
	log.Info("ingest started",
		slog.Int("discovered", len(sessions)),
		slog.Int("llm_concurrency", loaded.Config.Ingest.Concurrency))
	progress.Printf("ingest: discovered %d session file(s), llm_concurrency=%d\n", len(sessions), loaded.Config.Ingest.Concurrency)

	fileResults := make([][]ingestBatchResult, len(sessions))
	var completedFiles int32
	var wg sync.WaitGroup
	for sessionIndex := range sessions {
		wg.Add(1)
		go func(sessionIndex int) {
			defer wg.Done()
			session := sessions[sessionIndex]
			fileResults[sessionIndex] = ingestPathSegments(ctx, loaded, index, session.Path, true, limiter, progress)
			done := atomic.AddInt32(&completedFiles, 1)
			progress.Printf("ingest: file %d/%d done: %s\n", done, len(sessions), session.Path)
		}(sessionIndex)
	}
	wg.Wait()

	for _, result := range fileResults {
		output.Results = append(output.Results, result...)
	}
	countIngestResults(&output)
	logIngestCompleted(log, output)
	progress.Printf("ingest: complete succeeded=%d skipped=%d failed=%d\n", output.Succeeded, output.Skipped, output.Failed)

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

func planPathSegments(ctx context.Context, loaded config.Loaded, index *memory.Index, path string, skipIndexed bool) []ingestPlanResult {
	flats, err := provider.ParseFile(ctx, path)
	if err != nil {
		source, _ := provider.DetectSource(path)
		return []ingestPlanResult{{
			Source: source,
			Path:   path,
			Status: "failed",
			Error:  err.Error(),
		}}
	}
	if len(flats) == 0 {
		source, _ := provider.DetectSource(path)
		return []ingestPlanResult{{
			Source: source,
			Path:   path,
			Status: "skipped",
			Reason: "no_memory_events",
		}}
	}

	results := make([]ingestPlanResult, len(flats))
	for i, flat := range flats {
		results[i] = planFlatSegment(ctx, loaded, index, path, skipIndexed, flat)
	}
	return results
}

func planFlatSegment(ctx context.Context, loaded config.Loaded, index *memory.Index, path string, skipIndexed bool, flat *trace.FlatSession) ingestPlanResult {
	result := ingestPlanResult{
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
		return result
	}
	result.SectionCount, result.StepCount, result.EventCount = sessionCounts(session)
	result.TargetMemoryDir = memory.SessionDir(loaded.Dir, session)
	if session.EndedAt.IsZero() {
		result.Status = "skipped"
		result.Reason = "missing_last_event_at"
		return result
	}
	if skipIndexed && index != nil && index.IsIndexed(session) {
		result.Status = "skipped"
		result.Reason = "already_indexed"
		if entry, ok := index.Entry(session); ok {
			result.CurrentMemoryDir = memory.ResolveMemoryDir(loaded.Dir, entry)
		}
		return result
	}

	previousDir, previousMetadata, err := loadPreviousMetadata(loaded.Dir, index, session)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	result.CurrentMemoryDir = previousDir

	sectionMetadata, err := buildSectionMetadata(session)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		return result
	}
	result.Sections, result.Status, result.Reason = planSections(session, previousMetadata, sectionMetadata)
	return result
}

func planSections(session *trace.Session, previous *memory.Metadata, sectionMetadata map[string]memory.SectionMetadata) ([]sectionPlanEntry, string, string) {
	sections := make([]sectionPlanEntry, 0, len(session.Sections))
	if previous == nil || len(previous.Summaries.SectionSummaries) == 0 {
		reason := "new_segment"
		if previous != nil {
			reason = "missing_previous_summaries"
		}
		for _, section := range session.Sections {
			current := sectionMetadata[section.ID]
			sections = append(sections, sectionPlanEntry{
				ID:        section.ID,
				StartLine: section.StartLine,
				EndLine:   section.EndLine,
				Status:    current.Status,
				Action:    "summarize",
				Reason:    reason,
			})
		}
		return sections, "would_ingest", reason
	}

	var changed int
	var finalChanged bool
	for _, section := range session.Sections {
		current := sectionMetadata[section.ID]
		previousSection, hasPreviousSection := previous.Sections[section.ID]
		_, hasPreviousSummary := previous.Summaries.SectionSummaries[section.ID]
		entry := sectionPlanEntry{
			ID:        section.ID,
			StartLine: section.StartLine,
			EndLine:   section.EndLine,
			Status:    current.Status,
		}

		switch {
		case hasPreviousSection && previousSection.InputHash == current.InputHash && hasPreviousSummary:
			entry.Action = "reuse"
			entry.Reason = "unchanged"
		case hasPreviousSection &&
			previousSection.Status == memory.SectionStatusFinal &&
			previousSection.InputHash != "" &&
			previousSection.InputHash != current.InputHash:
			entry.Action = "error"
			entry.Reason = "final_section_changed"
			finalChanged = true
		case !hasPreviousSection:
			entry.Action = "summarize"
			entry.Reason = "new_section"
			changed++
		case !hasPreviousSummary:
			entry.Action = "summarize"
			entry.Reason = "missing_previous_summary"
			changed++
		default:
			entry.Action = "summarize"
			entry.Reason = "input_changed"
			changed++
		}
		sections = append(sections, entry)
	}

	if finalChanged {
		return sections, "would_fail", "final_section_changed"
	}
	if changed == 0 {
		return sections, "would_rewrite", "metadata_only"
	}
	return sections, "would_update", "changed_sections"
}

func ingestPathSegments(ctx context.Context, loaded config.Loaded, index *memory.Index, path string, skipIndexed bool, limiter *llm.Limiter, progress *progressReporter) []ingestBatchResult {
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
	progress.Printf("ingest: parsed %s into %d segment(s)\n", path, len(flats))

	results := make([]ingestBatchResult, len(flats))
	var wg sync.WaitGroup
	for i, flat := range flats {
		wg.Add(1)
		go func(i int, flat *trace.FlatSession) {
			defer wg.Done()
			results[i] = ingestFlatSegment(ctx, loaded, index, path, skipIndexed, limiter, progress, log, flat)
		}(i, flat)
	}
	wg.Wait()

	return results
}

func ingestFlatSegment(ctx context.Context, loaded config.Loaded, index *memory.Index, path string, skipIndexed bool, limiter *llm.Limiter, progress *progressReporter, log *slog.Logger, flat *trace.FlatSession) ingestBatchResult {
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
		return result
	}
	result.session = session
	if session.EndedAt.IsZero() {
		result.Status = "skipped"
		result.Reason = "missing_last_event_at"
		segLog.Info("segment skipped", slog.String("reason", result.Reason))
		progress.Printf("ingest: skip %s %s seg%03d (%s)\n", flat.Source, flat.ExternalID, flat.SegmentIndex, result.Reason)
		return result
	}
	if skipIndexed && index != nil && index.IsIndexed(session) {
		result.Status = "skipped"
		result.Reason = "already_indexed"
		segLog.Info("segment skipped", slog.String("reason", result.Reason))
		progress.Printf("ingest: skip %s %s seg%03d (%s)\n", flat.Source, flat.ExternalID, flat.SegmentIndex, result.Reason)
		return result
	}

	sections, steps, events := sessionCounts(session)
	segLog.Info("segment ingest started",
		slog.Int("sections", sections),
		slog.Int("steps", steps),
		slog.Int("events", events))
	progress.Printf("ingest: start %s %s seg%03d sections=%d steps=%d\n", session.Source, session.ExternalID, session.SegmentIndex, sections, steps)

	previousDir, previousMetadata, err := loadPreviousMetadata(loaded.Dir, index, session)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		segLog.Error("segment ingest failed", slog.String("stage", "load_previous_metadata"), slog.String("error", err.Error()))
		return result
	}

	summary, sectionMetadata, usage, err := summarizeSegment(segCtx, loaded.Config, limiter, session, previousMetadata)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		segLog.Error("segment ingest failed", slog.String("stage", "summarize"), slog.String("error", err.Error()))
		progress.Printf("ingest: fail %s %s seg%03d summarize: %s\n", session.Source, session.ExternalID, session.SegmentIndex, err.Error())
		return result
	}

	writeResult, err := memory.WriteSession(memory.WriteOptions{
		RecallDir:       loaded.Dir,
		Config:          loaded.Config,
		PreviousDir:     previousDir,
		SectionMetadata: sectionMetadata,
	}, session, summary)
	if err != nil {
		result.Status = "failed"
		result.Error = err.Error()
		segLog.Error("segment ingest failed", slog.String("stage", "write"), slog.String("error", err.Error()))
		progress.Printf("ingest: fail %s %s seg%03d write: %s\n", session.Source, session.ExternalID, session.SegmentIndex, err.Error())
		return result
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
	progress.Printf("ingest: done %s %s seg%03d input_tokens=%d output_tokens=%d\n", session.Source, session.ExternalID, session.SegmentIndex, usage.InputTokens, usage.OutputTokens)
	return result
}

func loadPreviousMetadata(recallDir string, index *memory.Index, session *trace.Session) (string, *memory.Metadata, error) {
	entry, ok := index.Entry(session)
	if !ok {
		return "", nil, nil
	}
	dir := memory.ResolveMemoryDir(recallDir, entry)
	if dir == "" {
		return "", nil, nil
	}
	metadata, err := memory.LoadMetadata(dir)
	if errors.Is(err, os.ErrNotExist) {
		return dir, nil, nil
	}
	if err != nil {
		return dir, nil, err
	}
	return dir, metadata, nil
}

func summarizeSegment(ctx context.Context, cfg config.Config, limiter *llm.Limiter, session *trace.Session, previous *memory.Metadata) (*summarize.Result, map[string]memory.SectionMetadata, llm.Usage, error) {
	sectionMetadata, err := buildSectionMetadata(session)
	if err != nil {
		return nil, nil, llm.Usage{}, err
	}
	if previous == nil || len(previous.Summaries.SectionSummaries) == 0 {
		result, usage, err := summarizeSession(ctx, cfg, limiter, session)
		return result, sectionMetadata, usage, err
	}

	reused := make(map[string]summarize.SectionResult, len(session.Sections))
	var changedSectionIDs []string
	for _, section := range session.Sections {
		current := sectionMetadata[section.ID]
		previousSection, hasPreviousSection := previous.Sections[section.ID]
		previousSummary, hasPreviousSummary := previous.Summaries.SectionSummaries[section.ID]
		if hasPreviousSection && previousSection.InputHash == current.InputHash && hasPreviousSummary {
			reused[section.ID] = previousSummary
			continue
		}
		if hasPreviousSection &&
			previousSection.Status == memory.SectionStatusFinal &&
			previousSection.InputHash != "" &&
			previousSection.InputHash != current.InputHash {
			return nil, sectionMetadata, llm.Usage{}, fmt.Errorf("incremental ingest: final section %s changed; remove its index entry or reingest from scratch", section.ID)
		}
		changedSectionIDs = append(changedSectionIDs, section.ID)
	}

	if len(changedSectionIDs) == 0 {
		result := previous.Summaries
		if err := summarize.ValidateResult(session, &result); err == nil {
			return &result, sectionMetadata, llm.Usage{}, nil
		}
	}

	result, usage, err := summarize.WithProviderOptionsIncremental(
		ctx,
		cfg.LLM.Provider,
		cfg.LLM.Model,
		cfg.LLM.Reasoning.Level,
		llmOptions(cfg.LLM),
		limiter,
		session,
		reused,
		changedSectionIDs,
	)
	return result, sectionMetadata, usage, err
}

func buildSectionMetadata(session *trace.Session) (map[string]memory.SectionMetadata, error) {
	sections := make(map[string]memory.SectionMetadata, len(session.Sections))
	for i := range session.Sections {
		section := &session.Sections[i]
		input, err := prepare.RenderSectionForLLM(session, section)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256([]byte(input))
		status := memory.SectionStatusFinal
		if i == len(session.Sections)-1 {
			status = memory.SectionStatusOpen
		}
		sections[section.ID] = memory.SectionMetadata{
			StartLine:   section.StartLine,
			EndLine:     section.EndLine,
			LastEventAt: formatEventTime(section.EndedAt),
			Status:      status,
			InputHash:   fmt.Sprintf("sha256:%x", sum),
		}
	}
	return sections, nil
}

func formatEventTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
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

func countPlanResults(output *ingestPlanOutput) {
	output.Segments = len(output.Results)
	output.Skipped = 0
	output.WouldRun = 0
	output.WouldFail = 0
	output.Failed = 0
	for _, result := range output.Results {
		switch result.Status {
		case "skipped":
			output.Skipped++
		case "would_ingest", "would_update", "would_rewrite":
			output.WouldRun++
		case "would_fail":
			output.WouldFail++
		case "failed":
			output.Failed++
		}
	}
}

func compactPlanOutput(output *ingestPlanOutput) {
	if output == nil {
		return
	}
	results := output.Results[:0]
	for _, result := range output.Results {
		if result.Status == "skipped" {
			continue
		}
		sections := result.Sections[:0]
		for _, section := range result.Sections {
			if section.Action == "reuse" {
				continue
			}
			sections = append(sections, section)
		}
		result.Sections = sections
		results = append(results, result)
	}
	output.Results = results
}

func writePlanSummary(w io.Writer, output ingestPlanOutput) error {
	if output.WouldRun == 0 && output.WouldFail == 0 && output.Failed == 0 {
		_, err := fmt.Fprintf(
			w,
			"No sessions would be ingested. discovered=%d segments=%d skipped=%d\n",
			output.Discovered,
			output.Segments,
			output.Skipped,
		)
		return err
	}

	if _, err := fmt.Fprintf(
		w,
		"Would run %d segment(s). discovered=%d segments=%d skipped=%d would_fail=%d failed=%d\n",
		output.WouldRun,
		output.Discovered,
		output.Segments,
		output.Skipped,
		output.WouldFail,
		output.Failed,
	); err != nil {
		return err
	}

	for _, result := range output.Results {
		if _, err := fmt.Fprintf(
			w,
			"%s %s seg%03d %s",
			result.Source,
			result.ExternalID,
			result.SegmentIndex,
			result.Status,
		); err != nil {
			return err
		}
		if len(result.Sections) == 0 {
			if _, err := fmt.Fprint(w, ": session\n"); err != nil {
				return err
			}
			continue
		}
		if _, err := fmt.Fprint(w, ": "); err != nil {
			return err
		}
		if _, err := fmt.Fprint(w, sectionListSummary(result.Sections)); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}
	return nil
}

func sectionListSummary(sections []sectionPlanEntry) string {
	if len(sections) == 0 {
		return "session"
	}
	if allSectionsShareReason(sections, "new_segment") {
		first := sections[0].ID
		last := sections[len(sections)-1].ID
		open := ""
		if sections[len(sections)-1].Status == memory.SectionStatusOpen {
			open = fmt.Sprintf(", open %s", last)
		}
		if len(sections) == 1 {
			return fmt.Sprintf("all 1 section (%s%s)", first, open)
		}
		return fmt.Sprintf("all %d sections (%s-%s%s)", len(sections), first, last, open)
	}

	var builder strings.Builder
	for i, section := range sections {
		if i > 0 {
			builder.WriteString(", ")
		}
		builder.WriteString(sectionSummaryLabel(section))
	}
	return builder.String()
}

func allSectionsShareReason(sections []sectionPlanEntry, reason string) bool {
	for _, section := range sections {
		if section.Reason != reason {
			return false
		}
	}
	return true
}

func sectionSummaryLabel(section sectionPlanEntry) string {
	if section.Status == memory.SectionStatusOpen {
		return fmt.Sprintf("%s[%s,open]", section.ID, section.Reason)
	}
	return fmt.Sprintf("%s[%s]", section.ID, section.Reason)
}

type progressReporter struct {
	mu      sync.Mutex
	w       io.Writer
	enabled bool
}

func newProgressReporter(enabled bool) *progressReporter {
	return &progressReporter{
		w:       os.Stderr,
		enabled: enabled,
	}
}

func (progress *progressReporter) Printf(format string, args ...any) {
	if progress == nil || !progress.enabled {
		return
	}
	progress.mu.Lock()
	defer progress.mu.Unlock()
	_, _ = fmt.Fprintf(progress.w, format, args...)
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

func summarizeSession(ctx context.Context, cfg config.Config, limiter *llm.Limiter, session *trace.Session) (*summarize.Result, llm.Usage, error) {
	return summarize.WithProviderOptions(
		ctx,
		cfg.LLM.Provider,
		cfg.LLM.Model,
		cfg.LLM.Reasoning.Level,
		llmOptions(cfg.LLM),
		limiter,
		session,
	)
}

func llmOptions(cfg config.LLMConfig) llm.Options {
	return llm.Options{
		BaseURL: cfg.BaseURL,
		Headers: cfg.Headers,
		Auth: llm.AuthConfig{
			Type:    cfg.Auth.Type,
			Env:     cfg.Auth.Env,
			Command: cfg.Auth.Command,
		},
	}
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
