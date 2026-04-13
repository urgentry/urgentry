package selfhostedops

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"urgentry/internal/postgrescontrol"
	"urgentry/internal/telemetrybridge"
)

type PreflightCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type PreflightReport struct {
	ControlDSN             string           `json:"controlDsn,omitempty"`
	TelemetryDSN           string           `json:"telemetryDsn,omitempty"`
	TelemetryBackend       string           `json:"telemetryBackend"`
	ControlTargetVersion   int              `json:"controlTargetVersion"`
	TelemetryTargetVersion int              `json:"telemetryTargetVersion"`
	Checks                 []PreflightCheck `json:"checks"`
}

type SecurityReport struct {
	Environment string           `json:"environment"`
	Checks      []PreflightCheck `json:"checks"`
}

type BootstrapRotationResult struct {
	Email        string `json:"email"`
	TokenPrefix  string `json:"tokenPrefix"`
	Rotated      bool   `json:"rotated"`
	ControlDSN   string `json:"controlDsn,omitempty"`
	Warning      string `json:"warning,omitempty"`
}

type Status struct {
	TelemetryBackend       string `json:"telemetryBackend"`
	ControlVersion         int    `json:"controlVersion"`
	ControlTargetVersion   int    `json:"controlTargetVersion"`
	TelemetryVersion       int    `json:"telemetryVersion"`
	TelemetryTargetVersion int    `json:"telemetryTargetVersion"`
}

type RollbackPlan struct {
	TelemetryBackend      string   `json:"telemetryBackend"`
	CurrentControlVersion int      `json:"currentControlVersion"`
	TargetControlVersion  int      `json:"targetControlVersion"`
	CurrentTelemetryVer   int      `json:"currentTelemetryVersion"`
	TargetTelemetryVer    int      `json:"targetTelemetryVersion"`
	Steps                 []string `json:"steps"`
}

type BackupArtifact struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	Capture  string `json:"capture"`
	Restore  string `json:"restore"`
	Reason   string `json:"reason,omitempty"`
}

type RecoveryExpectation struct {
	RecoveryPointObjective string `json:"recoveryPointObjective"`
	RecoveryTimeObjective  string `json:"recoveryTimeObjective"`
	PointInTimeBoundary    string `json:"pointInTimeBoundary"`
	PointInTimeRecovery    bool   `json:"pointInTimeRecovery"`
	Proof                  string `json:"proof"`
}

type BackupFileIntegrity struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type BackupPlan struct {
	TelemetryBackend string              `json:"telemetryBackend"`
	BlobBackend      string              `json:"blobBackend"`
	AsyncBackend     string              `json:"asyncBackend"`
	CacheBackend     string              `json:"cacheBackend"`
	Expectation      RecoveryExpectation `json:"expectation"`
	Artifacts        []BackupArtifact    `json:"artifacts"`
	Steps            []string            `json:"steps"`
	Drill            []string            `json:"drill"`
}

type BackupManifest struct {
	SchemaVersion  int                   `json:"schemaVersion"`
	CapturedAt     string                `json:"capturedAt"`
	ComposeProject string                `json:"composeProject,omitempty"`
	Files          []string              `json:"files"`
	Status         Status                `json:"status"`
	Preflight      PreflightReport       `json:"preflight"`
	BackupPlan     BackupPlan            `json:"backupPlan"`
	Integrity      []BackupFileIntegrity `json:"integrity,omitempty"`
}

type BackupVerificationFile struct {
	Name           string `json:"name"`
	Required       bool   `json:"required"`
	Present        bool   `json:"present"`
	Bytes          int64  `json:"bytes,omitempty"`
	ExpectedBytes  int64  `json:"expectedBytes,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	ExpectedSHA256 string `json:"expectedSha256,omitempty"`
	Verified       bool   `json:"verified"`
	Detail         string `json:"detail,omitempty"`
}

type BackupVerificationReport struct {
	BackupDir         string                   `json:"backupDir"`
	TelemetryBackend  string                   `json:"telemetryBackend"`
	StrictTargetMatch bool                     `json:"strictTargetMatch"`
	Verified          bool                     `json:"verified"`
	Warnings          []string                 `json:"warnings,omitempty"`
	Files             []BackupVerificationFile `json:"files"`
}

// TopologyConfig describes the operator-supplied deployment shape used by
// ValidateTopology to decide whether the configuration is a supported layout.
type TopologyConfig struct {
	// Roles lists the distinct roles the operator intends to run.
	// A single-role "all" deployment is Tiny-compatible; multiple distinct
	// roles (api, ingest, worker, scheduler) imply serious self-hosted.
	Roles []string

	// ControlDSN is the Postgres connection string for the control plane.
	// Empty means the operator has not configured a Postgres control plane.
	ControlDSN string

	// TelemetryDSN is the Postgres connection string for telemetry.
	TelemetryDSN string

	// DataDir is the value of URGENTRY_DATA_DIR. When non-empty and the
	// topology is serious, the configuration is rejected because serious
	// mode must not depend on a shared SQLite data directory.
	DataDir string

	// SeriousMode, when true, forces the topology into the serious
	// self-hosted contract even when only one role is declared.
	SeriousMode bool
}

// ValidateTopology rejects deployment shapes that are not supported.
// Serious self-hosted requires PostgreSQL DSNs and must not fall back to
// URGENTRY_DATA_DIR as the control-plane source. Tiny mode (single "all"
// role, no explicit serious flag) is allowed to use the data directory.
func ValidateTopology(cfg TopologyConfig) []PreflightCheck {
	serious := cfg.SeriousMode || isSeriousRoleSet(cfg.Roles)
	if !serious {
		return nil
	}

	var checks []PreflightCheck

	controlDSN := strings.TrimSpace(cfg.ControlDSN)
	telemetryDSN := strings.TrimSpace(cfg.TelemetryDSN)
	dataDir := strings.TrimSpace(cfg.DataDir)

	if controlDSN == "" {
		checks = append(checks, PreflightCheck{
			Name:   "topology",
			OK:     false,
			Detail: "serious self-hosted requires URGENTRY_CONTROL_DATABASE_URL; SQLite-backed control plane is Tiny-only",
		})
	}
	if telemetryDSN == "" {
		checks = append(checks, PreflightCheck{
			Name:   "topology",
			OK:     false,
			Detail: "serious self-hosted requires URGENTRY_TELEMETRY_DATABASE_URL; SQLite-backed telemetry is Tiny-only",
		})
	}
	if dataDir != "" && controlDSN == "" {
		checks = append(checks, PreflightCheck{
			Name:   "topology",
			OK:     false,
			Detail: "URGENTRY_DATA_DIR is set without a PostgreSQL control-plane DSN; shared SQLite data directory is not a supported serious self-hosted topology",
		})
	}

	if len(checks) == 0 {
		checks = append(checks, PreflightCheck{
			Name:   "topology",
			OK:     true,
			Detail: "serious self-hosted layout with PostgreSQL control plane",
		})
	}

	return checks
}

// isSeriousRoleSet returns true when the role list implies a multi-role
// serious deployment rather than a single Tiny "all" process.
func isSeriousRoleSet(roles []string) bool {
	if len(roles) > 1 {
		return true
	}
	for _, r := range roles {
		r = strings.ToLower(strings.TrimSpace(r))
		switch r {
		case "api", "ingest", "worker", "scheduler":
			return true
		}
	}
	return false
}

func RunPreflight(ctx context.Context, controlDSN, telemetryDSN string, backend telemetrybridge.Backend) (*PreflightReport, error) {
	report := &PreflightReport{
		ControlDSN:             redactDSN(controlDSN),
		TelemetryDSN:           redactDSN(telemetryDSN),
		TelemetryBackend:       string(backend),
		ControlTargetVersion:   len(postgrescontrol.AllMigrations()),
		TelemetryTargetVersion: len(telemetrybridge.Migrations(backend)),
	}
	if strings.TrimSpace(controlDSN) == "" {
		return nil, fmt.Errorf("control DSN is required")
	}
	if strings.TrimSpace(telemetryDSN) == "" {
		return nil, fmt.Errorf("telemetry DSN is required")
	}
	if backend == "" {
		return nil, fmt.Errorf("telemetry backend is required")
	}
	controlDB, err := openPing(ctx, controlDSN)
	if err != nil {
		report.Checks = append(report.Checks, PreflightCheck{Name: "control-plane", OK: false, Detail: err.Error()})
		return report, nil
	}
	_ = controlDB.Close()
	report.Checks = append(report.Checks, PreflightCheck{Name: "control-plane", OK: true, Detail: "reachable"})

	telemetryDB, err := openPing(ctx, telemetryDSN)
	if err != nil {
		report.Checks = append(report.Checks, PreflightCheck{Name: "telemetry-bridge", OK: false, Detail: err.Error()})
		return report, nil
	}
	_ = telemetryDB.Close()
	report.Checks = append(report.Checks, PreflightCheck{Name: "telemetry-bridge", OK: true, Detail: "reachable"})
	report.Checks = append(report.Checks, BuildSecurityReport(os.Getenv("URGENTRY_ENV"), controlDSN, telemetryDSN).Checks...)
	return report, nil
}

func BuildSecurityReport(env, controlDSN, telemetryDSN string) *SecurityReport {
	report := &SecurityReport{
		Environment: strings.TrimSpace(env),
	}
	if report.Environment == "" {
		report.Environment = "production"
	}
	bootstrapPassword := strings.TrimSpace(os.Getenv("URGENTRY_BOOTSTRAP_PASSWORD"))
	bootstrapPAT := strings.TrimSpace(os.Getenv("URGENTRY_BOOTSTRAP_PAT"))
	metricsToken := strings.TrimSpace(os.Getenv("URGENTRY_METRICS_TOKEN"))
	postgresPassword := firstNonEmpty(
		strings.TrimSpace(os.Getenv("POSTGRES_PASSWORD")),
		dsnPassword(controlDSN),
		dsnPassword(telemetryDSN),
	)
	minioPassword := firstNonEmpty(
		strings.TrimSpace(os.Getenv("MINIO_ROOT_PASSWORD")),
		strings.TrimSpace(os.Getenv("URGENTRY_S3_SECRET_KEY")),
	)

	report.Checks = append(report.Checks,
		secretCheck("bootstrap-password", bootstrapPassword),
		secretCheck("bootstrap-pat", bootstrapPAT),
		secretCheck("metrics-token", metricsToken),
		secretCheck("postgres-password", postgresPassword),
		secretCheck("minio-root-password", minioPassword),
	)
	return report
}

func RotateBootstrapAccess(ctx context.Context, controlDSN, email, password, pat string) (*BootstrapRotationResult, error) {
	db, err := openPing(ctx, controlDSN)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	result, err := postgrescontrol.NewAuthStore(db).RotateBootstrapAccess(ctx, email, password, pat)
	if err != nil {
		return nil, err
	}

	tokenPrefix := ""
	if result != nil {
		tokenPrefix = patPrefix(result.PAT)
		email = result.Email
	}
	return &BootstrapRotationResult{
		Email:       email,
		TokenPrefix: tokenPrefix,
		Rotated:     true,
		ControlDSN:  redactDSN(controlDSN),
	}, nil
}

func MigrateControl(ctx context.Context, dsn string) (*Status, error) {
	db, err := openPing(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := postgrescontrol.Migrate(ctx, db); err != nil {
		return nil, err
	}
	return statusFromControl(ctx, db, telemetrybridge.BackendPostgres)
}

func MigrateTelemetry(ctx context.Context, dsn string, backend telemetrybridge.Backend) (*Status, error) {
	db, err := telemetrybridge.Open(ctx, dsn)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if err := telemetrybridge.Migrate(ctx, db, backend); err != nil {
		return nil, err
	}
	return statusFromTelemetry(ctx, db, backend)
}

func LoadStatus(ctx context.Context, controlDSN, telemetryDSN string, backend telemetrybridge.Backend) (*Status, error) {
	controlDB, err := openPing(ctx, controlDSN)
	if err != nil {
		return nil, err
	}
	defer controlDB.Close()
	telemetryDB, err := telemetrybridge.Open(ctx, telemetryDSN)
	if err != nil {
		return nil, err
	}
	defer telemetryDB.Close()

	controlVersion, err := controlVersion(ctx, controlDB)
	if err != nil {
		return nil, err
	}
	telemetryVersion, err := telemetrybridge.CurrentVersion(ctx, telemetryDB)
	if err != nil {
		return nil, err
	}
	return &Status{
		TelemetryBackend:       string(backend),
		ControlVersion:         controlVersion,
		ControlTargetVersion:   len(postgrescontrol.AllMigrations()),
		TelemetryVersion:       telemetryVersion,
		TelemetryTargetVersion: len(telemetrybridge.Migrations(backend)),
	}, nil
}

func BuildRollbackPlan(currentControl, targetControl, currentTelemetry, targetTelemetry int, backend telemetrybridge.Backend) (*RollbackPlan, error) {
	if targetControl < 0 || targetTelemetry < 0 {
		return nil, fmt.Errorf("target versions must be non-negative")
	}
	if targetControl > currentControl {
		return nil, fmt.Errorf("target control version cannot exceed current control version")
	}
	if targetTelemetry > currentTelemetry {
		return nil, fmt.Errorf("target telemetry version cannot exceed current telemetry version")
	}
	return &RollbackPlan{
		TelemetryBackend:      string(backend),
		CurrentControlVersion: currentControl,
		TargetControlVersion:  targetControl,
		CurrentTelemetryVer:   currentTelemetry,
		TargetTelemetryVer:    targetTelemetry,
		Steps: []string{
			"Capture fresh Postgres control-plane and telemetry backups before changing application images.",
			"Scale API, ingest, worker, and scheduler roles down or drain them before restoring older schemas.",
			"Restore the control-plane backup that matches target control version " + fmt.Sprintf("%d", targetControl) + ".",
			"Restore the telemetry backup that matches target telemetry version " + fmt.Sprintf("%d", targetTelemetry) + " for backend " + string(backend) + ".",
			"Redeploy the application bundle that expects those schema versions, then rerun self-hosted preflight and smoke checks.",
		},
	}, nil
}

func BuildBackupPlan(blobBackend, asyncBackend, cacheBackend string, backend telemetrybridge.Backend) (*BackupPlan, error) {
	blobBackend = strings.ToLower(strings.TrimSpace(blobBackend))
	asyncBackend = strings.ToLower(strings.TrimSpace(asyncBackend))
	cacheBackend = strings.ToLower(strings.TrimSpace(cacheBackend))
	if blobBackend == "" {
		blobBackend = "s3"
	}
	if asyncBackend == "" {
		asyncBackend = "jetstream"
	}
	if cacheBackend == "" {
		cacheBackend = "valkey"
	}
	switch blobBackend {
	case "file", "s3":
	default:
		return nil, fmt.Errorf("unsupported blob backend %q", blobBackend)
	}
	switch asyncBackend {
	case "sqlite", "jetstream":
	default:
		return nil, fmt.Errorf("unsupported async backend %q", asyncBackend)
	}
	switch cacheBackend {
	case "sqlite", "valkey":
	default:
		return nil, fmt.Errorf("unsupported cache backend %q", cacheBackend)
	}

	plan := &BackupPlan{
		TelemetryBackend: string(backend),
		BlobBackend:      blobBackend,
		AsyncBackend:     asyncBackend,
		CacheBackend:     cacheBackend,
		Expectation: RecoveryExpectation{
			RecoveryPointObjective: "bounded by the latest completed backup capture",
			RecoveryTimeObjective:  "one cold-stack restore plus preflight and smoke validation",
			PointInTimeBoundary:    "snapshot restore only; no WAL or binlog PITR is shipped for the current bundle",
			PointInTimeRecovery:    false,
			Proof:                  "deploy/compose/backup-restore-drill.sh on the serious self-hosted bundle",
		},
		Artifacts: []BackupArtifact{
			{
				Name:     "postgres.sql.gz",
				Required: true,
				Capture:  "logical pg_dump from the serious self-hosted Postgres service",
				Restore:  "restore into a clean Postgres database before bringing Urgentry roles back up",
			},
			{
				Name:     "urgentry-data.tar.gz",
				Required: true,
				Capture:  "archive the shared urgentry_data volume that still holds SQLite runtime state",
				Restore:  "restore onto an empty urgentry_data volume before starting the app roles",
			},
		},
		Steps: []string{
			"Capture fresh backup metadata with self-hosted preflight, status, and backup-plan output.",
			"Capture the Postgres logical dump before destroying or downgrading any serious self-hosted services.",
			"Archive every persisted volume still used by the shipped serious self-hosted bundle.",
			"During restore, repopulate the archived volumes first, then restore Postgres, then rerun preflight, status, and smoke validation.",
		},
		Drill: []string{
			"Seed deterministic pre-backup data and a queued backlog item.",
			"Capture the backup set while the stack is live.",
			"Introduce post-backup divergence so the restore point can be proven.",
			"Destroy the stack volumes, restore from the captured backup set, and rerun smoke validation.",
			"Verify the pre-backup event and attachment returned, the queued backlog item drains after restore, and post-backup divergence is absent.",
		},
	}
	if blobBackend == "s3" {
		plan.Artifacts = append(plan.Artifacts, BackupArtifact{
			Name:     "minio-data.tar.gz",
			Required: true,
			Capture:  "archive the MinIO backing volume that stores blob bodies for artifacts and attachments",
			Restore:  "restore onto an empty MinIO volume before starting MinIO",
		})
	}
	if asyncBackend == "jetstream" {
		plan.Artifacts = append(plan.Artifacts, BackupArtifact{
			Name:     "nats-data.tar.gz",
			Required: true,
			Capture:  "archive the NATS JetStream volume that holds durable async backlog state",
			Restore:  "restore onto an empty NATS volume before starting the broker",
		})
	}
	if cacheBackend == "valkey" {
		plan.Artifacts = append(plan.Artifacts, BackupArtifact{
			Name:     "valkey-data.tar.gz",
			Required: false,
			Capture:  "archive the Valkey volume only when persistence is enabled for cache warm state",
			Restore:  "restore onto an empty Valkey volume before starting Valkey if warm state matters",
			Reason:   "leases, quotas, and query guard windows can cold-start safely in the shipped bundle",
		})
	}
	return plan, nil
}

func VerifyBackupSet(backupDir string, backend telemetrybridge.Backend, strictTargetMatch bool) (*BackupVerificationReport, error) {
	backupDir = strings.TrimSpace(backupDir)
	if backupDir == "" {
		return nil, fmt.Errorf("backup dir is required")
	}
	if backend == "" {
		return nil, fmt.Errorf("telemetry backend is required")
	}
	manifestPath := filepath.Join(backupDir, "manifest.json")
	payload, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read backup manifest: %w", err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(payload, &manifest); err != nil {
		return nil, fmt.Errorf("decode backup manifest: %w", err)
	}

	if manifest.SchemaVersion != 0 && manifest.SchemaVersion != 1 {
		return nil, fmt.Errorf("unsupported backup manifest schema version %d", manifest.SchemaVersion)
	}
	if manifest.Preflight.TelemetryBackend != "" && manifest.Preflight.TelemetryBackend != string(backend) {
		return nil, fmt.Errorf("backup telemetry backend %q does not match requested backend %q", manifest.Preflight.TelemetryBackend, backend)
	}
	if manifest.Status.TelemetryBackend != "" && manifest.Status.TelemetryBackend != string(backend) {
		return nil, fmt.Errorf("backup status telemetry backend %q does not match requested backend %q", manifest.Status.TelemetryBackend, backend)
	}
	if strictTargetMatch {
		if manifest.Preflight.ControlTargetVersion != 0 && manifest.Preflight.ControlTargetVersion != len(postgrescontrol.AllMigrations()) {
			return nil, fmt.Errorf("backup control target version %d does not match current binary target %d", manifest.Preflight.ControlTargetVersion, len(postgrescontrol.AllMigrations()))
		}
		if manifest.Preflight.TelemetryTargetVersion != 0 && manifest.Preflight.TelemetryTargetVersion != len(telemetrybridge.Migrations(backend)) {
			return nil, fmt.Errorf("backup telemetry target version %d does not match current binary target %d", manifest.Preflight.TelemetryTargetVersion, len(telemetrybridge.Migrations(backend)))
		}
	}

	required := map[string]bool{
		"compose.env":      true,
		"preflight.json":   true,
		"status.json":      true,
		"backup-plan.json": true,
	}
	for _, artifact := range manifest.BackupPlan.Artifacts {
		required[artifact.Name] = artifact.Required
	}

	fileNames := map[string]struct{}{}
	for _, name := range manifest.Files {
		fileNames[strings.TrimSpace(name)] = struct{}{}
	}
	for name := range required {
		fileNames[name] = struct{}{}
	}

	integrity := make(map[string]BackupFileIntegrity, len(manifest.Integrity))
	for _, item := range manifest.Integrity {
		integrity[strings.TrimSpace(item.Name)] = item
	}

	names := make([]string, 0, len(fileNames))
	for name := range fileNames {
		if name != "" {
			names = append(names, name)
		}
	}
	slices.Sort(names)

	report := &BackupVerificationReport{
		BackupDir:         backupDir,
		TelemetryBackend:  string(backend),
		StrictTargetMatch: strictTargetMatch,
		Verified:          true,
		Files:             make([]BackupVerificationFile, 0, len(names)),
	}
	if !strictTargetMatch {
		report.Warnings = append(report.Warnings, "target schema validation disabled; restore must still use a binary compatible with the captured backup")
	}
	for _, name := range names {
		item := BackupVerificationFile{
			Name:     name,
			Required: required[name],
		}
		fullPath := filepath.Join(backupDir, name)
		info, statErr := os.Stat(fullPath)
		if statErr != nil {
			item.Detail = statErr.Error()
			report.Files = append(report.Files, item)
			if item.Required {
				report.Verified = false
			}
			continue
		}
		if info.IsDir() {
			item.Detail = "expected file, found directory"
			report.Files = append(report.Files, item)
			if item.Required {
				report.Verified = false
			}
			continue
		}
		sum, size, err := fileSHA256(fullPath)
		if err != nil {
			item.Detail = err.Error()
			report.Files = append(report.Files, item)
			report.Verified = false
			continue
		}
		item.Present = true
		item.Bytes = size
		item.SHA256 = sum
		if expected, ok := integrity[name]; ok {
			item.ExpectedBytes = expected.Bytes
			item.ExpectedSHA256 = expected.SHA256
			item.Verified = expected.Bytes == size && expected.SHA256 == sum
			if !item.Verified {
				item.Detail = "checksum mismatch"
				report.Verified = false
			}
		} else {
			item.Verified = !item.Required
			if item.Required {
				item.Detail = "missing integrity entry"
				report.Verified = false
			}
		}
		report.Files = append(report.Files, item)
	}
	return report, nil
}

func ParseTelemetryBackend(raw string) (telemetrybridge.Backend, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(telemetrybridge.BackendPostgres):
		return telemetrybridge.BackendPostgres, nil
	case string(telemetrybridge.BackendTimescale):
		return telemetrybridge.BackendTimescale, nil
	default:
		return "", fmt.Errorf("unsupported telemetry backend %q", raw)
	}
}

func dsnPassword(dsn string) string {
	parsed, err := url.Parse(strings.TrimSpace(dsn))
	if err != nil || parsed.User == nil {
		return ""
	}
	password, _ := parsed.User.Password()
	return strings.TrimSpace(password)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func patPrefix(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if idx := strings.IndexByte(raw, '_'); idx > 0 {
		return raw[:idx]
	}
	if len(raw) <= 4 {
		return raw
	}
	return raw[:4]
}

func secretCheck(name, value string) PreflightCheck {
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		return PreflightCheck{Name: name, OK: false, Detail: "missing"}
	case slices.Contains(defaultInsecureValues, value):
		return PreflightCheck{Name: name, OK: false, Detail: "placeholder or insecure default"}
	case strings.HasPrefix(value, "REPLACE_ME"):
		return PreflightCheck{Name: name, OK: false, Detail: "placeholder — replace with a real secret"}
	default:
		return PreflightCheck{Name: name, OK: true, Detail: "configured"}
	}
}

var defaultInsecureValues = []string{
	"change-me-in-production",
	"SeriousSelfHosted!123",
	"gpat_self_hosted_bootstrap",
	"gpat_self_hosted_smoke",
	"gpat_self_hosted_eval",
	"gpat_self_hosted_perf",
	"gpat_self_hosted_drill",
	"minio123secret",
	"urgentry",
}

func controlVersion(ctx context.Context, db *sql.DB) (int, error) {
	if _, err := db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS _control_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
)`); err != nil {
		return 0, fmt.Errorf("create control migrations table: %w", err)
	}
	var version int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM _control_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("query control migration version: %w", err)
	}
	return version, nil
}

func statusFromControl(ctx context.Context, db *sql.DB, backend telemetrybridge.Backend) (*Status, error) {
	controlVersion, err := controlVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	return &Status{
		TelemetryBackend:       string(backend),
		ControlVersion:         controlVersion,
		ControlTargetVersion:   len(postgrescontrol.AllMigrations()),
		TelemetryTargetVersion: len(telemetrybridge.Migrations(backend)),
	}, nil
}

func statusFromTelemetry(ctx context.Context, db *sql.DB, backend telemetrybridge.Backend) (*Status, error) {
	telemetryVersion, err := telemetrybridge.CurrentVersion(ctx, db)
	if err != nil {
		return nil, err
	}
	return &Status{
		TelemetryBackend:       string(backend),
		ControlTargetVersion:   len(postgrescontrol.AllMigrations()),
		TelemetryVersion:       telemetryVersion,
		TelemetryTargetVersion: len(telemetrybridge.Migrations(backend)),
	}, nil
}

func openPing(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	return db, nil
}

func redactDSN(dsn string) string {
	if strings.TrimSpace(dsn) == "" {
		return ""
	}
	if at := strings.Index(dsn, "@"); at > 0 {
		if scheme := strings.Index(dsn, "://"); scheme > 0 {
			return dsn[:scheme+3] + "****" + dsn[at:]
		}
	}
	return dsn
}

func fileSHA256(path string) (string, int64, error) {
	fh, err := os.Open(path)
	if err != nil {
		return "", 0, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer fh.Close()

	hasher := sha256.New()
	size, err := io.Copy(hasher, fh)
	if err != nil {
		return "", 0, fmt.Errorf("hash %s: %w", filepath.Base(path), err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), size, nil
}
