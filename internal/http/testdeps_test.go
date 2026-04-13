package http

import (
	"context"
	"database/sql"
	"testing"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/api"
	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/ingest"
	"urgentry/internal/pipeline"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
	"urgentry/internal/web"
)

func sqliteServerDeps(t testing.TB, db *sql.DB, dataDir string, keyStore auth.KeyStore, authStore auth.Store, pipe *pipeline.Pipeline, blobs store.BlobStore, nativeCrashes *sqlite.NativeCrashStore) Deps {
	t.Helper()

	if blobs == nil {
		blobs = store.NewMemoryBlobStore()
	}
	control := controlplane.SQLiteServices(db)
	operatorAudits := sqlite.NewOperatorAuditStore(db)
	queryGuard := sqlite.NewQueryGuardStore(db)
	queryService := telemetryquery.NewSQLiteService(db, blobs)
	feedbackStore := sqlite.NewFeedbackStore(db)
	attachmentStore := sqlite.NewAttachmentStore(db, blobs)
	debugFiles := sqlite.NewDebugFileStore(db, blobs)
	releaseHealth := sqlite.NewReleaseHealthStore(db)
	outcomes := sqlite.NewOutcomeStore(db)
	analytics := analyticsservice.SQLiteServices(db)
	audits := sqlite.NewAuditStore(db)
	backfills := sqlite.NewBackfillStore(db)
	retention := sqlite.NewRetentionStore(db, blobs)
	nativeControl := sqlite.NewNativeControlStore(db, blobs, operatorAudits)
	importExport := sqlite.NewImportExportStore(db, attachmentStore, nil, nil, blobs)
	operatorStore := sqlite.NewOperatorStore(db, store.OperatorRuntime{Role: "test", Env: "test"}, nil, operatorAudits, func(context.Context) (int, error) {
		if pipe != nil {
			return pipe.Len(), nil
		}
		return 0, nil
	})

	return Deps{
		KeyStore:  keyStore,
		AuthStore: authStore,
		Pipeline:  pipe,
		DB:        db,
		Lifecycle: sqlite.NewLifecycleStore(db),
		Ingest: ingest.IngestDeps{
			Pipeline:        pipe,
			EventStore:      sqlite.NewEventStore(db),
			ReplayStore:     sqlite.NewReplayStore(db, blobs),
			ReplayPolicies:  sqlite.NewReplayConfigStore(db),
			ProfileStore:    sqlite.NewProfileStore(db, blobs),
			FeedbackStore:   feedbackStore,
			AttachmentStore: attachmentStore,
			BlobStore:       blobs,
			DebugFiles:      debugFiles,
			NativeCrashes:   nativeCrashes,
			SessionStore:    releaseHealth,
			OutcomeStore:    outcomes,
			MonitorStore:    control.Monitors,
		},
		API: api.Dependencies{
			DB:               db,
			Control:          control,
			PrincipalShadows: sqlite.NewPrincipalShadowStore(db),
			QueryGuard:       queryGuard,
			Operators:        operatorStore,
			OperatorAudits:   operatorAudits,
			Analytics:        analytics,
			Backfills:        backfills,
			Audits:           audits,
			NativeControl:    nativeControl,
			ReleaseHealth:    releaseHealth,
			DebugFiles:       debugFiles,
			Outcomes:         outcomes,
			Retention:        retention,
			ImportExport:     importExport,
			Attachments:      attachmentStore,
			BlobStore:        blobs,
			Queries:          queryService,
		},
		Web: web.Dependencies{
			WebStore:       sqlite.NewWebStore(db),
			Replays:        queryService,
			Queries:        queryService,
			DB:             db,
			BlobStore:      blobs,
			DataDir:        dataDir,
			Control:        control,
			Operators:      operatorStore,
			OperatorAudits: operatorAudits,
			QueryGuard:     queryGuard,
			NativeControl:  nativeControl,
			Analytics:      analytics,
		},
	}
}
