package app

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"urgentry/internal/attachment"
	"urgentry/internal/config"
	"urgentry/internal/postgrescontrol"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/testpostgres"

	_ "github.com/jackc/pgx/v5/stdlib"
)

func TestWithVersion(t *testing.T) {
	var opts runOptions
	WithVersion("test-version")(&opts)
	if opts.version != "test-version" {
		t.Fatalf("version = %q, want test-version", opts.version)
	}
}

func TestParseRole(t *testing.T) {
	tests := []struct {
		input   string
		want    Role
		wantErr bool
	}{
		{input: "all", want: RoleAll},
		{input: "API", want: RoleAPI},
		{input: " ingest ", want: RoleIngest},
		{input: "worker", want: RoleWorker},
		{input: "scheduler", want: RoleScheduler},
		{input: "invalid", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseRole(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("role = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewRoleMode(t *testing.T) {
	tests := []struct {
		role          Role
		wantMountsAPI bool
		wantWorker    bool
		wantScheduler bool
	}{
		{role: RoleAll, wantMountsAPI: true, wantWorker: true, wantScheduler: true},
		{role: RoleAPI, wantMountsAPI: true},
		{role: RoleIngest},
		{role: RoleWorker, wantWorker: true},
		{role: RoleScheduler, wantScheduler: true},
	}

	for _, tt := range tests {
		t.Run(string(tt.role), func(t *testing.T) {
			got := newRoleMode(tt.role)
			if got.mountsAPI != tt.wantMountsAPI || got.runsWorker != tt.wantWorker || got.runsScheduler != tt.wantScheduler {
				t.Fatalf("newRoleMode(%q) = %+v", tt.role, got)
			}
		})
	}
}

func TestRunBootstrapsOnlyRequiredRoles(t *testing.T) {
	tests := []struct {
		name      string
		role      Role
		wantUsers int
		wantKeys  int
		wantRules int
	}{
		{name: "api", role: RoleAPI, wantUsers: 1, wantKeys: 1, wantRules: 1},
		{name: "ingest", role: RoleIngest, wantUsers: 0, wantKeys: 0, wantRules: 0},
		{name: "worker", role: RoleWorker, wantUsers: 0, wantKeys: 0, wantRules: 0},
		{name: "scheduler", role: RoleScheduler, wantUsers: 0, wantKeys: 0, wantRules: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			cfg := config.Config{
				Env:               "test",
				HTTPAddr:          "bad-address",
				DataDir:           dataDir,
				BootstrapEmail:    "owner@example.com",
				BootstrapPassword: "test-password-123",
				BootstrapPAT:      "gpat_run_test_token",
				PipelineQueueSize: 1,
				PipelineWorkers:   1,
			}

			err := Run(cfg, tt.role, WithVersion("test"))
			if err == nil || !strings.Contains(err.Error(), "http server") {
				t.Fatalf("Run error = %v, want wrapped http server error", err)
			}

			db, err := sqlite.Open(dataDir)
			if err != nil {
				t.Fatalf("sqlite.Open: %v", err)
			}
			t.Cleanup(func() { db.Close() })

			assertCount(t, db, "SELECT COUNT(*) FROM users", tt.wantUsers)
			assertCount(t, db, "SELECT COUNT(*) FROM project_keys", tt.wantKeys)
			assertCount(t, db, "SELECT COUNT(*) FROM alert_rules", tt.wantRules)
		})
	}
}

func TestRuntimeStateNewHTTPServerReturnsValidationError(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	state := &runtimeState{
		cfg: config.Config{
			Env:               "test",
			HTTPAddr:          ":0",
			SessionCookieName: "urgentry_session",
			CSRFCookieName:    "urgentry_csrf",
		},
		role:    RoleAPI,
		dataDir: dataDir,
		db:      db,
	}

	_, err = state.newHTTPServer()
	if err == nil || !strings.Contains(err.Error(), "auth store") {
		t.Fatalf("newHTTPServer error = %v, want auth store validation error", err)
	}
}

func TestBuildSQLiteRuntimeControlPlaneUsesSQLiteStores(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	control := buildSQLiteRuntimeControlPlane(db)
	if _, ok := control.keyStore.(*sqlite.KeyStore); !ok {
		t.Fatalf("keyStore type = %T, want *sqlite.KeyStore", control.keyStore)
	}
	if _, ok := control.authStore.(*sqlite.AuthStore); !ok {
		t.Fatalf("authStore type = %T, want *sqlite.AuthStore", control.authStore)
	}
	if _, ok := control.alertStore.(*sqlite.AlertStore); !ok {
		t.Fatalf("alertStore type = %T, want *sqlite.AlertStore", control.alertStore)
	}
	if _, ok := control.monitorStore.(*sqlite.MonitorStore); !ok {
		t.Fatalf("monitorStore type = %T, want *sqlite.MonitorStore", control.monitorStore)
	}
	if _, ok := control.lifecycle.(*sqlite.LifecycleStore); !ok {
		t.Fatalf("lifecycle type = %T, want *sqlite.LifecycleStore", control.lifecycle)
	}
	if _, err := control.defaultKey(context.Background()); err != nil {
		t.Fatalf("defaultKey: %v", err)
	}
	bootstrap, err := control.bootstrap(context.Background(), config.Config{
		BootstrapEmail:    "owner@example.com",
		BootstrapPassword: "test-password-123",
		BootstrapPAT:      "gpat_sqlite_runtime_bootstrap",
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if bootstrap == nil || !bootstrap.Created {
		t.Fatalf("bootstrap = %+v, want created result", bootstrap)
	}
	assertCount(t, db, "SELECT COUNT(*) FROM users", 1)
	assertCount(t, db, "SELECT COUNT(*) FROM project_keys", 1)
}

func TestBuildPostgresRuntimeControlPlaneUsesPostgresStores(t *testing.T) {
	controlDB := &sql.DB{}
	queryDB := &sql.DB{}
	control := buildPostgresRuntimeControlPlane(controlDB, queryDB)

	if _, ok := control.keyStore.(*postgrescontrol.AuthStore); !ok {
		t.Fatalf("keyStore type = %T, want *postgrescontrol.AuthStore", control.keyStore)
	}
	if _, ok := control.authStore.(*shadowingAuthStore); !ok {
		t.Fatalf("authStore type = %T, want *shadowingAuthStore", control.authStore)
	}
	if _, ok := control.services.Catalog.(*postgrescontrol.CatalogStore); !ok {
		t.Fatalf("catalog type = %T, want *postgrescontrol.CatalogStore", control.services.Catalog)
	}
	if _, ok := control.services.Admin.(*shadowingAdminStore); !ok {
		t.Fatalf("admin type = %T, want *shadowingAdminStore", control.services.Admin)
	}
	if _, ok := control.services.Issues.(*postgrescontrol.GroupStore); !ok {
		t.Fatalf("issues type = %T, want *postgrescontrol.GroupStore", control.services.Issues)
	}
	if _, ok := control.services.IssueReads.(*postgrescontrol.IssueReadStore); !ok {
		t.Fatalf("issue reads type = %T, want *postgrescontrol.IssueReadStore", control.services.IssueReads)
	}
	if _, ok := control.alertStore.(*postgrescontrol.AlertStore); !ok {
		t.Fatalf("alertStore type = %T, want *postgrescontrol.AlertStore", control.alertStore)
	}
	if _, ok := control.monitorStore.(*postgrescontrol.MonitorStore); !ok {
		t.Fatalf("monitorStore type = %T, want *postgrescontrol.MonitorStore", control.monitorStore)
	}
	if _, ok := control.outbox.(*postgrescontrol.NotificationOutboxStore); !ok {
		t.Fatalf("outbox type = %T, want *postgrescontrol.NotificationOutboxStore", control.outbox)
	}
	if _, ok := control.deliveries.(*postgrescontrol.NotificationDeliveryStore); !ok {
		t.Fatalf("deliveries type = %T, want *postgrescontrol.NotificationDeliveryStore", control.deliveries)
	}
	if _, ok := control.lifecycle.(*postgrescontrol.LifecycleStore); !ok {
		t.Fatalf("lifecycle type = %T, want *postgrescontrol.LifecycleStore", control.lifecycle)
	}
	if control.close == nil {
		t.Fatal("close = nil, want closer")
	}
}

func TestOpenRuntimeControlPlaneSyncsControlUsersIntoQuerySQLite(t *testing.T) {
	provider := testpostgres.NewProvider("app-runtime-shadows")
	controlDB, controlDSN := provider.OpenDatabaseWithDSN(t, "control")
	if err := postgrescontrol.Migrate(context.Background(), controlDB); err != nil {
		t.Fatalf("postgres migrate: %v", err)
	}

	now := "2026-03-31T00:00:00Z"
	if _, err := controlDB.Exec(`INSERT INTO organizations (id, slug, name) VALUES ('org-1', 'acme', 'Acme')`); err != nil {
		t.Fatalf("seed control organization: %v", err)
	}
	if _, err := controlDB.Exec(`INSERT INTO projects (id, organization_id, slug, name, platform, status, created_at, updated_at) VALUES ('proj-1', 'org-1', 'backend', 'Backend', 'go', 'active', $1, $1)`, now); err != nil {
		t.Fatalf("seed control project: %v", err)
	}
	if _, err := controlDB.Exec(`INSERT INTO users (id, email, display_name, is_active, created_at, updated_at) VALUES ('user-1', 'owner@example.com', 'Owner', TRUE, $1, $1)`, now); err != nil {
		t.Fatalf("seed control user: %v", err)
	}
	if _, err := controlDB.Exec(`INSERT INTO organization_members (id, organization_id, user_id, role, created_at) VALUES ('mem-1', 'org-1', 'user-1', 'owner', $1)`, now); err != nil {
		t.Fatalf("seed control membership: %v", err)
	}

	queryDB, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = queryDB.Close() })

	control, err := openRuntimeControlPlane(context.Background(), config.Config{ControlDSN: controlDSN}, queryDB)
	if err != nil {
		t.Fatalf("openRuntimeControlPlane: %v", err)
	}
	t.Cleanup(func() { _ = control.close() })

	assertCount(t, queryDB, "SELECT COUNT(*) FROM users WHERE id = 'user-1'", 1)
	assertCount(t, queryDB, "SELECT COUNT(*) FROM organizations WHERE id = 'org-1'", 1)
	assertCount(t, queryDB, "SELECT COUNT(*) FROM projects WHERE id = 'proj-1' AND organization_id = 'org-1'", 1)
	assertCount(t, queryDB, "SELECT COUNT(*) FROM organization_members WHERE organization_id = 'org-1' AND user_id = 'user-1'", 1)
}

func TestPostgresDefaultKeySyncsQueryProjectShadowForAttachments(t *testing.T) {
	provider := testpostgres.NewProvider("app-runtime-default-key")
	controlDB, _ := provider.OpenDatabaseWithDSN(t, "control")
	if err := postgrescontrol.Migrate(context.Background(), controlDB); err != nil {
		t.Fatalf("postgres migrate: %v", err)
	}

	queryDB, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = queryDB.Close() })

	control := buildPostgresRuntimeControlPlane(controlDB, queryDB)
	t.Cleanup(func() { _ = control.close() })

	publicKey, err := control.defaultKey(context.Background())
	if err != nil {
		t.Fatalf("defaultKey: %v", err)
	}
	if publicKey == "" {
		t.Fatal("defaultKey returned empty public key")
	}

	attachments := sqlite.NewAttachmentStore(queryDB, sqliteTestBlobStore(t))
	if err := attachments.SaveAttachment(context.Background(), &attachment.Attachment{
		ProjectID: "default-project",
		EventID:   "evt-shadow-attachment",
		Name:      "baseline.txt",
	}, []byte("synthetic attachment body")); err != nil {
		t.Fatalf("SaveAttachment after defaultKey shadow sync: %v", err)
	}

	assertCount(t, queryDB, "SELECT COUNT(*) FROM organizations WHERE id = 'default-org'", 1)
	assertCount(t, queryDB, "SELECT COUNT(*) FROM projects WHERE id = 'default-project' AND organization_id = 'default-org'", 1)
	assertCount(t, queryDB, "SELECT COUNT(*) FROM event_attachments WHERE event_id = 'evt-shadow-attachment'", 1)
}

func TestNativeDebugFileStoreLookupReturnsWrappedFile(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sqlite.Open(dataDir)
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	defer db.Close()

	blobStore := sqliteTestBlobStore(t)
	debugStore := sqlite.NewDebugFileStore(db, blobStore)
	if _, err := sqlite.EnsureDefaultKey(context.Background(), db); err != nil {
		t.Fatalf("EnsureDefaultKey: %v", err)
	}
	if err := debugStore.Save(context.Background(), &sqlite.DebugFile{
		ID:        "dbg-1",
		ProjectID: "default-project",
		ReleaseID: "1.2.3",
		Kind:      "apple",
		Name:      "App.dSYM.zip",
		UUID:      "DEBUG-ID-1",
		CodeID:    "CODE-ID-1",
	}, []byte("debug bundle")); err != nil {
		t.Fatalf("save debug file: %v", err)
	}

	wrapper := &nativeDebugFileStore{store: debugStore}
	file, body, err := wrapper.LookupByDebugID(context.Background(), "default-project", "1.2.3", "apple", "DEBUG-ID-1")
	if err != nil {
		t.Fatalf("LookupByDebugID: %v", err)
	}
	if file == nil || file.ID != "dbg-1" {
		t.Fatalf("wrapped file = %+v, want dbg-1", file)
	}
	if string(body) != "debug bundle" {
		t.Fatalf("body = %q, want debug bundle", string(body))
	}

	file, body, err = wrapper.LookupByCodeID(context.Background(), "default-project", "1.2.3", "apple", "CODE-ID-1")
	if err != nil {
		t.Fatalf("LookupByCodeID: %v", err)
	}
	if file == nil || file.ID != "dbg-1" {
		t.Fatalf("wrapped file = %+v, want dbg-1", file)
	}
	if string(body) != "debug bundle" {
		t.Fatalf("body = %q, want debug bundle", string(body))
	}
}

func TestOpenBlobStoreDefaultsToFile(t *testing.T) {
	cfg := config.Config{}
	blobs, err := openBlobStore(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("openBlobStore() error = %v", err)
	}
	if _, ok := blobs.(*store.FileBlobStore); !ok {
		t.Fatalf("blob store type = %T, want *store.FileBlobStore", blobs)
	}
}

func TestOpenBlobStoreRejectsUnknownBackend(t *testing.T) {
	cfg := config.Config{BlobBackend: "mystery"}
	if _, err := openBlobStore(cfg, t.TempDir()); err == nil {
		t.Fatal("openBlobStore() expected error for unknown backend")
	}
}

func TestOpenBlobStoreValidatesS3Config(t *testing.T) {
	cfg := config.Config{BlobBackend: "s3"}
	if _, err := openBlobStore(cfg, t.TempDir()); err == nil || !strings.Contains(err.Error(), "s3 endpoint is required") {
		t.Fatalf("openBlobStore() error = %v, want missing endpoint error", err)
	}
}

func assertCount(t *testing.T, db *sql.DB, query string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("query %q: %v", query, err)
	}
	if got != want {
		t.Fatalf("%s count = %d, want %d", query, got, want)
	}
}

func sqliteTestBlobStore(t *testing.T) store.BlobStore {
	t.Helper()
	blobStore, err := store.NewFileBlobStore(t.TempDir())
	if err != nil {
		t.Fatalf("store.NewFileBlobStore: %v", err)
	}
	return blobStore
}
