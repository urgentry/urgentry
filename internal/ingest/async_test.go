package ingest

import (
	"database/sql"
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

func waitForIngestCondition(t *testing.T, name string, fn func() error) {
	t.Helper()
	err := testutil.Wait(asyncTestTimeout, asyncPollInterval, func() (bool, error) {
		err := fn()
		return err == nil, err
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func waitForTransactionCount(t *testing.T, db *sql.DB, projectID string, want int) {
	t.Helper()
	waitForIngestCondition(t, "wait for transaction persistence", func() error {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE project_id = ?`, projectID).Scan(&count); err != nil {
			return err
		}
		if count != want {
			return fmt.Errorf("transactions = %d, want %d", count, want)
		}
		return nil
	})
}

func waitForStoredEvent(t *testing.T, events store.EventStore, projectID string, check func(*store.StoredEvent) error) {
	t.Helper()
	waitForIngestCondition(t, "wait for stored event", func() error {
		items, err := events.ListEvents(t.Context(), projectID, store.ListOpts{Limit: 10})
		if err != nil {
			return err
		}
		if len(items) != 1 {
			return fmt.Errorf("events = %d, want 1", len(items))
		}
		return check(items[0])
	})
}
