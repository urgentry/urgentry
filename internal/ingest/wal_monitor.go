package ingest

import (
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

const (
	// defaultWALSizeLimitBytes is 500 MB.
	defaultWALSizeLimitBytes = 500 << 20

	// walCheckInterval is how long a cached WAL size reading is considered fresh.
	walCheckInterval = 5 * time.Second
)

// WALMonitor periodically checks the SQLite WAL file size and signals when it
// exceeds a configurable threshold. Checks are cached for walCheckInterval to
// avoid stat(2) on every ingest request. When walPath is empty the monitor is
// a no-op (Postgres mode has no WAL file).
type WALMonitor struct {
	walPath   string
	limitBytes int64

	// exceeded is 1 when the WAL is over the limit, 0 otherwise.
	exceeded atomic.Int32

	// lastChecked is the Unix nanosecond timestamp of the last stat.
	lastChecked atomic.Int64
}

// NewWALMonitor creates a WALMonitor for the given WAL file path and byte limit.
// Pass an empty walPath for a no-op monitor (e.g. Postgres deployments).
func NewWALMonitor(walPath string, limitBytes int64) *WALMonitor {
	if limitBytes <= 0 {
		limitBytes = defaultWALSizeLimitBytes
	}
	return &WALMonitor{
		walPath:    walPath,
		limitBytes: limitBytes,
	}
}

// NewWALMonitorFromEnv builds a WALMonitor using the data directory and the
// URGENTRY_WAL_SIZE_LIMIT environment variable (bytes; default 500 MB).
// Returns nil when dataDir is empty (Postgres-only deployment).
func NewWALMonitorFromEnv(dataDir string) *WALMonitor {
	if dataDir == "" {
		return nil
	}
	limit := int64(defaultWALSizeLimitBytes)
	if raw := os.Getenv("URGENTRY_WAL_SIZE_LIMIT"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	walPath := filepath.Join(dataDir, "urgentry.db-wal")
	return NewWALMonitor(walPath, limit)
}

// WALSizeExceeded returns true when the WAL file size exceeds the configured
// limit. The result is cached for walCheckInterval. Always returns false when
// the monitor was created with an empty walPath.
func (m *WALMonitor) WALSizeExceeded() bool {
	if m == nil || m.walPath == "" {
		return false
	}

	now := time.Now().UnixNano()
	last := m.lastChecked.Load()
	if now-last < walCheckInterval.Nanoseconds() {
		// Return cached result.
		return m.exceeded.Load() == 1
	}

	// Attempt to win the refresh race. If another goroutine already updated
	// lastChecked since we read it, we accept their result.
	if !m.lastChecked.CompareAndSwap(last, now) {
		return m.exceeded.Load() == 1
	}

	info, err := os.Stat(m.walPath)
	if err != nil {
		// WAL file absent or unreadable — not exceeded.
		m.exceeded.Store(0)
		return false
	}

	if info.Size() > m.limitBytes {
		m.exceeded.Store(1)
		return true
	}
	m.exceeded.Store(0)
	return false
}
