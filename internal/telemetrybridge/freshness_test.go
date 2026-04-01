package telemetrybridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProjectorAssessFreshnessTracksCursorLagAndErrors(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)

	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}
	projector := NewProjector(source, bridge)

	initial, err := projector.AssessFreshness(ctx, scope, FamilyLogs)
	if err != nil {
		t.Fatalf("AssessFreshness initial: %v", err)
	}
	if len(initial) != 1 || initial[0].CursorFound || !initial[0].Pending {
		t.Fatalf("unexpected initial freshness: %+v", initial)
	}

	if err := projector.SyncFamilies(ctx, scope, FamilyLogs); err != nil {
		t.Fatalf("SyncFamilies: %v", err)
	}

	now := time.Now().UTC()
	if _, err := source.Exec(`INSERT INTO events
		(id, project_id, event_id, group_id, release, environment, platform, level, event_type, title, message, culprit, payload_json, tags_json, occurred_at, ingested_at)
		VALUES ('evt-log-new', 'proj-1', 'evt-log-new', NULL, 'backend@1.2.3', 'production', 'go', 'info', 'log', 'late log', 'late log', 'worker.go', '{"logger":"api"}', '{}', ?, ?)`,
		now.Format(time.RFC3339), now.Format(time.RFC3339),
	); err != nil {
		t.Fatalf("seed late log: %v", err)
	}
	if _, err := bridge.Exec(`UPDATE telemetry.projector_cursors
		SET updated_at = $1, last_error = 'projection stalled'
		WHERE name = $2`,
		now.Add(-2*time.Minute), cursorName(scope, FamilyLogs),
	); err != nil {
		t.Fatalf("update cursor error state: %v", err)
	}

	items, err := projector.AssessFreshness(ctx, scope, FamilyLogs)
	if err != nil {
		t.Fatalf("AssessFreshness after lag: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("freshness row count = %d, want 1", len(items))
	}
	item := items[0]
	if !item.CursorFound || !item.Pending {
		t.Fatalf("unexpected lagging freshness item: %+v", item)
	}
	if item.Lag < time.Minute {
		t.Fatalf("lag = %s, want at least 1m", item.Lag)
	}
	if item.LastError != "projection stalled" {
		t.Fatalf("LastError = %q, want projection stalled", item.LastError)
	}
}

func TestProjectorUnsupportedFamiliesReturnErrors(t *testing.T) {
	ctx := context.Background()
	source := openProjectorSourceDB(t)
	seedProjectorSource(t, source)

	bridge := openMigratedTelemetryTestDatabase(t)

	scope := Scope{OrganizationID: "org-1", ProjectID: "proj-1"}
	projector := NewProjector(source, bridge)
	unsupported := Family("bogus")

	if _, err := projector.EstimateFamilies(ctx, scope, unsupported); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("EstimateFamilies error = %v, want unsupported family", err)
	}
	if _, err := projector.StepFamilies(ctx, scope, unsupported); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("StepFamilies error = %v, want unsupported family", err)
	}
	if err := projector.ResetScope(ctx, scope, unsupported); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("ResetScope error = %v, want unsupported family", err)
	}
	if _, err := projector.AssessFreshness(ctx, scope, unsupported); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("AssessFreshness error = %v, want unsupported family", err)
	}
}
