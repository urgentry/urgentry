package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	memorystore "urgentry/internal/store"
	profilefixtures "urgentry/internal/testfixtures/profiles"
)

func BenchmarkProfileStoreSaveEnvelopeProfile(b *testing.B) {
	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedProfileTestProjectFromBench(b, db, "bench-org", "bench-proj")

	profiles := NewProfileStore(db, memorystore.NewMemoryBlobStore())
	payloads := make([][]byte, b.N)
	for i := 0; i < b.N; i++ {
		payloads[i] = profilefixtures.CPUHeavy().Spec().
			WithIDs(fmt.Sprintf("evt-bench-save-%04d", i), fmt.Sprintf("profile-bench-save-%04d", i)).
			WithTimestamp(time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Second)).
			Payload()
	}

	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := profiles.SaveEnvelopeProfile(ctx, "bench-proj", payloads[i]); err != nil {
			b.Fatalf("SaveEnvelopeProfile %d: %v", i, err)
		}
	}
}

func BenchmarkProfileStoreListProfiles(b *testing.B) {
	fx := newBenchmarkProfileStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := fx.store.ListProfiles(fx.ctx, "bench-proj", 25)
		if err != nil {
			b.Fatalf("ListProfiles: %v", err)
		}
		if len(items) == 0 {
			b.Fatal("ListProfiles returned no profiles")
		}
	}
}

func BenchmarkProfileStoreGetProfile(b *testing.B) {
	fx := newBenchmarkProfileStore(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		record, err := fx.store.GetProfile(fx.ctx, "bench-proj", fx.profileIDs["mixed"])
		if err != nil {
			b.Fatalf("GetProfile: %v", err)
		}
		if record.Manifest.ProfileID == "" || len(record.TopFunctions) == 0 {
			b.Fatalf("GetProfile returned incomplete record: %+v", record)
		}
	}
}

func BenchmarkProfileStoreQueryTopDown(b *testing.B) {
	fx := newBenchmarkProfileStore(b)
	filter := memorystore.ProfileQueryFilter{ProfileID: fx.profileIDs["io"]}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree, err := fx.store.QueryTopDown(fx.ctx, "bench-proj", filter)
		if err != nil {
			b.Fatalf("QueryTopDown: %v", err)
		}
		if tree.TotalWeight == 0 {
			b.Fatal("QueryTopDown returned empty tree")
		}
	}
}

func BenchmarkProfileStoreQueryFlamegraph(b *testing.B) {
	fx := newBenchmarkProfileStore(b)
	filter := memorystore.ProfileQueryFilter{ProfileID: fx.profileIDs["io"], MaxDepth: 3}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tree, err := fx.store.QueryFlamegraph(fx.ctx, "bench-proj", filter)
		if err != nil {
			b.Fatalf("QueryFlamegraph: %v", err)
		}
		if tree.TotalWeight == 0 {
			b.Fatal("QueryFlamegraph returned empty tree")
		}
	}
}

func BenchmarkProfileStoreQueryHotPath(b *testing.B) {
	fx := newBenchmarkProfileStore(b)
	filter := memorystore.ProfileQueryFilter{ProfileID: fx.profileIDs["io"]}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		path, err := fx.store.QueryHotPath(fx.ctx, "bench-proj", filter)
		if err != nil {
			b.Fatalf("QueryHotPath: %v", err)
		}
		if len(path.Frames) == 0 {
			b.Fatal("QueryHotPath returned no frames")
		}
	}
}

func BenchmarkProfileStoreCompareProfiles(b *testing.B) {
	fx := newBenchmarkProfileStore(b)
	filter := memorystore.ProfileComparisonFilter{
		BaselineProfileID:  fx.profileIDs["io"],
		CandidateProfileID: fx.profileIDs["cpu"],
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		comparison, err := fx.store.CompareProfiles(fx.ctx, "bench-proj", filter)
		if err != nil {
			b.Fatalf("CompareProfiles: %v", err)
		}
		if comparison.DurationDeltaNS == 0 {
			b.Fatalf("CompareProfiles returned no duration delta: %+v", comparison)
		}
	}
}

type benchmarkProfileFixture struct {
	ctx        context.Context
	store      *ProfileStore
	profileIDs map[string]string
}

func newBenchmarkProfileStore(b *testing.B) benchmarkProfileFixture {
	b.Helper()

	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })
	seedProfileTestProjectFromBench(b, db, "bench-org", "bench-proj")

	blobs := memorystore.NewMemoryBlobStore()
	profiles := NewProfileStore(db, blobs)
	ctx := context.Background()

	fixtures := []struct {
		key     string
		fixture profilefixtures.Fixture
	}{
		{key: "io", fixture: profilefixtures.IOHeavy()},
		{key: "cpu", fixture: profilefixtures.CPUHeavy()},
		{key: "mixed", fixture: profilefixtures.MixedLanguage()},
		{key: "db", fixture: profilefixtures.DBHeavy()},
	}

	profileIDs := map[string]string{}
	for i := 0; i < 12; i++ {
		for _, item := range fixtures {
			spec := item.fixture.Spec().
				WithIDs(fmt.Sprintf("evt-bench-%s-%02d", item.key, i), fmt.Sprintf("profile-bench-%s-%02d", item.key, i)).
				WithTimestamp(time.Date(2026, time.March, 29, 10, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute))
			if i == 0 {
				profileIDs[item.key] = spec.ProfileID
			}
			profilefixtures.Save(b, profiles, "bench-proj", spec)
		}
	}

	return benchmarkProfileFixture{
		ctx:        ctx,
		store:      profiles,
		profileIDs: profileIDs,
	}
}

func seedProfileTestProjectFromBench(b *testing.B, db *sql.DB, orgID, projectID string) {
	b.Helper()
	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES (?, 'bench-org', 'Benchmark Org')`, orgID); err != nil {
		b.Fatalf("insert org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name) VALUES (?, ?, 'bench-project', 'Benchmark Project')`, projectID, orgID); err != nil {
		b.Fatalf("insert project: %v", err)
	}
}
