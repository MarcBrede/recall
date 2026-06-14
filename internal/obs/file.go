package obs

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// OpenLogFile opens (creating if needed) the day's NDJSON log under
// <recallDir>/logs. It opens with O_APPEND so multiple concurrent recall
// processes can share the same file without their records interleaving — the
// OS makes each append atomic. The caller owns the returned file; leaving it
// open for the process lifetime is fine since writes are unbuffered.
func OpenLogFile(recallDir string) (*os.File, error) {
	logDir := filepath.Join(recallDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, err
	}
	name := "recall-" + time.Now().UTC().Format("2006-01-02") + ".ndjson"
	return os.OpenFile(filepath.Join(logDir, name), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
}

var (
	runIDOnce sync.Once
	runIDVal  string
)

// RunID returns a short random id, stable for the lifetime of the process, used
// to tell concurrent runs apart in a shared log even when they touch the same
// session. All goroutines in one run share the same id.
func RunID() string {
	runIDOnce.Do(func() {
		buf := make([]byte, 4)
		if _, err := rand.Read(buf); err != nil {
			runIDVal = "00000000"
			return
		}
		runIDVal = hex.EncodeToString(buf)
	})
	return runIDVal
}
