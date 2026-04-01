package pipeline

import (
	"context"
	"fmt"
	"testing"
	"time"

	"urgentry/internal/store"
	"urgentry/internal/testutil"
)

const (
	asyncTestTimeout  = 5 * time.Second
	asyncPollInterval = 10 * time.Millisecond
)

func waitForPipelineCondition(t *testing.T, name string, fn func() error) {
	t.Helper()
	err := testutil.Wait(asyncTestTimeout, asyncPollInterval, func() (bool, error) {
		err := fn()
		return err == nil, err
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func waitForStoredEvents(t *testing.T, events store.EventStore, projectID string, want int) {
	t.Helper()
	waitForPipelineCondition(t, "wait for stored events", func() error {
		items, err := events.ListEvents(context.Background(), projectID, store.ListOpts{Limit: want + 10})
		if err != nil {
			return err
		}
		if len(items) != want {
			return fmt.Errorf("events = %d, want %d", len(items), want)
		}
		return nil
	})
}
