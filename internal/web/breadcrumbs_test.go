package web

import (
	"testing"
	"time"
)

func TestParseBreadcrumbs_Basic(t *testing.T) {
	payload := `{
		"breadcrumbs": {
			"values": [
				{"timestamp": "2024-01-15T10:29:57Z", "category": "http", "message": "GET /api", "level": "info", "type": "http"},
				{"timestamp": "2024-01-15T10:29:59Z", "category": "navigation", "message": "/dashboard", "level": "info"},
				{"timestamp": "2024-01-15T10:30:00Z", "category": "console", "message": "error thrown", "level": "error", "data": {"logger": "console", "extra": "value"}}
			]
		}
	}`
	eventTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	bcs := parseBreadcrumbs([]byte(payload), eventTime)

	if len(bcs) != 3 {
		t.Fatalf("expected 3 breadcrumbs, got %d", len(bcs))
	}

	// First breadcrumb: 3s before.
	if bcs[0].Category != "http" {
		t.Errorf("bc[0].Category = %q, want %q", bcs[0].Category, "http")
	}
	if bcs[0].Message != "GET /api" {
		t.Errorf("bc[0].Message = %q, want %q", bcs[0].Message, "GET /api")
	}
	if bcs[0].Type != "http" {
		t.Errorf("bc[0].Type = %q, want %q", bcs[0].Type, "http")
	}
	if bcs[0].TimestampRel != "3.0s before" {
		t.Errorf("bc[0].TimestampRel = %q, want %q", bcs[0].TimestampRel, "3.0s before")
	}

	// Second breadcrumb: 1s before.
	if bcs[1].TimestampRel != "1.0s before" {
		t.Errorf("bc[1].TimestampRel = %q, want %q", bcs[1].TimestampRel, "1.0s before")
	}

	// Third breadcrumb: at event time.
	if bcs[2].TimestampRel != "at event time" {
		t.Errorf("bc[2].TimestampRel = %q, want %q", bcs[2].TimestampRel, "at event time")
	}
	if bcs[2].Level != "error" {
		t.Errorf("bc[2].Level = %q, want %q", bcs[2].Level, "error")
	}

	// Data map.
	if bcs[2].Data == nil {
		t.Fatal("bc[2].Data is nil, expected non-nil")
	}
	if bcs[2].Data["logger"] != "console" {
		t.Errorf("bc[2].Data[logger] = %q, want %q", bcs[2].Data["logger"], "console")
	}
}

func TestParseBreadcrumbs_FloatTimestamp(t *testing.T) {
	payload := `{
		"breadcrumbs": {
			"values": [
				{"timestamp": 1705314597.5, "category": "query", "message": "SELECT *", "level": "debug"}
			]
		}
	}`
	// 1705314597.5 = 2024-01-15 10:29:57.5 UTC
	eventTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	bcs := parseBreadcrumbs([]byte(payload), eventTime)

	if len(bcs) != 1 {
		t.Fatalf("expected 1 breadcrumb, got %d", len(bcs))
	}
	if bcs[0].TimestampRel != "2.5s before" {
		t.Errorf("bc[0].TimestampRel = %q, want %q", bcs[0].TimestampRel, "2.5s before")
	}
	if bcs[0].Level != "debug" {
		t.Errorf("bc[0].Level = %q, want %q", bcs[0].Level, "debug")
	}
}

func TestParseBreadcrumbs_DefaultLevel(t *testing.T) {
	payload := `{
		"breadcrumbs": {
			"values": [
				{"timestamp": "2024-01-15T10:30:00Z", "category": "ui", "message": "click"}
			]
		}
	}`
	bcs := parseBreadcrumbs([]byte(payload), time.Time{})
	if len(bcs) != 1 {
		t.Fatalf("expected 1 breadcrumb, got %d", len(bcs))
	}
	if bcs[0].Level != "info" {
		t.Errorf("bc[0].Level = %q, want default %q", bcs[0].Level, "info")
	}
	if bcs[0].Type != "default" {
		t.Errorf("bc[0].Type = %q, want default %q", bcs[0].Type, "default")
	}
	// No relative time when eventTime is zero.
	if bcs[0].TimestampRel != "" {
		t.Errorf("bc[0].TimestampRel = %q, want empty when eventTime is zero", bcs[0].TimestampRel)
	}
}

func TestParseBreadcrumbs_Empty(t *testing.T) {
	if bcs := parseBreadcrumbs(nil, time.Time{}); bcs != nil {
		t.Errorf("expected nil for nil input, got %v", bcs)
	}
	if bcs := parseBreadcrumbs([]byte(`{}`), time.Time{}); bcs != nil {
		t.Errorf("expected nil for empty JSON, got %v", bcs)
	}
	if bcs := parseBreadcrumbs([]byte(`not json`), time.Time{}); bcs != nil {
		t.Errorf("expected nil for invalid JSON, got %v", bcs)
	}
}

func TestRelativeTimeBefore(t *testing.T) {
	base := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	tests := []struct {
		name     string
		bcTime   time.Time
		expected string
	}{
		{"at event time", base, "at event time"},
		{"5ms before", base.Add(-5 * time.Millisecond), "at event time"},
		{"150ms before", base.Add(-150 * time.Millisecond), "150ms before"},
		{"2.3s before", base.Add(-2300 * time.Millisecond), "2.3s before"},
		{"15s before", base.Add(-15 * time.Second), "15s before"},
		{"1m 30s before", base.Add(-90 * time.Second), "1m 30s before"},
		{"5m before", base.Add(-5 * time.Minute), "5m before"},
		{"after event", base.Add(500 * time.Millisecond), "500ms after"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := relativeTimeBefore(tt.bcTime, base)
			if got != tt.expected {
				t.Errorf("relativeTimeBefore() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		d        time.Duration
		expected string
	}{
		{"milliseconds", 150 * time.Millisecond, "150ms"},
		{"sub-10s", 2300 * time.Millisecond, "2.3s"},
		{"above-10s", 15 * time.Second, "15s"},
		{"minutes+seconds", 90 * time.Second, "1m 30s"},
		{"exact minutes", 5 * time.Minute, "5m"},
		{"hours+minutes", 90 * time.Minute, "1h 30m"},
		{"exact hours", 2 * time.Hour, "2h"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatDuration(tt.d)
			if got != tt.expected {
				t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.expected)
			}
		})
	}
}

func TestBreadcrumbCategoryClass(t *testing.T) {
	tests := []struct {
		category string
		expected string
	}{
		{"navigation", "bc-cat-navigation"},
		{"http", "bc-cat-http"},
		{"console", "bc-cat-console"},
		{"ui", "bc-cat-ui"},
		{"ui.click", "bc-cat-ui"},
		{"ui.input", "bc-cat-ui"},
		{"user", "bc-cat-user"},
		{"unknown", "bc-cat-default"},
		{"", "bc-cat-default"},
	}

	for _, tt := range tests {
		t.Run(tt.category, func(t *testing.T) {
			got := breadcrumbCategoryClass(tt.category)
			if got != tt.expected {
				t.Errorf("breadcrumbCategoryClass(%q) = %q, want %q", tt.category, got, tt.expected)
			}
		})
	}
}

func TestParseBreadcrumbsWithTime(t *testing.T) {
	payload := `{
		"breadcrumbs": {
			"values": [
				{"timestamp": "2024-01-15T10:29:58Z", "category": "http", "message": "GET /api", "level": "info"}
			]
		}
	}`
	eventTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	bcs := parseBreadcrumbsWithTime(payload, eventTime)

	if len(bcs) != 1 {
		t.Fatalf("expected 1 breadcrumb, got %d", len(bcs))
	}
	if bcs[0].CategoryCSS != "bc-cat-http" {
		t.Errorf("bc[0].CategoryCSS = %q, want %q", bcs[0].CategoryCSS, "bc-cat-http")
	}
	if bcs[0].TimestampRel != "2.0s before" {
		t.Errorf("bc[0].TimestampRel = %q, want %q", bcs[0].TimestampRel, "2.0s before")
	}
}

func TestParseBreadcrumbsFromNormalized_BackCompat(t *testing.T) {
	// Verify the zero-arg-time wrapper still works (used by issue_detail.go).
	payload := `{
		"breadcrumbs": {
			"values": [
				{"timestamp": "2024-01-15T10:30:00Z", "category": "navigation", "message": "/home"}
			]
		}
	}`
	bcs := parseBreadcrumbsFromNormalized(payload)
	if len(bcs) != 1 {
		t.Fatalf("expected 1, got %d", len(bcs))
	}
	if bcs[0].Category != "navigation" {
		t.Errorf("Category = %q, want %q", bcs[0].Category, "navigation")
	}
	// No relative time when event time is zero.
	if bcs[0].TimestampRel != "" {
		t.Errorf("TimestampRel = %q, want empty", bcs[0].TimestampRel)
	}
}
