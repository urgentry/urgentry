package api

import (
	"net/http"
	"testing"

	"urgentry/internal/httputil"
)

func TestAPIIssueAutofix_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	insertSQLiteGroup(t, db, "grp-api-autofix", "CheckoutError: card declined", "checkout.go in submit", "error", "unresolved")
	insertSQLiteEvent(t, db, "evt-api-autofix", "grp-api-autofix", "CheckoutError: card declined", "error")
	if _, err := db.Exec(`UPDATE groups SET last_event_id = 'evt-api-autofix' WHERE id = 'grp-api-autofix'`); err != nil {
		t.Fatalf("set last_event_id: %v", err)
	}

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	initial := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/issues/grp-api-autofix/autofix/", pat, nil)
	if initial.StatusCode != http.StatusOK {
		t.Fatalf("initial get status = %d, want 200", initial.StatusCode)
	}
	var initialBody map[string]any
	decodeBody(t, initial, &initialBody)
	if initialBody["autofix"] != nil {
		t.Fatalf("expected null autofix before POST, got %+v", initialBody["autofix"])
	}

	start := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/issues/grp-api-autofix/autofix/", pat, map[string]any{
		"event_id":             "evt-api-autofix",
		"instruction":          "Focus on the checkout path and avoid guessing.",
		"pr_to_comment_on_url": "https://github.com/acme/backend/pull/42",
		"stopping_point":       "open_pr",
	})
	if start.StatusCode != http.StatusAccepted {
		t.Fatalf("start status = %d, want 202", start.StatusCode)
	}
	var created startAutofixResponse
	decodeBody(t, start, &created)
	if created.RunID <= 0 {
		t.Fatalf("run_id = %d, want > 0", created.RunID)
	}

	get := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/issues/grp-api-autofix/autofix/", pat, nil)
	if get.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", get.StatusCode)
	}
	var body map[string]any
	decodeBody(t, get, &body)
	autofix, ok := body["autofix"].(map[string]any)
	if !ok {
		t.Fatalf("autofix = %#v, want object", body["autofix"])
	}
	if runID, ok := autofix["run_id"].(float64); !ok || int64(runID) != created.RunID {
		t.Fatalf("run_id = %#v, want %d", autofix["run_id"], created.RunID)
	}
	if autofix["status"] != "COMPLETED" {
		t.Fatalf("status = %#v, want COMPLETED", autofix["status"])
	}
	request, ok := autofix["request"].(map[string]any)
	if !ok || request["stopping_point"] != "open_pr" {
		t.Fatalf("request = %#v, want stopping_point open_pr", autofix["request"])
	}
	steps, ok := autofix["steps"].([]any)
	if !ok || len(steps) != 4 {
		t.Fatalf("steps = %#v, want 4 items", autofix["steps"])
	}
	pullRequest, ok := autofix["pull_request"].(map[string]any)
	if !ok || pullRequest["status"] != "SKIPPED" {
		t.Fatalf("pull_request = %#v, want SKIPPED", autofix["pull_request"])
	}

	invalid := authzJSONRequest(t, ts, http.MethodPost, "/api/0/organizations/test-org/issues/grp-api-autofix/autofix/", pat, map[string]any{
		"event_id": "evt-does-not-belong",
	})
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid event status = %d, want 400", invalid.StatusCode)
	}
	var invalidBody httputil.APIErrorBody
	decodeBody(t, invalid, &invalidBody)
	if invalidBody.Detail != "event_id must belong to the target issue." {
		t.Fatalf("unexpected invalid response: %+v", invalidBody)
	}
}
