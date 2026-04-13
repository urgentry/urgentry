package telemetrybridge

import (
	"context"
	"testing"

	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestProfileReadStoreReadsProjectedProfiles(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	blobs := store.NewMemoryBlobStore()
	profiles := sqlite.NewProfileStore(source, blobs)
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.IOHeavy().Spec().WithIDs("evt-profile-a", "profile-a"))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.CPUHeavy().Spec().
		WithIDs("evt-profile-b", "profile-b").
		WithTrace("trace-b").
		WithRelease("backend@1.2.3"))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.SaveRead().Spec().
		WithIDs("evt-profile-c", "profile-c").
		WithTrace("trace-c").
		WithTransaction("GET /payments").
		WithRelease("backend@2.0.0"))

	bridge := openMigratedTelemetryTestDatabase(t)

	projector := NewProjector(source, bridge)
	if err := projector.SyncFamilies(ctx, Scope{OrganizationID: "org-1", ProjectID: "proj-1"}, FamilyProfiles); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	readStore := NewProfileReadStore(bridge, blobs)

	items, err := readStore.ListProfiles(ctx, "proj-1", 10)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(items) < 3 || items[0].ProfileID == "" {
		t.Fatalf("unexpected profiles: %+v", items)
	}

	record, err := readStore.GetProfile(ctx, "proj-1", "profile-a")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if record.Manifest.ProfileID != "profile-a" || len(record.TopFrames) == 0 || len(record.TopFunctions) == 0 {
		t.Fatalf("unexpected profile record: %+v", record)
	}

	byTrace, err := readStore.FindProfilesByTrace(ctx, "proj-1", "0123456789abcdef0123456789abcdef", 10)
	if err != nil {
		t.Fatalf("FindProfilesByTrace: %v", err)
	}
	if len(byTrace) == 0 || byTrace[0].ProfileID == "" {
		t.Fatalf("unexpected trace references: %+v", byTrace)
	}

	highlights, err := readStore.ListReleaseProfileHighlights(ctx, "proj-1", "backend@1.2.3", 10)
	if err != nil {
		t.Fatalf("ListReleaseProfileHighlights: %v", err)
	}
	if len(highlights) < 2 {
		t.Fatalf("unexpected release highlights: %+v", highlights)
	}

	related, err := readStore.FindRelatedProfile(ctx, "proj-1", "trace-c", "GET /payments", "backend@2.0.0")
	if err != nil {
		t.Fatalf("FindRelatedProfile by trace: %v", err)
	}
	if related == nil || related.ProfileID != "profile-c" {
		t.Fatalf("unexpected related profile by trace: %+v", related)
	}

	fallback, err := readStore.FindRelatedProfile(ctx, "proj-1", "", "GET /payments", "backend@2.0.0")
	if err != nil {
		t.Fatalf("FindRelatedProfile fallback: %v", err)
	}
	if fallback == nil || fallback.ProfileID != "profile-c" {
		t.Fatalf("unexpected fallback related profile: %+v", fallback)
	}

	topDown, err := readStore.QueryTopDown(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-a"})
	if err != nil {
		t.Fatalf("QueryTopDown: %v", err)
	}
	if topDown.TotalWeight == 0 {
		t.Fatalf("unexpected top-down tree: %+v", topDown)
	}

	bottomUp, err := readStore.QueryBottomUp(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-a"})
	if err != nil {
		t.Fatalf("QueryBottomUp: %v", err)
	}
	if bottomUp.TotalWeight == 0 {
		t.Fatalf("unexpected bottom-up tree: %+v", bottomUp)
	}

	flamegraph, err := readStore.QueryFlamegraph(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-a", MaxDepth: 2})
	if err != nil {
		t.Fatalf("QueryFlamegraph: %v", err)
	}
	if len(flamegraph.Root.Children) == 0 {
		t.Fatalf("unexpected flamegraph: %+v", flamegraph)
	}

	hotPath, err := readStore.QueryHotPath(ctx, "proj-1", store.ProfileQueryFilter{ProfileID: "profile-a"})
	if err != nil {
		t.Fatalf("QueryHotPath: %v", err)
	}
	if len(hotPath.Frames) == 0 {
		t.Fatalf("unexpected hot path: %+v", hotPath)
	}

	comparison, err := readStore.CompareProfiles(ctx, "proj-1", store.ProfileComparisonFilter{
		BaselineProfileID:  "profile-a",
		CandidateProfileID: "profile-b",
	})
	if err != nil {
		t.Fatalf("CompareProfiles: %v", err)
	}
	if comparison.BaselineProfileID != "profile-a" || comparison.CandidateProfileID != "profile-b" {
		t.Fatalf("unexpected comparison: %+v", comparison)
	}
}
