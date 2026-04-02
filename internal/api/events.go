package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/discover"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

// handleListOrgEvents handles GET /api/0/organizations/{org_slug}/events/.
// Returns events across all projects in the organization (Discover events endpoint).
//
// When the request includes field[] query parameters (Sentry discover format),
// the handler delegates to the discover query engine which supports field
// selection and aggregations like count(), p95(duration.ms), avg(duration.ms).
func handleListOrgEvents(db *sql.DB, queries telemetryquery.Service, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		query := strings.TrimSpace(r.URL.Query().Get("query"))
		sortField := strings.TrimSpace(r.URL.Query().Get("sort"))
		if sortField == "" {
			sortField = "-timestamp"
		}
		limit := discoverLimit(r, 100)

		// If field[] params are present, use the discover query engine.
		fields := r.URL.Query()["field"]
		if len(fields) == 0 {
			fields = r.URL.Query()["field[]"]
		}
		if len(fields) > 0 && queries != nil {
			handleOrgEventsDiscover(w, r, queries, org, fields, query, sortField, limit)
			return
		}

		rows, err := sqlite.ListOrgEvents(r.Context(), db, org, query, sortField, limit)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organization events.")
			return
		}
		data := make([]OrgEventRow, 0, len(rows))
		for _, row := range rows {
			data = append(data, OrgEventRow{
				ID:          row.EventID,
				Title:       row.Title,
				Message:     row.Message,
				Level:       row.Level,
				Platform:    row.Platform,
				Culprit:     row.Culprit,
				ProjectName: row.ProjectName,
				Timestamp:   row.Timestamp,
				Tags:        apiEventTags(row.Tags),
			})
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
	}
}

// handleOrgEventsDiscover executes a discover query for the org events endpoint
// when field[] parameters are present.
func handleOrgEventsDiscover(w http.ResponseWriter, r *http.Request, queries telemetryquery.Service, orgSlug string, fields []string, rawQuery, sortField string, limit int) {
	selects, err := parseDiscoverFields(fields)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	dataset := inferDataset(r, selects)

	dq := discover.Query{
		Version: discover.CurrentVersion,
		Dataset: dataset,
		Scope: discover.Scope{
			Kind:         discover.ScopeKindOrganization,
			Organization: orgSlug,
		},
		Select: selects,
		Limit:  limit,
	}

	// Add sort/order_by.
	if orderBy := discoverOrderBy(sortField); orderBy != nil {
		dq.OrderBy = []discover.OrderBy{*orderBy}
	}

	// For non-issues datasets, require a time range to avoid rejected queries.
	if dataset != discover.DatasetIssues {
		statsPeriod := strings.TrimSpace(r.URL.Query().Get("statsPeriod"))
		if statsPeriod == "" {
			statsPeriod = "24h"
		}
		dq.TimeRange = &discover.TimeRange{Kind: "relative", Value: statsPeriod}
	}

	// Add groupBy for aggregations mixed with plain fields.
	hasAggregate := false
	var dimensionFields []discover.Expression
	for _, s := range selects {
		if s.Expr.Call != "" {
			hasAggregate = true
		} else if s.Expr.Field != "" {
			dimensionFields = append(dimensionFields, discover.Expression{Field: s.Expr.Field})
		}
	}
	if hasAggregate && len(dimensionFields) > 0 {
		dq.GroupBy = dimensionFields
	}

	result, err := queries.ExecuteTable(r.Context(), dq)
	if err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "Invalid discover query: "+err.Error())
		return
	}

	// Build Sentry-compatible response with data and meta.
	meta := map[string]string{}
	for _, col := range result.Columns {
		meta[col.Name] = discoverFieldType(col.Name, selects)
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"data": result.Rows,
		"meta": map[string]any{"fields": meta},
	})
}

// parseDiscoverFields parses Sentry-style field[] strings into discover SelectItems.
// Supported formats:
//   - "title" -> field reference
//   - "count()" -> zero-arg aggregate
//   - "p95(duration.ms)" -> one-arg aggregate
//   - "avg(duration.ms)" -> one-arg aggregate
func parseDiscoverFields(fields []string) ([]discover.SelectItem, error) {
	selects := make([]discover.SelectItem, 0, len(fields))
	for _, raw := range fields {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		item, err := parseOneField(raw)
		if err != nil {
			return nil, err
		}
		selects = append(selects, item)
	}
	if len(selects) == 0 {
		return nil, fmt.Errorf("no valid fields specified")
	}
	return selects, nil
}

// parseOneField parses a single Sentry-style field string.
func parseOneField(raw string) (discover.SelectItem, error) {
	parenIdx := strings.Index(raw, "(")
	if parenIdx < 0 {
		// Plain field reference like "title" or "project".
		return discover.SelectItem{
			Expr: discover.Expression{Field: raw},
		}, nil
	}

	// Function call like "count()" or "p95(duration.ms)".
	if !strings.HasSuffix(raw, ")") {
		return discover.SelectItem{}, fmt.Errorf("invalid field expression %q: missing closing parenthesis", raw)
	}
	funcName := strings.TrimSpace(raw[:parenIdx])
	argsStr := strings.TrimSpace(raw[parenIdx+1 : len(raw)-1])

	expr := discover.Expression{Call: funcName}
	if argsStr != "" {
		// Parse comma-separated args (currently all aggregates take 0 or 1 args).
		argParts := strings.Split(argsStr, ",")
		for _, arg := range argParts {
			arg = strings.TrimSpace(arg)
			if arg != "" {
				expr.Args = append(expr.Args, discover.Expression{Field: arg})
			}
		}
	}

	return discover.SelectItem{
		Alias: raw,
		Expr:  expr,
	}, nil
}

// inferDataset picks the discover dataset based on the requested fields
// and an explicit dataset query parameter.
func inferDataset(r *http.Request, selects []discover.SelectItem) discover.Dataset {
	if ds := strings.TrimSpace(r.URL.Query().Get("dataset")); ds != "" {
		return discover.Dataset(strings.ToLower(ds))
	}
	// Infer from fields: if any transaction-specific fields are requested, use transactions.
	txnFields := map[string]bool{
		"transaction": true, "op": true, "duration.ms": true,
	}
	for _, s := range selects {
		field := s.Expr.Field
		if field == "" && len(s.Expr.Args) > 0 {
			field = s.Expr.Args[0].Field
		}
		if txnFields[field] {
			return discover.DatasetTransactions
		}
	}
	// Default to issues for backwards compat with the events endpoint.
	return discover.DatasetIssues
}

// discoverOrderBy converts a Sentry-style sort string (e.g. "-timestamp",
// "count") into a discover OrderBy clause.
func discoverOrderBy(sort string) *discover.OrderBy {
	if sort == "" {
		return nil
	}
	direction := "asc"
	field := sort
	if strings.HasPrefix(sort, "-") {
		direction = "desc"
		field = sort[1:]
	}
	// Check if this looks like a function call alias (e.g. "-count()").
	if strings.Contains(field, "(") {
		return &discover.OrderBy{
			Expr:      discover.Expression{Alias: field},
			Direction: direction,
		}
	}
	return &discover.OrderBy{
		Expr:      discover.Expression{Field: field},
		Direction: direction,
	}
}

// discoverFieldType returns the Sentry-compatible type string for a column.
func discoverFieldType(colName string, selects []discover.SelectItem) string {
	for _, s := range selects {
		if s.Expr.Call != "" {
			alias := s.Alias
			if alias == "" {
				alias = s.Expr.Call
			}
			if alias == colName {
				return "number"
			}
		}
		if s.Expr.Field == colName {
			switch colName {
			case "timestamp", "first_seen", "last_seen":
				return "date"
			case "count", "duration.ms":
				return "number"
			default:
				return "string"
			}
		}
	}
	return "string"
}

// handleListProjectEvents handles GET /api/0/projects/{org_slug}/{proj_slug}/events/.
func handleListProjectEvents(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		pg := ParsePagination(r)
		events, err := listRecentEventsFromDBPaged(r, db, projectID, pg.Limit+1, pg.Offset)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list events.")
			return
		}
		page := SetPaginationHeaders(w, r, events, pg)
		if page == nil {
			page = []*Event{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetProjectEvent handles GET /api/0/projects/{org_slug}/{proj_slug}/events/{event_id}/.
func handleGetProjectEvent(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org := PathParam(r, "org_slug")
		proj := PathParam(r, "proj_slug")
		eventID := PathParam(r, "event_id")

		projectID, err := projectIDFromSlugs(r, db, org, proj)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve project.")
			return
		}
		if projectID == "" {
			httputil.WriteError(w, http.StatusNotFound, "Project not found.")
			return
		}
		evt, err := getEventFromDB(r, db, projectID, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		if evt == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}
		if err := enrichEventDetail(r, db, org, projectID, evt); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load event.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, evt)
	}
}

// ---------------------------------------------------------------------------
// SQLite query helpers for events
// ---------------------------------------------------------------------------

func listRecentEventsFromDBPaged(r *http.Request, db *sql.DB, projectID string, limit, offset int) ([]*Event, error) {
	rows, err := sqlite.ListProjectEventsPaged(r.Context(), db, projectID, limit, offset)
	if err != nil {
		return nil, err
	}
	return apiEventsFromWebEvents(rows), nil
}

func getEventFromDB(r *http.Request, db *sql.DB, projectID, eventID string) (*Event, error) {
	row, err := sqlite.GetProjectEvent(r.Context(), db, projectID, eventID)
	if err == sql.ErrNoRows || row == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return apiEventFromWebEvent(*row), nil
}

// handleResolveEventID handles GET /api/0/organizations/{org_slug}/eventids/{event_id}/.
// Given an event_id, returns the org slug, project slug, group ID, and event details.
func handleResolveEventID(db *sql.DB, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		eventID := PathParam(r, "event_id")

		resolved, err := sqlite.ResolveEventID(r.Context(), db, orgSlug, eventID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve event ID.")
			return
		}
		if resolved == nil {
			httputil.WriteError(w, http.StatusNotFound, "Event not found.")
			return
		}
		evt := apiEventFromWebEvent(resolved.Event)
		projectID, err := projectIDFromSlugs(r, db, resolved.OrgSlug, resolved.ProjectSlug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve event ID.")
			return
		}
		if projectID != "" {
			if err := enrichEventDetail(r, db, resolved.OrgSlug, projectID, evt); err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve event ID.")
				return
			}
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"organizationSlug": resolved.OrgSlug,
			"projectSlug":      resolved.ProjectSlug,
			"groupId":          resolved.GroupID,
			"eventId":          resolved.EventID,
			"event":            evt,
		})
	}
}

// handleResolveShortID handles GET /api/0/organizations/{org_slug}/shortids/{short_id}/.
// Given a short ID like "GENTRY-42", returns the full issue.
func handleResolveShortID(db *sql.DB, issues controlplane.IssueWorkflowStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgSlug := PathParam(r, "org_slug")
		raw := PathParam(r, "short_id")

		// Parse the short ID. Accept "GENTRY-42" or plain "42".
		numStr := raw
		if idx := strings.LastIndex(raw, "-"); idx >= 0 {
			numStr = raw[idx+1:]
		}
		shortID, err := strconv.Atoi(numStr)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid short ID format.")
			return
		}

		issue, projectSlug, err := sqlite.ResolveShortID(r.Context(), db, orgSlug, shortID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve short ID.")
			return
		}
		if issue == nil {
			httputil.WriteError(w, http.StatusNotFound, "Issue not found.")
			return
		}

		extras := loadIssueResponseExtras(r.Context(), db, issues, principalUserID(authPrincipalFromContext(r.Context())), []store.WebIssue{*issue})
		apiIssue := apiIssueFromWebIssueWithExtras(*issue, extras[issue.ID])
		apiIssue.ShortID = fmt.Sprintf("GENTRY-%d", issue.ShortID)
		apiIssue.ProjectRef, err = projectRefForIssue(r.Context(), db, issue.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to resolve short ID.")
			return
		}
		if apiIssue.ProjectRef.Slug == "" {
			apiIssue.ProjectRef.Slug = projectSlug
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"organizationSlug": orgSlug,
			"projectSlug":      projectSlug,
			"group":            apiIssue,
		})
	}
}

func enrichEventDetail(r *http.Request, db *sql.DB, orgSlug, projectID string, evt *Event) error {
	if db == nil || evt == nil || projectID == "" || evt.EventID == "" {
		return nil
	}

	var (
		groupID        string
		eventType      string
		releaseVersion string
		ingestedAt     string
		payloadJSON    string
	)
	if err := db.QueryRowContext(r.Context(),
		`SELECT COALESCE(group_id, ''), COALESCE(event_type, 'error'), COALESCE(release, ''), COALESCE(ingested_at, ''), COALESCE(payload_json, '')
		 FROM events
		 WHERE project_id = ? AND event_id = ?`,
		projectID, evt.EventID,
	).Scan(&groupID, &eventType, &releaseVersion, &ingestedAt, &payloadJSON); err != nil {
		return err
	}

	dist, payloadRelease := eventPayloadDistRelease(payloadJSON)
	if releaseVersion == "" {
		releaseVersion = payloadRelease
	}

	evt.Type = eventType
	evt.Size = int64(len(payloadJSON))
	evt.Dist = dist
	if ingestedAt != "" {
		dateReceived := sqlutil.ParseDBTime(ingestedAt)
		if !dateReceived.IsZero() {
			evt.DateReceived = &dateReceived
		}
	}

	release, err := eventRelease(r.Context(), db, orgSlug, releaseVersion)
	if err != nil {
		return err
	}
	evt.Release = release

	report, err := eventUserReport(r.Context(), db, projectID, evt.EventID)
	if err != nil {
		return err
	}
	evt.UserReport = report

	previousEventID, nextEventID, err := eventNeighborIDs(r.Context(), db, projectID, groupID, evt.EventID, ingestedAt)
	if err != nil {
		return err
	}
	evt.PreviousEventID = previousEventID
	evt.NextEventID = nextEventID
	return nil
}

func eventPayloadDistRelease(payloadJSON string) (string, string) {
	if strings.TrimSpace(payloadJSON) == "" {
		return "", ""
	}
	var payload storedEventPayload
	if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
		return "", ""
	}
	return strings.TrimSpace(payload.Dist), strings.TrimSpace(payload.Release)
}

func eventRelease(ctx context.Context, db *sql.DB, orgSlug, version string) (*Release, error) {
	version = strings.TrimSpace(version)
	if db == nil || orgSlug == "" || version == "" {
		return nil, nil
	}
	var (
		release      Release
		createdAt    string
		dateReleased string
	)
	err := db.QueryRowContext(ctx,
		`SELECT r.id, r.version, COALESCE(r.ref, ''), COALESCE(r.url, ''), COALESCE(r.created_at, ''), COALESCE(r.date_released, '')
		 FROM releases r
		 JOIN organizations o ON o.id = r.organization_id
		 WHERE o.slug = ? AND r.version = ?
		 LIMIT 1`,
		orgSlug, version,
	).Scan(&release.ID, &release.Version, &release.Ref, &release.URL, &createdAt, &dateReleased)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	release.ShortVersion = release.Version
	release.DateCreated = sqlutil.ParseDBTime(createdAt)
	if dateReleased != "" {
		parsed := sqlutil.ParseDBTime(dateReleased)
		if !parsed.IsZero() {
			release.DateReleased = &parsed
		}
	}
	return &release, nil
}

func eventUserReport(ctx context.Context, db *sql.DB, projectID, eventID string) (*UserReport, error) {
	if db == nil || projectID == "" || eventID == "" {
		return nil, nil
	}
	var report UserReport
	var createdAt string
	err := db.QueryRowContext(ctx,
		`SELECT id, COALESCE(event_id, ''), COALESCE(name, ''), COALESCE(email, ''), COALESCE(comments, ''), COALESCE(created_at, '')
		 FROM user_feedback
		 WHERE project_id = ? AND event_id = ?
		 ORDER BY created_at DESC
		 LIMIT 1`,
		projectID, eventID,
	).Scan(&report.ID, &report.EventID, &report.Name, &report.Email, &report.Comments, &createdAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	if createdAt != "" {
		report.DateCreated = sqlutil.ParseDBTime(createdAt).UTC().Format(time.RFC3339)
	}
	return &report, nil
}

func eventNeighborIDs(ctx context.Context, db *sql.DB, projectID, groupID, eventID, ingestedAt string) (string, string, error) {
	if db == nil || projectID == "" || groupID == "" || eventID == "" || ingestedAt == "" {
		return "", "", nil
	}
	var previousEventID, nextEventID string
	err := db.QueryRowContext(ctx,
		`SELECT
			COALESCE((
				SELECT event_id
				FROM events
				WHERE project_id = ? AND group_id = ?
				  AND (ingested_at > ? OR (ingested_at = ? AND event_id > ?))
				ORDER BY ingested_at ASC, event_id ASC
				LIMIT 1
			), ''),
			COALESCE((
				SELECT event_id
				FROM events
				WHERE project_id = ? AND group_id = ?
				  AND (ingested_at < ? OR (ingested_at = ? AND event_id < ?))
				ORDER BY ingested_at DESC, event_id DESC
				LIMIT 1
			), '')`,
		projectID, groupID, ingestedAt, ingestedAt, eventID,
		projectID, groupID, ingestedAt, ingestedAt, eventID,
	).Scan(&previousEventID, &nextEventID)
	if err != nil {
		return "", "", err
	}
	return previousEventID, nextEventID, nil
}
