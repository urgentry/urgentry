package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
)

func authDelete(t *testing.T, ts *httptest.Server, path string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func TestAlertCRUD_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	createBody := alert.Rule{
		Name:   "First Seen Alert",
		Status: "active",
		Conditions: []alert.Condition{{
			ID:   "sentry.rules.conditions.first_seen_event.FirstSeenEventCondition",
			Name: "First seen",
		}},
		Actions: []alert.Action{{
			Type:   "email",
			Target: "dev@example.com",
		}},
	}
	resp := authPost(t, ts, "/api/0/projects/test-org/test-project/alerts/", createBody)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var created alert.Rule
	decodeBody(t, resp, &created)
	if created.ID == "" || created.ProjectID == "" {
		t.Fatalf("unexpected created rule: %+v", created)
	}

	listResp := authGet(t, ts, "/api/0/projects/test-org/test-project/alerts/")
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("list status = %d, want 200", listResp.StatusCode)
	}
	var rules []alert.Rule
	decodeBody(t, listResp, &rules)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	created.Name = "Updated Alert"
	created.Status = "disabled"
	created.Actions = []alert.Action{
		{Type: alert.ActionTypeWebhook, Target: "https://example.com/hook"},
		{Type: alert.ActionTypeSlack, Target: "https://hooks.slack.test/services/T000/B000/XYZ"},
	}
	created.Conditions = []alert.Condition{
		alert.SlowTransactionCondition(900),
	}
	updateBody, _ := json.Marshal(created)
	req, err := http.NewRequest(http.MethodPut, ts.URL+"/api/0/projects/test-org/test-project/alerts/"+created.ID+"/", bytes.NewReader(updateBody))
	if err != nil {
		t.Fatalf("new update request: %v", err)
	}
	req.Header.Set("Authorization", testToken)
	req.Header.Set("Content-Type", "application/json")
	updateResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("update request failed: %v", err)
	}
	if updateResp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", updateResp.StatusCode)
	}
	updateResp.Body.Close()

	getResp := authGet(t, ts, "/api/0/projects/test-org/test-project/alerts/"+created.ID+"/")
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", getResp.StatusCode)
	}
	var updated alert.Rule
	decodeBody(t, getResp, &updated)
	if updated.Status != "disabled" || updated.Name != "Updated Alert" {
		t.Fatalf("unexpected updated rule: %+v", updated)
	}
	if len(updated.Actions) != 2 || updated.Actions[1].Type != alert.ActionTypeSlack {
		t.Fatalf("unexpected updated actions: %+v", updated.Actions)
	}
	if len(updated.Conditions) != 1 || updated.Conditions[0].ID != alert.ConditionSlowTransaction {
		t.Fatalf("unexpected updated conditions: %+v", updated.Conditions)
	}

	delResp := authDelete(t, ts, "/api/0/projects/test-org/test-project/alerts/"+created.ID+"/")
	if delResp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", delResp.StatusCode)
	}
	delResp.Body.Close()

	finalResp := authGet(t, ts, "/api/0/projects/test-org/test-project/alerts/")
	if finalResp.StatusCode != http.StatusOK {
		t.Fatalf("final list status = %d, want 200", finalResp.StatusCode)
	}
	decodeBody(t, finalResp, &rules)
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules after delete, got %d", len(rules))
	}
}

func TestAlertDeliveries_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	ts := newSQLiteTestServer(t, db)
	defer ts.Close()

	store := sqlite.NewNotificationDeliveryStore(db)
	if err := store.RecordDelivery(t.Context(), &notify.DeliveryRecord{
		ProjectID: "test-proj-id",
		Kind:      notify.DeliveryKindWebhook,
		Target:    "https://example.com/hook",
		Status:    notify.DeliveryStatusQueued,
		Attempts:  1,
		CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("RecordDelivery: %v", err)
	}

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/alerts/deliveries/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("deliveries status = %d, want 200", resp.StatusCode)
	}
	var deliveries []notify.DeliveryRecord
	decodeBody(t, resp, &deliveries)
	if len(deliveries) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(deliveries))
	}
	if deliveries[0].Kind != notify.DeliveryKindWebhook || deliveries[0].Target != "https://example.com/hook" {
		t.Fatalf("unexpected delivery: %+v", deliveries[0])
	}
}
