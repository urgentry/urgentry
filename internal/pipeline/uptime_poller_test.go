package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

func TestUptimePollerRejectsPrivateTargetWithoutDialing(t *testing.T) {
	db := openStoreTestDB(t)
	store := sqlite.NewUptimeMonitorStore(db)
	monitor, err := store.CreateUptimeMonitor(context.Background(), &sqlite.UptimeMonitor{
		ProjectID: "proj-1",
		Name:      "private",
		URL:       "http://127.0.0.1/internal",
	})
	if err != nil {
		t.Fatalf("CreateUptimeMonitor: %v", err)
	}

	poller := NewUptimePoller(store, nil)
	requests := 0
	poller.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected outbound request to %s", r.URL.String())
	})}

	if err := poller.PollDue(context.Background(), time.Now().UTC()); err != nil {
		t.Fatalf("PollDue: %v", err)
	}
	if requests != 0 {
		t.Fatalf("requests = %d, want 0", requests)
	}

	results, err := store.ListCheckResults(context.Background(), monitor.ID, 10)
	if err != nil {
		t.Fatalf("ListCheckResults: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	if results[0].Status != "error" {
		t.Fatalf("status = %q, want error", results[0].Status)
	}
	if !strings.Contains(results[0].Error, "disallowed target") {
		t.Fatalf("error = %q, want disallowed target", results[0].Error)
	}
}
