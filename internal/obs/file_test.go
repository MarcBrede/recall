package obs

import (
	"bufio"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestOpenLogFileConcurrentAppendsStayIntact simulates many concurrent recall
// processes: each goroutine opens its own O_APPEND handle to the day's file
// (an independent fd, like a separate process) and writes many records. Because
// nothing is shared between them, only the OS append-atomicity keeps lines from
// interleaving. Every line must parse as JSON and the count must be exact.
func TestOpenLogFileConcurrentAppendsStayIntact(t *testing.T) {
	dir := t.TempDir()

	const writers = 8
	const linesPerWriter = 250
	payload := strings.Repeat("x", 300) // large enough to stress atomicity

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			file, err := OpenLogFile(dir)
			if err != nil {
				t.Errorf("open log file: %v", err)
				return
			}
			defer file.Close()

			logger := New(io.Discard, Options{
				Level:     LevelSilent,
				File:      file,
				FileLevel: slog.LevelInfo,
			}).With(slog.Int("writer", id))

			for i := 0; i < linesPerWriter; i++ {
				logger.Info("entry", slog.Int("seq", i), slog.String("pad", payload))
			}
		}(w)
	}
	wg.Wait()

	matches, err := filepath.Glob(filepath.Join(dir, "logs", "*.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected 1 log file, got %d", len(matches))
	}

	file, err := os.Open(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	count := 0
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("line %d is not valid JSON (interleaved write): %v", count, err)
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if want := writers * linesPerWriter; count != want {
		t.Fatalf("line count = %d, want %d", count, want)
	}
}
