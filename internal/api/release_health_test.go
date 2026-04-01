package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"urgentry/internal/sqlite"
)

func TestProjectReleaseHealthAndSessions_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	if err := releaseHealth.SaveSession(t.Context(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@1.2.3",
		DistinctID:  "user-1",
		Status:      "crashed",
		Errors:      1,
		StartedAt:   time.Now().UTC(),
		Quantity:    1,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession single: %v", err)
	}
	if err := releaseHealth.SaveSession(t.Context(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@1.2.3",
		Status:      "errored",
		Errors:      1,
		Quantity:    2,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession aggregate: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/health/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", resp.StatusCode)
	}
	var health ReleaseHealth
	decodeBody(t, resp, &health)
	if health.Version != "ios@1.2.3" {
		t.Fatalf("Version = %q, want ios@1.2.3", health.Version)
	}
	if health.SessionCount != 3 || health.CrashedSessions != 1 || health.ErroredSessions != 2 {
		t.Fatalf("health = %+v", health)
	}

	resp = authGet(t, ts, "/api/0/projects/test-org/test-project/releases/ios@1.2.3/sessions/?limit=10")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sessions status = %d, want 200", resp.StatusCode)
	}
	var sessions []ReleaseSession
	decodeBody(t, resp, &sessions)
	if len(sessions) != 2 {
		t.Fatalf("sessions len = %d, want 2", len(sessions))
	}
	if sessions[0].Quantity == 0 {
		t.Fatalf("expected quantity on release session response: %+v", sessions[0])
	}
}

func TestListReleases_SQLiteIncludesHealth(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)

	releaseHealth := sqlite.NewReleaseHealthStore(db)
	if err := releaseHealth.SaveSession(t.Context(), &sqlite.ReleaseSession{
		ProjectID:   "test-proj-id",
		Release:     "ios@9.9.9",
		Status:      "abnormal",
		Quantity:    1,
		DateCreated: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	ts := httptest.NewServer(NewRouter(sqliteAuthorizedDependencies(t, db, Dependencies{})))
	defer ts.Close()

	resp := authGet(t, ts, "/api/0/organizations/test-org/releases/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var releases []Release
	decodeBody(t, resp, &releases)
	for _, release := range releases {
		if release.Version != "ios@9.9.9" {
			continue
		}
		if release.SessionCount != 1 || release.AbnormalSessions != 1 {
			t.Fatalf("release = %+v", release)
		}
		return
	}

	t.Fatal("expected release with health metrics")
}
