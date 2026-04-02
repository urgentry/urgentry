package api

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

func TestAPIPreprodArtifacts_SQLite(t *testing.T) {
	db := openTestSQLite(t)
	seedSQLiteAuth(t, db)
	seedSQLitePreprodArtifacts(t, db)

	ts, pat := newSQLiteAuthorizedServer(t, db, Dependencies{})
	defer ts.Close()

	install := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/preprodartifacts/artifact-head/install-details/", pat, nil)
	if install.StatusCode != http.StatusOK {
		t.Fatalf("install-details status = %d, want 200", install.StatusCode)
	}
	var installResp preprodArtifactResponse
	decodeBody(t, install, &installResp)
	if installResp.BuildID != "build-42" || installResp.ProjectID != "test-proj-id" || installResp.ProjectSlug != "test-project" {
		t.Fatalf("unexpected install-details response: %+v", installResp)
	}
	if installResp.InstallURL == nil || *installResp.InstallURL != "https://example.invalid/install/build-42.plist" {
		t.Fatalf("unexpected install url: %+v", installResp.InstallURL)
	}

	size := authzJSONRequest(t, ts, http.MethodGet, "/api/0/organizations/test-org/preprodartifacts/artifact-head/size-analysis/", pat, nil)
	if size.StatusCode != http.StatusOK {
		t.Fatalf("size-analysis status = %d, want 200", size.StatusCode)
	}
	var sizeResp preprodSizeAnalysisResponse
	decodeBody(t, size, &sizeResp)
	if sizeResp.State != "COMPLETED" || sizeResp.BaseBuildID == nil || *sizeResp.BaseBuildID != "build-41" {
		t.Fatalf("unexpected size-analysis response: %+v", sizeResp)
	}
	if len(sizeResp.Comparisons) != 1 || sizeResp.Comparisons[0].SizeMetricDiff == nil {
		t.Fatalf("expected one size comparison, got %+v", sizeResp.Comparisons)
	}
	if sizeResp.Comparisons[0].SizeMetricDiff.HeadDownloadSize != 120 || sizeResp.Comparisons[0].SizeMetricDiff.BaseDownloadSize != 100 {
		t.Fatalf("unexpected size diff: %+v", sizeResp.Comparisons[0].SizeMetricDiff)
	}

	latest := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/preprodartifacts/build-distribution/latest/?appId=com.example.app&platform=apple&buildVersion=1.0.0&buildNumber=41", pat, nil)
	if latest.StatusCode != http.StatusOK {
		t.Fatalf("latest build status = %d, want 200", latest.StatusCode)
	}
	var latestResp preprodLatestResponse
	decodeBody(t, latest, &latestResp)
	if latestResp.LatestArtifact == nil || latestResp.LatestArtifact.BuildID != "build-42" {
		t.Fatalf("unexpected latest artifact: %+v", latestResp.LatestArtifact)
	}
	if latestResp.CurrentArtifact == nil || latestResp.CurrentArtifact.BuildID != "build-41" {
		t.Fatalf("unexpected current artifact: %+v", latestResp.CurrentArtifact)
	}

	invalid := authzJSONRequest(t, ts, http.MethodGet, "/api/0/projects/test-org/test-project/preprodartifacts/build-distribution/latest/?platform=apple", pat, nil)
	if invalid.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid query status = %d, want 400", invalid.StatusCode)
	}
	var invalidResp httputil.APIErrorBody
	decodeBody(t, invalid, &invalidResp)
	if !strings.Contains(invalidResp.Detail, "appId") {
		t.Fatalf("unexpected invalid response: %+v", invalidResp)
	}
}

func seedSQLitePreprodArtifacts(t *testing.T, db *sql.DB) {
	t.Helper()

	store := sqlite.NewPreprodArtifactStore(db)
	ctx := t.Context()

	build41 := 41
	build42 := 42
	prNumber := 108
	trueValue := true
	download100 := int64(100)
	install200 := int64(200)
	download120 := int64(120)
	install230 := int64(230)
	duration := 12.5
	baseAdded := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	headAdded := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)

	for _, artifact := range []*sqlite.PreprodArtifact{
		{
			ID:             "artifact-base",
			OrganizationID: "test-org-id",
			ProjectID:      "test-proj-id",
			BuildID:        "build-41",
			State:          "PROCESSED",
			AppInfo: sqlite.PreprodArtifactAppInfo{
				AppID:        "com.example.app",
				Name:         "Example App",
				Version:      "1.0.0",
				BuildNumber:  &build41,
				ArtifactType: "XCARCHIVE",
				DateAdded:    baseAdded,
				DateBuilt:    baseAdded,
			},
			Platform:             "APPLE",
			BuildConfiguration:   "release",
			IsInstallable:        true,
			InstallURL:           "https://example.invalid/install/build-41.plist",
			DownloadCount:        2,
			ReleaseNotes:         "Initial QA build",
			InstallGroups:        []string{"qa"},
			IsCodeSignatureValid: &trueValue,
			ProfileName:          "iOS Team Provisioning Profile",
			CodesigningType:      "development",
			MainBinaryIdentifier: "macho-base",
			AnalysisState:        "COMPLETED",
			DownloadSize:         &download100,
			InstallSize:          &install200,
			AnalysisDuration:     &duration,
			AnalysisVersion:      "1.0.0",
		},
		{
			ID:             "artifact-head",
			OrganizationID: "test-org-id",
			ProjectID:      "test-proj-id",
			BuildID:        "build-42",
			State:          "PROCESSED",
			AppInfo: sqlite.PreprodArtifactAppInfo{
				AppID:        "com.example.app",
				Name:         "Example App",
				Version:      "1.1.0",
				BuildNumber:  &build42,
				ArtifactType: "XCARCHIVE",
				DateAdded:    headAdded,
				DateBuilt:    headAdded,
			},
			GitInfo: &sqlite.PreprodArtifactGitInfo{
				HeadSHA:      "abc123",
				BaseSHA:      "def456",
				Provider:     "github",
				HeadRepoName: "acme/mobile",
				BaseRepoName: "acme/mobile",
				HeadRef:      "feature/mobile-builds",
				BaseRef:      "main",
				PRNumber:     &prNumber,
			},
			Platform:              "APPLE",
			BuildConfiguration:    "release",
			IsInstallable:         true,
			InstallURL:            "https://example.invalid/install/build-42.plist",
			DownloadCount:         5,
			ReleaseNotes:          "Release candidate",
			InstallGroups:         []string{"qa", "beta"},
			IsCodeSignatureValid:  &trueValue,
			ProfileName:           "iOS Team Provisioning Profile",
			CodesigningType:       "development",
			MainBinaryIdentifier:  "macho-head",
			DefaultBaseArtifactID: "artifact-base",
			AnalysisState:         "COMPLETED",
			DownloadSize:          &download120,
			InstallSize:           &install230,
			AnalysisDuration:      &duration,
			AnalysisVersion:       "1.0.0",
			Insights: map[string]any{
				"assetSavings": 14,
			},
			AppComponents: []sqlite.PreprodArtifactComponent{
				{
					ComponentType: "APP",
					Name:          "Example App",
					AppID:         "com.example.app",
					Path:          "Payload/Example.app/Example",
					DownloadSize:  120,
					InstallSize:   230,
				},
			},
		},
	} {
		if err := store.Save(ctx, artifact); err != nil {
			t.Fatalf("save preprod artifact %s: %v", artifact.ID, err)
		}
	}
}
