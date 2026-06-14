package obs

import (
	"log/slog"
	"os"
	"strings"
)

// Environment variables that configure logging for every recall command.
const (
	EnvLevel  = "RECALL_LOG"        // level: debug|info|warn|error|off (default warn)
	EnvFormat = "RECALL_LOG_FORMAT" // format: text|json (default text)
)

// Configure installs the process default logger from the environment. It is
// stderr-only and quiet by default (warn), keeping stdout a clean JSON data
// channel for piped commands. Call once at startup before dispatching.
func Configure() {
	slog.SetDefault(New(os.Stderr, Options{
		Level: ParseLevel(os.Getenv(EnvLevel)),
		JSON:  formatIsJSON(os.Getenv(EnvFormat)),
	}))
}

// SetupIngest installs the logger for an ingest run and returns it. Records tee
// to stderr (at the flag/env level) and to an append-only NDJSON file under
// <dir>/logs that always captures at least the info-level lifecycle. pid and
// run_id are bound so concurrent runs sharing the day's file stay
// distinguishable. The --verbose and --log-json flags raise verbosity on top of
// any RECALL_LOG setting but never lower it.
func SetupIngest(dir string, verbose bool, logJSON bool) (*slog.Logger, error) {
	stderrLevel := ParseLevel(os.Getenv(EnvLevel))
	if verbose && stderrLevel > slog.LevelInfo {
		stderrLevel = slog.LevelInfo
	}
	json := logJSON || formatIsJSON(os.Getenv(EnvFormat))

	fileLevel := slog.LevelInfo
	if stderrLevel < fileLevel {
		fileLevel = stderrLevel
	}

	file, err := OpenLogFile(dir)
	if err != nil {
		return nil, err
	}

	logger := New(os.Stderr, Options{
		Level:     stderrLevel,
		JSON:      json,
		File:      file,
		FileLevel: fileLevel,
	}).With(
		slog.Int("pid", os.Getpid()),
		slog.String("run_id", RunID()),
	)
	slog.SetDefault(logger)
	return logger, nil
}

func formatIsJSON(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), "json")
}
