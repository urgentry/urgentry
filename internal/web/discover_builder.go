package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
	"urgentry/internal/store"
	"urgentry/internal/telemetryquery"
)

type discoverBuilderState struct {
	SavedID         string
	SaveName        string
	SaveDescription string
	SaveVisibility  string
	SaveFavorite    bool
	Dataset         string
	Query           string
	Filter          string
	Environment     string
	Visualization   string
	Columns         string
	Aggregate       string
	GroupBy         string
	OrderBy         string
	TimeRange       string
	Rollup          string
	ReturnTo        string
}

type discoverSavedQuery struct {
	ID          string
	Name        string
	Description string
	Tags        []string
	Favorite    bool
	Dataset     string
	Visibility  string
	Query       string
	Environment string
	URL         string
	OpenURL     string
}

type discoverCell struct {
	Text string
	Href string
}

type discoverResultView struct {
	Type      string
	Columns   []string
	Rows      [][]discoverCell
	StatLabel string
	StatValue string
}

func discoverStateFromRequest(r *http.Request, defaultDataset string) discoverBuilderState {
	state := discoverStateFromValues(r.URL.Query(), defaultDataset)
	// If the user hasn't set an explicit time range via URL params, fall back
	// to the global time range cookie so the nav selector scopes all views.
	if state.TimeRange == "" {
		if c, err := r.Cookie("urgentry_timerange"); err == nil && c.Value != "" && validTimeRange(c.Value) {
			state.TimeRange = c.Value
		}
	}
	return state
}

func discoverStateFromValues(values url.Values, defaultDataset string) discoverBuilderState {
	dataset := strings.ToLower(strings.TrimSpace(values.Get("dataset")))
	if dataset == "" {
		dataset = strings.ToLower(strings.TrimSpace(values.Get("scope")))
	}
	if dataset == "" || dataset == "all" {
		dataset = defaultDataset
	}
	if dataset == "" {
		dataset = string(discover.DatasetIssues)
	}
	filter := strings.TrimSpace(values.Get("filter"))
	if filter == "" {
		filter = "all"
	}
	visualization := strings.ToLower(strings.TrimSpace(values.Get("visualization")))
	if visualization == "" {
		visualization = "table"
	}
	return discoverBuilderState{
		SavedID:         strings.TrimSpace(values.Get("saved")),
		SaveName:        strings.TrimSpace(values.Get("name")),
		SaveDescription: strings.TrimSpace(values.Get("description")),
		SaveVisibility:  discoverFirstNonEmpty(strings.TrimSpace(values.Get("visibility")), string(sqlite.SavedSearchVisibilityPrivate)),
		SaveFavorite:    discoverBool(values.Get("favorite")),
		Dataset:         dataset,
		Query:           strings.TrimSpace(values.Get("query")),
		Filter:          filter,
		Environment:     strings.TrimSpace(values.Get("environment")),
		Visualization:   visualization,
		Columns:         strings.TrimSpace(values.Get("columns")),
		Aggregate:       strings.TrimSpace(values.Get("aggregate")),
		GroupBy:         strings.TrimSpace(values.Get("group_by")),
		OrderBy:         strings.TrimSpace(values.Get("order_by")),
		TimeRange:       strings.TrimSpace(values.Get("time_range")),
		Rollup:          strings.TrimSpace(values.Get("rollup")),
		ReturnTo:        strings.TrimSpace(values.Get("return_to")),
	}
}

func discoverStateFromSaved(saved sqlite.SavedSearch, defaultDataset, returnTo string) discoverBuilderState {
	state := discoverBuilderState{
		SavedID:         saved.ID,
		SaveName:        saved.Name,
		SaveDescription: saved.Description,
		SaveVisibility:  string(saved.Visibility),
		SaveFavorite:    saved.Favorite,
		Dataset:         discoverFirstNonEmpty(savedQueryDataset(saved), defaultDataset),
		Query:           saved.Query,
		Filter:          discoverFirstNonEmpty(saved.Filter, "all"),
		Environment:     saved.Environment,
		Visualization:   "table",
		Columns:         savedQueryColumns(saved.QueryDoc),
		Aggregate:       savedQueryAggregates(saved.QueryDoc),
		GroupBy:         savedQueryFields(saved.QueryDoc.GroupBy),
		OrderBy:         savedQueryOrderBy(saved.QueryDoc),
		ReturnTo:        returnTo,
	}
	if saved.QueryDoc.Rollup != nil {
		state.Visualization = "series"
		state.Rollup = saved.QueryDoc.Rollup.Interval
	}
	if state.Aggregate != "" {
		if state.Visualization != "series" && len(saved.QueryDoc.GroupBy) == 0 && len(saved.QueryDoc.Select) == 1 {
			state.Visualization = "stat"
		}
	}
	if saved.QueryDoc.Rollup == nil && (len(saved.QueryDoc.GroupBy) > 0 || len(saved.QueryDoc.Select) > 1 || state.OrderBy != "") {
		state.Visualization = "table"
	}
	if saved.QueryDoc.TimeRange != nil && saved.QueryDoc.TimeRange.Kind == "relative" {
		state.TimeRange = saved.QueryDoc.TimeRange.Value
	}
	return state
}

func discoverBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes":
		return true
	default:
		return false
	}
}

func buildDiscoverQuery(orgSlug string, state discoverBuilderState, limit int) (discover.Query, error) {
	dataset := discover.Dataset(strings.ToLower(strings.TrimSpace(state.Dataset)))
	timeRange := strings.TrimSpace(state.TimeRange)
	if timeRange == "" && (dataset == discover.DatasetLogs || dataset == discover.DatasetTransactions) {
		timeRange = "24h"
	}
	base, _, err := discover.ParseLegacy(discover.LegacyInput{
		Dataset:      dataset,
		Organization: orgSlug,
		Filter:       state.Filter,
		Query:        state.Query,
		Environment:  state.Environment,
		TimeRange:    timeRange,
		Limit:        limit,
	})
	if err != nil {
		return discover.Query{}, err
	}
	if timeRange != "" {
		base.TimeRange = &discover.TimeRange{Kind: "relative", Value: timeRange}
	}
	return applyDiscoverBuilderState(base, dataset, state)
}

func applyDiscoverBuilderState(base discover.Query, dataset discover.Dataset, state discoverBuilderState) (discover.Query, error) {
	aggregates, err := discoverAggregateSelects(dataset, state.Aggregate)
	if err != nil {
		return discover.Query{}, err
	}
	groupBy := discoverBuilderFields(state.GroupBy)
	columns := discoverBuilderFields(state.Columns)
	visualization := strings.ToLower(strings.TrimSpace(state.Visualization))
	if visualization == "" {
		visualization = "table"
	}
	switch visualization {
	case "series":
		if len(groupBy) > 0 {
			return discover.Query{}, fmt.Errorf("series queries do not support group_by yet")
		}
		if len(columns) > 0 {
			return discover.Query{}, fmt.Errorf("series queries do not support raw columns")
		}
		if len(aggregates) == 0 {
			aggregates, err = discoverAggregateSelects(dataset, "count")
			if err != nil {
				return discover.Query{}, err
			}
		}
		base.Select = aggregates
		base.Rollup = &discover.Rollup{Interval: discoverFirstNonEmpty(state.Rollup, defaultRollup(state.TimeRange))}
		if base.TimeRange == nil {
			base.TimeRange = &discover.TimeRange{Kind: "relative", Value: "7d"}
		}
	case "stat":
		if len(groupBy) > 0 {
			return discover.Query{}, fmt.Errorf("stat queries do not support group_by")
		}
		if len(columns) > 0 {
			return discover.Query{}, fmt.Errorf("stat queries do not support raw columns")
		}
		if len(aggregates) == 0 {
			aggregates, err = discoverAggregateSelects(dataset, "count")
			if err != nil {
				return discover.Query{}, err
			}
		}
		if len(aggregates) != 1 {
			return discover.Query{}, fmt.Errorf("stat queries support exactly one aggregate")
		}
		base.Select = aggregates
	case "table":
		if len(aggregates) > 0 || len(groupBy) > 0 {
			selects := make([]discover.SelectItem, 0, len(groupBy)+len(aggregates))
			for _, field := range groupBy {
				base.GroupBy = append(base.GroupBy, discover.Expression{Field: field})
				selects = append(selects, discover.SelectItem{
					Alias: field,
					Expr:  discover.Expression{Field: field},
				})
			}
			if len(aggregates) == 0 {
				aggregates, err = discoverAggregateSelects(dataset, "count")
				if err != nil {
					return discover.Query{}, err
				}
			}
			base.Select = append(selects, aggregates...)
		} else if len(columns) > 0 {
			base.Select = discoverFieldSelects(columns)
		} else if strings.TrimSpace(state.OrderBy) != "" {
			base.Select = discoverFieldSelects(defaultDiscoverFields(dataset))
		}
	default:
		return discover.Query{}, fmt.Errorf("unsupported visualization %q", state.Visualization)
	}
	orderBy, err := discoverOrderBy(base.Select, state.OrderBy)
	if err != nil {
		return discover.Query{}, err
	}
	if visualization == "table" && len(orderBy) == 0 && len(base.Select) > 0 && hasDiscoverAggregate(base) {
		alias := firstAggregateAlias(base.Select)
		if alias != "" {
			orderBy = []discover.OrderBy{{Expr: discover.Expression{Alias: alias}, Direction: "desc"}}
		}
	}
	base.OrderBy = orderBy
	return base, nil
}

func discoverBuilderFields(raw string) []string {
	return discoverCSV(raw, true)
}

func discoverCSV(raw string, ignoreNone bool) []string {
	parts := strings.Split(raw, ",")
	items := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		value := strings.ToLower(strings.TrimSpace(part))
		if value == "" {
			continue
		}
		if ignoreNone && value == "none" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		items = append(items, value)
	}
	return items
}

func discoverFieldSelects(fields []string) []discover.SelectItem {
	selects := make([]discover.SelectItem, 0, len(fields))
	for _, field := range fields {
		selects = append(selects, discover.SelectItem{
			Alias: field,
			Expr:  discover.Expression{Field: field},
		})
	}
	return selects
}

func defaultDiscoverFields(dataset discover.Dataset) []string {
	switch dataset {
	case discover.DatasetIssues:
		return []string{"issue.id", "project.id", "project", "title", "culprit", "level", "status", "first_seen", "last_seen", "count", "issue.short_id", "assignee"}
	case discover.DatasetLogs:
		return []string{"event.id", "project.id", "project", "title", "message", "level", "platform", "culprit", "environment", "release", "logger", "trace.id", "span.id", "timestamp"}
	default:
		return []string{"event.id", "project.id", "project", "transaction", "op", "status", "environment", "release", "trace.id", "span.id", "timestamp", "duration.ms"}
	}
}

func discoverAggregateSelects(dataset discover.Dataset, raw string) ([]discover.SelectItem, error) {
	items := discoverCSV(raw, true)
	if len(items) == 0 {
		return nil, nil
	}
	selects := make([]discover.SelectItem, 0, len(items))
	used := map[string]int{}
	for _, item := range items {
		alias, expr, err := discoverAggregate(dataset, item)
		if err != nil {
			return nil, err
		}
		alias = discoverUniqueAlias(alias, used)
		selects = append(selects, discover.SelectItem{Alias: alias, Expr: expr})
	}
	return selects, nil
}

func discoverAggregate(dataset discover.Dataset, raw string) (string, discover.Expression, error) {
	normalized := strings.ToLower(strings.TrimSpace(raw))
	if normalized == "" {
		normalized = "count"
	}
	switch normalized {
	case "count":
		return "count", discover.Expression{Call: "count"}, nil
	case "avg(duration.ms)":
		if dataset != discover.DatasetTransactions {
			return "", discover.Expression{}, fmt.Errorf("avg(duration.ms) is only supported for transactions")
		}
		return "avg", discover.Expression{Call: "avg", Args: []discover.Expression{{Field: "duration.ms"}}}, nil
	case "p50(duration.ms)":
		if dataset != discover.DatasetTransactions {
			return "", discover.Expression{}, fmt.Errorf("p50(duration.ms) is only supported for transactions")
		}
		return "p50", discover.Expression{Call: "p50", Args: []discover.Expression{{Field: "duration.ms"}}}, nil
	case "p95(duration.ms)":
		if dataset != discover.DatasetTransactions {
			return "", discover.Expression{}, fmt.Errorf("p95(duration.ms) is only supported for transactions")
		}
		return "p95", discover.Expression{Call: "p95", Args: []discover.Expression{{Field: "duration.ms"}}}, nil
	case "max(duration.ms)":
		if dataset != discover.DatasetTransactions {
			return "", discover.Expression{}, fmt.Errorf("max(duration.ms) is only supported for transactions")
		}
		return "max", discover.Expression{Call: "max", Args: []discover.Expression{{Field: "duration.ms"}}}, nil
	default:
		return "", discover.Expression{}, fmt.Errorf("unsupported aggregate %q", raw)
	}
}

func discoverUniqueAlias(alias string, used map[string]int) string {
	if used[alias] == 0 {
		used[alias] = 1
		return alias
	}
	used[alias]++
	return fmt.Sprintf("%s_%d", alias, used[alias])
}

func discoverOrderBy(selects []discover.SelectItem, raw string) ([]discover.OrderBy, error) {
	items := discoverCSV(raw, false)
	if len(items) == 0 {
		return nil, nil
	}
	aliases := make(map[string]struct{}, len(selects))
	for _, item := range selects {
		if strings.TrimSpace(item.Alias) != "" {
			aliases[strings.TrimSpace(item.Alias)] = struct{}{}
		}
	}
	orderBy := make([]discover.OrderBy, 0, len(items))
	for _, item := range items {
		direction := "asc"
		name := item
		if strings.HasPrefix(name, "-") {
			direction = "desc"
			name = strings.TrimSpace(strings.TrimPrefix(name, "-"))
		} else if strings.HasPrefix(name, "+") {
			name = strings.TrimSpace(strings.TrimPrefix(name, "+"))
		}
		if name == "" {
			continue
		}
		order := discover.OrderBy{Direction: direction}
		if _, ok := aliases[name]; ok {
			order.Expr = discover.Expression{Alias: name}
		} else {
			order.Expr = discover.Expression{Field: name}
		}
		orderBy = append(orderBy, order)
	}
	return orderBy, nil
}

func defaultRollup(rawTimeRange string) string {
	switch strings.TrimSpace(rawTimeRange) {
	case "1h", "6h":
		return "5m"
	case "24h", "7d":
		return "1h"
	default:
		return "1d"
	}
}

func workloadForDataset(dataset string) sqlite.QueryWorkload {
	switch strings.ToLower(strings.TrimSpace(dataset)) {
	case "issues":
		return sqlite.QueryWorkloadOrgIssues
	case "logs":
		return sqlite.QueryWorkloadLogs
	case "transactions":
		return sqlite.QueryWorkloadTransactions
	default:
		return sqlite.QueryWorkloadDiscover
	}
}

func savedQueryDataset(saved sqlite.SavedSearch) string {
	if saved.QueryDoc.Dataset == "" {
		return string(discover.DatasetIssues)
	}
	return string(saved.QueryDoc.Dataset)
}

func savedQueryColumns(query discover.Query) string {
	if hasDiscoverAggregate(query) || len(query.GroupBy) > 0 || query.Rollup != nil {
		return ""
	}
	fields := make([]string, 0, len(query.Select))
	for _, item := range query.Select {
		if strings.TrimSpace(item.Expr.Field) == "" {
			continue
		}
		fields = append(fields, strings.ToLower(strings.TrimSpace(item.Expr.Field)))
	}
	return strings.Join(fields, ", ")
}

func savedQueryAggregates(query discover.Query) string {
	values := make([]string, 0, len(query.Select))
	for _, item := range query.Select {
		if text := discoverAggregateText(item.Expr); text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, ", ")
}

func savedQueryFields(groupBy []discover.Expression) string {
	values := make([]string, 0, len(groupBy))
	for _, item := range groupBy {
		if strings.TrimSpace(item.Field) == "" {
			continue
		}
		values = append(values, strings.ToLower(strings.TrimSpace(item.Field)))
	}
	return strings.Join(values, ", ")
}

func savedQueryOrderBy(query discover.Query) string {
	values := make([]string, 0, len(query.OrderBy))
	for _, item := range query.OrderBy {
		name := strings.TrimSpace(item.Expr.Alias)
		if name == "" {
			name = strings.ToLower(strings.TrimSpace(item.Expr.Field))
		}
		if name == "" {
			continue
		}
		if strings.EqualFold(item.Direction, "desc") {
			name = "-" + name
		}
		values = append(values, name)
	}
	return strings.Join(values, ", ")
}

func discoverAggregateText(expr discover.Expression) string {
	switch expr.Call {
	case "count":
		return "count"
	case "avg", "p50", "p95", "max":
		if len(expr.Args) == 1 && strings.TrimSpace(expr.Args[0].Field) != "" {
			return fmt.Sprintf("%s(%s)", expr.Call, strings.ToLower(strings.TrimSpace(expr.Args[0].Field)))
		}
	}
	return ""
}

func firstAggregateAlias(selects []discover.SelectItem) string {
	for _, item := range selects {
		if strings.TrimSpace(item.Expr.Call) != "" {
			return strings.TrimSpace(item.Alias)
		}
	}
	return ""
}

func discoverSavedQueryURL(path string, saved sqlite.SavedSearch) string {
	values := url.Values{}
	values.Set("saved", saved.ID)
	return savedQueryPath(savedQueryDataset(saved)) + "?" + values.Encode()
}

func discoverSavedQueryDetailURL(saved sqlite.SavedSearch) string {
	return "/discover/queries/" + saved.ID + "/"
}

func savedQueryPath(dataset string) string {
	if strings.EqualFold(strings.TrimSpace(dataset), string(discover.DatasetLogs)) {
		return "/logs/"
	}
	return "/discover/"
}

func formatDiscoverValue(value any) string {
	switch v := value.(type) {
	case time.Time:
		if v.IsZero() {
			return ""
		}
		return v.UTC().Format(time.RFC3339)
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', 2, 64)
	case float32:
		f := float64(v)
		if f == float64(int64(f)) {
			return strconv.FormatInt(int64(f), 10)
		}
		return strconv.FormatFloat(f, 'f', 2, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprintf("%v", value)
	}
}

func executeDiscoverResult(ctx context.Context, queries telemetryquery.Service, query discover.Query, viewType string) (discoverResultView, error) {
	switch strings.ToLower(strings.TrimSpace(viewType)) {
	case "series":
		result, err := queries.ExecuteSeries(ctx, query)
		if err != nil {
			return discoverResultView{}, err
		}
		return renderDiscoverSeries(result), nil
	case "stat":
		result, err := queries.ExecuteTable(ctx, query)
		if err != nil {
			return discoverResultView{}, err
		}
		return renderDiscoverStat(result), nil
	default:
		result, err := queries.ExecuteTable(ctx, query)
		if err != nil {
			return discoverResultView{}, err
		}
		return renderDiscoverTable(ctx, queries, query, result), nil
	}
}

func discoverUsesIssueSearchFastPath(state discoverBuilderState) bool {
	if strings.ToLower(strings.TrimSpace(state.Dataset)) != string(discover.DatasetIssues) {
		return false
	}
	if strings.ToLower(strings.TrimSpace(state.Visualization)) != "table" && strings.TrimSpace(state.Visualization) != "" {
		return false
	}
	if strings.TrimSpace(state.Columns) != "" || strings.TrimSpace(state.OrderBy) != "" {
		return false
	}
	return strings.TrimSpace(state.Aggregate) == "" && discoverGroupByIsNone(state.GroupBy)
}

func discoverIssueSearchQuery(state discoverBuilderState) string {
	query := strings.TrimSpace(state.Query)
	env := strings.TrimSpace(state.Environment)
	if env == "" {
		return query
	}
	if strings.Contains(strings.ToLower(query), "environment:") || strings.Contains(strings.ToLower(query), "env:") {
		return query
	}
	if query != "" {
		query += " "
	}
	return query + "environment:" + env
}

func discoverGroupByIsNone(raw string) bool {
	value := strings.TrimSpace(raw)
	return value == "" || strings.EqualFold(value, "none")
}

func renderDiscoverTable(ctx context.Context, queries telemetryquery.Service, query discover.Query, result discover.TableResult) discoverResultView {
	view := discoverResultView{Type: "table"}
	for _, col := range result.Columns {
		view.Columns = append(view.Columns, col.Name)
	}
	needsProfile := query.Dataset == discover.DatasetTransactions && len(query.GroupBy) == 0 && query.Rollup == nil && !hasDiscoverAggregate(query)
	if needsProfile {
		view.Columns = append(view.Columns, "profile")
	}
	for _, row := range result.Rows {
		rendered := make([]discoverCell, 0, len(view.Columns))
		for _, column := range result.Columns {
			text := formatDiscoverValue(row[column.Name])
			cell := discoverCell{Text: text}
			switch column.Name {
			case "issue.id":
				if text != "" {
					cell.Href = "/issues/" + text + "/"
				}
			case "event.id":
				if text != "" {
					cell.Href = "/events/" + text + "/"
				}
			case "trace.id":
				if text != "" {
					cell.Href = "/traces/" + text + "/"
				}
			}
			rendered = append(rendered, cell)
		}
		if needsProfile {
			profileCell := discoverCell{}
			projectID := formatDiscoverValue(row["project.id"])
			traceID := formatDiscoverValue(row["trace.id"])
			transaction := formatDiscoverValue(row["transaction"])
			release := formatDiscoverValue(row["release"])
			if related, err := queries.FindRelatedProfile(ctx, projectID, traceID, transaction, release); err == nil && related != nil {
				profileCell.Text = related.ProfileID
				profileCell.Href = "/profiles/" + related.ProfileID + "/"
			} else {
				profileCell.Text = "-"
			}
			rendered = append(rendered, profileCell)
		}
		view.Rows = append(view.Rows, rendered)
	}
	return view
}

func renderDiscoverIssueRows(rows []store.DiscoverIssue) discoverResultView {
	view := discoverResultView{
		Type:    "table",
		Columns: []string{"issue.id", "project.slug", "title", "culprit", "status", "last_seen"},
	}
	for _, row := range rows {
		shortID := row.ID
		if row.ShortID > 0 {
			shortID = "GENTRY-" + strconv.Itoa(row.ShortID)
		}
		view.Rows = append(view.Rows, []discoverCell{
			{Text: shortID, Href: "/issues/" + row.ID + "/"},
			{Text: row.ProjectSlug},
			{Text: row.Title, Href: "/issues/" + row.ID + "/"},
			{Text: row.Culprit},
			{Text: row.Status},
			{Text: row.LastSeen.UTC().Format(time.RFC3339)},
		})
	}
	return view
}

func renderDiscoverSeries(result discover.SeriesResult) discoverResultView {
	view := discoverResultView{
		Type:    "series",
		Columns: []string{"bucket"},
	}
	valueColumns := make([]string, 0, len(result.Columns))
	for _, col := range result.Columns {
		valueColumns = append(valueColumns, col.Name)
		view.Columns = append(view.Columns, col.Name)
	}
	for _, point := range result.Points {
		row := []discoverCell{{Text: point.Bucket.UTC().Format(time.RFC3339)}}
		for _, col := range valueColumns {
			row = append(row, discoverCell{Text: formatDiscoverValue(point.Values[col])})
		}
		view.Rows = append(view.Rows, row)
	}
	return view
}

func renderDiscoverStat(result discover.TableResult) discoverResultView {
	view := discoverResultView{Type: "stat"}
	if len(result.Columns) == 0 || len(result.Rows) == 0 {
		return view
	}
	view.StatLabel = result.Columns[0].Name
	view.StatValue = formatDiscoverValue(result.Rows[0][result.Columns[0].Name])
	return view
}

func hasDiscoverAggregate(query discover.Query) bool {
	for _, item := range query.Select {
		if strings.TrimSpace(item.Expr.Call) != "" {
			return true
		}
	}
	return false
}

func discoverFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
