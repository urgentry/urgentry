package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"urgentry/internal/issue"
	"urgentry/internal/nativesym"
	sharedstore "urgentry/internal/store"
	nativefixture "urgentry/internal/testfixtures/nativecrash"
)

func BenchmarkNativeCrashProcessStackwalkJob(b *testing.B) {
	ctx := context.Background()
	fixture := nativefixture.ByName(b, "apple_multimodule")

	db, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { _ = db.Close() })

	if _, err := db.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('bench-org', 'bench-org', 'Benchmark Org')`); err != nil {
		b.Fatalf("seed org: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status) VALUES ('bench-proj', 'bench-org', 'bench-project', 'Benchmark Project', 'cocoa', 'active')`); err != nil {
		b.Fatalf("seed project: %v", err)
	}

	blobs := sharedstore.NewMemoryBlobStore()
	jobs := NewJobStore(db)
	debugFiles := NewDebugFileStore(db, blobs)
	for i, symbol := range fixture.Symbols {
		if err := debugFiles.Save(ctx, &DebugFile{
			ID:         fmt.Sprintf("bench-debug-%d", i+1),
			ProjectID:  "bench-proj",
			ReleaseID:  fixture.Release,
			Kind:       symbol.Kind,
			Name:       symbol.Name,
			UUID:       symbol.DebugID,
			CodeID:     symbol.CodeID,
			CreatedAt:  time.Now().UTC(),
			ModuleName: "app",
		}, symbol.Body); err != nil {
			b.Fatalf("Save debug file: %v", err)
		}
	}

	processor := &issue.Processor{
		Events: NewEventStore(db),
		Groups: NewGroupStore(db),
		Blobs:  blobs,
		Native: nativesym.NewResolver(nativeDebugFileLookup{store: debugFiles}),
	}
	nativeCrashes := NewNativeCrashStore(db, blobs, jobs, b.N+16)

	projectIDs := make([]string, 0, b.N)
	payloads := make([][]byte, 0, b.N)
	for i := 0; i < b.N; i++ {
		eventID := fmt.Sprintf("%032x", i+1)
		crash, created, err := nativeCrashes.IngestMinidump(ctx, MinidumpReceiptInput{
			ProjectID:   "bench-proj",
			EventID:     eventID,
			Filename:    fixture.DumpFilename,
			ContentType: "application/x-dmp",
			Dump:        fixture.Dump,
			EventJSON:   fixture.EventJSON(b, eventID),
		})
		if err != nil {
			b.Fatalf("IngestMinidump: %v", err)
		}
		if !created || crash == nil {
			b.Fatalf("expected crash receipt for %s, got created=%v crash=%+v", eventID, created, crash)
		}
		job, err := jobs.ClaimNext(ctx, "bench-native-worker", time.Minute)
		if err != nil {
			b.Fatalf("ClaimNext: %v", err)
		}
		if job == nil {
			b.Fatal("expected native stackwalk job")
		}
		projectIDs = append(projectIDs, job.ProjectID)
		payloads = append(payloads, append([]byte(nil), job.Payload...))
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := nativeCrashes.ProcessStackwalkJob(ctx, processor, projectIDs[i], payloads[i]); err != nil {
			b.Fatalf("ProcessStackwalkJob: %v", err)
		}
	}
}
