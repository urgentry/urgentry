package selfhostedops

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"urgentry/internal/postgrescontrol"
	"urgentry/internal/telemetrybridge"
)

func TestValidateTopologyRejectsSeriousWithoutPostgres(t *testing.T) {
	checks := ValidateTopology(TopologyConfig{
		Roles:   []string{"api", "worker"},
		DataDir: "/data",
	})
	if len(checks) == 0 {
		t.Fatal("expected topology rejection for serious mode without Postgres DSNs")
	}
	for _, c := range checks {
		if c.OK {
			t.Fatalf("expected all topology checks to fail, got %+v", c)
		}
	}
}

func TestValidateTopologyRejectsSingleSeriousRoleWithoutPostgres(t *testing.T) {
	for _, role := range []string{"api", "ingest", "worker", "scheduler"} {
		checks := ValidateTopology(TopologyConfig{
			Roles:   []string{role},
			DataDir: "/data",
		})
		if len(checks) == 0 {
			t.Fatalf("expected rejection for single serious role %q without Postgres", role)
		}
		for _, c := range checks {
			if c.OK {
				t.Fatalf("role=%q: expected failure, got %+v", role, c)
			}
		}
	}
}

func TestValidateTopologyAcceptsTinyMode(t *testing.T) {
	checks := ValidateTopology(TopologyConfig{
		Roles:   []string{"all"},
		DataDir: "/data",
	})
	if len(checks) != 0 {
		t.Fatalf("expected no topology checks for Tiny mode, got %+v", checks)
	}
}

func TestValidateTopologyAcceptsSeriousWithPostgres(t *testing.T) {
	checks := ValidateTopology(TopologyConfig{
		Roles:        []string{"api", "worker", "scheduler"},
		ControlDSN:   "postgres://urgentry:secret@postgres:5432/urgentry",
		TelemetryDSN: "postgres://urgentry:secret@postgres:5432/urgentry",
		DataDir:      "/data",
	})
	if len(checks) != 1 || !checks[0].OK {
		t.Fatalf("expected passing topology check, got %+v", checks)
	}
}

func TestValidateTopologyExplicitSeriousModeFlag(t *testing.T) {
	checks := ValidateTopology(TopologyConfig{
		Roles:       []string{"all"},
		SeriousMode: true,
		DataDir:     "/data",
	})
	if len(checks) == 0 {
		t.Fatal("expected rejection when SeriousMode is explicit but no Postgres DSNs")
	}
	for _, c := range checks {
		if c.OK {
			t.Fatalf("expected failure, got %+v", c)
		}
	}
}

func TestValidateTopologyExplicitSeriousWithPostgres(t *testing.T) {
	checks := ValidateTopology(TopologyConfig{
		Roles:        []string{"all"},
		SeriousMode:  true,
		ControlDSN:   "postgres://urgentry:secret@postgres:5432/urgentry",
		TelemetryDSN: "postgres://urgentry:secret@postgres:5432/urgentry",
	})
	if len(checks) != 1 || !checks[0].OK {
		t.Fatalf("expected passing topology, got %+v", checks)
	}
}

func TestValidateTopologyDataDirWithoutControlDSN(t *testing.T) {
	checks := ValidateTopology(TopologyConfig{
		Roles:        []string{"api"},
		DataDir:      "/data",
		TelemetryDSN: "postgres://urgentry:secret@postgres:5432/urgentry",
	})
	var dataDirCheck *PreflightCheck
	for _, c := range checks {
		if c.Detail != "" && !c.OK && c.Name == "topology" {
			cc := c
			dataDirCheck = &cc
		}
	}
	if dataDirCheck == nil {
		t.Fatal("expected a failed topology check about URGENTRY_DATA_DIR")
	}
}

func TestIsSeriousRoleSet(t *testing.T) {
	tests := []struct {
		roles []string
		want  bool
	}{
		{roles: nil, want: false},
		{roles: []string{}, want: false},
		{roles: []string{"all"}, want: false},
		{roles: []string{"ALL"}, want: false},
		{roles: []string{" all "}, want: false},
		{roles: []string{"api"}, want: true},
		{roles: []string{"ingest"}, want: true},
		{roles: []string{"worker"}, want: true},
		{roles: []string{"scheduler"}, want: true},
		{roles: []string{"api", "worker"}, want: true},
		{roles: []string{"all", "api"}, want: true},
	}
	for _, tt := range tests {
		got := isSeriousRoleSet(tt.roles)
		if got != tt.want {
			t.Errorf("isSeriousRoleSet(%v) = %v, want %v", tt.roles, got, tt.want)
		}
	}
}

func TestParseTelemetryBackend(t *testing.T) {
	tests := []struct {
		raw  string
		want telemetrybridge.Backend
		ok   bool
	}{
		{raw: "", want: telemetrybridge.BackendPostgres, ok: true},
		{raw: "postgres", want: telemetrybridge.BackendPostgres, ok: true},
		{raw: "timescale", want: telemetrybridge.BackendTimescale, ok: true},
		{raw: "sqlite", ok: false},
	}
	for _, tt := range tests {
		got, err := ParseTelemetryBackend(tt.raw)
		if tt.ok && err != nil {
			t.Fatalf("ParseTelemetryBackend(%q) error = %v", tt.raw, err)
		}
		if !tt.ok && err == nil {
			t.Fatalf("ParseTelemetryBackend(%q) error = nil, want failure", tt.raw)
		}
		if tt.ok && got != tt.want {
			t.Fatalf("ParseTelemetryBackend(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

func TestBuildRollbackPlan(t *testing.T) {
	plan, err := BuildRollbackPlan(2, 1, 1, 1, telemetrybridge.BackendPostgres)
	if err != nil {
		t.Fatalf("BuildRollbackPlan() error = %v", err)
	}
	if len(plan.Steps) < 5 {
		t.Fatalf("expected rollback steps, got %+v", plan)
	}
	if _, err := BuildRollbackPlan(1, 2, 1, 1, telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected forward target control version to fail")
	}
	if _, err := BuildRollbackPlan(1, 1, 1, 2, telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected forward target telemetry version to fail")
	}
}

func TestBuildRollbackPlanRejectsNegativeTargets(t *testing.T) {
	if _, err := BuildRollbackPlan(2, -1, 2, 1, telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected negative control target to fail")
	}
	if _, err := BuildRollbackPlan(2, 1, 2, -1, telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected negative telemetry target to fail")
	}
}

func TestBuildBackupPlan(t *testing.T) {
	plan, err := BuildBackupPlan("s3", "jetstream", "valkey", telemetrybridge.BackendPostgres)
	if err != nil {
		t.Fatalf("BuildBackupPlan() error = %v", err)
	}
	if got, want := len(plan.Artifacts), 5; got != want {
		t.Fatalf("artifact count = %d, want %d", got, want)
	}
	if !plan.Artifacts[0].Required || plan.Artifacts[0].Name != "postgres.sql.gz" {
		t.Fatalf("first artifact = %+v, want required Postgres dump", plan.Artifacts[0])
	}
	if plan.Expectation.Proof == "" {
		t.Fatal("expected proof text")
	}
}

func TestBuildBackupPlanRejectsUnknownBackends(t *testing.T) {
	if _, err := BuildBackupPlan("weird", "jetstream", "valkey", telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected blob backend validation error")
	}
	if _, err := BuildBackupPlan("s3", "weird", "valkey", telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected async backend validation error")
	}
	if _, err := BuildBackupPlan("s3", "jetstream", "weird", telemetrybridge.BackendPostgres); err == nil {
		t.Fatal("expected cache backend validation error")
	}
}

func TestBuildBackupPlanNormalizesValuesAndDefaults(t *testing.T) {
	plan, err := BuildBackupPlan(" S3 ", " JetStream ", " VALKEY ", telemetrybridge.BackendTimescale)
	if err != nil {
		t.Fatalf("BuildBackupPlan() error = %v", err)
	}
	if plan.TelemetryBackend != "timescale" {
		t.Fatalf("TelemetryBackend = %q, want timescale", plan.TelemetryBackend)
	}
	if plan.BlobBackend != "s3" || plan.AsyncBackend != "jetstream" || plan.CacheBackend != "valkey" {
		t.Fatalf("normalized backends = %#v", plan)
	}

	defaults, err := BuildBackupPlan("", "", "", telemetrybridge.BackendPostgres)
	if err != nil {
		t.Fatalf("BuildBackupPlan(defaults) error = %v", err)
	}
	if defaults.BlobBackend != "s3" || defaults.AsyncBackend != "jetstream" || defaults.CacheBackend != "valkey" {
		t.Fatalf("default backends = %#v", defaults)
	}
}

func TestRedactDSN(t *testing.T) {
	got := redactDSN("postgres://user:secret@localhost:5432/urgentry?sslmode=disable")
	if got != "postgres://****@localhost:5432/urgentry?sslmode=disable" {
		t.Fatalf("redactDSN() = %q", got)
	}
}

func TestVerifyBackupSet(t *testing.T) {
	dir := t.TempDir()
	writeBackupTestFile(t, dir, "compose.env", []byte("COMPOSE_PROJECT_NAME=urgentry-selfhosted\n"))
	writeBackupTestFile(t, dir, "preflight.json", []byte(`{"telemetryBackend":"postgres","checks":[{"name":"control-plane","ok":true}]}`))
	writeBackupTestFile(t, dir, "status.json", []byte(`{"telemetryBackend":"postgres","controlVersion":2,"controlTargetVersion":2,"telemetryVersion":3,"telemetryTargetVersion":3}`))
	writeBackupTestFile(t, dir, "backup-plan.json", []byte(`{"telemetryBackend":"postgres","blobBackend":"s3","asyncBackend":"jetstream","cacheBackend":"valkey","expectation":{"recoveryPointObjective":"latest backup","recoveryTimeObjective":"cold restore","proof":"drill"},"artifacts":[{"name":"postgres.sql.gz","required":true},{"name":"urgentry-data.tar.gz","required":true},{"name":"valkey-data.tar.gz","required":false}]}`))
	writeBackupTestFile(t, dir, "postgres.sql.gz", []byte("postgres"))
	writeBackupTestFile(t, dir, "urgentry-data.tar.gz", []byte("sqlite"))

	manifest := backupManifestFixture(t, dir, []string{
		"backup-plan.json",
		"compose.env",
		"urgentry-data.tar.gz",
		"postgres.sql.gz",
		"preflight.json",
		"status.json",
	})
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	report, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, true)
	if err != nil {
		t.Fatalf("VerifyBackupSet() error = %v", err)
	}
	if !report.Verified {
		t.Fatalf("VerifyBackupSet() verified = false, want true: %+v", report.Files)
	}
}

func TestVerifyBackupSetRejectsChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	writeBackupTestFile(t, dir, "compose.env", []byte("COMPOSE_PROJECT_NAME=urgentry-selfhosted\n"))
	writeBackupTestFile(t, dir, "preflight.json", []byte(`{"telemetryBackend":"postgres","checks":[]}`))
	writeBackupTestFile(t, dir, "status.json", []byte(`{"telemetryBackend":"postgres"}`))
	writeBackupTestFile(t, dir, "backup-plan.json", []byte(`{"telemetryBackend":"postgres","artifacts":[{"name":"postgres.sql.gz","required":true}]}`))
	writeBackupTestFile(t, dir, "postgres.sql.gz", []byte("postgres"))

	manifest := backupManifestFixture(t, dir, []string{
		"backup-plan.json",
		"compose.env",
		"postgres.sql.gz",
		"preflight.json",
		"status.json",
	})
	for idx := range manifest.Integrity {
		if manifest.Integrity[idx].Name == "postgres.sql.gz" {
			manifest.Integrity[idx].SHA256 = "deadbeef"
		}
	}
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	report, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, true)
	if err != nil {
		t.Fatalf("VerifyBackupSet() error = %v", err)
	}
	if report.Verified {
		t.Fatalf("VerifyBackupSet() verified = true, want false")
	}
}

func TestBuildSecurityReportRejectsDefaults(t *testing.T) {
	t.Setenv("URGENTRY_BOOTSTRAP_PASSWORD", "change-me-in-production")
	t.Setenv("URGENTRY_BOOTSTRAP_PAT", "gpat_self_hosted_bootstrap")
	t.Setenv("URGENTRY_METRICS_TOKEN", "")
	t.Setenv("POSTGRES_PASSWORD", "change-me-in-production")
	t.Setenv("MINIO_ROOT_PASSWORD", "change-me-in-production")

	report := BuildSecurityReport("production", "postgres://urgentry:change-me-in-production@postgres:5432/urgentry", "postgres://urgentry:change-me-in-production@postgres:5432/urgentry")
	if report.Environment != "production" {
		t.Fatalf("environment = %q, want production", report.Environment)
	}
	var failed int
	for _, check := range report.Checks {
		if !check.OK {
			failed++
		}
	}
	if failed != len(report.Checks) {
		t.Fatalf("expected all security checks to fail, got %+v", report.Checks)
	}
}

func TestBuildSecurityReportAcceptsConfiguredSecrets(t *testing.T) {
	t.Setenv("URGENTRY_BOOTSTRAP_PASSWORD", "serious-selfhosted-bootstrap-password")
	t.Setenv("URGENTRY_BOOTSTRAP_PAT", "gpat_serious_self_hosted_123456")
	t.Setenv("URGENTRY_METRICS_TOKEN", "metrics-self-hosted-123456")
	t.Setenv("POSTGRES_PASSWORD", "serious-selfhosted-postgres")
	t.Setenv("MINIO_ROOT_PASSWORD", "serious-selfhosted-minio")

	report := BuildSecurityReport("production", "postgres://urgentry:serious-selfhosted-postgres@postgres:5432/urgentry", "postgres://urgentry:serious-selfhosted-postgres@postgres:5432/urgentry")
	for _, check := range report.Checks {
		if !check.OK {
			t.Fatalf("unexpected failed security check: %+v", check)
		}
	}
}

func TestBuildSecurityReportDefaultsEnvironmentAndUsesDSNPasswords(t *testing.T) {
	t.Setenv("URGENTRY_BOOTSTRAP_PASSWORD", "")
	t.Setenv("URGENTRY_BOOTSTRAP_PAT", "")
	t.Setenv("URGENTRY_METRICS_TOKEN", "")
	t.Setenv("POSTGRES_PASSWORD", "")
	t.Setenv("MINIO_ROOT_PASSWORD", "")
	t.Setenv("URGENTRY_S3_SECRET_KEY", "")

	report := BuildSecurityReport("", "postgres://urgentry:dsn-secret@postgres:5432/urgentry", "postgres://urgentry:ignored@telemetry:5432/urgentry")
	if report.Environment != "production" {
		t.Fatalf("environment = %q, want production", report.Environment)
	}
	checks := indexChecks(report.Checks)
	if checks["postgres-password"].OK != true {
		t.Fatalf("postgres-password check = %#v, want configured from DSN", checks["postgres-password"])
	}
	if checks["bootstrap-password"].Detail != "missing" {
		t.Fatalf("bootstrap-password check = %#v, want missing", checks["bootstrap-password"])
	}
	if checks["minio-root-password"].Detail != "missing" {
		t.Fatalf("minio-root-password check = %#v, want missing", checks["minio-root-password"])
	}
}

func TestRotateBootstrapAccess(t *testing.T) {
	db, dsn := openMigratedMaintenanceDatabase(t, "rotate_bootstrap")
	authStore := postgrescontrol.NewAuthStore(db)
	if _, err := authStore.EnsureBootstrapAccess(t.Context(), postgrescontrol.BootstrapOptions{
		DefaultOrganizationID: "default-org",
		Email:                 "admin@urgentry.local",
		DisplayName:           "Bootstrap Admin",
		Password:              "old-password",
		PersonalAccessToken:   "gpat_old_bootstrap",
	}); err != nil {
		t.Fatalf("EnsureBootstrapAccess() error = %v", err)
	}

	result, err := RotateBootstrapAccess(t.Context(), dsn, "admin@urgentry.local", "new-password", "gpat_new_bootstrap")
	if err != nil {
		t.Fatalf("RotateBootstrapAccess() error = %v", err)
	}
	if !result.Rotated || result.TokenPrefix != "gpat" {
		t.Fatalf("RotateBootstrapAccess() = %#v", result)
	}
	if _, err := authStore.AuthenticatePAT(t.Context(), "gpat_old_bootstrap"); err == nil {
		t.Fatal("old bootstrap PAT still authenticates")
	}
	if _, err := authStore.AuthenticatePAT(t.Context(), "gpat_new_bootstrap"); err != nil {
		t.Fatalf("AuthenticatePAT(new) error = %v", err)
	}
}

func TestVerifyBackupSetRejectsTargetVersionSkew(t *testing.T) {
	dir := t.TempDir()
	writeBackupTestFile(t, dir, "compose.env", []byte("COMPOSE_PROJECT_NAME=urgentry-selfhosted\n"))
	writeBackupTestFile(t, dir, "preflight.json", []byte(`{"telemetryBackend":"postgres","controlTargetVersion":999,"telemetryTargetVersion":999,"checks":[]}`))
	writeBackupTestFile(t, dir, "status.json", []byte(`{"telemetryBackend":"postgres","controlVersion":1,"telemetryVersion":1}`))
	writeBackupTestFile(t, dir, "backup-plan.json", []byte(`{"telemetryBackend":"postgres","artifacts":[{"name":"postgres.sql.gz","required":true}]}`))
	writeBackupTestFile(t, dir, "postgres.sql.gz", []byte("postgres"))

	manifest := backupManifestFixture(t, dir, []string{
		"backup-plan.json",
		"compose.env",
		"postgres.sql.gz",
		"preflight.json",
		"status.json",
	})
	manifest.Preflight.ControlTargetVersion = 999
	manifest.Preflight.TelemetryTargetVersion = 999
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, true); err == nil {
		t.Fatal("expected target version skew to fail")
	}
	if _, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, false); err != nil {
		t.Fatalf("VerifyBackupSet(strict=false) error = %v", err)
	}
}

func TestVerifyBackupSetRejectsUnsupportedSchemaVersion(t *testing.T) {
	dir := t.TempDir()
	writeBackupTestFile(t, dir, "manifest.json", []byte(`{"schemaVersion":9}`))

	if _, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, true); err == nil {
		t.Fatal("expected unsupported schema version to fail")
	}
}

func TestVerifyBackupSetRejectsBackendMismatch(t *testing.T) {
	dir := t.TempDir()
	writeBackupTestFile(t, dir, "compose.env", []byte("COMPOSE_PROJECT_NAME=urgentry-selfhosted\n"))
	writeBackupTestFile(t, dir, "preflight.json", []byte(`{"telemetryBackend":"timescale"}`))
	writeBackupTestFile(t, dir, "status.json", []byte(`{"telemetryBackend":"timescale"}`))
	writeBackupTestFile(t, dir, "backup-plan.json", []byte(`{"telemetryBackend":"timescale","artifacts":[{"name":"postgres.sql.gz","required":true}]}`))
	writeBackupTestFile(t, dir, "postgres.sql.gz", []byte("postgres"))

	manifest := backupManifestFixture(t, dir, []string{
		"backup-plan.json",
		"compose.env",
		"postgres.sql.gz",
		"preflight.json",
		"status.json",
	})
	manifest.Preflight.TelemetryBackend = "timescale"
	manifest.Status.TelemetryBackend = "timescale"
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	if _, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, true); err == nil {
		t.Fatal("expected backend mismatch to fail")
	}
}

func TestVerifyBackupSetFlagsMissingIntegrityForRequiredFile(t *testing.T) {
	dir := t.TempDir()
	writeBackupTestFile(t, dir, "compose.env", []byte("COMPOSE_PROJECT_NAME=urgentry-selfhosted\n"))
	writeBackupTestFile(t, dir, "preflight.json", []byte(`{"telemetryBackend":"postgres","checks":[]}`))
	writeBackupTestFile(t, dir, "status.json", []byte(`{"telemetryBackend":"postgres"}`))
	writeBackupTestFile(t, dir, "backup-plan.json", []byte(`{"telemetryBackend":"postgres","artifacts":[{"name":"postgres.sql.gz","required":true}]}`))
	writeBackupTestFile(t, dir, "postgres.sql.gz", []byte("postgres"))

	manifest := backupManifestFixture(t, dir, []string{
		"backup-plan.json",
		"compose.env",
		"postgres.sql.gz",
		"preflight.json",
		"status.json",
	})
	filtered := manifest.Integrity[:0]
	for _, item := range manifest.Integrity {
		if item.Name != "postgres.sql.gz" {
			filtered = append(filtered, item)
		}
	}
	manifest.Integrity = filtered
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), payload, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	report, err := VerifyBackupSet(dir, telemetrybridge.BackendPostgres, true)
	if err != nil {
		t.Fatalf("VerifyBackupSet() error = %v", err)
	}
	if report.Verified {
		t.Fatal("expected report to fail without required integrity entry")
	}
	check := reportFile(report, "postgres.sql.gz")
	if check.Detail != "missing integrity entry" {
		t.Fatalf("postgres.sql.gz detail = %q, want missing integrity entry", check.Detail)
	}
}

func TestDSNPasswordAndRedactDSNEdgeCases(t *testing.T) {
	if got := dsnPassword("postgres://urgentry:secret@postgres:5432/urgentry"); got != "secret" {
		t.Fatalf("dsnPassword() = %q, want secret", got)
	}
	if got := dsnPassword("not a url"); got != "" {
		t.Fatalf("dsnPassword(invalid) = %q, want empty", got)
	}
	if got := redactDSN(""); got != "" {
		t.Fatalf("redactDSN(empty) = %q, want empty", got)
	}
	raw := "postgres://postgres:5432/urgentry?sslmode=disable"
	if got := redactDSN(raw); got != raw {
		t.Fatalf("redactDSN(no-password) = %q, want %q", got, raw)
	}
}

func writeBackupTestFile(t *testing.T, dir, name string, body []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func backupManifestFixture(t *testing.T, dir string, files []string) BackupManifest {
	t.Helper()
	integrity := make([]BackupFileIntegrity, 0, len(files))
	for _, name := range files {
		sum, size, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("fileSHA256(%s): %v", name, err)
		}
		integrity = append(integrity, BackupFileIntegrity{
			Name:   name,
			Bytes:  size,
			SHA256: sum,
		})
	}
	return BackupManifest{
		SchemaVersion:  1,
		CapturedAt:     "2026-03-30T00:00:00Z",
		ComposeProject: "urgentry-selfhosted",
		Files:          files,
		Preflight: PreflightReport{
			TelemetryBackend:       "postgres",
			ControlTargetVersion:   len(postgrescontrol.AllMigrations()),
			TelemetryTargetVersion: len(telemetrybridge.Migrations(telemetrybridge.BackendPostgres)),
		},
		Status: Status{
			TelemetryBackend: "postgres",
		},
		BackupPlan: BackupPlan{
			TelemetryBackend: "postgres",
			Artifacts: []BackupArtifact{
				{Name: "postgres.sql.gz", Required: true},
				{Name: "urgentry-data.tar.gz", Required: true},
			},
		},
		Integrity: integrity,
	}
}

func indexChecks(checks []PreflightCheck) map[string]PreflightCheck {
	indexed := make(map[string]PreflightCheck, len(checks))
	for _, check := range checks {
		indexed[check.Name] = check
	}
	return indexed
}

func reportFile(report *BackupVerificationReport, name string) BackupVerificationFile {
	for _, file := range report.Files {
		if file.Name == name {
			return file
		}
	}
	return BackupVerificationFile{}
}
