package analyticsreport

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"urgentry/internal/analyticsservice"
	"urgentry/internal/analyticssnapshot"
	"urgentry/internal/discover"
	"urgentry/internal/notify"
	"urgentry/internal/sqlite"
	"urgentry/internal/telemetryquery"
)

const (
	SourceTypeSavedQuery      = "saved_query"
	SourceTypeDashboardWidget = "dashboard_widget"
)

type SourceSnapshot struct {
	Snapshot   *sqlite.AnalyticsSnapshot
	ProjectID  string
	SourceName string
}

type SourceFreezer interface {
	FreezeSavedQuery(ctx context.Context, organizationSlug, userID, savedQueryID string) (*SourceSnapshot, error)
	FreezeDashboardWidget(ctx context.Context, organizationSlug, userID, widgetID string) (*SourceSnapshot, error)
}

type scheduleStore interface {
	ListDue(ctx context.Context, now time.Time, limit int) ([]sqlite.AnalyticsReportSchedule, error)
	MarkDelivered(ctx context.Context, id string, attemptedAt time.Time, cadence sqlite.AnalyticsReportCadence, snapshotToken string) error
	MarkFailed(ctx context.Context, id string, attemptedAt time.Time, cadence sqlite.AnalyticsReportCadence, errText string) error
}

type Runner struct {
	Schedules  scheduleStore
	Freezer    SourceFreezer
	Outbox     notify.EmailOutbox
	Deliveries notify.DeliveryRecorder
	BaseURL    string
	Limit      int
}

func (r *Runner) RunDue(ctx context.Context, now time.Time) error {
	if r == nil || r.Schedules == nil || r.Freezer == nil || r.Outbox == nil {
		return nil
	}
	limit := r.Limit
	if limit <= 0 {
		limit = 25
	}
	items, err := r.Schedules.ListDue(ctx, now.UTC(), limit)
	if err != nil {
		return err
	}
	for _, item := range items {
		r.runOne(ctx, now.UTC(), item)
	}
	return nil
}

func (r *Runner) runOne(ctx context.Context, now time.Time, item sqlite.AnalyticsReportSchedule) {
	var (
		snapshot *SourceSnapshot
		err      error
	)
	switch strings.TrimSpace(item.SourceType) {
	case SourceTypeSavedQuery:
		snapshot, err = r.Freezer.FreezeSavedQuery(ctx, item.OrganizationSlug, item.CreatedByUserID, item.SourceID)
	case SourceTypeDashboardWidget:
		snapshot, err = r.Freezer.FreezeDashboardWidget(ctx, item.OrganizationSlug, item.CreatedByUserID, item.SourceID)
	default:
		err = fmt.Errorf("unsupported report source %q", item.SourceType)
	}
	if err != nil {
		_ = r.Schedules.MarkFailed(ctx, item.ID, now, item.Cadence, err.Error())
		return
	}
	if err := r.Outbox.RecordEmail(ctx, &notify.EmailNotification{
		ProjectID: snapshot.ProjectID,
		Recipient: item.Recipient,
		Subject:   reportSubject(snapshot.SourceName, item.Cadence),
		Body:      reportBody(snapshot, item, r.BaseURL),
		Transport: "tiny-report",
		Status:    notify.DeliveryStatusQueued,
		CreatedAt: now,
	}); err != nil {
		_ = r.Schedules.MarkFailed(ctx, item.ID, now, item.Cadence, err.Error())
		return
	}
	if r.Deliveries != nil {
		_ = r.Deliveries.RecordDelivery(ctx, &notify.DeliveryRecord{
			ProjectID:   snapshot.ProjectID,
			EventID:     snapshot.Snapshot.ID,
			Kind:        notify.DeliveryKindEmail,
			Target:      item.Recipient,
			Status:      notify.DeliveryStatusQueued,
			Attempts:    1,
			PayloadJSON: reportPayload(snapshot, item, r.BaseURL),
			CreatedAt:   now,
		})
	}
	_ = r.Schedules.MarkDelivered(ctx, item.ID, now, item.Cadence, snapshot.Snapshot.ShareToken)
}

type Freezer struct {
	Analytics analyticsservice.Services
	Queries   telemetryquery.Service
}

func (f *Freezer) FreezeSavedQuery(ctx context.Context, organizationSlug, userID, savedQueryID string) (*SourceSnapshot, error) {
	if f == nil || f.Analytics.Searches == nil || f.Analytics.Snapshots == nil || f.Queries == nil {
		return nil, fmt.Errorf("saved query freezing unavailable")
	}
	saved, err := f.Analytics.Searches.Get(ctx, userID, organizationSlug, savedQueryID)
	if err != nil {
		return nil, err
	}
	if saved == nil {
		return nil, fmt.Errorf("saved query not found")
	}
	visualization := savedSearchVisualization(*saved)
	body, err := freezeQueryBody(ctx, f.Queries, saved.QueryDoc, visualization, "Saved query", string(savedQueryDataset(*saved)), nil)
	if err != nil {
		return nil, err
	}
	snapshot, err := f.Analytics.Snapshots.Create(ctx, organizationSlug, userID, SourceTypeSavedQuery, saved.ID, saved.Name, body)
	if err != nil {
		return nil, err
	}
	return &SourceSnapshot{
		Snapshot:   snapshot,
		ProjectID:  projectIDForQuery(saved.QueryDoc),
		SourceName: saved.Name,
	}, nil
}

func (f *Freezer) FreezeDashboardWidget(ctx context.Context, organizationSlug, userID, widgetID string) (*SourceSnapshot, error) {
	if f == nil || f.Analytics.Dashboards == nil || f.Analytics.Snapshots == nil || f.Queries == nil {
		return nil, fmt.Errorf("dashboard widget freezing unavailable")
	}
	dashboard, widget, err := f.Analytics.Dashboards.GetDashboardWidget(ctx, organizationSlug, widgetID, userID)
	if err != nil {
		return nil, err
	}
	query := dashboardWidgetQuery(widget.QueryDoc, dashboard.Config)
	filters := dashboardWidgetFilters(dashboard.Config)
	sourceLabel := "Dashboard widget query"
	if strings.TrimSpace(widget.SavedSearchID) != "" {
		sourceLabel = "Saved query"
	}
	body, err := freezeQueryBody(ctx, f.Queries, query, string(widget.Kind), sourceLabel, string(widget.QueryDoc.Dataset), filters)
	if err != nil {
		return nil, err
	}
	title := strings.TrimSpace(dashboard.Title + " - " + widget.Title)
	snapshot, err := f.Analytics.Snapshots.Create(ctx, organizationSlug, userID, SourceTypeDashboardWidget, widget.ID, title, body)
	if err != nil {
		return nil, err
	}
	return &SourceSnapshot{
		Snapshot:   snapshot,
		ProjectID:  projectIDForQuery(query),
		SourceName: title,
	}, nil
}

func freezeQueryBody(ctx context.Context, queries telemetryquery.Service, query discover.Query, visualization, sourceLabel, dataset string, filters []string) (sqlite.SnapshotBody, error) {
	var result analyticssnapshot.Result
	switch strings.ToLower(strings.TrimSpace(visualization)) {
	case "series":
		series, err := queries.ExecuteSeries(ctx, query)
		if err != nil {
			return sqlite.SnapshotBody{}, err
		}
		result = analyticssnapshot.FromSeries(series)
	case "stat":
		table, err := queries.ExecuteTable(ctx, query)
		if err != nil {
			return sqlite.SnapshotBody{}, err
		}
		result = analyticssnapshot.FromStat(table)
	default:
		table, err := queries.ExecuteTable(ctx, query)
		if err != nil {
			return sqlite.SnapshotBody{}, err
		}
		result = analyticssnapshot.FromTable(table)
		visualization = "table"
	}
	return analyticssnapshot.BodyFromResult(result, query, sourceLabel, dataset, visualization, filters), nil
}

type dashboardConfig struct {
	Filters dashboardFilters `json:"filters,omitempty"`
}

type dashboardFilters struct {
	Environment string `json:"environment,omitempty"`
	Release     string `json:"release,omitempty"`
	Transaction string `json:"transaction,omitempty"`
}

func dashboardWidgetQuery(query discover.Query, raw json.RawMessage) discover.Query {
	cfg := dashboardWidgetConfig(raw)
	if discover.SupportsField(query.Dataset, "environment") {
		query.Where = appendPredicate(query.Where, cfg.Filters.Environment, "environment")
	}
	if discover.SupportsField(query.Dataset, "release") {
		query.Where = appendPredicate(query.Where, cfg.Filters.Release, "release")
	}
	if discover.SupportsField(query.Dataset, "transaction") {
		query.Where = appendPredicate(query.Where, cfg.Filters.Transaction, "transaction")
	}
	return query
}

func dashboardWidgetFilters(raw json.RawMessage) []string {
	cfg := dashboardWidgetConfig(raw)
	var out []string
	if cfg.Filters.Environment != "" {
		out = append(out, "env:"+cfg.Filters.Environment)
	}
	if cfg.Filters.Release != "" {
		out = append(out, "release:"+cfg.Filters.Release)
	}
	if cfg.Filters.Transaction != "" {
		out = append(out, "transaction:"+cfg.Filters.Transaction)
	}
	return out
}

func dashboardWidgetConfig(raw json.RawMessage) dashboardConfig {
	var cfg dashboardConfig
	if len(raw) == 0 {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return dashboardConfig{}
	}
	cfg.Filters.Environment = strings.TrimSpace(cfg.Filters.Environment)
	cfg.Filters.Release = strings.TrimSpace(cfg.Filters.Release)
	cfg.Filters.Transaction = strings.TrimSpace(cfg.Filters.Transaction)
	return cfg
}

func appendPredicate(where *discover.Predicate, value, field string) *discover.Predicate {
	value = strings.TrimSpace(value)
	if value == "" {
		return where
	}
	predicate := discover.Predicate{Op: "=", Field: field, Value: value}
	if where == nil {
		return &predicate
	}
	if strings.EqualFold(where.Op, "and") {
		args := append([]discover.Predicate(nil), where.Args...)
		args = append(args, predicate)
		return &discover.Predicate{Op: "and", Args: args}
	}
	return &discover.Predicate{Op: "and", Args: []discover.Predicate{*where, predicate}}
}

func savedQueryDataset(saved sqlite.SavedSearch) discover.Dataset {
	if saved.QueryDoc.Dataset == "" {
		return discover.DatasetIssues
	}
	return saved.QueryDoc.Dataset
}

func savedSearchVisualization(saved sqlite.SavedSearch) string {
	if saved.QueryDoc.Rollup != nil {
		return "series"
	}
	if len(saved.QueryDoc.GroupBy) == 0 && hasAggregate(saved.QueryDoc) {
		return "stat"
	}
	return "table"
}

func hasAggregate(query discover.Query) bool {
	for _, item := range query.Select {
		if strings.TrimSpace(item.Expr.Call) != "" {
			return true
		}
	}
	return false
}

func projectIDForQuery(query discover.Query) string {
	if strings.TrimSpace(query.Scope.ProjectID) != "" {
		return strings.TrimSpace(query.Scope.ProjectID)
	}
	if len(query.Scope.ProjectIDs) == 1 {
		return strings.TrimSpace(query.Scope.ProjectIDs[0])
	}
	return ""
}

func reportSubject(sourceName string, cadence sqlite.AnalyticsReportCadence) string {
	return fmt.Sprintf("[Urgentry Report] %s (%s)", strings.TrimSpace(sourceName), strings.TrimSpace(string(cadence)))
}

func reportBody(snapshot *SourceSnapshot, item sqlite.AnalyticsReportSchedule, baseURL string) string {
	link := reportLink(snapshot.Snapshot.ShareToken, baseURL)
	body := []string{
		"Scheduled analytics report",
		"",
		"Source: " + snapshot.SourceName,
		"Cadence: " + string(item.Cadence),
		"Generated: " + snapshot.Snapshot.CreatedAt.UTC().Format(time.RFC3339),
		"Snapshot: " + link,
	}
	if len(snapshot.Snapshot.Body.Filters) > 0 {
		body = append(body, "Filters: "+strings.Join(snapshot.Snapshot.Body.Filters, ", "))
	}
	if strings.TrimSpace(snapshot.Snapshot.Body.CostLabel) != "" {
		body = append(body, "Cost: "+snapshot.Snapshot.Body.CostLabel)
	}
	return strings.Join(body, "\n")
}

func reportPayload(snapshot *SourceSnapshot, item sqlite.AnalyticsReportSchedule, baseURL string) string {
	payload, err := json.Marshal(map[string]any{
		"source":   snapshot.SourceName,
		"cadence":  item.Cadence,
		"snapshot": reportLink(snapshot.Snapshot.ShareToken, baseURL),
		"created":  snapshot.Snapshot.CreatedAt.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return ""
	}
	return string(payload)
}

func reportLink(token, baseURL string) string {
	path := "/analytics/snapshots/" + strings.TrimSpace(token) + "/"
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return path
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return path
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + path
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}
