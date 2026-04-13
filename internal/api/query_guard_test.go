package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

type queryGuardStub struct {
	decision sqlite.QueryGuardDecision
	err      error
}

func (s queryGuardStub) CheckAndRecord(context.Context, sqlite.QueryGuardRequest) (sqlite.QueryGuardDecision, error) {
	return s.decision, s.err
}

func TestEnforceQueryGuardFailureReturnsTypedAPIError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/0/organizations/test-org/discover/", nil)
	rec := httptest.NewRecorder()

	allowed := enforceQueryGuard(rec, req, queryGuardStub{
		err: errors.New("guard backend unavailable"),
	}, "test-org-id", "test-proj-id", sqlite.QueryEstimate{
		Workload: sqlite.QueryWorkloadDiscover,
		Limit:    50,
	})

	if allowed {
		t.Fatal("enforceQueryGuard = true, want false")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := decodeAPIError(t, rec.Result())
	if body.Code != "query_guard_failure" {
		t.Fatalf("error body = %+v, want query_guard_failure", body)
	}
}

func TestEnforceQueryGuardBlockedSetsRetryAfter(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/0/organizations/test-org/discover/", nil)
	rec := httptest.NewRecorder()

	allowed := enforceQueryGuard(rec, req, queryGuardStub{
		decision: sqlite.QueryGuardDecision{
			Allowed:    false,
			StatusCode: http.StatusTooManyRequests,
			Reason:     "rate limited",
			RetryAfter: 1500 * time.Millisecond,
		},
	}, "test-org-id", "test-proj-id", sqlite.QueryEstimate{
		Workload: sqlite.QueryWorkloadDiscover,
		Limit:    50,
	})

	if allowed {
		t.Fatal("enforceQueryGuard = true, want false")
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("Retry-After = %q, want 2", got)
	}
	body := decodeAPIError(t, rec.Result())
	if body.Code != "query_guard_blocked" {
		t.Fatalf("error body = %+v, want query_guard_blocked", body)
	}
}
