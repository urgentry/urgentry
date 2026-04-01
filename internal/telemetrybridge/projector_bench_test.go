package telemetrybridge

import (
	"context"
	"testing"
)

func BenchmarkProjectorSyncFamilies(b *testing.B) {
	ctx := context.Background()
	source := openProjectorSourceDB(b)
	seedProjectorSource(b, source)
	bridge := openMigratedTelemetryTestDatabase(b)
	projector := NewProjector(source, bridge)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := projector.ResetScope(ctx, scope); err != nil {
			b.Fatalf("ResetScope: %v", err)
		}
		if err := projector.SyncFamilies(ctx, scope); err != nil {
			b.Fatalf("SyncFamilies: %v", err)
		}
	}
}

func BenchmarkProjectorStepFamilies(b *testing.B) {
	ctx := context.Background()
	source := openProjectorSourceDB(b)
	seedProjectorSource(b, source)
	bridge := openMigratedTelemetryTestDatabase(b)
	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		projector := NewProjector(source, bridge)
		projector.batchSize = 1
		if err := projector.ResetScope(ctx, scope); err != nil {
			b.Fatalf("ResetScope: %v", err)
		}
		for {
			result, err := projector.StepFamilies(ctx, scope)
			if err != nil {
				b.Fatalf("StepFamilies: %v", err)
			}
			if result.Done {
				break
			}
		}
	}
}

func BenchmarkProjectorDrainBacklogBySurface(b *testing.B) {
	cases := []struct {
		name     string
		families []Family
	}{
		{name: "logs", families: []Family{FamilyLogs}},
		{name: "transactions", families: []Family{FamilyTransactions}},
		{name: "traces", families: []Family{FamilyTransactions, FamilySpans}},
		{name: "replays", families: []Family{FamilyReplays, FamilyReplayTimeline}},
		{name: "profiles", families: []Family{FamilyProfiles}},
	}
	for _, tc := range cases {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			source := openProjectorSourceDB(b)
			seedProjectorSource(b, source)
			bridge := openMigratedTelemetryTestDatabase(b)
			scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				projector := NewProjector(source, bridge)
				if err := projector.ResetScope(ctx, scope, tc.families...); err != nil {
					b.Fatalf("ResetScope: %v", err)
				}
				for {
					result, err := projector.StepFamilies(ctx, scope, tc.families...)
					if err != nil {
						b.Fatalf("StepFamilies: %v", err)
					}
					if result.Done {
						break
					}
				}
			}
		})
	}
}
