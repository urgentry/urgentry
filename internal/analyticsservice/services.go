package analyticsservice

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

type DashboardStore interface {
	ListDashboards(ctx context.Context, orgSlug, actorUserID string) ([]sqlite.Dashboard, error)
	GetDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID string) (*sqlite.Dashboard, error)
	GetDashboardWidget(ctx context.Context, orgSlug, widgetID, actorUserID string) (*sqlite.Dashboard, *sqlite.DashboardWidget, error)
	CreateDashboard(ctx context.Context, orgSlug, actorUserID string, input sqlite.DashboardInput) (*sqlite.Dashboard, error)
	UpdateDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID string, input sqlite.DashboardInput) (*sqlite.Dashboard, error)
	DeleteDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID string) error
	DuplicateDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID, title string) (*sqlite.Dashboard, error)
	CreateWidget(ctx context.Context, orgSlug, dashboardID, actorUserID string, input sqlite.DashboardWidgetInput) (*sqlite.DashboardWidget, error)
	UpdateWidget(ctx context.Context, orgSlug, dashboardID, widgetID, actorUserID string, input sqlite.DashboardWidgetInput) (*sqlite.DashboardWidget, error)
	DeleteWidget(ctx context.Context, orgSlug, dashboardID, widgetID, actorUserID string) error
}

type SearchStore interface {
	Save(ctx context.Context, userID, organizationSlug string, visibility sqlite.SavedSearchVisibility, name, description, query, filter, env, sort string, favorite bool) (*sqlite.SavedSearch, error)
	SaveQuery(ctx context.Context, userID, organizationSlug string, visibility sqlite.SavedSearchVisibility, name, description, query, filter, env, sort string, favorite bool, queryDoc discover.Query) (*sqlite.SavedSearch, error)
	List(ctx context.Context, userID, organizationSlug string) ([]sqlite.SavedSearch, error)
	Get(ctx context.Context, userID, organizationSlug, id string) (*sqlite.SavedSearch, error)
	Delete(ctx context.Context, userID, id string) error
	UpdateMetadata(ctx context.Context, userID, organizationSlug, id, name, description string, visibility sqlite.SavedSearchVisibility, tags []string) (*sqlite.SavedSearch, error)
	SetFavorite(ctx context.Context, userID, organizationSlug, id string, favorite bool) error
	Clone(ctx context.Context, userID, organizationSlug, id, name string, visibility sqlite.SavedSearchVisibility, favorite bool) (*sqlite.SavedSearch, error)
}

type SnapshotStore interface {
	Create(ctx context.Context, organizationSlug, userID, sourceType, sourceID, title string, body sqlite.SnapshotBody) (*sqlite.AnalyticsSnapshot, error)
	GetByShareToken(ctx context.Context, token string) (*sqlite.AnalyticsSnapshot, error)
}

type ReportScheduleStore interface {
	Create(ctx context.Context, organizationSlug, sourceType, sourceID, userID, recipient string, cadence sqlite.AnalyticsReportCadence) (*sqlite.AnalyticsReportSchedule, error)
	ListBySource(ctx context.Context, organizationSlug, sourceType, sourceID, userID string) ([]sqlite.AnalyticsReportSchedule, error)
	ListDue(ctx context.Context, now time.Time, limit int) ([]sqlite.AnalyticsReportSchedule, error)
	Delete(ctx context.Context, organizationSlug, userID, id string) error
	MarkDelivered(ctx context.Context, id string, attemptedAt time.Time, cadence sqlite.AnalyticsReportCadence, snapshotToken string) error
	MarkFailed(ctx context.Context, id string, attemptedAt time.Time, cadence sqlite.AnalyticsReportCadence, errText string) error
}

type Services struct {
	Dashboards      DashboardStore
	Searches        SearchStore
	Snapshots       SnapshotStore
	ReportSchedules ReportScheduleStore
}

func Validate(services Services) error {
	switch {
	case services.Dashboards == nil:
		return errors.New("requires dashboard analytics service")
	case services.Searches == nil:
		return errors.New("requires search analytics service")
	case services.Snapshots == nil:
		return errors.New("requires snapshot analytics service")
	case services.ReportSchedules == nil:
		return errors.New("requires report schedule analytics service")
	default:
		return nil
	}
}

func SQLiteServices(db *sql.DB) Services {
	return Services{
		Dashboards:      sqlite.NewDashboardStore(db),
		Searches:        sqlite.NewSearchStore(db),
		Snapshots:       sqlite.NewAnalyticsSnapshotStore(db),
		ReportSchedules: sqlite.NewAnalyticsReportScheduleStore(db),
	}
}
