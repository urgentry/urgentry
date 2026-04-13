package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteWebErrorRendersHTMLForPageRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	rec := httptest.NewRecorder()

	writeWebInternal(rec, req, "Failed to load dashboard.")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q, want text/html; charset=utf-8", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<!DOCTYPE html>") {
		t.Fatalf("body = %q, want HTML document", body)
	}
	if !strings.Contains(body, "Failed to load dashboard.") {
		t.Fatalf("body = %q, want rendered message", body)
	}
}

func TestWriteWebErrorKeepsHTMXFailuresPlainText(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/issues/issue-1/assign", nil)
	req.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()

	writeWebBadRequest(rec, req, "Invalid action")

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Fatalf("body = %q, want plain-text error", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Invalid action") {
		t.Fatalf("body = %q, want error message", rec.Body.String())
	}
}

func TestDashboardPageUnavailableUsesHTMLErrorPage(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/", nil)
	rec := httptest.NewRecorder()

	h.dashboardPage(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Fatalf("body = %q, want HTML error page", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Web UI unavailable") {
		t.Fatalf("body = %q, want unavailable message", rec.Body.String())
	}
}
