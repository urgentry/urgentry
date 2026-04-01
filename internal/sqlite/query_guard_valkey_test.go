package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"urgentry/internal/auth"
)

func startQueryGuardMiniRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	srv, err := miniredis.Run()
	if err != nil {
		t.Fatalf("miniredis.Run: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestValkeyQueryGuardSharesQuotaAndRecoversAfterKeyExpiry(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC)
	redis := startQueryGuardMiniRedis(t)

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO query_guard_policies
			(organization_id, workload, max_cost_per_request, max_requests_per_window, max_cost_per_window, window_seconds)
		 VALUES ('org-1', ?, 200, 1, 200, 60)`,
		string(QueryWorkloadDiscover),
	); err != nil {
		t.Fatalf("seed query guard policy: %v", err)
	}

	guard, err := NewValkeyQueryGuardStore(db, "redis://"+redis.Addr())
	if err != nil {
		t.Fatalf("NewValkeyQueryGuardStore: %v", err)
	}
	t.Cleanup(func() {
		if err := guard.Close(); err != nil {
			t.Fatalf("Close guard: %v", err)
		}
	})
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
	if !first.Allowed {
		t.Fatalf("unexpected first decision: %+v", first)
	}

	second, err := guard.CheckAndRecord(ctx, req)
	if err != nil {
		t.Fatalf("CheckAndRecord second: %v", err)
	}
	if second.Allowed {
		t.Fatalf("unexpected second decision: %+v", second)
	}

	redis.FlushAll()
	third, err := guard.CheckAndRecord(ctx, req)
	if err != nil {
		t.Fatalf("CheckAndRecord third: %v", err)
	}
	if !third.Allowed {
		t.Fatalf("expected quota to recover after stale key removal, got %+v", third)
	}
}

func TestValkeyQueryGuardFailsClosedOnOutage(t *testing.T) {
	db := openStoreTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	redis := startQueryGuardMiniRedis(t)

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name, created_at) VALUES ('org-1', 'acme', 'Acme', ?)`, now.Format(time.RFC3339)); err != nil {
		t.Fatalf("seed org: %v", err)
	}

	guard, err := NewValkeyQueryGuardStore(db, "redis://"+redis.Addr())
	if err != nil {
		t.Fatalf("NewValkeyQueryGuardStore: %v", err)
	}
	t.Cleanup(func() {
		if err := guard.Close(); err != nil {
			t.Fatalf("Close guard: %v", err)
		}
	})
	redis.Close()

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
			Query:    "checkout",
			Scope:    "all",
		},
	})
	if err != nil {
		t.Fatalf("expected graceful degradation, got error: %v", err)
	}
	if !decision.Allowed {
		t.Fatal("expected degraded request to be allowed")
	}
	if !decision.Degraded {
		t.Fatal("expected decision to be marked as degraded")
	}
}
