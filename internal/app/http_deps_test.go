package app

import (
	"testing"

	"urgentry/internal/sqlite"
)

func TestNewHTTPDepsPassesFeedbackStoreToAPI(t *testing.T) {
	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	feedbackStore := sqlite.NewFeedbackStore(db)
	deps := newHTTPDeps(httpDepsInput{
		db:            db,
		dataDir:       t.TempDir(),
		feedbackStore: feedbackStore,
	})

	if deps.Ingest.FeedbackStore != feedbackStore {
		t.Fatalf("ingest feedback store = %p, want %p", deps.Ingest.FeedbackStore, feedbackStore)
	}
	if deps.API.FeedbackStore != feedbackStore {
		t.Fatalf("api feedback store = %p, want %p", deps.API.FeedbackStore, feedbackStore)
	}
}
