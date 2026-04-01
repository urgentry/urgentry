package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/discover"
)

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

type dashboardData struct {
	Title            string
	Nav              string
	Guide            analyticsGuide
	Environment      string   // selected environment ("" = all)
	Environments     []string // available environments
	BannerColor      string
	BannerIcon       rawHTML
	BannerText       string
	TotalEvents      string
	EventsTrend      rawHTML
	EventsTrendClass string
	TotalErrors      string
	ErrorsTrend      rawHTML
	ErrorsTrendClass string
	UsersAffected    string
	UsersTrend       rawHTML
	UsersTrendClass  string
	LatencyLabel     string
	LatencyValue     string
	LatencyHint      string
	StarterViews     []analyticsStarterViewCard
	BurningIssues    []burningIssue
	RecentActivity   []activityItem
	RecentLogs       []dashboardLogRow
	RecentTraces     []dashboardTraceRow
	RecentReleases   []releaseRow
	RecentReplays    []replayRow
	RecentProfiles   []profileRow
	// Beyond-Sentry features
	FirstEventMetric string           // time-to-first-event display text
	ErrorBudget      *ErrorBudgetData // error budget bar data
	QueryWidgets     []queryWidget
}

type burningIssue struct {
	ID     string
	Title  string
	Change int
}

type activityItem struct {
	Icon    string
	TimeAgo string
	Color   string
	Text    string
}

type queryWidget struct {
	Name        string
	Query       string
	Filter      string
	Environment string
	Count       string
	URL         string
}

// trendInfo holds the display string and CSS class for a trend value.
type trendInfo struct {
	Text  rawHTML
	Class string
}

// calcTrend computes a trend between current and previous period counts.
// rising=true means an increase is bad (e.g., errors going up).
func calcTrend(current, previous int, rising bool) trendInfo {
	if previous == 0 && current == 0 {
		return trendInfo{Text: rawHTML("&#8594; 0%"), Class: "neutral"}
	}
	denom := previous
	if denom == 0 {
		denom = 1
	}
	pct := (current - previous) * 100 / denom

	if pct > 0 {
		cls := "bad"
		if !rising {
			cls = "good"
		}
		return trendInfo{
			Text:  rawHTML(fmt.Sprintf("&#8599; +%d%%", pct)),
			Class: cls,
		}
	}
	if pct < 0 {
		cls := "good"
		if !rising {
			cls = "bad"
		}
		return trendInfo{
			Text:  rawHTML(fmt.Sprintf("&#8600; %d%%", pct)),
			Class: cls,
		}
	}
	return trendInfo{Text: rawHTML("&#8594; 0%"), Class: "neutral"}
}

func (h *Handler) dashboardPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	h.dashboardFromDB(w, r)
}

func (h *Handler) dashboardFromDB(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	now := time.Now().UTC()
	env := getSelectedEnvironment(w, r)
	scope, err := h.defaultPageScope(ctx)
	if err != nil {
		writeWebInternal(w, r, "Failed to resolve dashboard scope.")
		return
	}
	environments, err := h.webStore.ListEnvironments(ctx)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard environments.")
		return
	}

	summary, err := h.webStore.DashboardSummary(ctx, now)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard summary.")
		return
	}
	events, err := h.listRecentEventsDB(ctx, 20)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard activity.")
		return
	}
	burningRows, err := h.webStore.ListBurningIssues(ctx, now, 5)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard issues.")
		return
	}

	eventsTrend := calcTrend(summary.EventsCurrent, summary.EventsPrevious, true)
	errorsTrend := calcTrend(summary.ErrorsCurrent, summary.ErrorsPrevious, true)
	usersTrend := calcTrend(summary.UsersCurrent, summary.UsersPrevious, true)

	// Determine banner
	bannerColor := "green"
	bannerIcon := rawHTML("&#9679;")
	bannerText := "All Clear — Error rate within baseline"
	if summary.UnresolvedGroups > 3 {
		bannerColor = "amber"
		bannerIcon = rawHTML("&#9888;")
		bannerText = fmt.Sprintf("Elevated — %d unresolved issues above baseline", summary.UnresolvedGroups)
	}
	if summary.UnresolvedGroups > 10 {
		bannerColor = "red"
		bannerIcon = rawHTML("&#9888;")
		bannerText = fmt.Sprintf("Critical — %d unresolved issues, error rate elevated", summary.UnresolvedGroups)
	}
	burning := make([]burningIssue, 0, len(burningRows))
	for _, item := range burningRows {
		burning = append(burning, burningIssue{ID: item.ID, Title: item.Title, Change: item.Change})
	}

	// Recent activity from recent events
	activity := make([]activityItem, 0, len(events))
	for _, e := range events {
		ago := timeAgo(e.Timestamp)
		activity = append(activity, activityItem{
			TimeAgo: ago,
			Color:   levelColor(e.Level),
			Text:    fmt.Sprintf("New event: %s", e.Title),
		})
	}
	if len(activity) > 8 {
		activity = activity[:8]
	}

	// Beyond-Sentry features.
	firstEventText := h.timeToFirstEvent(ctx)
	errorBudget := h.computeErrorBudget(ctx)
	queryWidgets := h.dashboardQueryWidgets(ctx)
	starterViews := analyticsStarterViewCards("")
	recentLogs, err := h.dashboardRecentLogs(ctx, scope.OrganizationSlug, env, 5)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard logs.")
		return
	}
	recentTraces, latencyLabel, latencyValue, latencyHint, err := h.dashboardRecentTransactions(ctx, scope.OrganizationSlug, env, 5)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard transactions.")
		return
	}
	recentReleases, err := h.dashboardRecentReleases(ctx, 4)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard releases.")
		return
	}
	recentReplays, err := h.dashboardRecentReplays(ctx, scope.ProjectID, env, 4)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard replays.")
		return
	}
	recentProfiles, err := h.dashboardRecentProfiles(ctx, scope.ProjectID, env, 4)
	if err != nil {
		writeWebInternal(w, r, "Failed to load dashboard profiles.")
		return
	}

	data := dashboardData{
		Title:            "Dashboard",
		Nav:              "dashboard",
		Guide:            analyticsHomeGuide(),
		Environment:      env,
		Environments:     environments,
		BannerColor:      bannerColor,
		BannerIcon:       bannerIcon,
		BannerText:       bannerText,
		TotalEvents:      formatNumber(summary.TotalEvents),
		EventsTrend:      eventsTrend.Text,
		EventsTrendClass: eventsTrend.Class,
		TotalErrors:      fmt.Sprintf("%d", summary.UnresolvedGroups),
		ErrorsTrend:      errorsTrend.Text,
		ErrorsTrendClass: errorsTrend.Class,
		UsersAffected:    formatNumber(summary.UsersTotal),
		UsersTrend:       usersTrend.Text,
		UsersTrendClass:  usersTrend.Class,
		LatencyLabel:     latencyLabel,
		LatencyValue:     latencyValue,
		LatencyHint:      latencyHint,
		StarterViews:     starterViews,
		BurningIssues:    burning,
		RecentActivity:   activity,
		RecentLogs:       recentLogs,
		RecentTraces:     recentTraces,
		RecentReleases:   recentReleases,
		RecentReplays:    recentReplays,
		RecentProfiles:   recentProfiles,
		FirstEventMetric: firstEventText,
		ErrorBudget:      errorBudget,
		QueryWidgets:     queryWidgets,
	}

	h.render(w, "dashboard.html", data)
}

func (h *Handler) dashboardQueryWidgets(ctx context.Context) []queryWidget {
	if h.searches == nil || h.webStore == nil {
		return nil
	}
	principal := auth.PrincipalFromContext(ctx)
	if principal == nil || principal.User == nil {
		return nil
	}
	scope, err := h.defaultPageScope(ctx)
	if err != nil {
		return nil
	}
	searches, err := h.searches.List(ctx, principal.User.ID, scope.OrganizationSlug)
	if err != nil {
		return nil
	}
	if len(searches) > 4 {
		searches = searches[:4]
	}
	widgets := make([]queryWidget, 0, len(searches))
	for _, saved := range searches {
		if saved.QueryDoc.Dataset != discover.DatasetIssues || len(saved.QueryDoc.Select) > 0 || len(saved.QueryDoc.GroupBy) > 0 || saved.QueryDoc.Rollup != nil {
			continue
		}
		count, err := h.savedSearchCount(ctx, saved.Filter, saved.Query, saved.Environment)
		if err != nil {
			continue
		}
		widgets = append(widgets, queryWidget{
			Name:        saved.Name,
			Query:       saved.Query,
			Filter:      saved.Filter,
			Environment: saved.Environment,
			Count:       formatNumber(count),
			URL:         savedSearchURL(saved.Filter, saved.Query, saved.Environment, saved.Sort),
		})
	}
	return widgets
}

func (h *Handler) savedSearchCount(ctx context.Context, filter, query, environment string) (int, error) {
	if environment != "" {
		return h.webStore.CountSearchGroupsForEnvironment(ctx, environment, filter, query)
	}
	return h.webStore.CountSearchGroups(ctx, filter, query)
}

func savedSearchURL(filter, query, environment, sort string) string {
	values := url.Values{}
	if filter != "" {
		values.Set("filter", filter)
	}
	if query != "" {
		values.Set("query", query)
	}
	if environment != "" {
		values.Set("environment", environment)
	}
	if sort != "" {
		values.Set("sort", sort)
	}
	return "/issues/?" + values.Encode()
}
