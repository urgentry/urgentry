package controlplane

import (
	"database/sql"

	"urgentry/internal/sqlite"
)

func SQLiteServices(db *sql.DB) Services {
	if db == nil {
		return Services{}
	}
	return Services{
		Catalog:      sqlite.NewCatalogStore(db),
		Admin:        sqlite.NewAdminStore(db),
		Issues:       sqlite.NewGroupStore(db),
		IssueReads:   sqlite.NewGroupStore(db),
		Ownership:    sqlite.NewOwnershipStore(db),
		Releases:     sqlite.NewReleaseStore(db),
		Alerts:       sqlite.NewAlertStore(db),
		MetricAlerts: sqlite.NewMetricAlertStore(db),
		Outbox:       sqlite.NewNotificationOutboxStore(db),
		Deliveries:   sqlite.NewNotificationDeliveryStore(db),
		Monitors:     sqlite.NewMonitorStore(db),
	}
}

func WithSQLiteDefaults(db *sql.DB, current Services) Services {
	defaults := SQLiteServices(db)
	if current.Catalog == nil {
		current.Catalog = defaults.Catalog
	}
	if current.Admin == nil {
		current.Admin = defaults.Admin
	}
	if current.Issues == nil {
		current.Issues = defaults.Issues
	}
	if current.IssueReads == nil {
		current.IssueReads = defaults.IssueReads
	}
	if current.Ownership == nil {
		current.Ownership = defaults.Ownership
	}
	if current.Releases == nil {
		current.Releases = defaults.Releases
	}
	if current.Alerts == nil {
		current.Alerts = defaults.Alerts
	}
	if current.MetricAlerts == nil {
		current.MetricAlerts = defaults.MetricAlerts
	}
	if current.Outbox == nil {
		current.Outbox = defaults.Outbox
	}
	if current.Deliveries == nil {
		current.Deliveries = defaults.Deliveries
	}
	if current.Monitors == nil {
		current.Monitors = defaults.Monitors
	}
	return current
}
