// Package metrics provides lightweight in-process metrics collection.
// No external collector required — designed for Tiny (single-node) mode.
package metrics

import (
	"runtime"
	"sync/atomic"
	"time"
)

const (
	StageIngestRequest = "ingest_request"
	StageAuthLookup    = "auth_lookup"
	StageAuthTouch     = "auth_touch"
	StageEnqueue       = "enqueue"
	StageQueueWait     = "queue_wait"
	StageProcessing    = "processing"
	StageNormalize     = "normalize"
	StageGrouping      = "grouping"
	StageBlobWrite     = "blob_write"
	StageGroupLookup   = "group_lookup"
	StageGroupUpsert   = "group_upsert"
	StageEventWrite    = "event_write"
)

var stageNames = []string{
	StageIngestRequest,
	StageAuthLookup,
	StageAuthTouch,
	StageEnqueue,
	StageQueueWait,
	StageProcessing,
	StageNormalize,
	StageGrouping,
	StageBlobWrite,
	StageGroupLookup,
	StageGroupUpsert,
	StageEventWrite,
}

type stageStats struct {
	Count   atomic.Int64
	Errors  atomic.Int64
	TotalNs atomic.Int64
	Buckets [14]atomic.Int64
}

// Metrics holds atomic counters for in-process telemetry.
// All fields are safe for concurrent use.
type Metrics struct {
	// Ingest
	IngestRequests atomic.Int64
	IngestErrors   atomic.Int64
	IngestBytes    atomic.Int64

	// Pipeline
	EventsQueued     atomic.Int64
	EventsProcessed  atomic.Int64
	EventsFailed     atomic.Int64
	EventsDropped    atomic.Int64
	ProcessingTimeNs atomic.Int64 // cumulative nanoseconds

	// Groups
	GroupsCreated atomic.Int64
	GroupsUpdated atomic.Int64
	Regressions   atomic.Int64

	// Alerts
	AlertsFired          atomic.Int64
	AlertDispatchQueued  atomic.Int64
	AlertDispatchDropped atomic.Int64

	// System
	StartedAt    time.Time
	MetricsToken string // bearer token for remote /metrics access

	// Latency histogram: 14 buckets
	// [<1ms, <2ms, <5ms, <10ms, <25ms, <50ms, <100ms, <250ms, <500ms, <1s, <2s, <5s, <10s, >=10s]
	latencyBuckets [14]atomic.Int64

	stageTimings map[string]*stageStats
}

// New creates a Metrics instance with StartedAt set to now.
func New() *Metrics {
	m := &Metrics{
		StartedAt: time.Now(),
	}
	m.stageTimings = make(map[string]*stageStats, len(stageNames))
	for _, name := range stageNames {
		m.stageTimings[name] = &stageStats{}
	}
	return m
}

// latencyThresholds are the upper bounds for each bucket (in nanoseconds).
var latencyThresholds = [13]int64{
	int64(1 * time.Millisecond),
	int64(2 * time.Millisecond),
	int64(5 * time.Millisecond),
	int64(10 * time.Millisecond),
	int64(25 * time.Millisecond),
	int64(50 * time.Millisecond),
	int64(100 * time.Millisecond),
	int64(250 * time.Millisecond),
	int64(500 * time.Millisecond),
	int64(1 * time.Second),
	int64(2 * time.Second),
	int64(5 * time.Second),
	int64(10 * time.Second),
}

// RecordIngest records an ingest request with body size and optional error.
func (m *Metrics) RecordIngest(bytes int, err error) {
	m.IngestRequests.Add(1)
	m.IngestBytes.Add(int64(bytes))
	if err != nil {
		m.IngestErrors.Add(1)
	}
}

// RecordProcessing records event processing duration and outcome.
func (m *Metrics) RecordProcessing(duration time.Duration, isNew, isRegression bool) {
	m.EventsProcessed.Add(1)
	m.ProcessingTimeNs.Add(int64(duration))
	m.recordLatency(duration)
	m.RecordStage(StageProcessing, duration, nil)
	if isNew {
		m.GroupsCreated.Add(1)
	} else {
		m.GroupsUpdated.Add(1)
	}
	if isRegression {
		m.Regressions.Add(1)
	}
}

// RecordProcessingFailure records a failed processing attempt so timing
// snapshots do not silently exclude slow failures.
func (m *Metrics) RecordProcessingFailure(duration time.Duration, err error) {
	m.EventsFailed.Add(1)
	m.ProcessingTimeNs.Add(int64(duration))
	m.recordLatency(duration)
	m.RecordStage(StageProcessing, duration, err)
}

// RecordAlert records an alert being fired.
func (m *Metrics) RecordAlert() {
	m.AlertsFired.Add(1)
}

// RecordAlertDispatchQueued records a queued alert callback invocation.
func (m *Metrics) RecordAlertDispatchQueued() {
	m.AlertDispatchQueued.Add(1)
}

// RecordAlertDispatchDropped records an alert callback dropped under backpressure.
func (m *Metrics) RecordAlertDispatchDropped() {
	m.AlertDispatchDropped.Add(1)
}

// RecordDrop records an event that was dropped (queue full).
func (m *Metrics) RecordDrop() {
	m.EventsDropped.Add(1)
}

// RecordQueued increments the queued events counter.
func (m *Metrics) RecordQueued() {
	m.EventsQueued.Add(1)
}

// RecordStage records timing and optional errors for one named ingest stage.
func (m *Metrics) RecordStage(name string, duration time.Duration, err error) {
	if m == nil {
		return
	}
	stats := m.stageTimings[name]
	if stats == nil {
		return
	}
	stats.Count.Add(1)
	stats.TotalNs.Add(int64(duration))
	if err != nil {
		stats.Errors.Add(1)
	}
	recordLatencyBuckets(&stats.Buckets, duration)
}

func (m *Metrics) recordLatency(d time.Duration) {
	recordLatencyBuckets(&m.latencyBuckets, d)
}

func recordLatencyBuckets(buckets *[14]atomic.Int64, d time.Duration) {
	ns := int64(d)
	for i, threshold := range latencyThresholds {
		if ns < threshold {
			buckets[i].Add(1)
			return
		}
	}
	// >= 10s goes in the last bucket
	buckets[len(buckets)-1].Add(1)
}

// MetricsSnapshot is a JSON-serializable point-in-time view of all metrics.
type MetricsSnapshot struct {
	Ingest   IngestSnapshot           `json:"ingest"`
	Pipeline PipelineSnapshot         `json:"pipeline"`
	Groups   GroupsSnapshot           `json:"groups"`
	Alerts   AlertsSnapshot           `json:"alerts"`
	Latency  LatencySnapshot          `json:"latency"`
	Stages   map[string]StageSnapshot `json:"stages,omitempty"`
	Runtime  RuntimeSnapshot          `json:"runtime"`
	Uptime   string                   `json:"uptime"`
}

// IngestSnapshot holds ingest counters.
type IngestSnapshot struct {
	Requests int64 `json:"requests"`
	Errors   int64 `json:"errors"`
	Bytes    int64 `json:"bytes"`
}

// PipelineSnapshot holds pipeline counters.
type PipelineSnapshot struct {
	Queued          int64   `json:"queued"`
	Processed       int64   `json:"processed"`
	Errors          int64   `json:"errors"`
	Dropped         int64   `json:"dropped"`
	AvgProcessingMs float64 `json:"avg_processing_ms"`
}

// GroupsSnapshot holds group counters.
type GroupsSnapshot struct {
	Created     int64 `json:"created"`
	Updated     int64 `json:"updated"`
	Regressions int64 `json:"regressions"`
}

// AlertsSnapshot holds alert counters.
type AlertsSnapshot struct {
	Fired           int64 `json:"fired"`
	DispatchQueued  int64 `json:"dispatch_queued"`
	DispatchDropped int64 `json:"dispatch_dropped"`
}

// LatencySnapshot holds histogram bucket counts.
type LatencySnapshot struct {
	Buckets []LatencyBucket `json:"buckets"`
}

// LatencyBucket is one histogram bucket.
type LatencyBucket struct {
	LE    string `json:"le"`
	Count int64  `json:"count"`
}

type StageSnapshot struct {
	Count   int64           `json:"count"`
	Errors  int64           `json:"errors"`
	AvgMs   float64         `json:"avg_ms"`
	Buckets []LatencyBucket `json:"buckets"`
}

// RuntimeSnapshot holds Go runtime stats.
type RuntimeSnapshot struct {
	Goroutines  int     `json:"goroutines"`
	HeapAllocMB float64 `json:"heap_alloc_mb"`
	NumGC       uint32  `json:"num_gc"`
	GoVersion   string  `json:"go_version"`
}

// Snapshot returns a point-in-time copy of all metrics.
func (m *Metrics) Snapshot() MetricsSnapshot {
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	processed := m.EventsProcessed.Load()
	failed := m.EventsFailed.Load()
	attempts := processed + failed
	var avgMs float64
	if attempts > 0 {
		avgMs = float64(m.ProcessingTimeNs.Load()) / float64(attempts) / 1e6
	}

	bucketLabels := []string{
		"<1ms", "<2ms", "<5ms", "<10ms", "<25ms", "<50ms",
		"<100ms", "<250ms", "<500ms", "<1s", "<2s", "<5s", "<10s", ">=10s",
	}
	buckets := make([]LatencyBucket, len(m.latencyBuckets))
	for i := range m.latencyBuckets {
		buckets[i] = LatencyBucket{
			LE:    bucketLabels[i],
			Count: m.latencyBuckets[i].Load(),
		}
	}
	stageSnapshots := make(map[string]StageSnapshot, len(stageNames))
	for _, name := range stageNames {
		stats := m.stageTimings[name]
		if stats == nil {
			continue
		}
		stageBuckets := make([]LatencyBucket, len(stats.Buckets))
		for i := range stats.Buckets {
			stageBuckets[i] = LatencyBucket{
				LE:    bucketLabels[i],
				Count: stats.Buckets[i].Load(),
			}
		}
		count := stats.Count.Load()
		avgMs := 0.0
		if count > 0 {
			avgMs = float64(stats.TotalNs.Load()) / float64(count) / 1e6
		}
		stageSnapshots[name] = StageSnapshot{
			Count:   count,
			Errors:  stats.Errors.Load(),
			AvgMs:   avgMs,
			Buckets: stageBuckets,
		}
	}

	return MetricsSnapshot{
		Ingest: IngestSnapshot{
			Requests: m.IngestRequests.Load(),
			Errors:   m.IngestErrors.Load(),
			Bytes:    m.IngestBytes.Load(),
		},
		Pipeline: PipelineSnapshot{
			Queued:          m.EventsQueued.Load(),
			Processed:       processed,
			Errors:          failed,
			Dropped:         m.EventsDropped.Load(),
			AvgProcessingMs: avgMs,
		},
		Groups: GroupsSnapshot{
			Created:     m.GroupsCreated.Load(),
			Updated:     m.GroupsUpdated.Load(),
			Regressions: m.Regressions.Load(),
		},
		Alerts: AlertsSnapshot{
			Fired:           m.AlertsFired.Load(),
			DispatchQueued:  m.AlertDispatchQueued.Load(),
			DispatchDropped: m.AlertDispatchDropped.Load(),
		},
		Latency: LatencySnapshot{
			Buckets: buckets,
		},
		Stages: stageSnapshots,
		Runtime: RuntimeSnapshot{
			Goroutines:  runtime.NumGoroutine(),
			HeapAllocMB: float64(memStats.HeapAlloc) / 1024 / 1024,
			NumGC:       memStats.NumGC,
			GoVersion:   runtime.Version(),
		},
		Uptime: time.Since(m.StartedAt).Round(time.Second).String(),
	}
}
