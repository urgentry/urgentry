package app

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/alert"
	"urgentry/internal/auth"
	"urgentry/internal/config"
	"urgentry/internal/controlplane"
	"urgentry/internal/integration"
	"urgentry/internal/issue"
	"urgentry/internal/notify"
	"urgentry/internal/pipeline"
	"urgentry/internal/postgrescontrol"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
)

type bootstrapResult struct {
	Created  bool
	Email    string
	Password string
	PAT      string
}

type runtimeControlPlane struct {
	services         controlplane.Services
	keyStore         auth.KeyStore
	authStore        auth.Store
	alertStore       alert.RuleStore
	alertHistory     pipeline.AlertHistoryStore
	outbox           notify.EmailOutbox
	deliveries       notify.DeliveryRecorder
	lifecycle        store.LifecycleStore
	operatorAudits   store.OperatorAuditStore
	monitorStore     controlplane.MonitorStore
	ownershipStore   controlplane.OwnershipStore
	preventStore     store.PreventStore
	integrationStore integration.Store
	sentryAppStore   integration.AppStore
	externalIssues   integration.ExternalIssueStore
	groupStore       issue.GroupStore
	close            func() error
	defaultKey       func(context.Context) (string, error)
	bootstrap        func(context.Context, config.Config) (*bootstrapResult, error)
}

func openRuntimeControlPlane(ctx context.Context, cfg config.Config, queryDB *sql.DB) (runtimeControlPlane, error) {
	if controlDSN := strings.TrimSpace(cfg.ControlDSN); controlDSN != "" {
		controlDB, err := postgrescontrol.Open(ctx, controlDSN)
		if err != nil {
			return runtimeControlPlane{}, fmt.Errorf("open control plane: %w", err)
		}
		control := buildPostgresRuntimeControlPlane(controlDB, queryDB)
		control.keyStore = auth.NewCachedKeyStore(control.keyStore, 0, 0)
		if err := retrySQLiteBusy(90*time.Second, func() error {
			return syncPostgresControlPlaneShadows(ctx, controlDB, sqlite.NewPrincipalShadowStore(queryDB))
		}); err != nil {
			_ = control.close()
			return runtimeControlPlane{}, fmt.Errorf("sync control-plane sqlite shadows: %w", err)
		}
		if err := retrySQLiteBusy(90*time.Second, func() error {
			return syncPostgresCatalogShadows(ctx, control.services.Catalog, queryDB)
		}); err != nil {
			_ = control.close()
			return runtimeControlPlane{}, fmt.Errorf("sync control-plane sqlite catalog shadows: %w", err)
		}
		return control, nil
	}
	return buildSQLiteRuntimeControlPlane(queryDB), nil
}

func buildSQLiteRuntimeControlPlane(queryDB *sql.DB) runtimeControlPlane {
	keyStore := sqlite.NewKeyStore(queryDB)
	authStore := sqlite.NewAuthStore(queryDB)
	services := controlplane.SQLiteServices(queryDB)
	return runtimeControlPlane{
		services:         services,
		keyStore:         keyStore,
		authStore:        authStore,
		alertStore:       services.Alerts,
		alertHistory:     sqlite.NewAlertHistoryStore(queryDB),
		outbox:           services.Outbox,
		deliveries:       services.Deliveries,
		lifecycle:        sqlite.NewLifecycleStore(queryDB),
		operatorAudits:   sqlite.NewOperatorAuditStore(queryDB),
		monitorStore:     services.Monitors,
		ownershipStore:   services.Ownership,
		preventStore:     sqlite.NewPreventStore(queryDB),
		integrationStore: sqlite.NewIntegrationConfigStore(queryDB),
		sentryAppStore:   sqlite.NewSentryAppStore(queryDB),
		externalIssues:   sqlite.NewExternalIssueStore(queryDB),
		groupStore:       sqlite.NewGroupStore(queryDB),
		close:            func() error { return nil },
		defaultKey: func(ctx context.Context) (string, error) {
			return sqlite.EnsureDefaultKey(ctx, queryDB)
		},
		bootstrap: func(ctx context.Context, cfg config.Config) (*bootstrapResult, error) {
			result, err := authStore.EnsureBootstrapAccess(ctx, sqlite.BootstrapOptions{
				DefaultOrganizationID: "default-org",
				Email:                 cfg.BootstrapEmail,
				DisplayName:           "Bootstrap Admin",
				Password:              cfg.BootstrapPassword,
				PersonalAccessToken:   cfg.BootstrapPAT,
			})
			if err != nil {
				return nil, err
			}
			return &bootstrapResult{
				Created:  result.Created,
				Email:    result.Email,
				Password: result.Password,
				PAT:      result.PAT,
			}, nil
		},
	}
}

func buildPostgresRuntimeControlPlane(controlDB, queryDB *sql.DB) runtimeControlPlane {
	baseAuthStore := postgrescontrol.NewAuthStore(controlDB)
	shadowStore := sqlite.NewPrincipalShadowStore(queryDB)
	authStore := newShadowingAuthStore(baseAuthStore, shadowStore)
	groupStore := postgrescontrol.NewGroupStore(controlDB)
	adminStore := newShadowingAdminStore(postgrescontrol.NewAdminStore(controlDB), shadowStore)
	catalogStore := postgrescontrol.NewCatalogStore(controlDB)
	services := controlplane.Services{
		Catalog:      catalogStore,
		Admin:        adminStore,
		Issues:       groupStore,
		IssueReads:   postgrescontrol.NewIssueReadStore(controlDB, queryDB),
		Ownership:    postgrescontrol.NewOwnershipStore(controlDB),
		Releases:     postgrescontrol.NewReleaseStore(controlDB),
		Alerts:       postgrescontrol.NewAlertStore(controlDB),
		MetricAlerts: postgrescontrol.NewMetricAlertStore(controlDB),
		Outbox:       postgrescontrol.NewNotificationOutboxStore(controlDB),
		Deliveries:   postgrescontrol.NewNotificationDeliveryStore(controlDB),
		Monitors:     postgrescontrol.NewMonitorStore(controlDB),
	}
	return runtimeControlPlane{
		services:         services,
		keyStore:         baseAuthStore,
		authStore:        authStore,
		alertStore:       services.Alerts,
		alertHistory:     postgrescontrol.NewAlertHistoryStore(controlDB),
		outbox:           services.Outbox,
		deliveries:       services.Deliveries,
		lifecycle:        postgrescontrol.NewLifecycleStore(controlDB),
		operatorAudits:   postgrescontrol.NewOperatorAuditStore(controlDB),
		monitorStore:     services.Monitors,
		ownershipStore:   services.Ownership,
		preventStore:     postgrescontrol.NewPreventStore(controlDB),
		integrationStore: postgrescontrol.NewIntegrationConfigStore(controlDB),
		sentryAppStore:   postgrescontrol.NewSentryAppStore(controlDB),
		externalIssues:   postgrescontrol.NewExternalIssueStore(controlDB),
		groupStore:       groupStore,
		close:            controlDB.Close,
		defaultKey: func(ctx context.Context) (string, error) {
			publicKey, err := postgrescontrol.EnsureDefaultKey(ctx, controlDB)
			if err != nil {
				return "", err
			}
			if err := retrySQLiteBusy(90*time.Second, func() error {
				return syncPostgresCatalogShadows(ctx, catalogStore, queryDB)
			}); err != nil {
				return "", err
			}
			return publicKey, nil
		},
		bootstrap: func(ctx context.Context, cfg config.Config) (*bootstrapResult, error) {
			result, err := baseAuthStore.EnsureBootstrapAccess(ctx, postgrescontrol.BootstrapOptions{
				DefaultOrganizationID: "default-org",
				Email:                 cfg.BootstrapEmail,
				DisplayName:           "Bootstrap Admin",
				Password:              cfg.BootstrapPassword,
				PersonalAccessToken:   cfg.BootstrapPAT,
			})
			if err != nil {
				return nil, err
			}
			if result.Created {
				if err := syncBootstrapUserShadow(ctx, baseAuthStore, shadowStore, result.Email, result.Password); err != nil {
					return nil, err
				}
				if err := retrySQLiteBusy(90*time.Second, func() error {
					return syncPostgresControlPlaneShadows(ctx, controlDB, shadowStore)
				}); err != nil {
					return nil, err
				}
				if err := retrySQLiteBusy(90*time.Second, func() error {
					return syncPostgresCatalogShadows(ctx, catalogStore, queryDB)
				}); err != nil {
					return nil, err
				}
			}
			return &bootstrapResult{
				Created:  result.Created,
				Email:    result.Email,
				Password: result.Password,
				PAT:      result.PAT,
			}, nil
		},
	}
}

func retrySQLiteBusy(timeout time.Duration, fn func() error) error {
	deadline := time.Now().Add(timeout)
	for {
		err := fn()
		if err == nil {
			return nil
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "sqlite_busy") && !strings.Contains(msg, "database is locked") {
			return err
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(200 * time.Millisecond)
	}
}
