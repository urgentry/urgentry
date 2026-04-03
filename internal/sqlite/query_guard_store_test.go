package sqlite

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/auth"
)

func TestQueryGuardStoreCheckAndRecord(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('org-1', ?, 200, 1, 200, 300)`,
		string(QueryWorkloadDiscover),
	); err != nil {
		t.Fatalf("seed query guard policy: %v", err)
	}

	guard := NewQueryGuardStore(db)
	guard.now = func() time.Time { return now }
	req := QueryGuardRequest{
		Principal: &auth.Principal{
			Kind:         auth.CredentialPAT,
			CredentialID: "pat-1",
			User:         &auth.User{ID: "user-1"},
		},
		OrganizationID: "org-1",
		RequestPath:    "/api/0/organizations/acme/discover/",
		RequestMethod:  "GET",
		Estimate: QueryEstimate{
			Workload: QueryWorkloadDiscover,
			Limit:    25,
			Query:    "checkout",
			Scope:    "all",
		},
	}

	first, err := guard.CheckAndRecord(ctx, req)
	if err != nil {
		t.Fatalf("CheckAndRecord first: %v", err)
	}
	if !first.Allowed || first.Cost <= 0 {
		t.Fatalf("unexpected first decision: %+v", first)
	}

	second, err := guard.CheckAndRecord(ctx, req)
	if err != nil {
		t.Fatalf("CheckAndRecord second: %v", err)
	}
	if second.Allowed || second.StatusCode != http.StatusTooManyRequests || second.RetryAfter <= 0 {
		t.Fatalf("unexpected second decision: %+v", second)
	}

	var auditCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM auth_audit_logs WHERE organization_id = 'org-1' AND action LIKE 'query.discover.%'`).Scan(&auditCount); err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if auditCount != 2 {
		t.Fatalf("auditCount = %d, want 2", auditCount)
	}
}

func TestQueryGuardStoreRejectsOversizedQuery(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	guard := NewQueryGuardStore(db)
	decision, err := guard.CheckAndRecord(ctx, QueryGuardRequest{
		Principal: &auth.Principal{
			Kind:         auth.CredentialSession,
			CredentialID: "session-1",
			User:         &auth.User{ID: "user-1"},
		},
		OrganizationID: "org-1",
		RequestPath:    "/api/0/organizations/acme/discover/",
		RequestMethod:  "GET",
		Estimate: QueryEstimate{
			Workload: QueryWorkloadDiscover,
			Limit:    25,
			Query:    strings.Repeat("a", 513),
		},
	})
	if err != nil {
		t.Fatalf("CheckAndRecord: %v", err)
	}
	if decision.Allowed || decision.StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected oversized-query decision: %+v", decision)
	}
}

func TestQueryGuardStoreChargesTokenHeavyQueries(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('org-1', ?, 300, 10, 3000, 300)`,
		string(QueryWorkloadDiscover),
	); err != nil {
		t.Fatalf("seed query guard policy: %v", err)
	}

	guard := NewQueryGuardStore(db)
	guard.now = func() time.Time { return now }
	decision, err := guard.CheckAndRecord(ctx, QueryGuardRequest{
		Principal: &auth.Principal{
			Kind:         auth.CredentialPAT,
			CredentialID: "pat-1",
			User:         &auth.User{ID: "user-1"},
		},
		OrganizationID: "org-1",
		RequestPath:    "/api/0/organizations/acme/discover/",
		RequestMethod:  "GET",
		Estimate: QueryEstimate{
			Workload: QueryWorkloadDiscover,
			Limit:    25,
			Query:    "a b c d e f g h i j k l m n o",
			Scope:    "all",
		},
	})
	if err != nil {
		t.Fatalf("CheckAndRecord: %v", err)
	}
	if decision.Allowed || decision.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("unexpected token-heavy decision: %+v", decision)
	}
}
