package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
)

func postFormNoRedirect(t *testing.T, client *http.Client, url string, form url.Values) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func TestAlertsPageCRUDAndOutbox(t *testing.T) {
	srv, db := setupTestServer(t)
	defer srv.Close()

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	createResp := postFormNoRedirect(t, client, srv.URL+"/alerts/", url.Values{
		"project_id":    {"test-proj"},
		"name":          {"Production regressions"},
		"status":        {"active"},
		"trigger":       {"slow_transaction"},
		"threshold_ms":  {"750"},
		"email_targets": {"dev@example.com, oncall@example.com"},
		"webhook_url":   {"https://example.com/hook"},
		"slack_url":     {"https://hooks.slack.test/services/T000/B000/XYZ"},
	})
	if createResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create status = %d, want 303", createResp.StatusCode)
	}
	createResp.Body.Close()

	var ruleID string
	if err := db.QueryRow(`SELECT id FROM alert_rules WHERE project_id = 'test-proj' LIMIT 1`).Scan(&ruleID); err != nil {
		t.Fatalf("lookup rule id: %v", err)
	}

	updateResp := postFormNoRedirect(t, client, srv.URL+"/alerts/"+ruleID+"/update", url.Values{
		"project_id":    {"test-proj"},
		"name":          {"Updated regressions"},
		"status":        {"disabled"},
		"trigger":       {"regression"},
		"threshold_ms":  {"900"},
		"email_targets": {"ops@example.com"},
		"webhook_url":   {"https://example.com/updated-hook"},
		"slack_url":     {"https://hooks.slack.test/services/T111/B111/ABC"},
	})
	if updateResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("update status = %d, want 303", updateResp.StatusCode)
	}
	updateResp.Body.Close()

	var updatedName, updatedStatus string
	if err := db.QueryRow(`SELECT name, status FROM alert_rules WHERE id = ?`, ruleID).Scan(&updatedName, &updatedStatus); err != nil {
		t.Fatalf("lookup updated rule: %v", err)
	}
	if updatedName != "Updated regressions" || updatedStatus != "disabled" {
		t.Fatalf("unexpected updated rule: name=%q status=%q", updatedName, updatedStatus)
	}

	deleteResp := postFormNoRedirect(t, client, srv.URL+"/alerts/"+ruleID+"/delete", url.Values{})
	if deleteResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete status = %d, want 303", deleteResp.StatusCode)
	}
	deleteResp.Body.Close()

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM alert_rules WHERE id = ?`, ruleID).Scan(&count); err != nil {
		t.Fatalf("count deleted rules: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rule to be deleted, still have %d rows", count)
	}

	deliveries := sqlite.NewNotificationDeliveryStore(db)
	if err := deliveries.RecordDelivery(context.Background(), &notify.DeliveryRecord{
		ProjectID: "test-proj",
		RuleID:    "rule-1",
		Kind:      notify.DeliveryKindWebhook,
		Target:    "https://example.com/hook",
		Status:    notify.DeliveryStatusQueued,
		Attempts:  1,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}

	pageResp, err := http.Get(srv.URL + "/alerts/")
	if err != nil {
		t.Fatalf("GET /alerts/: %v", err)
	}
	body := getBody(t, pageResp)
	if pageResp.StatusCode != http.StatusOK {
		t.Fatalf("alerts page status = %d, want 200", pageResp.StatusCode)
	}
	if !strings.Contains(body, "Notification Deliveries") || !strings.Contains(body, "https://example.com/hook") {
		t.Fatalf("alerts page did not render notification delivery record: %s", body)
	}
}
