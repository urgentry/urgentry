package telemetrycolumnar

import (
	"strings"
	"testing"

	"urgentry/internal/sqlite"
)

func TestBuildLogQueryAppliesFilters(t *testing.T) {
	query, args := buildLogQuery("org-1", sqlite.IssueSearchQuery{
		Terms:       []string{"payment", "timeout"},
		Release:     "backend@1.2.3",
		Environment: "production",
		Level:       "ERROR",
	}, 250)

	if !strings.Contains(query, "telemetry_log_facts FINAL") {
		t.Fatalf("query = %q, want FINAL read from telemetry_log_facts", query)
	}
	if !strings.Contains(query, "release = ?") || !strings.Contains(query, "environment = ?") || !strings.Contains(query, "lower(level) = ?") {
		t.Fatalf("query = %q, want release/environment/level filters", query)
	}
	if strings.Count(query, "positionCaseInsensitive(search_text, ?) > 0") != 2 {
		t.Fatalf("query = %q, want one search clause per term", query)
	}
	if got, want := len(args), 7; got != want {
		t.Fatalf("len(args) = %d, want %d", got, want)
	}
	if got, want := args[len(args)-1], any(100); got != want {
		t.Fatalf("limit arg = %#v, want %#v", got, want)
	}
}
