package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urgentry/internal/issue"
	"urgentry/internal/pipeline"
	"urgentry/internal/store"
)

func TestSecurityReportHandlerAcceptsCSP(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()
	processor := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}
	pipe := pipeline.New(processor, 10, 1)
	pipe.Start(context.Background())
	defer pipe.Stop()

	body := `{"csp-report":{"document-uri":"https://app.example.com/checkout","effective-directive":"script-src-elem","blocked-uri":"https://cdn.bad.test/app.js","disposition":"enforce"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/1/security/", strings.NewReader(body))
	rec := httptest.NewRecorder()
	SecurityReportHandler(pipe, nil).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	waitForStoredEvent(t, events, "1", func(item *store.StoredEvent) error {
		if !strings.Contains(item.Title, "CSP") {
			return fmt.Errorf("title = %q, want CSP summary", item.Title)
		}
		if !strings.Contains(item.Message, "script-src-elem") {
			return fmt.Errorf("message = %q, want directive detail", item.Message)
		}
		return nil
	})
}
