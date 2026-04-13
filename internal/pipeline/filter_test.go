package pipeline

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"urgentry/internal/domain"
	"urgentry/internal/issue"
	"urgentry/internal/normalize"
	"urgentry/internal/store"
)

// memFilterStore is a test-only in-memory FilterStore.
type memFilterStore struct {
	filters []*domain.InboundFilter
}

func (m *memFilterStore) ListFilters(_ context.Context, _ string) ([]*domain.InboundFilter, error) {
	return m.filters, nil
}

func activeFilter(ft domain.FilterType, pattern string) *domain.InboundFilter {
	return &domain.InboundFilter{
		ID:        "test-" + string(ft),
		ProjectID: "proj-1",
		Type:      ft,
		Active:    true,
		Pattern:   pattern,
	}
}

func TestCheckFilters_NoFilters(t *testing.T) {
	evt := &normalize.Event{Message: "hello", Level: "error"}
	reason := checkFilters(context.Background(), &memFilterStore{}, "proj-1", evt)
	if reason != nil {
		t.Fatalf("expected nil reason, got %+v", reason)
	}
}

func TestCheckFilters_NilStore(t *testing.T) {
	evt := &normalize.Event{Message: "hello", Level: "error"}
	reason := checkFilters(context.Background(), nil, "proj-1", evt)
	if reason != nil {
		t.Fatalf("expected nil reason, got %+v", reason)
	}
}

func TestCheckFilters_BrowserExtension_StackFrame(t *testing.T) {
	inApp := true
	evt := &normalize.Event{
		Level: "error",
		Exception: &normalize.ExceptionList{
			Values: []normalize.Exception{{
				Type:  "TypeError",
				Value: "Cannot read property 'foo' of undefined",
				Stacktrace: &normalize.Stacktrace{
					Frames: []normalize.Frame{
						{Filename: "app.js", InApp: &inApp},
						{AbsPath: "chrome-extension://abcdef123456/content.js", Filename: "content.js"},
					},
				},
			}},
		},
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{activeFilter(domain.FilterLocalhost, "")}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason == nil {
		t.Fatal("expected event to be filtered by browser-extensions")
	}
	if reason.FilterID != "browser-extensions" {
		t.Fatalf("expected FilterID=browser-extensions, got %s", reason.FilterID)
	}
}

func TestCheckFilters_BrowserExtension_Message(t *testing.T) {
	evt := &normalize.Event{
		Level: "error",
		Exception: &normalize.ExceptionList{
			Values: []normalize.Exception{{
				Type:  "Error",
				Value: "Error in moz-extension://abc/background.js",
			}},
		},
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{activeFilter(domain.FilterLocalhost, "")}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason == nil {
		t.Fatal("expected event to be filtered by browser-extensions message")
	}
}

func TestCheckFilters_BrowserExtension_InactiveFilter(t *testing.T) {
	evt := &normalize.Event{
		Level: "error",
		Exception: &normalize.ExceptionList{
			Values: []normalize.Exception{{
				Value: "Error in chrome-extension://xyz/script.js",
			}},
		},
	}

	inactive := activeFilter(domain.FilterLocalhost, "")
	inactive.Active = false
	fs := &memFilterStore{filters: []*domain.InboundFilter{inactive}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason != nil {
		t.Fatal("inactive filter should not match")
	}
}

func TestCheckFilters_LegacyBrowser_IE(t *testing.T) {
	evt := &normalize.Event{
		Level: "error",
		Request: &normalize.Request{
			Headers: map[string]string{
				"User-Agent": "Mozilla/5.0 (compatible; MSIE 9.0; Windows NT 6.1; Trident/5.0)",
			},
		},
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{activeFilter(domain.FilterLegacyBrowser, "")}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason == nil {
		t.Fatal("expected event to be filtered as legacy browser")
	}
	if reason.FilterID != "legacy-browsers" {
		t.Fatalf("expected FilterID=legacy-browsers, got %s", reason.FilterID)
	}
}

func TestCheckFilters_LegacyBrowser_ModernChrome(t *testing.T) {
	evt := &normalize.Event{
		Level: "error",
		Request: &normalize.Request{
			Headers: map[string]string{
				"User-Agent": "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/120.0.0.0 Safari/537.36",
			},
		},
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{activeFilter(domain.FilterLegacyBrowser, "")}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason != nil {
		t.Fatalf("modern Chrome should not be filtered, got %+v", reason)
	}
}

func TestCheckFilters_WebCrawler(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want bool
	}{
		{"Googlebot", "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", true},
		{"Bingbot", "Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", true},
		{"Twitterbot", "Twitterbot/1.0", true},
		{"HeadlessChrome", "Mozilla/5.0 HeadlessChrome/120.0.0.0 Safari/537.36", true},
		{"Normal user", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120.0.0.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			evt := &normalize.Event{
				Level: "error",
				Request: &normalize.Request{
					Headers: map[string]string{"User-Agent": tt.ua},
				},
			}
			fs := &memFilterStore{filters: []*domain.InboundFilter{activeFilter(domain.FilterCrawler, "")}}
			reason := checkFilters(context.Background(), fs, "proj-1", evt)
			if tt.want && reason == nil {
				t.Fatalf("expected crawler to be filtered: %s", tt.ua)
			}
			if !tt.want && reason != nil {
				t.Fatalf("expected normal UA not to be filtered: %s", tt.ua)
			}
		})
	}
}

func TestCheckFilters_FilteredTransaction(t *testing.T) {
	evt := &normalize.Event{
		Type:        "transaction",
		Transaction: "/api/healthcheck",
		Level:       "info",
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{
		activeFilter(domain.FilterIPRange, "/api/health*,/internal/*"),
	}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason == nil {
		t.Fatal("expected transaction to be filtered")
	}
	if reason.FilterID != "filtered-transaction" {
		t.Fatalf("expected FilterID=filtered-transaction, got %s", reason.FilterID)
	}
}

func TestCheckFilters_FilteredTransaction_NoMatch(t *testing.T) {
	evt := &normalize.Event{
		Type:        "transaction",
		Transaction: "/api/users",
		Level:       "info",
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{
		activeFilter(domain.FilterIPRange, "/api/health*,/internal/*"),
	}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason != nil {
		t.Fatalf("expected transaction not to be filtered, got %+v", reason)
	}
}

func TestCheckFilters_FilteredTransaction_ErrorEvent(t *testing.T) {
	evt := &normalize.Event{
		Type:    "error",
		Message: "/api/healthcheck",
		Level:   "error",
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{
		activeFilter(domain.FilterIPRange, "/api/health*"),
	}}
	reason := checkFilters(context.Background(), fs, "proj-1", evt)
	if reason != nil {
		t.Fatal("filtered-transaction should not match error events")
	}
}

func TestPipeline_FilterDropsEvent(t *testing.T) {
	events := store.NewMemoryEventStore()
	groups := issue.NewMemoryGroupStore()
	blobs := store.NewMemoryBlobStore()

	proc := &issue.Processor{
		Events: events,
		Groups: groups,
		Blobs:  blobs,
	}

	fs := &memFilterStore{filters: []*domain.InboundFilter{
		activeFilter(domain.FilterCrawler, ""),
	}}

	p := New(proc, 100, 1)
	p.SetFilterStore(fs)

	ctx := context.Background()
	p.Start(ctx)

	// Enqueue an event from a web crawler -- should be filtered.
	crawlerPayload, _ := json.Marshal(map[string]any{
		"event_id":  "aaaa1111bbbb2222cccc3333dddd4444",
		"platform":  "javascript",
		"level":     "error",
		"message":   "some error from a bot",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"request": map[string]any{
			"headers": map[string]string{
				"User-Agent": "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)",
			},
		},
	})
	ok := p.Enqueue(Item{ProjectID: "proj-filter", RawEvent: crawlerPayload})
	if !ok {
		t.Fatal("Enqueue returned false")
	}

	// Also enqueue a normal event -- should be processed.
	normalPayload, _ := json.Marshal(map[string]any{
		"event_id":  "eeee5555ffff6666aaaa7777bbbb8888",
		"platform":  "go",
		"level":     "error",
		"message":   "normal error",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	ok = p.Enqueue(Item{ProjectID: "proj-filter", RawEvent: normalPayload})
	if !ok {
		t.Fatal("Enqueue returned false for normal event")
	}

	// Wait for the normal event to be stored.
	waitForStoredEvents(t, events, "proj-filter", 1)
	p.Stop()

	// Verify only 1 event stored (the normal one, not the crawler).
	evts, err := events.ListEvents(context.Background(), "proj-filter", store.ListOpts{Limit: 10})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(evts) != 1 {
		t.Fatalf("expected 1 event after filtering, got %d", len(evts))
	}
}
