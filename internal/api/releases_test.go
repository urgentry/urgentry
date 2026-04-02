package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"urgentry/internal/sqlite"
)

func TestReleaseHandlersRequireCatalogOrganization(t *testing.T) {
	db := openTestSQLite(t)
	releases := sqlite.NewReleaseStore(db)

	tests := []struct {
		name    string
		method  string
		handler http.HandlerFunc
	}{
		{
			name:    "list deploys",
			method:  http.MethodGet,
			handler: handleListReleaseDeploys(testCatalog{}, releases, allowAllAuth),
		},
		{
			name:    "create deploy",
			method:  http.MethodPost,
			handler: handleCreateReleaseDeploy(testCatalog{}, releases, allowAllAuth),
		},
		{
			name:    "list commits",
			method:  http.MethodGet,
			handler: handleListReleaseCommits(testCatalog{}, releases, allowAllAuth),
		},
		{
			name:    "create commit",
			method:  http.MethodPost,
			handler: handleCreateReleaseCommit(testCatalog{}, releases, allowAllAuth),
		},
		{
			name:    "list suspects",
			method:  http.MethodGet,
			handler: handleListReleaseSuspects(testCatalog{}, releases, allowAllAuth),
		},
		{
			name:    "list commit files",
			method:  http.MethodGet,
			handler: handleListReleaseCommitFiles(testCatalog{}, releases, allowAllAuth),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "/api/0/organizations/missing/releases/backend@1.2.3/", nil)
			req.SetPathValue("org_slug", "missing")
			req.SetPathValue("version", "backend@1.2.3")

			rec := httptest.NewRecorder()
			tc.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
		})
	}
}
