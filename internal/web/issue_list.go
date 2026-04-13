package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/store"
)

const defaultPageSize = 25

// ---------------------------------------------------------------------------
// Issue List
// ---------------------------------------------------------------------------

type savedSearchView struct {
	ID          string
	Name        string
	Favorite    bool
	Query       string
	Filter      string
	Environment string
}

type issueListData struct {
	Title            string
	Nav              string
	Filter           string
	Query            string
	Sort             string            // current sort field
	Environment      string            // selected environment ("" = all)
	Environments     []string          // available environments
	TimeRange        string            // current time range preset value
	TimeRangeOptions []TimeRangePreset // available presets
	TotalCount       int
	UnresolvedCount  int
	ResolvedCount    int
	IgnoredCount     int
	Issues           []issueRow
	CurrentPage      int
	TotalPages       int
	HasPrev          bool
	HasNext          bool
	PrevPage         int
	NextPage         int
	RangeStart       int // 1-based start index for "1-25 of 243"
	RangeEnd         int // 1-based end index
	FilteredCount    int // total matching current filter/search
	SavedSearches    []savedSearchView
}

type issueRow struct {
	ID            string
	ShortID       string
	Title         string
	Culprit       string
	Level         string
	Status        string
	StatusLabel   string
	Release       string
	EventCount    string
	UserCount     string
	TimeAgo       string
	Age           string
	TimeClass     string
	Sparkline     []int
	Assignee      string
	Priority      int    // 0=Critical, 1=High, 2=Medium, 3=Low
	PriorityLabel string // "Critical", "High", "Medium", "Low"
}

func parsePage(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 1 {
		return 1
	}
	return p
}

func paginationData(page, totalItems int) (currentPage, totalPages, prevPage, nextPage int, hasPrev, hasNext bool) {
	totalPages = (totalItems + defaultPageSize - 1) / defaultPageSize
	if totalPages < 1 {
		totalPages = 1
	}
	currentPage = page
	if currentPage > totalPages {
		currentPage = totalPages
	}
	hasPrev = currentPage > 1
	hasNext = currentPage < totalPages
	prevPage = currentPage - 1
	nextPage = currentPage + 1
	return
}

func (h *Handler) issueListPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		http.Error(w, "Web UI unavailable", http.StatusServiceUnavailable)
		return
	}
	h.issueListFromDB(w, r)
}

func (h *Handler) issueListFromDB(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filter := r.URL.Query().Get("filter")
	if filter == "" {
		filter = "all"
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	page := parsePage(r)
	env := getSelectedEnvironment(w, r)
	sort := getSelectedSort(w, r)
	timeRange, since := getSelectedTimeRange(w, r)

	// Fetch available environments for the dropdown.
	environments, err := h.webStore.ListEnvironments(ctx)
	if err != nil {
		http.Error(w, "Failed to load environments.", http.StatusInternalServerError)
		return
	}

	// Get counts for tabs from DB — scoped by environment and time range.
	// Use ListIssues with Limit=1 to get counts without fetching full rows.
	countForFilter := func(f string) (int, error) {
		_, n, err := h.webStore.ListIssues(ctx, store.IssueListOpts{
			Filter: f, Environment: env, Since: since, Limit: 1,
		})
		return n, err
	}
	totalCount, err := countForFilter("all")
	if err != nil {
		http.Error(w, "Failed to load issue counts.", http.StatusInternalServerError)
		return
	}
	unresolvedCount, err := countForFilter("unresolved")
	if err != nil {
		http.Error(w, "Failed to load issue counts.", http.StatusInternalServerError)
		return
	}
	resolvedCount, err := countForFilter("resolved")
	if err != nil {
		http.Error(w, "Failed to load issue counts.", http.StatusInternalServerError)
		return
	}
	ignoredCount, err := countForFilter("ignored")
	if err != nil {
		http.Error(w, "Failed to load issue counts.", http.StatusInternalServerError)
		return
	}

	// Determine filtered count for pagination.
	var filteredCount int
	if query != "" {
		_, filteredCount, err = h.webStore.ListIssues(ctx, store.IssueListOpts{
			Filter: filter, Query: query, Environment: env, Since: since, Limit: 1,
		})
		if err != nil {
			http.Error(w, "Failed to load filtered issue count.", http.StatusInternalServerError)
			return
		}
	} else {
		switch filter {
		case "unresolved":
			filteredCount = unresolvedCount
		case "resolved":
			filteredCount = resolvedCount
		case "ignored":
			filteredCount = ignoredCount
		default:
			filteredCount = totalCount
		}
	}

	currentPage, totalPages, prevPage, nextPage, hasPrev, hasNext := paginationData(page, filteredCount)
	offset := (currentPage - 1) * defaultPageSize

	opts := store.IssueListOpts{
		Filter:      filter,
		Query:       query,
		Environment: env,
		Sort:        sort,
		Since:       since,
		Limit:       defaultPageSize,
		Offset:      offset,
	}
	wsIssues, _, err := h.webStore.ListIssues(ctx, opts)
	if err != nil {
		http.Error(w, "Failed to load issues.", http.StatusInternalServerError)
		return
	}

	groupIDs := make([]string, len(wsIssues))
	for i, issue := range wsIssues {
		groupIDs[i] = issue.ID
	}

	// Batch: user counts (1 query instead of N).
	userCounts, err := h.webStore.BatchUserCounts(ctx, groupIDs)
	if err != nil {
		http.Error(w, "Failed to load issue user counts.", http.StatusInternalServerError)
		return
	}

	// Batch: sparkline data (1 query instead of N).
	sparklines, err := h.webStore.BatchSparklines(ctx, groupIDs, 7, 24*time.Hour)
	if err != nil {
		http.Error(w, "Failed to load issue sparkline data.", http.StatusInternalServerError)
		return
	}

	rows := make([]issueRow, 0, len(wsIssues))
	for i, issue := range wsIssues {
		ago := timeAgo(issue.LastSeen)
		cls := timeAgoClass(issue.LastSeen)
		age := timeAgoCompact(issue.FirstSeen)

		sparkline := sparklines[issue.ID]
		if sparkline == nil {
			sparkline = []int{}
		}

		userCount := userCounts[issue.ID]

		shortID := fmt.Sprintf("GENTRY-%d", issue.ShortID)
		if issue.ShortID == 0 {
			shortID = fmt.Sprintf("GENTRY-%d", offset+i+1)
		}

		rows = append(rows, issueRow{
			ID:            issue.ID,
			ShortID:       shortID,
			Title:         issue.Title,
			Culprit:       issue.Culprit,
			Level:         issue.Level,
			Status:        issue.Status,
			StatusLabel:   issueStatusLabel(issue.Status, issue.ResolutionSubstatus, issue.ResolvedInRelease),
			EventCount:    formatNumber(int(issue.Count)),
			UserCount:     formatNumber(userCount),
			TimeAgo:       ago,
			Age:           age,
			TimeClass:     cls,
			Sparkline:     sparkline,
			Assignee:      issue.Assignee,
			Priority:      issue.Priority,
			PriorityLabel: priorityLabel(issue.Priority),
		})
	}

	// Calculate range for "1-25 of 243" display.
	rangeStart := offset + 1
	rangeEnd := offset + len(rows)
	if filteredCount == 0 {
		rangeStart = 0
	}

	// Load saved searches.
	var savedSearchViews []savedSearchView
	if h.searches != nil {
		if principal := auth.PrincipalFromContext(ctx); principal != nil && principal.User != nil {
			scope, err := h.defaultPageScope(ctx)
			if err == nil {
				if ss, err := h.searches.List(ctx, principal.User.ID, scope.OrganizationSlug); err == nil {
					for _, s := range ss {
						savedSearchViews = append(savedSearchViews, savedSearchView{
							ID:          s.ID,
							Name:        s.Name,
							Favorite:    s.Favorite,
							Query:       s.Query,
							Filter:      s.Filter,
							Environment: s.Environment,
						})
					}
				}
			}
		}
	}

	data := issueListData{
		Title:            "Issues",
		Nav:              "issues",
		Filter:           filter,
		Query:            query,
		Sort:             sort,
		Environment:      env,
		Environments:     environments,
		TimeRange:        timeRange,
		TimeRangeOptions: timeRangePresets,
		TotalCount:       totalCount,
		UnresolvedCount:  unresolvedCount,
		ResolvedCount:    resolvedCount,
		IgnoredCount:     ignoredCount,
		Issues:           rows,
		CurrentPage:      currentPage,
		TotalPages:       totalPages,
		HasPrev:          hasPrev,
		HasNext:          hasNext,
		PrevPage:         prevPage,
		NextPage:         nextPage,
		RangeStart:       rangeStart,
		RangeEnd:         rangeEnd,
		FilteredCount:    filteredCount,
		SavedSearches:    savedSearchViews,
	}

	h.render(w, "issue-list.html", data)
}
