package pipeline

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func setupThrottleDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE projects (
		id TEXT PRIMARY KEY,
		spike_protection INTEGER NOT NULL DEFAULT 0
	)`)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestSpikeThrottle_AllowsWhenDisabled(t *testing.T) {
	db := setupThrottleDB(t)
	_, _ = db.Exec(`INSERT INTO projects (id, spike_protection) VALUES ('proj-1', 0)`)

	st := NewSpikeThrottle(db)
	defer st.Stop()

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		if !st.Allow(ctx, "proj-1") {
			t.Fatal("should allow all events when spike protection is disabled")
		}
	}
}

func TestSpikeThrottle_AllowsNilThrottle(t *testing.T) {
	var st *SpikeThrottle
	if !st.Allow(context.Background(), "proj-1") {
		t.Fatal("nil throttle should allow")
	}
}

func TestSpikeThrottle_AllowsNilDB(t *testing.T) {
	st := &SpikeThrottle{
		now:    time.Now,
		stopCh: make(chan struct{}),
	}
	defer close(st.stopCh)

	if !st.Allow(context.Background(), "proj-1") {
		t.Fatal("nil db throttle should allow")
	}
}

func TestSpikeThrottle_ThrottlesSpike(t *testing.T) {
	db := setupThrottleDB(t)
	_, _ = db.Exec(`INSERT INTO projects (id, spike_protection) VALUES ('proj-1', 1)`)

	var mu sync.Mutex
	fakeMinute := int64(1000)
	st := NewSpikeThrottle(db)
	st.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Unix(fakeMinute*60, 0)
	}
	defer st.Stop()

	ctx := context.Background()

	// Establish a baseline: 20 events per minute for 10 minutes.
	for min := 0; min < baselineWindow; min++ {
		mu.Lock()
		fakeMinute = int64(1000 + min)
		mu.Unlock()
		for i := 0; i < 20; i++ {
			if !st.Allow(ctx, "proj-1") {
				t.Fatalf("minute %d, event %d: should allow during baseline", min, i)
			}
		}
	}

	// Now advance to a new minute and send a spike (>2x baseline=20, so >40).
	mu.Lock()
	fakeMinute = int64(1000 + baselineWindow)
	mu.Unlock()

	allowed := 0
	rejected := 0
	for i := 0; i < 60; i++ {
		if st.Allow(ctx, "proj-1") {
			allowed++
		} else {
			rejected++
		}
	}

	if rejected == 0 {
		t.Fatal("expected some events to be rejected during spike")
	}
	// The threshold is 2x baseline (20) = 40. So up to 40 should be allowed,
	// and the rest rejected. The 41st event pushes count to 41 which > 40.
	if allowed > 41 {
		t.Errorf("expected at most ~41 allowed events, got %d", allowed)
	}
	if allowed < 30 {
		t.Errorf("expected at least 30 allowed events (2x baseline), got %d", allowed)
	}
}

func TestSpikeThrottle_AllowsLowTraffic(t *testing.T) {
	db := setupThrottleDB(t)
	_, _ = db.Exec(`INSERT INTO projects (id, spike_protection) VALUES ('proj-1', 1)`)

	var mu sync.Mutex
	fakeMinute := int64(1000)
	st := NewSpikeThrottle(db)
	st.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Unix(fakeMinute*60, 0)
	}
	defer st.Stop()

	ctx := context.Background()

	// Establish a low baseline: 3 events per minute (below minBaselineCount).
	for min := 0; min < baselineWindow; min++ {
		mu.Lock()
		fakeMinute = int64(1000 + min)
		mu.Unlock()
		for i := 0; i < 3; i++ {
			st.Allow(ctx, "proj-1")
		}
	}

	// Spike to 30 events — should still be allowed because baseline is below min.
	mu.Lock()
	fakeMinute = int64(1000 + baselineWindow)
	mu.Unlock()

	for i := 0; i < 30; i++ {
		if !st.Allow(ctx, "proj-1") {
			t.Fatalf("event %d: should allow when baseline is below min threshold", i)
		}
	}
}

func TestSpikeThrottle_UnknownProjectAllowed(t *testing.T) {
	db := setupThrottleDB(t)
	// No project inserted.

	st := NewSpikeThrottle(db)
	defer st.Stop()

	for i := 0; i < 50; i++ {
		if !st.Allow(context.Background(), "unknown-project") {
			t.Fatal("unknown project should always be allowed")
		}
	}
}

func TestSpikeThrottle_CachesEnabledFlag(t *testing.T) {
	db := setupThrottleDB(t)
	_, _ = db.Exec(`INSERT INTO projects (id, spike_protection) VALUES ('proj-1', 0)`)

	st := NewSpikeThrottle(db)
	defer st.Stop()

	ctx := context.Background()

	// First call populates cache.
	st.Allow(ctx, "proj-1")

	// Enable spike protection in the DB.
	_, _ = db.Exec(`UPDATE projects SET spike_protection = 1 WHERE id = 'proj-1'`)

	// Should still use cached value (disabled).
	if !st.Allow(ctx, "proj-1") {
		t.Fatal("should still allow with cached disabled value")
	}

	// Expire the cache.
	st.enabledCache.Delete("proj-1")

	// Now it should see the enabled flag. But with no baseline it won't
	// throttle yet — just verify the cache was refreshed.
	st.Allow(ctx, "proj-1")
	v, ok := st.enabledCache.Load("proj-1")
	if !ok {
		t.Fatal("expected cache entry after refresh")
	}
	if !v.(*enabledEntry).enabled {
		t.Fatal("expected enabled=true after DB update and cache refresh")
	}
}
