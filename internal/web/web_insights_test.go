package web

import (
	"net/http"
	"strings"
	"testing"
)

func TestExplorePage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/explore/")
	if err != nil {
		t.Fatalf("GET /explore/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Explore") {
		t.Fatalf("expected Explore heading, got %s", body)
	}
	if !strings.Contains(body, "/discover/") || !strings.Contains(body, "/logs/") || !strings.Contains(body, "/profiles/") {
		t.Fatalf("expected links to discover, logs, profiles in explore page, got %s", body)
	}
}

func TestInsightsHubPage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/insights/")
	if err != nil {
		t.Fatalf("GET /insights/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Insights") {
		t.Fatalf("expected Insights heading, got %s", body)
	}
	if !strings.Contains(body, "/insights/http/") || !strings.Contains(body, "/insights/database/") {
		t.Fatalf("expected links to http and database insights, got %s", body)
	}
}

func TestInsightsHTTPPage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/insights/http/")
	if err != nil {
		t.Fatalf("GET /insights/http/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "HTTP Performance") {
		t.Fatalf("expected HTTP Performance heading, got %s", body)
	}
}

func TestInsightsDatabasePage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/insights/database/")
	if err != nil {
		t.Fatalf("GET /insights/database/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Database") {
		t.Fatalf("expected Database heading, got %s", body)
	}
}

func TestPerformanceSummaryPage(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/performance/summary/?transaction=GET+/api/users")
	if err != nil {
		t.Fatalf("GET /performance/summary/: %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Transaction Summary") {
		t.Fatalf("expected Transaction Summary heading, got %s", body)
	}
	if !strings.Contains(body, "GET /api/users") {
		t.Fatalf("expected transaction name in page, got %s", body)
	}
}

func TestPerformanceSummaryPageMissingTransaction(t *testing.T) {
	srv, _ := setupTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/performance/summary/")
	if err != nil {
		t.Fatalf("GET /performance/summary/ (no param): %v", err)
	}
	body := getBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "Choose a transaction") {
		t.Fatalf("expected guided empty state, got %s", body)
	}
}
