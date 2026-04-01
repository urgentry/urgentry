//go:build integration

package compat

import (
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// securityURL returns the security report endpoint URL with sentry_key query param.
func securityURL(srv *compatServer) string {
	return srv.server.URL + "/api/default-project/security/?sentry_key=" + srv.projectKey
}

// TestCSPReport sends a standard CSP violation report and verifies 200.
func TestCSPReport(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `{"csp-report":{"document-uri":"https://example.com","violated-directive":"script-src","blocked-uri":"https://evil.com","original-policy":"default-src 'self'"}}`
	resp := doRequest(t, http.MethodPost, securityURL(srv), strings.NewReader(body), map[string]string{
		"Content-Type": "application/json",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
}

// TestCSPReportCTJSON sends a CSP violation with Content-Type: application/csp-report.
func TestCSPReportCTJSON(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `{"csp-report":{"document-uri":"https://example.com/page","violated-directive":"style-src","blocked-uri":"https://evil.com/style.css","original-policy":"style-src 'self'"}}`
	resp := doRequest(t, http.MethodPost, securityURL(srv), strings.NewReader(body), map[string]string{
		"Content-Type": "application/csp-report",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
}

// TestCSPReportCTReportsAPI sends a Report-To format payload with Content-Type: application/reports+json.
func TestCSPReportCTReportsAPI(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `[{"type":"csp-violation","url":"https://example.com/app","body":{"documentURL":"https://example.com/app","effectiveDirective":"img-src","blockedURL":"https://tracker.example.com/pixel.gif","disposition":"enforce"}}]`
	resp := doRequest(t, http.MethodPost, securityURL(srv), strings.NewReader(body), map[string]string{
		"Content-Type": "application/reports+json",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
}

// TestExpectCTReport sends an Expect-CT report and verifies 200.
func TestExpectCTReport(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `{"type":"expect-ct","url":"https://example.com","body":{"hostname":"example.com","port":443,"effective-expiration-date":"2026-12-31T00:00:00Z","served-certificate-chain":["..."],"validated-certificate-chain":["..."],"scts":[]}}`
	resp := doRequest(t, http.MethodPost, securityURL(srv), strings.NewReader(body), map[string]string{
		"Content-Type": "application/json",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
}

// TestHPKPReport sends an HPKP report and verifies 200.
func TestHPKPReport(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `{"type":"hpkp","url":"https://example.com","body":{"hostname":"example.com","port":443,"noted-hostname":"example.com","include-subdomains":false,"effective-expiration-date":"2026-12-31T00:00:00Z","served-certificate-chain":["..."],"validated-certificate-chain":["..."],"known-pins":["pin-sha256=\"...\""],"date-time":"2026-01-01T00:00:00Z"}}`
	resp := doRequest(t, http.MethodPost, securityURL(srv), strings.NewReader(body), map[string]string{
		"Content-Type": "application/json",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}
}

// TestSecurityReportAuthRequired verifies that missing sentry_key returns 401.
func TestSecurityReportAuthRequired(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `{"csp-report":{"document-uri":"https://example.com","violated-directive":"script-src","blocked-uri":"https://evil.com","original-policy":"default-src 'self'"}}`

	// No sentry_key, no auth header.
	urlNoKey := srv.server.URL + "/api/default-project/security/"
	resp := doRequest(t, http.MethodPost, urlNoKey, strings.NewReader(body), map[string]string{
		"Content-Type": "application/json",
	})
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestSecurityReportCreatesEvent sends a CSP report and verifies it creates an
// error-level event visible through the stored events.
func TestSecurityReportCreatesEvent(t *testing.T) {
	srv := newCompatServer(t, compatOptions{startWorkers: true, ingestRateLimit: 60})
	defer srv.close()

	body := `{"csp-report":{"document-uri":"https://example.com/checkout","effective-directive":"script-src-elem","blocked-uri":"https://cdn.bad.test/app.js","disposition":"enforce"}}`
	resp := doRequest(t, http.MethodPost, securityURL(srv), strings.NewReader(body), map[string]string{
		"Content-Type": "application/csp-report",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200; body = %s", resp.StatusCode, respBody)
	}

	// Wait for the event to be processed and stored.
	waitForProjectEventCount(t, srv.db, "default-project", 1)

	// Verify the stored event contains CSP details.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var title sql.NullString
		err := srv.db.QueryRow(`SELECT title FROM events WHERE project_id = ? ORDER BY rowid DESC LIMIT 1`, "default-project").Scan(&title)
		if err == nil && title.Valid && strings.Contains(title.String, "CSP") {
			// Verify via the API as well.
			eventsResp := apiRequest(t, http.MethodGet, srv.server.URL+"/api/0/projects/urgentry-org/default/events/", srv.pat, nil, "")
			defer eventsResp.Body.Close()
			if eventsResp.StatusCode != http.StatusOK {
				t.Fatalf("events API status = %d, want 200", eventsResp.StatusCode)
			}
			var events []struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(eventsResp.Body).Decode(&events); err != nil {
				t.Fatalf("decode events: %v", err)
			}
			if len(events) == 0 {
				t.Fatal("no events returned from API")
			}
			if !strings.Contains(events[0].Title, "CSP") {
				t.Fatalf("event title = %q, want to contain CSP", events[0].Title)
			}
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("stored event with CSP title not found")
}
