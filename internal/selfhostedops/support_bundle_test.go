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
		Services:         []store.OperatorServiceStatus{{Name: "sqlite", Status: "ok"}},
		Queue:            store.OperatorQueueStatus{Depth: 4},
		Backfills:        []store.OperatorBackfillStatus{{ID: "run-1"}},
	}, now)
	if bundle == nil {
		t.Fatal("BuildSupportBundle() = nil")
	}
	if got, want := bundle.OrganizationSlug, "acme"; got != want {
		t.Fatalf("OrganizationSlug = %q, want %q", got, want)
	}
	if got, want := bundle.Queue.Depth, 4; got != want {
		t.Fatalf("Queue.Depth = %d, want %d", got, want)
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
