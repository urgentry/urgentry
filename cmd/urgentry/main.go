package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/app"
	"urgentry/internal/config"
	"urgentry/internal/logging"
	"urgentry/internal/selfhostedops"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
)

// version returns the embedded build version for backward compatibility.
func version() string { return config.Version }

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "version":
		fmt.Println(config.GetVersionInfo())
	case "self-hosted":
		runSelfHosted(os.Args[2:])
	default:
		usage()
	}
}

func serve(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	role := fs.String("role", "all", "runtime role: api|ingest|worker|scheduler|all")
	addr := fs.String("addr", "", "override bind address")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse flags")
	}

	cfg := config.Load()
	logging.Setup(cfg.Env)

	log.Info().Str("version", version()).Msg("urgentry starting")

	if *addr != "" {
		cfg.HTTPAddr = *addr
	}

	roleValue, err := app.ParseRole(*role)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid role")
	}

	if err := app.Run(cfg, roleValue, app.WithVersion(version())); err != nil {
		log.Fatal().Err(err).Msg("run failed")
	}
}

func runSelfHosted(args []string) {
	if len(args) == 0 {
		selfHostedUsage()
	}
	switch args[0] {
	case "preflight":
		runSelfHostedPreflight(args[1:])
	case "status":
		runSelfHostedStatus(args[1:])
	case "migrate-control":
		runSelfHostedMigrateControl(args[1:])
	case "migrate-telemetry":
		runSelfHostedMigrateTelemetry(args[1:])
	case "backup-plan":
		runSelfHostedBackupPlan(args[1:])
	case "security-report":
		runSelfHostedSecurityReport(args[1:])
	case "rotate-bootstrap":
		runSelfHostedRotateBootstrap(args[1:])
	case "verify-backup":
		runSelfHostedVerifyBackup(args[1:])
	case "rollback-plan":
		runSelfHostedRollbackPlan(args[1:])
	case "maintenance-status":
		runSelfHostedMaintenanceStatus(args[1:])
	case "enter-maintenance":
		runSelfHostedEnterMaintenance(args[1:])
	case "leave-maintenance":
		runSelfHostedLeaveMaintenance(args[1:])
	case "record-action":
		runSelfHostedRecordAction(args[1:])
	default:
		selfHostedUsage()
	}
}

type selfHostedFlags struct {
	controlDSN   string
	telemetryDSN string
	backend      telemetrybridge.Backend
}

func parseSelfHostedFlags(name string, args []string) selfHostedFlags {
	fs := flag.NewFlagSet("self-hosted "+name, flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	telemetryDSN := fs.String("telemetry-dsn", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted telemetry bridge")
	backendRaw := fs.String("telemetry-backend", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_BACKEND"), "postgres"), "telemetry backend: postgres|timescale")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msgf("parse self-hosted %s flags", name)
	}
	backend, err := selfhostedops.ParseTelemetryBackend(*backendRaw)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid telemetry backend")
	}
	return selfHostedFlags{controlDSN: *controlDSN, telemetryDSN: *telemetryDSN, backend: backend}
}

func runSelfHostedPreflight(args []string) {
	f := parseSelfHostedFlags("preflight", args)
	report, err := selfhostedops.RunPreflight(context.Background(), f.controlDSN, f.telemetryDSN, f.backend)
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted preflight failed")
	}
	writeJSON(report)
}

func runSelfHostedStatus(args []string) {
	f := parseSelfHostedFlags("status", args)
	status, err := selfhostedops.LoadStatus(context.Background(), f.controlDSN, f.telemetryDSN, f.backend)
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted status failed")
	}
	writeJSON(status)
}

func runSelfHostedMigrateControl(args []string) {
	fs := flag.NewFlagSet("self-hosted migrate-control", flag.ExitOnError)
	controlDSN := fs.String("dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted migrate-control flags")
	}
	status, err := selfhostedops.MigrateControl(context.Background(), *controlDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("control migration failed")
	}
	writeJSON(status)
}

func runSelfHostedMigrateTelemetry(args []string) {
	fs := flag.NewFlagSet("self-hosted migrate-telemetry", flag.ExitOnError)
	telemetryDSN := fs.String("dsn", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted telemetry bridge")
	backendRaw := fs.String("telemetry-backend", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_BACKEND"), "postgres"), "telemetry backend: postgres|timescale")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted migrate-telemetry flags")
	}
	backend, err := selfhostedops.ParseTelemetryBackend(*backendRaw)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid telemetry backend")
	}
	status, err := selfhostedops.MigrateTelemetry(context.Background(), *telemetryDSN, backend)
	if err != nil {
		log.Fatal().Err(err).Msg("telemetry migration failed")
	}
	writeJSON(status)
}

func runSelfHostedRollbackPlan(args []string) {
	fs := flag.NewFlagSet("self-hosted rollback-plan", flag.ExitOnError)
	currentControl := fs.Int("current-control-version", 0, "current applied control-plane migration version")
	targetControl := fs.Int("target-control-version", 0, "target control-plane migration version after rollback")
	currentTelemetry := fs.Int("current-telemetry-version", 0, "current applied telemetry migration version")
	targetTelemetry := fs.Int("target-telemetry-version", 0, "target telemetry migration version after rollback")
	backendRaw := fs.String("telemetry-backend", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_BACKEND"), "postgres"), "telemetry backend: postgres|timescale")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted rollback-plan flags")
	}
	backend, err := selfhostedops.ParseTelemetryBackend(*backendRaw)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid telemetry backend")
	}
	plan, err := selfhostedops.BuildRollbackPlan(*currentControl, *targetControl, *currentTelemetry, *targetTelemetry, backend)
	if err != nil {
		log.Fatal().Err(err).Msg("build rollback plan failed")
	}
	writeJSON(plan)
}

func runSelfHostedMaintenanceStatus(args []string) {
	fs := flag.NewFlagSet("self-hosted maintenance-status", flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted maintenance-status flags")
	}
	status, err := selfhostedops.LoadMaintenanceStatus(context.Background(), *controlDSN)
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted maintenance-status failed")
	}
	writeJSON(status)
}

func runSelfHostedEnterMaintenance(args []string) {
	fs := flag.NewFlagSet("self-hosted enter-maintenance", flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	reason := fs.String("reason", "", "operator reason for the maintenance window")
	actor := fs.String("actor", firstNonEmpty(os.Getenv("URGENTRY_OPERATOR_ACTOR"), os.Getenv("USER"), "system"), "operator identity written into the install audit ledger")
	source := fs.String("source", firstNonEmpty(os.Getenv("URGENTRY_OPERATOR_SOURCE"), "cli"), "operator source written into the install audit ledger")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted enter-maintenance flags")
	}
	status, err := selfhostedops.EnterMaintenance(context.Background(), *controlDSN, *reason, *actor, *source, time.Now().UTC())
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted enter-maintenance failed")
	}
	writeJSON(status)
}

func runSelfHostedLeaveMaintenance(args []string) {
	fs := flag.NewFlagSet("self-hosted leave-maintenance", flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	actor := fs.String("actor", firstNonEmpty(os.Getenv("URGENTRY_OPERATOR_ACTOR"), os.Getenv("USER"), "system"), "operator identity written into the install audit ledger")
	source := fs.String("source", firstNonEmpty(os.Getenv("URGENTRY_OPERATOR_SOURCE"), "cli"), "operator source written into the install audit ledger")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted leave-maintenance flags")
	}
	status, err := selfhostedops.LeaveMaintenance(context.Background(), *controlDSN, *actor, *source, time.Now().UTC())
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted leave-maintenance failed")
	}
	writeJSON(status)
}

func runSelfHostedRecordAction(args []string) {
	fs := flag.NewFlagSet("self-hosted record-action", flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	organizationID := fs.String("organization-id", "", "optional organization id for scoped operator actions")
	projectID := fs.String("project-id", "", "optional project id for scoped operator actions")
	action := fs.String("action", "", "operator action name to record")
	status := fs.String("status", "succeeded", "operator action status")
	source := fs.String("source", firstNonEmpty(os.Getenv("URGENTRY_OPERATOR_SOURCE"), "cli"), "operator source written into the install audit ledger")
	actor := fs.String("actor", firstNonEmpty(os.Getenv("URGENTRY_OPERATOR_ACTOR"), os.Getenv("USER"), "system"), "operator identity written into the install audit ledger")
	detail := fs.String("detail", "", "optional human-readable operator action detail")
	metadata := fs.String("metadata", "{}", "optional JSON metadata object")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted record-action flags")
	}
	receipt, err := selfhostedops.RecordOperatorAction(context.Background(), *controlDSN, store.OperatorAuditRecord{
		OrganizationID: *organizationID,
		ProjectID:      *projectID,
		Action:         *action,
		Status:         *status,
		Source:         *source,
		Actor:          *actor,
		Detail:         *detail,
		MetadataJSON:   *metadata,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted record-action failed")
	}
	writeJSON(receipt)
}

func runSelfHostedBackupPlan(args []string) {
	fs := flag.NewFlagSet("self-hosted backup-plan", flag.ExitOnError)
	backendRaw := fs.String("telemetry-backend", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_BACKEND"), "postgres"), "telemetry backend: postgres|timescale")
	blobBackend := fs.String("blob-backend", firstNonEmpty(os.Getenv("URGENTRY_BLOB_BACKEND"), "s3"), "blob backend: file|s3")
	asyncBackend := fs.String("async-backend", firstNonEmpty(os.Getenv("URGENTRY_ASYNC_BACKEND"), "jetstream"), "async backend: sqlite|jetstream")
	cacheBackend := fs.String("cache-backend", firstNonEmpty(os.Getenv("URGENTRY_CACHE_BACKEND"), "valkey"), "cache backend: sqlite|valkey")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted backup-plan flags")
	}
	backend, err := selfhostedops.ParseTelemetryBackend(*backendRaw)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid telemetry backend")
	}
	plan, err := selfhostedops.BuildBackupPlan(*blobBackend, *asyncBackend, *cacheBackend, backend)
	if err != nil {
		log.Fatal().Err(err).Msg("build backup plan failed")
	}
	writeJSON(plan)
}

func runSelfHostedSecurityReport(args []string) {
	fs := flag.NewFlagSet("self-hosted security-report", flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	telemetryDSN := fs.String("telemetry-dsn", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted telemetry bridge")
	env := fs.String("env", firstNonEmpty(os.Getenv("URGENTRY_ENV"), "production"), "environment name used for serious self-hosted secret policy")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted security-report flags")
	}
	report := selfhostedops.BuildSecurityReport(*env, *controlDSN, *telemetryDSN)
	writeJSON(report)
	for _, check := range report.Checks {
		if !check.OK {
			log.Fatal().Msg("security report failed")
		}
	}
}

func runSelfHostedRotateBootstrap(args []string) {
	fs := flag.NewFlagSet("self-hosted rotate-bootstrap", flag.ExitOnError)
	controlDSN := fs.String("control-dsn", firstNonEmpty(os.Getenv("URGENTRY_CONTROL_DATABASE_URL"), os.Getenv("URGENTRY_DATABASE_URL")), "Postgres DSN for the serious self-hosted control plane")
	email := fs.String("email", firstNonEmpty(os.Getenv("URGENTRY_BOOTSTRAP_EMAIL"), "admin@urgentry.local"), "bootstrap owner email")
	password := fs.String("password", os.Getenv("URGENTRY_BOOTSTRAP_PASSWORD"), "bootstrap owner password")
	pat := fs.String("pat", os.Getenv("URGENTRY_BOOTSTRAP_PAT"), "bootstrap personal access token")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted rotate-bootstrap flags")
	}
	result, err := selfhostedops.RotateBootstrapAccess(context.Background(), *controlDSN, *email, *password, *pat)
	if err != nil {
		log.Fatal().Err(err).Msg("self-hosted rotate-bootstrap failed")
	}
	writeJSON(result)
}

func runSelfHostedVerifyBackup(args []string) {
	fs := flag.NewFlagSet("self-hosted verify-backup", flag.ExitOnError)
	backupDir := fs.String("dir", "", "directory containing a backup manifest and captured artifacts")
	backendRaw := fs.String("telemetry-backend", firstNonEmpty(os.Getenv("URGENTRY_TELEMETRY_BACKEND"), "postgres"), "telemetry backend: postgres|timescale")
	strictTargetMatch := fs.Bool("strict-target-match", true, "require the backup target schema versions to match the current binary targets")
	if err := fs.Parse(args); err != nil {
		log.Fatal().Err(err).Msg("parse self-hosted verify-backup flags")
	}
	backend, err := selfhostedops.ParseTelemetryBackend(*backendRaw)
	if err != nil {
		log.Fatal().Err(err).Msg("invalid telemetry backend")
	}
	report, err := selfhostedops.VerifyBackupSet(*backupDir, backend, *strictTargetMatch)
	if err != nil {
		log.Fatal().Err(err).Msg("verify backup failed")
	}
	writeJSON(report)
	if !report.Verified {
		log.Fatal().Msg("backup verification failed")
	}
}

func writeJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		log.Fatal().Err(err).Msg("encode json")
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func selfHostedUsage() {
	fmt.Fprintf(os.Stderr, "usage: urgentry self-hosted <command> [args]\n\n")
	fmt.Fprintf(os.Stderr, "commands:\n")
	fmt.Fprintf(os.Stderr, "  preflight          Validate control-plane and telemetry DSNs and print target versions\n")
	fmt.Fprintf(os.Stderr, "  status             Print applied vs target control-plane and telemetry versions\n")
	fmt.Fprintf(os.Stderr, "  migrate-control    Apply serious self-hosted control-plane migrations\n")
	fmt.Fprintf(os.Stderr, "  migrate-telemetry  Apply serious self-hosted telemetry bridge migrations\n")
	fmt.Fprintf(os.Stderr, "  backup-plan        Print the serious self-hosted backup and disaster-recovery contract\n")
	fmt.Fprintf(os.Stderr, "  security-report    Validate serious self-hosted secret hygiene and metrics protection\n")
	fmt.Fprintf(os.Stderr, "  rotate-bootstrap   Rotate the live bootstrap password and PAT to match the current secret set\n")
	fmt.Fprintf(os.Stderr, "  verify-backup      Verify a captured serious self-hosted backup set against its manifest\n")
	fmt.Fprintf(os.Stderr, "  rollback-plan      Print the documented rollback procedure for target schema versions\n")
	fmt.Fprintf(os.Stderr, "  maintenance-status Show install-wide maintenance state and drain guidance\n")
	fmt.Fprintf(os.Stderr, "  enter-maintenance  Freeze writes and start the documented drain workflow\n")
	fmt.Fprintf(os.Stderr, "  leave-maintenance  Reopen writes after operator work completes\n")
	fmt.Fprintf(os.Stderr, "  record-action      Write an operator action into the install audit ledger\n")
	os.Exit(2)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: urgentry <command> [args]\n\n")
	fmt.Fprintf(os.Stderr, "commands:\n")
	fmt.Fprintf(os.Stderr, "  serve    Start the server (--role=all|api|ingest|worker|scheduler)\n")
	fmt.Fprintf(os.Stderr, "  self-hosted  Run serious self-hosted migration and rollback tooling\n")
	fmt.Fprintf(os.Stderr, "  version  Print version and exit\n")
	os.Exit(2)
}
