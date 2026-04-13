package metrics

import (
	"fmt"
	"testing"
	"time"
)

func TestMetrics_RecordIngest(t *testing.T) {
	m := New()

	m.RecordIngest(1024, nil)
	m.RecordIngest(512, nil)
	m.RecordIngest(256, fmt.Errorf("test error"))

	if m.IngestRequests.Load() != 3 {
		t.Errorf("IngestRequests = %d, want 3", m.IngestRequests.Load())
	}
	if m.IngestBytes.Load() != 1792 {
		t.Errorf("IngestBytes = %d, want 1792", m.IngestBytes.Load())
	}
	if m.IngestErrors.Load() != 1 {
		t.Errorf("IngestErrors = %d, want 1", m.IngestErrors.Load())
	}
}

func TestMetrics_RecordProcessing(t *testing.T) {
	m := New()

	m.RecordProcessing(500*time.Microsecond, true, false) // new group, <1ms bucket
	m.RecordProcessing(50*time.Millisecond, false, false) // update, <100ms bucket
	m.RecordProcessing(3*time.Millisecond, false, true)   // regression, <5ms bucket

	if m.EventsProcessed.Load() != 3 {
		t.Errorf("EventsProcessed = %d, want 3", m.EventsProcessed.Load())
	}
	if m.GroupsCreated.Load() != 1 {
		t.Errorf("GroupsCreated = %d, want 1", m.GroupsCreated.Load())
	}
	if m.GroupsUpdated.Load() != 2 {
		t.Errorf("GroupsUpdated = %d, want 2", m.GroupsUpdated.Load())
	}
	if m.Regressions.Load() != 1 {
		t.Errorf("Regressions = %d, want 1", m.Regressions.Load())
	}
}

func TestMetrics_Snapshot(t *testing.T) {
	m := New()
	m.RecordIngest(100, nil)
	m.RecordProcessing(5*time.Millisecond, true, false)
	m.RecordStage(StageEnqueue, 2*time.Millisecond, nil)
	m.RecordStage(StageQueueWait, 3*time.Millisecond, fmt.Errorf("queue saturated"))
	m.RecordAlert()
	m.RecordAlertDispatchQueued()
	m.RecordAlertDispatchDropped()

	snap := m.Snapshot()

	if snap.Ingest.Requests != 1 {
		t.Errorf("snap.Ingest.Requests = %d, want 1", snap.Ingest.Requests)
	}
	if snap.Pipeline.Processed != 1 {
		t.Errorf("snap.Pipeline.Processed = %d, want 1", snap.Pipeline.Processed)
	}
	if snap.Groups.Created != 1 {
		t.Errorf("snap.Groups.Created = %d, want 1", snap.Groups.Created)
	}
	if snap.Alerts.Fired != 1 {
		t.Errorf("snap.Alerts.Fired = %d, want 1", snap.Alerts.Fired)
	}
	if snap.Alerts.DispatchQueued != 1 {
		t.Errorf("snap.Alerts.DispatchQueued = %d, want 1", snap.Alerts.DispatchQueued)
	}
	if snap.Alerts.DispatchDropped != 1 {
		t.Errorf("snap.Alerts.DispatchDropped = %d, want 1", snap.Alerts.DispatchDropped)
	}
	if snap.Uptime == "" {
		t.Error("snap.Uptime should not be empty")
	}
	if snap.Runtime.Goroutines <= 0 {
		t.Errorf("snap.Runtime.Goroutines = %d, should be > 0", snap.Runtime.Goroutines)
	}
	if snap.Runtime.GoVersion == "" {
		t.Error("snap.Runtime.GoVersion should not be empty")
	}
	if len(snap.Latency.Buckets) != 14 {
		t.Errorf("snap.Latency.Buckets length = %d, want 14", len(snap.Latency.Buckets))
	}
	if snap.Stages[StageEnqueue].Count != 1 {
		t.Errorf("snap.Stages[%q].Count = %d, want 1", StageEnqueue, snap.Stages[StageEnqueue].Count)
	}
	if snap.Stages[StageQueueWait].Errors != 1 {
		t.Errorf("snap.Stages[%q].Errors = %d, want 1", StageQueueWait, snap.Stages[StageQueueWait].Errors)
	}
	if len(snap.Stages[StageProcessing].Buckets) != 14 {
		t.Errorf("snap.Stages[%q].Buckets length = %d, want 14", StageProcessing, len(snap.Stages[StageProcessing].Buckets))
	}
}

func TestMetrics_LatencyBuckets(t *testing.T) {
	m := New()

	// Record in different buckets
	m.RecordProcessing(500*time.Microsecond, true, false)  // <1ms
	m.RecordProcessing(1500*time.Microsecond, true, false) // <2ms
	m.RecordProcessing(15*time.Second, true, false)        // >=10s

	snap := m.Snapshot()

	// Bucket 0 (<1ms) should have 1
	if snap.Latency.Buckets[0].Count != 1 {
		t.Errorf("<1ms bucket = %d, want 1", snap.Latency.Buckets[0].Count)
	}
	// Bucket 1 (<2ms) should have 1
	if snap.Latency.Buckets[1].Count != 1 {
		t.Errorf("<2ms bucket = %d, want 1", snap.Latency.Buckets[1].Count)
	}
	// Last bucket (>=10s) should have 1
	if snap.Latency.Buckets[13].Count != 1 {
		t.Errorf(">=10s bucket = %d, want 1", snap.Latency.Buckets[13].Count)
	}
}
