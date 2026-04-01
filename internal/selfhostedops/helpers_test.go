package selfhostedops

import (
	"encoding/json"
	"strings"
	"testing"

	"urgentry/internal/store"
)

func TestSecretCheckMissing(t *testing.T) {
	t.Parallel()

	check := secretCheck("test-secret", "")
	if check.OK {
		t.Fatal("expected secretCheck(empty) to fail")
	}
	if check.Detail != "missing" {
		t.Fatalf("detail = %q, want missing", check.Detail)
	}
}

func TestSecretCheckInsecureDefault(t *testing.T) {
	t.Parallel()

	for _, insecure := range defaultInsecureValues {
		check := secretCheck("test", insecure)
		if check.OK {
			t.Fatalf("expected secretCheck(%q) to fail", insecure)
		}
		if !strings.Contains(check.Detail, "placeholder") && !strings.Contains(check.Detail, "insecure") {
			t.Fatalf("detail = %q, want placeholder/insecure hint", check.Detail)
		}
	}
}

func TestSecretCheckConfigured(t *testing.T) {
	t.Parallel()

	check := secretCheck("test", "a-genuine-secret-value")
	if !check.OK {
		t.Fatalf("expected secretCheck(valid) to pass, detail = %q", check.Detail)
	}
	if check.Detail != "configured" {
		t.Fatalf("detail = %q, want configured", check.Detail)
	}
}

func TestPatPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"standard PAT", "gpat_self_hosted_bootstrap", "gpat"},
		{"short PAT no underscore", "abc", "abc"},
		{"longer no underscore", "abcdef", "abcd"},
		{"empty", "", ""},
		{"whitespace", "  ", ""},
		{"underscore at start", "_test", "_tes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := patPrefix(tt.input); got != tt.want {
				t.Fatalf("patPrefix(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRedactDSNVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"with credentials", "postgres://user:secret@localhost:5432/db", "postgres://****@localhost:5432/db"},
		{"empty", "", ""},
		{"whitespace only", "  ", ""},
		{"no at sign", "postgres://localhost:5432/db", "postgres://localhost:5432/db"},
		{"no scheme", "user:pass@host:5432/db", "user:pass@host:5432/db"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactDSN(tt.dsn); got != tt.want {
				t.Fatalf("redactDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestDSNPassword(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{"with password", "postgres://user:secret@localhost:5432/db", "secret"},
		{"no password", "postgres://user@localhost:5432/db", ""},
		{"invalid URL", "not a url{}", ""},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dsnPassword(tt.dsn); got != tt.want {
				t.Fatalf("dsnPassword(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	t.Parallel()

	if got := firstNonEmpty("", "  ", "hello"); got != "hello" {
		t.Fatalf("firstNonEmpty = %q, want hello", got)
	}
	if got := firstNonEmpty("", ""); got != "" {
		t.Fatalf("firstNonEmpty(all empty) = %q, want empty", got)
	}
	if got := firstNonEmpty("first", "second"); got != "first" {
		t.Fatalf("firstNonEmpty = %q, want first", got)
	}
}

func TestNormalizeOperatorAuditRecord(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record store.OperatorAuditRecord
		err    bool
		check  func(store.OperatorAuditRecord) string
	}{
		{
			name:   "empty action",
			record: store.OperatorAuditRecord{},
			err:    true,
		},
		{
			name: "valid minimal",
			record: store.OperatorAuditRecord{
				Action: "test.action",
			},
			check: func(r store.OperatorAuditRecord) string {
				if r.Status != "succeeded" {
					return "default status should be 'succeeded'"
				}
				if r.Source != "cli" {
					return "default source should be 'cli'"
				}
				if r.Actor != "system" {
					return "default actor should be 'system'"
				}
				if r.MetadataJSON != "{}" {
					return "default metadata should be '{}'"
				}
				return ""
			},
		},
		{
			name: "preserves explicit values",
			record: store.OperatorAuditRecord{
				Action:       "  test.action  ",
				Status:       "failed",
				Source:        "api",
				Actor:         "admin@example.com",
				MetadataJSON: `{"key":"value"}`,
			},
			check: func(r store.OperatorAuditRecord) string {
				if r.Action != "test.action" {
					return "action not trimmed"
				}
				if r.Status != "failed" {
					return "explicit status not preserved"
				}
				if r.Source != "api" {
					return "explicit source not preserved"
				}
				return ""
			},
		},
		{
			name: "invalid metadata JSON",
			record: store.OperatorAuditRecord{
				Action:       "test",
				MetadataJSON: "not json",
			},
			err: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := normalizeOperatorAuditRecord(tt.record)
			if tt.err && err == nil {
				t.Fatal("expected error")
			}
			if !tt.err && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.err && tt.check != nil {
				if msg := tt.check(result); msg != "" {
					t.Fatal(msg)
				}
			}
		})
	}
}

func TestBuildMaintenanceStatusOpen(t *testing.T) {
	t.Parallel()

	status := buildMaintenanceStatus(nil)
	if !status.WritesOpen {
		t.Fatal("WritesOpen = false, want true for nil state")
	}
	if status.DrainState != "writes_open" {
		t.Fatalf("DrainState = %q, want writes_open", status.DrainState)
	}
	if len(status.Steps) == 0 {
		t.Fatal("expected non-empty steps")
	}
}

func TestBuildMaintenanceStatusDraining(t *testing.T) {
	t.Parallel()

	status := buildMaintenanceStatus(&store.InstallState{
		MaintenanceMode: true,
	})
	if status.WritesOpen {
		t.Fatal("WritesOpen = true, want false during maintenance")
	}
	if status.DrainState != "draining" {
		t.Fatalf("DrainState = %q, want draining", status.DrainState)
	}
}

func TestBuildMaintenanceStatusNonMaintenanceState(t *testing.T) {
	t.Parallel()

	status := buildMaintenanceStatus(&store.InstallState{
		MaintenanceMode: false,
	})
	if !status.WritesOpen {
		t.Fatal("WritesOpen = false for non-maintenance state")
	}
}

func TestBuildBackupPlanFileBackend(t *testing.T) {
	t.Parallel()

	plan, err := BuildBackupPlan("file", "sqlite", "sqlite", "postgres")
	if err != nil {
		t.Fatalf("BuildBackupPlan(file) error = %v", err)
	}
	// file backend should NOT include minio artifact
	for _, artifact := range plan.Artifacts {
		if artifact.Name == "minio-data.tar.gz" {
			t.Fatal("file backend should not include minio artifact")
		}
	}
	// sqlite async should NOT include nats artifact
	for _, artifact := range plan.Artifacts {
		if artifact.Name == "nats-data.tar.gz" {
			t.Fatal("sqlite async should not include nats artifact")
		}
	}
	// sqlite cache should NOT include valkey artifact
	for _, artifact := range plan.Artifacts {
		if artifact.Name == "valkey-data.tar.gz" {
			t.Fatal("sqlite cache should not include valkey artifact")
		}
	}
	if len(plan.Artifacts) != 2 {
		t.Fatalf("artifact count = %d, want 2 (postgres + urgentry-data)", len(plan.Artifacts))
	}
}

func TestBuildRollbackPlanStepsContainVersionInfo(t *testing.T) {
	t.Parallel()

	plan, err := BuildRollbackPlan(5, 3, 4, 2, "postgres")
	if err != nil {
		t.Fatalf("BuildRollbackPlan error = %v", err)
	}
	found := false
	for _, step := range plan.Steps {
		if strings.Contains(step, "3") && strings.Contains(step, "control") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("rollback steps should mention target control version")
	}
}

func TestBuildRollbackPlanSameVersions(t *testing.T) {
	t.Parallel()

	plan, err := BuildRollbackPlan(3, 3, 2, 2, "postgres")
	if err != nil {
		t.Fatalf("BuildRollbackPlan(same versions) error = %v", err)
	}
	if plan.CurrentControlVersion != 3 || plan.TargetControlVersion != 3 {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestParseTelemetryBackendVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw  string
		want string
		err  bool
	}{
		{"", "postgres", false},
		{"postgres", "postgres", false},
		{"POSTGRES", "postgres", false},
		{"  Postgres  ", "postgres", false},
		{"timescale", "timescale", false},
		{"TIMESCALE", "timescale", false},
		{"clickhouse", "", true},
		{"sqlite", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := ParseTelemetryBackend(tt.raw)
			if tt.err && err == nil {
				t.Fatal("expected error")
			}
			if !tt.err && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.err && string(got) != tt.want {
				t.Fatalf("ParseTelemetryBackend(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDefaultInsecureValuesAreComplete(t *testing.T) {
	t.Parallel()

	// The list should include at least the known dangerous defaults
	required := []string{
		"change-me-in-production",
		"gpat_self_hosted_bootstrap",
		"minio123secret",
	}
	for _, r := range required {
		found := false
		for _, v := range defaultInsecureValues {
			if v == r {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("defaultInsecureValues missing %q", r)
		}
	}
}

func TestBackupManifestStructTags(t *testing.T) {
	t.Parallel()

	manifest := BackupManifest{
		SchemaVersion:  1,
		CapturedAt:     "2026-03-30T00:00:00Z",
		ComposeProject: "urgentry",
		Files:          []string{"a.json"},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal error = %v", err)
	}
	if _, ok := parsed["schemaVersion"]; !ok {
		t.Fatal("missing camelCase schemaVersion in JSON")
	}
	if _, ok := parsed["capturedAt"]; !ok {
		t.Fatal("missing camelCase capturedAt in JSON")
	}
}
