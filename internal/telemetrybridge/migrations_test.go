package telemetrybridge

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPostgresMigrationsContainBridgeTables(t *testing.T) {
	t.Parallel()

	sql := joinSQL(Migrations(BackendPostgres))
	required := []string{
		"telemetry.event_facts",
		"telemetry.log_facts",
		"telemetry.transaction_facts",
		"telemetry.span_facts",
		"telemetry.outcome_facts",
		"telemetry.replay_manifests",
		"telemetry.replay_timeline_items",
		"telemetry.profile_manifests",
		"telemetry.profile_samples",
		"telemetry.projector_cursors",
	}
	for _, name := range required {
		if !strings.Contains(sql, name) {
			t.Fatalf("postgres migrations missing %s", name)
		}
	}
	requiredFragments := []string{
		"idx_log_facts_org_time",
		"idx_transaction_facts_org_time",
		"idx_outcome_facts_org_time",
		"idx_replay_manifests_org_time",
		"idx_profile_manifests_org_time",
		"cursor_family TEXT NOT NULL",
		"scope_kind TEXT NOT NULL",
		"scope_id TEXT NOT NULL",
		"last_event_at TIMESTAMPTZ",
		"metadata_json JSONB NOT NULL DEFAULT '{}'::jsonb",
	}
	for _, fragment := range requiredFragments {
		if !strings.Contains(sql, fragment) {
			t.Fatalf("postgres migrations missing fragment %q", fragment)
		}
	}
}

func TestTimescaleMigrationsAddHypertables(t *testing.T) {
	t.Parallel()

	sql := joinSQL(Migrations(BackendTimescale))
	if !strings.Contains(sql, "CREATE EXTENSION IF NOT EXISTS timescaledb") {
		t.Fatal("timescale migrations missing extension")
	}
	if !strings.Contains(sql, "create_hypertable('telemetry.transaction_facts'") {
		t.Fatal("timescale migrations missing transaction hypertable")
	}
}

func TestApplyRunsMigrationsInOrder(t *testing.T) {
	t.Parallel()

	var exec fakeExecutor
	if err := Apply(context.Background(), &exec, BackendTimescale); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if len(exec.sql) != 3 {
		t.Fatalf("Apply() executed %d migrations, want 3", len(exec.sql))
	}
	if !strings.Contains(exec.sql[0], "CREATE SCHEMA IF NOT EXISTS telemetry") {
		t.Fatal("Apply() did not execute base migration first")
	}
	if !strings.Contains(exec.sql[1], "ALTER TABLE telemetry.profile_manifests ADD COLUMN IF NOT EXISTS profile_kind TEXT") {
		t.Fatal("Apply() did not execute profile expansion migration second")
	}
	if !strings.Contains(exec.sql[2], "CREATE EXTENSION IF NOT EXISTS timescaledb") {
		t.Fatal("Apply() did not execute timescale migration third")
	}
}

func TestApplyPropagatesMigrationFailure(t *testing.T) {
	t.Parallel()

	exec := &fakeExecutor{failAt: 2}
	err := Apply(context.Background(), exec, BackendTimescale)
	if err == nil {
		t.Fatal("Apply() error = nil, want failure")
	}
	if !strings.Contains(err.Error(), "apply telemetry bridge migration 2") {
		t.Fatalf("Apply() error = %v, want migration context", err)
	}
}

func joinSQL(migrations []Migration) string {
	var b strings.Builder
	for _, migration := range migrations {
		b.WriteString(migration.SQL)
		b.WriteString("\n")
	}
	return b.String()
}

type fakeExecutor struct {
	sql    []string
	failAt int
}

func (f *fakeExecutor) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.sql = append(f.sql, sql)
	if f.failAt > 0 && len(f.sql) == f.failAt {
		return pgconn.CommandTag{}, fmt.Errorf("boom")
	}
	return pgconn.CommandTag{}, nil
}
