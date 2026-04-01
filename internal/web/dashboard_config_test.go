package web

import (
	"encoding/json"
	"strings"
	"testing"

	"urgentry/internal/discover"
)

func TestDashboardQueryWithFiltersSkipsUnsupportedFields(t *testing.T) {
	query := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: discover.DatasetIssues,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: "test-org",
		},
	}
	cfg := dashboardPresentationConfig{
		Filters: dashboardFilterConfig{
			Environment: "production",
			Release:     "1.2.4",
			Transaction: "checkout",
		},
	}

	filtered := dashboardQueryWithFilters(query, cfg)
	if filtered.Where == nil {
		t.Fatal("expected dashboard filters to produce a predicate")
	}
	if got := dashboardQueryEstimateText(filtered); got == "" {
		t.Fatal("expected filtered query estimate text")
	}
	raw, err := json.Marshal(filtered.Where)
	if err != nil {
		t.Fatalf("marshal predicate: %v", err)
	}
	text := string(raw)
	if !containsAll(text, "environment", "release") {
		t.Fatalf("expected supported filters in predicate: %s", text)
	}
	if containsAll(text, "transaction") {
		t.Fatalf("did not expect unsupported transaction filter in issues predicate: %s", text)
	}
}

func containsAll(text string, items ...string) bool {
	for _, item := range items {
		if item == "" {
			continue
		}
		if !strings.Contains(text, item) {
			return false
		}
	}
	return true
}
