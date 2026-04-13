package selfhostedops

import (
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestBuildSupportBundleCopiesOverview(t *testing.T) {
	now := time.Date(2026, time.March, 30, 12, 0, 0, 0, time.UTC)
	bundle := BuildSupportBundle(&store.OperatorOverview{
		OrganizationSlug: "acme",
		Install:          &store.InstallState{InstallID: "install-1", Version: "v1.2.3"},
		Runtime:          store.OperatorRuntime{Role: "api", Env: "prod", Version: "v1"},
		FleetNodes:       []store.OperatorFleetNode{{ID: "api-0", Role: "api", Env: "prod", Hostname: "node-a"}},
		Services:         []store.OperatorServiceStatus{{Name: "postgres", Status: "ok"}, {Name: "bridge", Status: "warn"}},
		Queue:            store.OperatorQueueStatus{Depth: 24},
		Backfills:        []store.OperatorBackfillStatus{{ID: "run-1", FailedItems: 2}},
	}, now)
	if bundle == nil {
		t.Fatal("BuildSupportBundle() = nil")
	}
	if got, want := bundle.OrganizationSlug, "acme"; got != want {
		t.Fatalf("OrganizationSlug = %q, want %q", got, want)
	}
	if got, want := bundle.Queue.Depth, 24; got != want {
		t.Fatalf("Queue.Depth = %d, want %d", got, want)
	}
	if bundle.Summary.Health != "warn" {
		t.Fatalf("Summary.Health = %q, want warn", bundle.Summary.Health)
	}
	if bundle.Summary.ServiceCounts["ok"] != 1 || bundle.Summary.ServiceCounts["warn"] != 1 {
		t.Fatalf("unexpected service counts: %+v", bundle.Summary.ServiceCounts)
	}
	if bundle.Summary.FailedBackfills != 1 {
		t.Fatalf("FailedBackfills = %d, want 1", bundle.Summary.FailedBackfills)
	}
	if bundle.Summary.FleetNodeCount != 1 {
		t.Fatalf("FleetNodeCount = %d, want 1", bundle.Summary.FleetNodeCount)
	}
	if len(bundle.FleetNodes) != 1 || bundle.FleetNodes[0].ID != "api-0" || bundle.FleetNodes[0].Hostname != "node-a" {
		t.Fatalf("unexpected fleet nodes: %+v", bundle.FleetNodes)
	}
	if len(bundle.Summary.RecommendedInvestigation) == 0 {
		t.Fatalf("expected investigation hints: %+v", bundle.Summary)
	}
	if bundle.Install == nil || bundle.Install.InstallID != "install-1" {
		t.Fatalf("Install = %#v, want install-1", bundle.Install)
	}
	if got, want := bundle.CapturedAt, now; !got.Equal(want) {
		t.Fatalf("CapturedAt = %s, want %s", got, want)
	}
	if len(bundle.Redactions) == 0 {
		t.Fatal("expected redaction notes")
	}
}
