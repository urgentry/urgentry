package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	memorystore "urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func TestProfileStoreSaveAndRead(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	blobs := memorystore.NewMemoryBlobStore()
	profiles := NewProfileStore(db, blobs)

	fixture := profilefixtures.SaveRead()
	profileID := profilefixtures.Save(t, profiles, "proj-1", fixture.Spec().WithIDs("evt-profile-1", "profile-1"))
	if profileID != "profile-1" {
		t.Fatalf("profileID = %q, want profile-1", profileID)
	}

	items, err := profiles.ListProfiles(context.Background(), "proj-1", 10)
	if err != nil {
		t.Fatalf("ListProfiles: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(ListProfiles) = %d, want 1", len(items))
	}
	if items[0].SampleCount != fixture.Expected.SampleCount ||
		items[0].FrameCount != fixture.Expected.FrameCount ||
		items[0].FunctionCount != fixture.Expected.FunctionCount ||
		items[0].ThreadCount != fixture.Expected.ThreadCount ||
		items[0].StackCount != fixture.Expected.StackCount {
		t.Fatalf("unexpected manifest counts: %+v", items[0])
	}

	record, err := profiles.GetProfile(context.Background(), "proj-1", "profile-1")
	if err != nil {
		t.Fatalf("GetProfile: %v", err)
	}
	if record.Manifest.ProcessingStatus != memorystore.ProfileProcessingStatusCompleted {
		t.Fatalf("processing status = %q, want completed", record.Manifest.ProcessingStatus)
	}
	if len(record.RawPayload) == 0 {
		t.Fatal("expected raw payload")
	}
	if len(record.TopFrames) == 0 || record.TopFrames[0].Name != fixture.Expected.TopFrame || record.TopFrames[0].Count != fixture.Expected.SampleCount {
		t.Fatalf("unexpected top frames: %+v", record.TopFrames)
	}
	if len(record.TopFunctions) == 0 || record.TopFunctions[0].Name != fixture.Expected.TopFunction || record.TopFunctions[0].Count != fixture.Expected.SampleCount {
		t.Fatalf("unexpected top functions: %+v", record.TopFunctions)
	}
	if _, err := blobs.Get(context.Background(), record.Manifest.RawBlobKey); err != nil {
		t.Fatalf("Get raw blob: %v", err)
	}

	assertCount(t, db, `SELECT COUNT(*) FROM profile_threads WHERE manifest_id = '`+record.Manifest.ID+`'`, 2)
	assertCount(t, db, `SELECT COUNT(*) FROM profile_frames WHERE manifest_id = '`+record.Manifest.ID+`'`, 2)
	assertCount(t, db, `SELECT COUNT(*) FROM profile_stacks WHERE manifest_id = '`+record.Manifest.ID+`'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM profile_samples WHERE manifest_id = '`+record.Manifest.ID+`'`, 2)
}

func TestProfileStoreDuplicateAndMalformedProfiles(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	profiles := NewProfileStore(db, memorystore.NewMemoryBlobStore())

	valid := profilefixtures.SaveRead().Spec().WithIDs("evt-profile-dup", "profile-dup")
	profilefixtures.Save(t, profiles, "proj-1", valid)
	profilefixtures.Save(t, profiles, "proj-1", valid)
	record, err := profiles.GetProfile(context.Background(), "proj-1", "profile-dup")
	if err != nil {
		t.Fatalf("GetProfile duplicate: %v", err)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM profile_manifests WHERE project_id = 'proj-1' AND profile_id = 'profile-dup'`, 1)
	assertCount(t, db, `SELECT COUNT(*) FROM profile_samples WHERE manifest_id = '`+record.Manifest.ID+`'`, 2)

	conflict := valid.WithIDs("evt-profile-dup-2", "profile-dup").WithBody([]byte(`{
		"frames":[{"function":"other","filename":"other.go","lineno":9}],
		"samples":[{"frames":[0],"weight":3}]
	}`))
	if _, err := profiles.SaveEnvelopeProfile(context.Background(), "proj-1", conflict.Payload()); !errors.Is(err, errProfilePayloadConflict) {
		t.Fatalf("SaveEnvelopeProfile conflicting duplicate error = %v, want %v", err, errProfilePayloadConflict)
	}
	assertCount(t, db, `SELECT COUNT(*) FROM profile_manifests WHERE project_id = 'proj-1' AND profile_id = 'profile-dup'`, 1)

	malformed := profilefixtures.MalformedEmpty().Spec().WithIDs("evt-profile-bad", "profile-bad").WithTransaction("broken")
	profilefixtures.Save(t, profiles, "proj-1", malformed)
	bad, err := profiles.GetProfile(context.Background(), "proj-1", "profile-bad")
	if err != nil {
		t.Fatalf("GetProfile malformed: %v", err)
	}
	if bad.Manifest.ProcessingStatus != memorystore.ProfileProcessingStatusFailed {
		t.Fatalf("processing status = %q, want failed", bad.Manifest.ProcessingStatus)
	}
	if bad.Manifest.IngestError == "" {
		t.Fatal("expected ingest error for malformed profile")
	}
	assertCount(t, db, `SELECT COUNT(*) FROM profile_samples WHERE manifest_id = '`+bad.Manifest.ID+`'`, 0)

	invalidFrames := profilefixtures.InvalidFrames().Spec().WithIDs("evt-profile-invalid-frames", "profile-invalid-frames").WithTransaction("broken")
	profilefixtures.Save(t, profiles, "proj-1", invalidFrames)
	invalidGraph, err := profiles.GetProfile(context.Background(), "proj-1", "profile-invalid-frames")
	if err != nil {
		t.Fatalf("GetProfile invalid frames: %v", err)
	}
	if invalidGraph.Manifest.ProcessingStatus != memorystore.ProfileProcessingStatusFailed {
		t.Fatalf("invalid frames status = %q, want failed", invalidGraph.Manifest.ProcessingStatus)
	}
	if invalidGraph.Manifest.IngestError != "profile sample references invalid frames" {
		t.Fatalf("invalid frames ingest error = %q", invalidGraph.Manifest.IngestError)
	}

	invalidJSON := []byte(`{"event_id":"evt-profile-invalid","transaction":"broken",`)
	invalidID, err := profiles.SaveEnvelopeProfile(context.Background(), "proj-1", invalidJSON)
	if err != nil {
		t.Fatalf("SaveEnvelopeProfile invalid json: %v", err)
	}
	invalid, err := profiles.GetProfile(context.Background(), "proj-1", invalidID)
	if err != nil {
		t.Fatalf("GetProfile invalid json: %v", err)
	}
	if invalid.Manifest.ProcessingStatus != memorystore.ProfileProcessingStatusFailed {
		t.Fatalf("invalid json status = %q, want failed", invalid.Manifest.ProcessingStatus)
	}
	if !strings.Contains(invalid.Manifest.IngestError, "parse profile payload") {
		t.Fatalf("invalid json ingest error = %q, want parse message", invalid.Manifest.IngestError)
	}
	if _, err := profiles.blobs.Get(context.Background(), invalid.Manifest.RawBlobKey); err != nil {
		t.Fatalf("Get invalid raw blob: %v", err)
	}
}

func TestProfileStoreFixtureCorpus(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	profiles := NewProfileStore(db, memorystore.NewMemoryBlobStore())

	for _, fixture := range profilefixtures.Corpus() {
		spec := fixture.Spec()
		profilefixtures.Save(t, profiles, "proj-1", spec)

		record, err := profiles.GetProfile(context.Background(), "proj-1", spec.ProfileID)
		if err != nil {
			t.Fatalf("GetProfile %s: %v", fixture.Name, err)
		}
		if record.Manifest.SampleCount != fixture.Expected.SampleCount ||
			record.Manifest.FrameCount != fixture.Expected.FrameCount ||
			record.Manifest.FunctionCount != fixture.Expected.FunctionCount ||
			record.Manifest.ThreadCount != fixture.Expected.ThreadCount ||
			record.Manifest.StackCount != fixture.Expected.StackCount {
			t.Fatalf("%s manifest mismatch: %+v", fixture.Name, record.Manifest)
		}
		if len(record.TopFrames) == 0 || record.TopFrames[0].Name != fixture.Expected.TopFrame {
			t.Fatalf("%s top frames = %+v", fixture.Name, record.TopFrames)
		}
		if len(record.TopFunctions) == 0 || record.TopFunctions[0].Name != fixture.Expected.TopFunction {
			t.Fatalf("%s top functions = %+v", fixture.Name, record.TopFunctions)
		}
	}

	var mixedPython int
	if err := db.QueryRow(`SELECT COUNT(*) FROM profile_frames WHERE manifest_id = (SELECT id FROM profile_manifests WHERE profile_id = ?) AND module_name = 'orders.worker' AND package_name = 'orders.worker'`, profilefixtures.MixedLanguage().Spec().ProfileID).Scan(&mixedPython); err != nil {
		t.Fatalf("count mixed-language python frame: %v", err)
	}
	var mixedRust int
	if err := db.QueryRow(`SELECT COUNT(*) FROM profile_frames WHERE manifest_id = (SELECT id FROM profile_manifests WHERE profile_id = ?) AND module_name = 'payments_decoder' AND package_name = 'crate::decoder'`, profilefixtures.MixedLanguage().Spec().ProfileID).Scan(&mixedRust); err != nil {
		t.Fatalf("count mixed-language rust frame: %v", err)
	}
	if mixedPython != 1 || mixedRust != 1 {
		t.Fatalf("mixed-language modules were not preserved: python=%d rust=%d", mixedPython, mixedRust)
	}
}

func TestProfileStoreQueryViews(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	profiles := NewProfileStore(db, memorystore.NewMemoryBlobStore())

	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.IOHeavy().Spec().WithIDs("evt-profile-query-a", "profile-query-a"))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.CPUHeavy().Spec().WithIDs("evt-profile-query-b", "profile-query-b"))

	topDown, err := profiles.QueryTopDown(context.Background(), "proj-1", memorystore.ProfileQueryFilter{ProfileID: "profile-query-a"})
	if err != nil {
		t.Fatalf("QueryTopDown: %v", err)
	}
	if topDown.TotalWeight != 8 || len(topDown.Root.Children) != 1 || topDown.Root.Children[0].Name != "rootHandler @ app.go:1" {
		t.Fatalf("unexpected top-down root: %+v", topDown)
	}
	checkout := topDown.Root.Children[0].Children[0]
	if checkout.Name != "checkout @ checkout.go:41" || checkout.InclusiveWeight != 8 || len(checkout.Children) != 2 {
		t.Fatalf("unexpected checkout node: %+v", checkout)
	}
	if checkout.Children[0].Name != "dbQuery @ db.go:12" || checkout.Children[1].Name != "readReplica @ replica.go:8" {
		t.Fatalf("unexpected checkout children: %+v", checkout.Children)
	}

	bottomUp, err := profiles.QueryBottomUp(context.Background(), "proj-1", memorystore.ProfileQueryFilter{ProfileID: "profile-query-a"})
	if err != nil {
		t.Fatalf("QueryBottomUp: %v", err)
	}
	if bottomUp.TotalWeight != 8 || len(bottomUp.Root.Children) != 2 || bottomUp.Root.Children[0].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected bottom-up tree: %+v", bottomUp)
	}

	flamegraph, err := profiles.QueryFlamegraph(context.Background(), "proj-1", memorystore.ProfileQueryFilter{ProfileID: "profile-query-a", MaxDepth: 2})
	if err != nil {
		t.Fatalf("QueryFlamegraph: %v", err)
	}
	if !flamegraph.Truncated || len(flamegraph.Root.Children) != 1 || flamegraph.Root.Children[0].Name != "rootHandler @ app.go:1" {
		t.Fatalf("unexpected flamegraph response: %+v", flamegraph)
	}
	if len(flamegraph.Root.Children[0].Children) != 1 || flamegraph.Root.Children[0].Children[0].Name != "checkout @ checkout.go:41" {
		t.Fatalf("unexpected flamegraph call tree: %+v", flamegraph.Root.Children[0].Children)
	}

	hotPath, err := profiles.QueryHotPath(context.Background(), "proj-1", memorystore.ProfileQueryFilter{ProfileID: "profile-query-a"})
	if err != nil {
		t.Fatalf("QueryHotPath: %v", err)
	}
	if len(hotPath.Frames) != 3 || hotPath.Frames[2].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected hot path: %+v", hotPath)
	}

	comparison, err := profiles.CompareProfiles(context.Background(), "proj-1", memorystore.ProfileComparisonFilter{
		BaselineProfileID:  "profile-query-a",
		CandidateProfileID: "profile-query-b",
	})
	if err != nil {
		t.Fatalf("CompareProfiles: %v", err)
	}
	if comparison.DurationDeltaNS != 3000000 || len(comparison.TopRegressions) == 0 || comparison.TopRegressions[0].Name != "scoreRules" || comparison.TopRegressions[0].DeltaWeight != 7 {
		t.Fatalf("unexpected regressions: %+v", comparison)
	}
	if len(comparison.TopImprovements) == 0 || comparison.TopImprovements[0].Name != "dbQuery" || comparison.TopImprovements[0].DeltaWeight != -5 {
		t.Fatalf("unexpected improvements: %+v", comparison)
	}
	if comparison.Confidence != "low" {
		t.Fatalf("comparison confidence = %q, want low", comparison.Confidence)
	}
}

func TestProfileStoreLinkingViews(t *testing.T) {
	db := openStoreTestDB(t)
	seedProfileTestProject(t, db, "org-1", "proj-1")
	profiles := NewProfileStore(db, memorystore.NewMemoryBlobStore())

	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.DBHeavy().Spec().WithIDs("evt-link-profile-a", "profile-link-a").WithDuration(42000000))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.CPUHeavy().Spec().WithIDs("evt-link-profile-b", "profile-link-b").WithDuration(18000000))
	profilefixtures.Save(t, profiles, "proj-1", profilefixtures.SaveRead().Spec().
		WithIDs("evt-link-profile-c", "profile-link-c").
		WithTransaction("GET /payments").
		WithTrace("trace-other").
		WithRelease("backend@2.0.0").
		WithDuration(51000000).
		WithBody([]byte(`{"frames":[{"function":"payments","filename":"payments.go","lineno":9}],"samples":[{"frames":[0],"weight":5}]}`)))

	byTrace, err := profiles.FindProfilesByTrace(context.Background(), "proj-1", "0123456789abcdef0123456789abcdef", 10)
	if err != nil {
		t.Fatalf("FindProfilesByTrace: %v", err)
	}
	if len(byTrace) != 2 {
		t.Fatalf("unexpected trace profiles: %+v", byTrace)
	}
	foundTraceProfile := false
	for _, item := range byTrace {
		if item.ProfileID == "profile-link-a" && item.TopFunction == "dbQuery" {
			foundTraceProfile = true
		}
	}
	if !foundTraceProfile {
		t.Fatalf("missing expected trace-linked profile: %+v", byTrace)
	}

	highlights, err := profiles.ListReleaseProfileHighlights(context.Background(), "proj-1", "backend@1.2.3", 10)
	if err != nil {
		t.Fatalf("ListReleaseProfileHighlights: %v", err)
	}
	if len(highlights) != 2 || highlights[0].ProfileID != "profile-link-a" || highlights[0].TopFunction != "dbQuery" {
		t.Fatalf("unexpected release highlights: %+v", highlights)
	}

	related, err := profiles.FindRelatedProfile(context.Background(), "proj-1", "trace-other", "GET /payments", "backend@2.0.0")
	if err != nil {
		t.Fatalf("FindRelatedProfile: %v", err)
	}
	if related == nil || related.ProfileID != "profile-link-c" || related.TopFunction != "payments" {
		t.Fatalf("unexpected related profile: %+v", related)
	}
}

func seedProfileTestProject(t *testing.T, db *sql.DB, orgID, projectID string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES (?, 'test-org', 'Test Org')`, orgID); err != nil {
		t.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES (?, ?, 'test-project', 'Test Project')`, projectID, orgID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
}
