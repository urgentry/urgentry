package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/discover"
)

var (
	ErrDashboardNotFound  = errors.New("dashboard not found")
	ErrDashboardForbidden = errors.New("dashboard forbidden")
)

type DashboardVisibility string

const (
	DashboardVisibilityPrivate      DashboardVisibility = "private"
	DashboardVisibilityOrganization DashboardVisibility = "organization"
)

type DashboardWidgetKind string

const (
	DashboardWidgetKindTable        DashboardWidgetKind = "table"
	DashboardWidgetKindStat         DashboardWidgetKind = "stat"
	DashboardWidgetKindSeries       DashboardWidgetKind = "series"
	DashboardWidgetKindCustomMetric DashboardWidgetKind = "custom_metric"
)

type Dashboard struct {
	ID             string              `json:"id"`
	OrganizationID string              `json:"organizationId"`
	OwnerUserID    string              `json:"ownerUserId"`
	Title          string              `json:"title"`
	Description    string              `json:"description,omitempty"`
	Visibility     DashboardVisibility `json:"visibility"`
	Config         json.RawMessage     `json:"config,omitempty"`
	CreatedAt      time.Time           `json:"dateCreated"`
	UpdatedAt      time.Time           `json:"dateUpdated"`
	Widgets        []DashboardWidget   `json:"widgets,omitempty"`
}

type DashboardWidget struct {
	ID            string              `json:"id"`
	DashboardID   string              `json:"dashboardId"`
	Title         string              `json:"title"`
	Description   string              `json:"description,omitempty"`
	Kind          DashboardWidgetKind `json:"kind"`
	Position      int                 `json:"position"`
	Width         int                 `json:"width"`
	Height        int                 `json:"height"`
	SavedSearchID string              `json:"savedSearchId,omitempty"`
	QueryVersion  int                 `json:"queryVersion"`
	QueryDoc      discover.Query      `json:"query"`
	Config        json.RawMessage     `json:"config,omitempty"`
	CreatedAt     time.Time           `json:"dateCreated"`
	UpdatedAt     time.Time           `json:"dateUpdated"`
}

type DashboardInput struct {
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Visibility  DashboardVisibility `json:"visibility"`
	Config      json.RawMessage     `json:"config"`
}

type DashboardWidgetInput struct {
	Title         string              `json:"title"`
	Description   string              `json:"description"`
	Kind          DashboardWidgetKind `json:"kind"`
	Position      int                 `json:"position"`
	Width         int                 `json:"width"`
	Height        int                 `json:"height"`
	SavedSearchID string              `json:"savedSearchId"`
	QueryDoc      discover.Query      `json:"query"`
	Config        json.RawMessage     `json:"config"`
}

type DashboardStore struct {
	db *sql.DB
}

func NewDashboardStore(db *sql.DB) *DashboardStore {
	return &DashboardStore{db: db}
}

func (s *DashboardStore) ListDashboards(ctx context.Context, orgSlug, actorUserID string) ([]Dashboard, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, err
	}
	query := `SELECT id, organization_id, owner_user_id, title, description, visibility, config_json, created_at, updated_at
		FROM dashboards
		WHERE organization_id = ?`
	args := []any{access.organizationID}
	if !access.isAdmin {
		query += ` AND (visibility = ? OR owner_user_id = ?)`
		args = append(args, string(DashboardVisibilityOrganization), actorUserID)
	}
	query += ` ORDER BY updated_at DESC, created_at DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list dashboards: %w", err)
	}
	defer rows.Close()

	items, err := scanDashboards(rows)
	if err != nil {
		return nil, err
	}
	return s.attachWidgets(ctx, items)
}

func (s *DashboardStore) GetDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID string) (*Dashboard, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, err
	}
	item, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrDashboardNotFound
	}
	if !canViewDashboard(access, *item, actorUserID) {
		return nil, ErrDashboardForbidden
	}
	items, err := s.attachWidgets(ctx, []Dashboard{*item})
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (s *DashboardStore) GetDashboardWidget(ctx context.Context, orgSlug, widgetID, actorUserID string) (*Dashboard, *DashboardWidget, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, nil, err
	}
	row := s.db.QueryRowContext(ctx, `SELECT dashboard_id FROM dashboard_widgets WHERE id = ?`, strings.TrimSpace(widgetID))
	var dashboardID string
	if err := row.Scan(&dashboardID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrDashboardNotFound
		}
		return nil, nil, fmt.Errorf("load dashboard widget: %w", err)
	}
	item, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return nil, nil, err
	}
	if item == nil {
		return nil, nil, ErrDashboardNotFound
	}
	if !canViewDashboard(access, *item, actorUserID) {
		return nil, nil, ErrDashboardForbidden
	}
	widget, err := s.loadWidget(ctx, dashboardID, widgetID)
	if err != nil {
		return nil, nil, err
	}
	if widget == nil {
		return nil, nil, ErrDashboardNotFound
	}
	return item, widget, nil
}

func (s *DashboardStore) CreateDashboard(ctx context.Context, orgSlug, actorUserID string, input DashboardInput) (*Dashboard, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, err
	}
	if !canWriteDashboards(access.role) {
		return nil, ErrDashboardForbidden
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("dashboard title is required")
	}
	configJSON, err := normalizeDashboardConfig(input.Config)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	id := generateID()
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO dashboards
			(id, organization_id, owner_user_id, title, description, visibility, config_json, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		access.organizationID,
		actorUserID,
		title,
		strings.TrimSpace(input.Description),
		string(normalizeDashboardVisibility(input.Visibility)),
		string(configJSON),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("create dashboard: %w", err)
	}
	return s.GetDashboard(ctx, orgSlug, id, actorUserID)
}

func (s *DashboardStore) UpdateDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID string, input DashboardInput) (*Dashboard, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, err
	}
	item, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrDashboardNotFound
	}
	if !canEditDashboard(access, *item, actorUserID) {
		return nil, ErrDashboardForbidden
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("dashboard title is required")
	}
	configJSON, err := normalizeDashboardConfig(input.Config)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if _, err := s.db.ExecContext(ctx,
		`UPDATE dashboards
		 SET title = ?, description = ?, visibility = ?, config_json = ?, updated_at = ?
		 WHERE id = ? AND organization_id = ?`,
		title,
		strings.TrimSpace(input.Description),
		string(normalizeDashboardVisibility(input.Visibility)),
		string(configJSON),
		now.Format(time.RFC3339),
		dashboardID,
		access.organizationID,
	); err != nil {
		return nil, fmt.Errorf("update dashboard: %w", err)
	}
	return s.GetDashboard(ctx, orgSlug, dashboardID, actorUserID)
}

func (s *DashboardStore) DeleteDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID string) error {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return err
	}
	item, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return err
	}
	if item == nil {
		return ErrDashboardNotFound
	}
	if !canEditDashboard(access, *item, actorUserID) {
		return ErrDashboardForbidden
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `DELETE FROM dashboard_widgets WHERE dashboard_id = ?`, dashboardID); err != nil {
		return fmt.Errorf("delete dashboard widgets: %w", err)
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM dashboards WHERE id = ? AND organization_id = ?`, dashboardID, access.organizationID); err != nil {
		return fmt.Errorf("delete dashboard: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit dashboard delete: %w", err)
	}
	return nil
}

func (s *DashboardStore) DuplicateDashboard(ctx context.Context, orgSlug, dashboardID, actorUserID, title string) (*Dashboard, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, err
	}
	source, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, ErrDashboardNotFound
	}
	if !canViewDashboard(access, *source, actorUserID) {
		return nil, ErrDashboardForbidden
	}
	dashboards, err := s.attachWidgets(ctx, []Dashboard{*source})
	if err != nil {
		return nil, err
	}
	source = &dashboards[0]
	newTitle := strings.TrimSpace(title)
	if newTitle == "" {
		newTitle = source.Title + " copy"
	}
	created, err := s.CreateDashboard(ctx, orgSlug, actorUserID, DashboardInput{
		Title:       newTitle,
		Description: source.Description,
		Visibility:  DashboardVisibilityPrivate,
		Config:      source.Config,
	})
	if err != nil {
		return nil, err
	}
	for _, widget := range source.Widgets {
		if _, err := s.CreateWidget(ctx, orgSlug, created.ID, actorUserID, DashboardWidgetInput{
			Title:         widget.Title,
			Description:   widget.Description,
			Kind:          widget.Kind,
			Position:      widget.Position,
			Width:         widget.Width,
			Height:        widget.Height,
			SavedSearchID: widget.SavedSearchID,
			QueryDoc:      widget.QueryDoc,
			Config:        widget.Config,
		}); err != nil {
			return nil, err
		}
	}
	return s.GetDashboard(ctx, orgSlug, created.ID, actorUserID)
}

func (s *DashboardStore) CreateWidget(ctx context.Context, orgSlug, dashboardID, actorUserID string, input DashboardWidgetInput) (*DashboardWidget, error) {
	return s.upsertWidget(ctx, orgSlug, dashboardID, "", actorUserID, input)
}

func (s *DashboardStore) UpdateWidget(ctx context.Context, orgSlug, dashboardID, widgetID, actorUserID string, input DashboardWidgetInput) (*DashboardWidget, error) {
	return s.upsertWidget(ctx, orgSlug, dashboardID, widgetID, actorUserID, input)
}

func (s *DashboardStore) DeleteWidget(ctx context.Context, orgSlug, dashboardID, widgetID, actorUserID string) error {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return err
	}
	dashboard, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return err
	}
	if dashboard == nil {
		return ErrDashboardNotFound
	}
	if !canEditDashboard(access, *dashboard, actorUserID) {
		return ErrDashboardForbidden
	}
	result, err := s.db.ExecContext(ctx, `DELETE FROM dashboard_widgets WHERE id = ? AND dashboard_id = ?`, widgetID, dashboardID)
	if err != nil {
		return fmt.Errorf("delete widget: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return ErrDashboardNotFound
	}
	return nil
}

func (s *DashboardStore) upsertWidget(ctx context.Context, orgSlug, dashboardID, widgetID, actorUserID string, input DashboardWidgetInput) (*DashboardWidget, error) {
	access, err := s.loadAccess(ctx, orgSlug, actorUserID)
	if err != nil {
		return nil, err
	}
	dashboard, err := s.loadDashboard(ctx, access.organizationID, dashboardID)
	if err != nil {
		return nil, err
	}
	if dashboard == nil {
		return nil, ErrDashboardNotFound
	}
	if !canEditDashboard(access, *dashboard, actorUserID) {
		return nil, ErrDashboardForbidden
	}

	existing, err := s.loadWidget(ctx, dashboardID, widgetID)
	if err != nil {
		return nil, err
	}
	if widgetID != "" && existing == nil {
		return nil, ErrDashboardNotFound
	}

	kind, err := normalizeDashboardWidgetKind(input.Kind)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.Title) == "" {
		switch kind {
		case DashboardWidgetKindTable:
			input.Title = "Table"
		case DashboardWidgetKindStat:
			input.Title = "Stat"
		default:
			input.Title = "Series"
		}
	}
	if input.Width <= 0 {
		input.Width = 4
	}
	if input.Height <= 0 {
		input.Height = 3
	}
	if input.Position < 0 {
		input.Position = 0
	}

	savedSearchID, queryVersion, queryJSON, err := s.resolveWidgetQuery(ctx, access.organizationID, orgSlug, actorUserID, input, existing)
	if err != nil {
		return nil, err
	}
	configJSON, err := normalizeWidgetConfig(input.Config)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if existing == nil {
		widgetID = generateID()
		_, err = s.db.ExecContext(ctx,
			`INSERT INTO dashboard_widgets
				(id, dashboard_id, title, description, kind, position, width, height, saved_search_id, query_version, query_json, config_json, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			widgetID,
			dashboardID,
			strings.TrimSpace(input.Title),
			strings.TrimSpace(input.Description),
			string(kind),
			input.Position,
			input.Width,
			input.Height,
			savedSearchID,
			queryVersion,
			queryJSON,
			string(configJSON),
			now.Format(time.RFC3339),
			now.Format(time.RFC3339),
		)
		if err != nil {
			return nil, fmt.Errorf("create widget: %w", err)
		}
	} else {
		_, err = s.db.ExecContext(ctx,
			`UPDATE dashboard_widgets
			 SET title = ?, description = ?, kind = ?, position = ?, width = ?, height = ?, saved_search_id = ?, query_version = ?, query_json = ?, config_json = ?, updated_at = ?
			 WHERE id = ? AND dashboard_id = ?`,
			strings.TrimSpace(input.Title),
			strings.TrimSpace(input.Description),
			string(kind),
			input.Position,
			input.Width,
			input.Height,
			savedSearchID,
			queryVersion,
			queryJSON,
			string(configJSON),
			now.Format(time.RFC3339),
			widgetID,
			dashboardID,
		)
		if err != nil {
			return nil, fmt.Errorf("update widget: %w", err)
		}
	}
	return s.loadWidget(ctx, dashboardID, widgetID)
}

type dashboardAccess struct {
	organizationID string
	role           string
	isAdmin        bool
}

func (s *DashboardStore) loadAccess(ctx context.Context, orgSlug, actorUserID string) (dashboardAccess, error) {
	var access dashboardAccess
	row := s.db.QueryRowContext(ctx,
		`SELECT o.id, om.role
		 FROM organizations o
		 JOIN organization_members om ON om.organization_id = o.id
		 WHERE o.slug = ? AND om.user_id = ?`,
		orgSlug, actorUserID,
	)
	if err := row.Scan(&access.organizationID, &access.role); err != nil {
		if err == sql.ErrNoRows {
			return dashboardAccess{}, ErrDashboardForbidden
		}
		return dashboardAccess{}, fmt.Errorf("load dashboard access: %w", err)
	}
	access.isAdmin = access.role == "owner" || access.role == "admin"
	return access, nil
}

func (s *DashboardStore) loadDashboard(ctx context.Context, organizationID, dashboardID string) (*Dashboard, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, organization_id, owner_user_id, title, description, visibility, config_json, created_at, updated_at
		 FROM dashboards
		 WHERE id = ? AND organization_id = ?`,
		dashboardID, organizationID,
	)
	item, err := scanDashboard(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *DashboardStore) loadWidget(ctx context.Context, dashboardID, widgetID string) (*DashboardWidget, error) {
	if strings.TrimSpace(widgetID) == "" {
		return nil, nil
	}
	row := s.db.QueryRowContext(ctx,
		`SELECT id, dashboard_id, title, description, kind, position, width, height, saved_search_id, query_version, query_json, config_json, created_at, updated_at
		 FROM dashboard_widgets
		 WHERE id = ? AND dashboard_id = ?`,
		widgetID, dashboardID,
	)
	item, err := scanDashboardWidget(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *DashboardStore) attachWidgets(ctx context.Context, items []Dashboard) ([]Dashboard, error) {
	if len(items) == 0 {
		return items, nil
	}
	ids := make([]string, 0, len(items))
	index := make(map[string]int, len(items))
	for i, item := range items {
		ids = append(ids, item.ID)
		index[item.ID] = i
	}
	holders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids))
	for _, id := range ids {
		args = append(args, id)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, dashboard_id, title, description, kind, position, width, height, saved_search_id, query_version, query_json, config_json, created_at, updated_at
		 FROM dashboard_widgets
		 WHERE dashboard_id IN (`+holders+`)
		 ORDER BY dashboard_id ASC, position ASC, created_at ASC`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("list widgets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		widget, err := scanDashboardWidget(rows)
		if err != nil {
			return nil, err
		}
		items[index[widget.DashboardID]].Widgets = append(items[index[widget.DashboardID]].Widgets, widget)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate widgets: %w", err)
	}
	return items, nil
}

func (s *DashboardStore) resolveWidgetQuery(ctx context.Context, organizationID, orgSlug, actorUserID string, input DashboardWidgetInput, existing *DashboardWidget) (string, int, string, error) {
	savedSearchID := strings.TrimSpace(input.SavedSearchID)
	doc := input.QueryDoc
	if savedSearchID != "" {
		saved, err := NewSearchStore(s.db).Get(ctx, actorUserID, orgSlug, savedSearchID)
		if err != nil {
			return "", 0, "", err
		}
		if saved == nil {
			return "", 0, "", fmt.Errorf("saved search not found")
		}
		doc = saved.QueryDoc
		if doc.Scope.Kind == discover.ScopeKindOrganization {
			doc.Scope.Organization = orgSlug
		}
	}
	if doc.Version == 0 && existing != nil && savedSearchID == "" {
		return existing.SavedSearchID, existing.QueryVersion, mustMarshalWidgetQuery(existing.QueryDoc), nil
	}
	if doc.Version == 0 {
		return "", 0, "", fmt.Errorf("widget query is required")
	}
	normalized, _, err := discover.ValidateQuery(doc)
	if err != nil {
		return "", 0, "", err
	}
	if err := s.validateDashboardQueryScope(ctx, organizationID, orgSlug, normalized); err != nil {
		return "", 0, "", err
	}
	if err := validateWidgetQueryKind(input.Kind, normalized); err != nil {
		return "", 0, "", err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return "", 0, "", fmt.Errorf("marshal widget query: %w", err)
	}
	return savedSearchID, normalized.Version, string(raw), nil
}

func (s *DashboardStore) validateDashboardQueryScope(ctx context.Context, organizationID, orgSlug string, query discover.Query) error {
	switch query.Scope.Kind {
	case discover.ScopeKindOrganization:
		if strings.TrimSpace(query.Scope.Organization) != orgSlug {
			return fmt.Errorf("widget query scope must match organization %q", orgSlug)
		}
		for _, projectID := range query.Scope.ProjectIDs {
			if err := s.ensureProjectInOrganization(ctx, organizationID, projectID); err != nil {
				return err
			}
		}
	case discover.ScopeKindProject:
		return s.ensureProjectInOrganization(ctx, organizationID, query.Scope.ProjectID)
	default:
		return fmt.Errorf("widget query scope is required")
	}
	return nil
}

func (s *DashboardStore) ensureProjectInOrganization(ctx context.Context, organizationID, projectID string) error {
	if strings.TrimSpace(projectID) == "" {
		return fmt.Errorf("project-scoped query requires a project id")
	}
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM projects WHERE id = ? AND organization_id = ?`,
		projectID, organizationID,
	).Scan(&count); err != nil {
		return fmt.Errorf("validate project scope: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("project %q is outside the dashboard organization", projectID)
	}
	return nil
}

func canWriteDashboards(role string) bool {
	switch role {
	case "owner", "admin", "member":
		return true
	default:
		return false
	}
}

func canViewDashboard(access dashboardAccess, item Dashboard, actorUserID string) bool {
	if access.isAdmin {
		return true
	}
	if item.OwnerUserID == actorUserID {
		return true
	}
	return item.Visibility == DashboardVisibilityOrganization
}

func canEditDashboard(access dashboardAccess, item Dashboard, actorUserID string) bool {
	if access.isAdmin {
		return true
	}
	return access.role == "member" && item.OwnerUserID == actorUserID
}

func normalizeDashboardVisibility(value DashboardVisibility) DashboardVisibility {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case "", string(DashboardVisibilityPrivate):
		return DashboardVisibilityPrivate
	case string(DashboardVisibilityOrganization), "shared":
		return DashboardVisibilityOrganization
	default:
		return DashboardVisibilityPrivate
	}
}

func normalizeDashboardWidgetKind(value DashboardWidgetKind) (DashboardWidgetKind, error) {
	switch strings.ToLower(strings.TrimSpace(string(value))) {
	case string(DashboardWidgetKindTable):
		return DashboardWidgetKindTable, nil
	case string(DashboardWidgetKindStat):
		return DashboardWidgetKindStat, nil
	case string(DashboardWidgetKindSeries), "timeseries":
		return DashboardWidgetKindSeries, nil
	case string(DashboardWidgetKindCustomMetric):
		return DashboardWidgetKindCustomMetric, nil
	default:
		return "", fmt.Errorf("unsupported widget kind %q", value)
	}
}

func normalizeDashboardConfig(raw json.RawMessage) (json.RawMessage, error) {
	return normalizeDashboardJSON(raw, "dashboard config")
}

func normalizeWidgetConfig(raw json.RawMessage) (json.RawMessage, error) {
	return normalizeDashboardJSON(raw, "widget config")
}

func normalizeDashboardJSON(raw json.RawMessage, label string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("%s must be valid JSON", label)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, fmt.Errorf("%s must be valid JSON", label)
	}
	return json.RawMessage(compact.String()), nil
}

func validateWidgetQueryKind(kind DashboardWidgetKind, query discover.Query) error {
	switch kind {
	case DashboardWidgetKindSeries:
		if query.Rollup == nil {
			return fmt.Errorf("series widgets require a rollup query")
		}
	case DashboardWidgetKindTable, DashboardWidgetKindStat:
		if query.Rollup != nil {
			return fmt.Errorf("%s widgets do not support rollup queries", kind)
		}
	case DashboardWidgetKindCustomMetric:
		// Custom metric widgets use MetricBucketQueryEngine; query doc is optional.
	default:
		return fmt.Errorf("unsupported widget kind %q", kind)
	}
	return nil
}

func scanDashboards(rows *sql.Rows) ([]Dashboard, error) {
	var items []Dashboard
	for rows.Next() {
		item, err := scanDashboard(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate dashboards: %w", err)
	}
	return items, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDashboard(scanner rowScanner) (Dashboard, error) {
	var (
		item                                 Dashboard
		description, visibility, config      sql.NullString
		createdAt, updatedAt, organizationID sql.NullString
		ownerUserID                          sql.NullString
	)
	if err := scanner.Scan(&item.ID, &organizationID, &ownerUserID, &item.Title, &description, &visibility, &config, &createdAt, &updatedAt); err != nil {
		return Dashboard{}, err
	}
	item.OrganizationID = nullStr(organizationID)
	item.OwnerUserID = nullStr(ownerUserID)
	item.Description = nullStr(description)
	item.Visibility = DashboardVisibility(firstNonEmpty(nullStr(visibility), string(DashboardVisibilityPrivate)))
	item.Config = json.RawMessage(firstNonEmpty(nullStr(config), "{}"))
	item.CreatedAt = parseTime(nullStr(createdAt))
	item.UpdatedAt = parseTime(nullStr(updatedAt))
	return item, nil
}

func scanDashboardWidget(scanner rowScanner) (DashboardWidget, error) {
	var (
		item                                                       DashboardWidget
		title, description, kind, savedSearchID, queryJSON, config sql.NullString
		createdAt, updatedAt, dashboardID                          sql.NullString
		position, width, height, queryVersion                      sql.NullInt64
	)
	if err := scanner.Scan(&item.ID, &dashboardID, &title, &description, &kind, &position, &width, &height, &savedSearchID, &queryVersion, &queryJSON, &config, &createdAt, &updatedAt); err != nil {
		return DashboardWidget{}, err
	}
	item.DashboardID = nullStr(dashboardID)
	item.Title = nullStr(title)
	item.Description = nullStr(description)
	item.Kind = DashboardWidgetKind(nullStr(kind))
	item.SavedSearchID = nullStr(savedSearchID)
	item.QueryVersion = int(queryVersion.Int64)
	if raw := nullStr(queryJSON); raw != "" {
		doc, _, err := discover.UnmarshalQuery([]byte(raw))
		if err == nil {
			item.QueryDoc = doc
		}
	}
	item.Config = json.RawMessage(firstNonEmpty(nullStr(config), "{}"))
	item.Position = int(position.Int64)
	item.Width = int(width.Int64)
	item.Height = int(height.Int64)
	item.CreatedAt = parseTime(nullStr(createdAt))
	item.UpdatedAt = parseTime(nullStr(updatedAt))
	return item, nil
}

func mustMarshalWidgetQuery(doc discover.Query) string {
	raw, err := json.Marshal(doc)
	if err != nil {
		return ""
	}
	return string(raw)
}
