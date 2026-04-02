package pipeline

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

// SpikeThrottle tracks per-project event counts per minute and rejects events
// when a project with spike protection enabled experiences a volume spike.
// A spike is defined as the current minute's count exceeding 2x the rolling
// average of the previous baselineWindow minutes.
type SpikeThrottle struct {
	db *sql.DB

	// buckets maps projectID -> *projectBuckets
	buckets sync.Map

	// enabledCache maps projectID -> *enabledEntry (cached spike_protection flag)
	enabledCache sync.Map

	// now is a clock function, overridable for testing.
	now func() time.Time

	stopCh chan struct{}
	stopWG sync.WaitGroup
}

const (
	// baselineWindow is the number of past minutes used to compute the rolling average.
	baselineWindow = 10
	// spikeMultiplier is how many times the baseline the current rate must exceed.
	spikeMultiplier = 2
	// minBaselineCount is the minimum baseline count to avoid false positives
	// when traffic is very low. A project must average at least this many events
	// per minute before throttling kicks in.
	minBaselineCount = 10
	// enabledCacheTTL is how long we cache the spike_protection flag per project.
	enabledCacheTTL = 30 * time.Second
	// cleanupInterval is how often stale buckets are swept.
	cleanupInterval = 5 * time.Minute
)

type enabledEntry struct {
	enabled   bool
	expiresAt int64 // UnixNano
}

// projectBuckets holds per-minute counters for a single project.
// The ring buffer stores counts for the most recent baselineWindow+1 minutes
// (current minute + baselineWindow historical minutes).
type projectBuckets struct {
	mu       sync.Mutex
	counts   [baselineWindow + 1]int64
	startMin int64 // minute number for counts[0]
}

// NewSpikeThrottle creates a new throttle that checks the spike_protection
// column on the projects table. Pass nil db to disable throttling.
func NewSpikeThrottle(db *sql.DB) *SpikeThrottle {
	st := &SpikeThrottle{
		db:     db,
		now:    time.Now,
		stopCh: make(chan struct{}),
	}
	st.stopWG.Add(1)
	go st.cleanupLoop()
	return st
}

// Stop shuts down the background cleanup goroutine.
func (st *SpikeThrottle) Stop() {
	close(st.stopCh)
	st.stopWG.Wait()
}

// Allow records an event for the given project and returns whether the event
// should be admitted. Returns true (allow) when spike protection is not enabled
// or when the rate is within acceptable bounds.
func (st *SpikeThrottle) Allow(ctx context.Context, projectID string) bool {
	if st == nil || st.db == nil {
		return true
	}

	// Check if spike protection is enabled for this project (cached).
	if !st.isEnabled(ctx, projectID) {
		return true
	}

	now := st.now()
	curMin := now.Unix() / 60

	pb := st.getOrCreateBuckets(projectID)
	pb.mu.Lock()
	defer pb.mu.Unlock()

	// Advance the ring buffer if needed.
	st.advanceBuckets(pb, curMin)

	// Compute current index and baseline average.
	idx := int(curMin - pb.startMin)
	if idx < 0 || idx >= len(pb.counts) {
		// Should not happen after advanceBuckets, but be safe.
		return true
	}

	// Increment current count.
	pb.counts[idx]++
	currentCount := pb.counts[idx]

	// Compute baseline: average of all minutes before the current one.
	var total int64
	var n int64
	for i := 0; i < len(pb.counts); i++ {
		if i == idx {
			continue
		}
		total += pb.counts[i]
		n++
	}
	if n == 0 {
		return true
	}
	baseline := total / n
	if baseline < minBaselineCount {
		return true
	}

	if currentCount > baseline*spikeMultiplier {
		log.Warn().
			Str("project_id", projectID).
			Int64("current", currentCount).
			Int64("baseline_avg", baseline).
			Msg("spike throttle: rejecting event")
		return false
	}
	return true
}

func (st *SpikeThrottle) getOrCreateBuckets(projectID string) *projectBuckets {
	if v, ok := st.buckets.Load(projectID); ok {
		return v.(*projectBuckets)
	}
	pb := &projectBuckets{
		startMin: st.now().Unix() / 60,
	}
	actual, _ := st.buckets.LoadOrStore(projectID, pb)
	return actual.(*projectBuckets)
}

// advanceBuckets shifts the ring buffer so that curMin falls within it.
func (st *SpikeThrottle) advanceBuckets(pb *projectBuckets, curMin int64) {
	endMin := pb.startMin + int64(len(pb.counts)) - 1
	if curMin <= endMin {
		return // current minute is still within the buffer
	}

	shift := int(curMin - endMin)
	if shift >= len(pb.counts) {
		// Entire buffer is stale; reset.
		pb.startMin = curMin - int64(len(pb.counts)) + 1
		for i := range pb.counts {
			pb.counts[i] = 0
		}
		return
	}

	// Shift entries left by `shift` positions.
	copy(pb.counts[:], pb.counts[shift:])
	for i := len(pb.counts) - shift; i < len(pb.counts); i++ {
		pb.counts[i] = 0
	}
	pb.startMin += int64(shift)
}

// isEnabled checks whether spike protection is enabled for a project,
// using a short-lived cache to avoid hitting SQLite on every event.
func (st *SpikeThrottle) isEnabled(ctx context.Context, projectID string) bool {
	nowNano := st.now().UnixNano()

	if v, ok := st.enabledCache.Load(projectID); ok {
		entry := v.(*enabledEntry)
		if atomic.LoadInt64(&entry.expiresAt) > nowNano {
			return entry.enabled
		}
	}

	var enabled int
	err := st.db.QueryRowContext(ctx,
		`SELECT spike_protection FROM projects WHERE id = ?`, projectID,
	).Scan(&enabled)
	if err != nil {
		// Project not found or error — don't throttle.
		return false
	}

	st.enabledCache.Store(projectID, &enabledEntry{
		enabled:   enabled != 0,
		expiresAt: nowNano + int64(enabledCacheTTL),
	})
	return enabled != 0
}

func (st *SpikeThrottle) cleanupLoop() {
	defer st.stopWG.Done()
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			st.cleanup()
		case <-st.stopCh:
			return
		}
	}
}

func (st *SpikeThrottle) cleanup() {
	curMin := st.now().Unix() / 60
	st.buckets.Range(func(key, value any) bool {
		pb := value.(*projectBuckets)
		pb.mu.Lock()
		endMin := pb.startMin + int64(len(pb.counts)) - 1
		pb.mu.Unlock()
		// If the entire buffer is older than 2x the baseline window, evict.
		if curMin-endMin > int64(baselineWindow)*2 {
			st.buckets.Delete(key)
		}
		return true
	})
	// Evict expired cache entries.
	nowNano := st.now().UnixNano()
	st.enabledCache.Range(func(key, value any) bool {
		entry := value.(*enabledEntry)
		if atomic.LoadInt64(&entry.expiresAt) < nowNano {
			st.enabledCache.Delete(key)
		}
		return true
	})
}
