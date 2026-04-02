package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/auth"
	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

type preventPageInfo struct {
	EndCursor       *string `json:"endCursor"`
	StartCursor     *string `json:"startCursor"`
	HasPreviousPage bool    `json:"hasPreviousPage"`
	HasNextPage     bool    `json:"hasNextPage"`
}

type preventRepositoryResponse struct {
	Name           string `json:"name"`
	UpdatedAt      string `json:"updatedAt"`
	LatestCommitAt string `json:"latestCommitAt"`
	DefaultBranch  string `json:"defaultBranch"`
}

type preventRepositoriesResponse struct {
	Results    []preventRepositoryResponse `json:"results"`
	PageInfo   preventPageInfo             `json:"pageInfo"`
	TotalCount int                         `json:"totalCount"`
}

type preventRepositoryTokenResponse struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

type preventRepositoryTokensResponse struct {
	Results    []preventRepositoryTokenResponse `json:"results"`
	PageInfo   preventPageInfo                  `json:"pageInfo"`
	TotalCount int                              `json:"totalCount"`
}

type preventRepositoryDetailResponse struct {
	UploadToken          string `json:"uploadToken"`
	TestAnalyticsEnabled bool   `json:"testAnalyticsEnabled"`
}

type preventRepositoryBranchResponse struct {
	Name string `json:"name"`
}

type preventRepositoryBranchesResponse struct {
	DefaultBranch string                            `json:"defaultBranch"`
	Results       []preventRepositoryBranchResponse `json:"results"`
	PageInfo      preventPageInfo                   `json:"pageInfo"`
	TotalCount    int                               `json:"totalCount"`
}

type preventRepositorySyncResponse struct {
	IsSyncing bool `json:"isSyncing"`
}

type preventRepositoryTestResultResponse struct {
	UpdatedAt           string  `json:"updatedAt"`
	AvgDuration         float64 `json:"avgDuration"`
	TotalDuration       float64 `json:"totalDuration"`
	Name                string  `json:"name"`
	FailureRate         float64 `json:"failureRate"`
	FlakeRate           float64 `json:"flakeRate"`
	TotalFailCount      int     `json:"totalFailCount"`
	TotalFlakyFailCount int     `json:"totalFlakyFailCount"`
	TotalSkipCount      int     `json:"totalSkipCount"`
	TotalPassCount      int     `json:"totalPassCount"`
	LastDuration        float64 `json:"lastDuration"`
}

type preventRepositoryTestResultsResponse struct {
	DefaultBranch string                                `json:"defaultBranch"`
	Results       []preventRepositoryTestResultResponse `json:"results"`
	PageInfo      preventPageInfo                       `json:"pageInfo"`
	TotalCount    int                                   `json:"totalCount"`
}

type preventRepositoryTestSuitesResponse struct {
	TestSuites []string `json:"testSuites"`
}

type preventRepositoryTestResultsAggregatesResponse struct {
	TotalDuration                     float64 `json:"totalDuration"`
	TotalDurationPercentChange        float64 `json:"totalDurationPercentChange"`
	SlowestTestsDuration              float64 `json:"slowestTestsDuration"`
	SlowestTestsDurationPercentChange float64 `json:"slowestTestsDurationPercentChange"`
	TotalSlowTests                    int     `json:"totalSlowTests"`
	TotalSlowTestsPercentChange       float64 `json:"totalSlowTestsPercentChange"`
	TotalFails                        int     `json:"totalFails"`
	TotalFailsPercentChange           float64 `json:"totalFailsPercentChange"`
	TotalSkips                        int     `json:"totalSkips"`
	TotalSkipsPercentChange           float64 `json:"totalSkipsPercentChange"`
	FlakeCount                        int     `json:"flakeCount"`
	FlakeCountPercentChange           float64 `json:"flakeCountPercentChange"`
	FlakeRate                         float64 `json:"flakeRate"`
	FlakeRatePercentChange            float64 `json:"flakeRatePercentChange"`
}

func handleListPreventRepositories(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		if owner == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Owner is required.")
			return
		}
		repos, err := prevent.ListRepositories(r.Context(), org.Slug, owner)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent repositories.")
			return
		}
		term := strings.TrimSpace(r.URL.Query().Get("term"))
		filtered := make([]store.PreventRepository, 0, len(repos))
		for _, repo := range repos {
			if matchesPreventTerm(term, repo.Name, repo.ExternalSlug, repo.Provider) {
				filtered = append(filtered, repo)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].DateCreated.Equal(filtered[j].DateCreated) {
				return filtered[i].Name < filtered[j].Name
			}
			return filtered[i].DateCreated.After(filtered[j].DateCreated)
		})
		limit, err := parsePreventLimit(r.URL.Query().Get("limit"), 50)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		page, info, err := paginatePrevent(filtered, limit, r.URL.Query().Get("cursor"), r.URL.Query().Get("navigation"))
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		results := make([]preventRepositoryResponse, 0, len(page))
		for _, repo := range page {
			results = append(results, preventRepositoryResponse{
				Name:           repo.Name,
				UpdatedAt:      preventRepositoryUpdatedAt(repo),
				LatestCommitAt: preventRepositoryLatestCommitAt(repo),
				DefaultBranch:  preventDefaultBranch(repo.DefaultBranch),
			})
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositoriesResponse{
			Results:    results,
			PageInfo:   info,
			TotalCount: len(filtered),
		})
	}
}

func handleGetPreventRepository(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		repository := strings.TrimSpace(PathParam(r, "repository"))
		repo, ok := loadPreventRepository(w, r, prevent, PathParam(r, "org_slug"), owner, repository)
		if !ok {
			return
		}
		token, ok, err := getPreventPrimaryToken(r, prevent, PathParam(r, "org_slug"), owner, repository)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Prevent repository token.")
			return
		}
		if !ok {
			token = store.PreventRepositoryToken{}
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositoryDetailResponse{
			UploadToken:          preventTokenValue(token),
			TestAnalyticsEnabled: repo.TestAnalyticsEnabled,
		})
	}
}

func handleListPreventRepositoryTokens(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug"))
		if !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		if owner == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Owner is required.")
			return
		}
		repos, err := prevent.ListRepositories(r.Context(), org.Slug, owner)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent repository tokens.")
			return
		}
		items := make([]preventRepositoryTokenResponse, 0, len(repos))
		for _, repo := range repos {
			tokens, err := prevent.ListRepositoryTokens(r.Context(), org.Slug, owner, repo.Name)
			if err != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent repository tokens.")
				return
			}
			token, ok := preventPrimaryToken(tokens)
			if !ok {
				continue
			}
			items = append(items, preventRepositoryTokenResponse{
				Name:  repo.Name,
				Token: preventTokenValue(token),
			})
		}
		term := strings.TrimSpace(r.URL.Query().Get("term"))
		filtered := make([]preventRepositoryTokenResponse, 0, len(items))
		for _, item := range items {
			if matchesPreventTerm(term, item.Name, item.Token) {
				filtered = append(filtered, item)
			}
		}
		sort.Slice(filtered, func(i, j int) bool { return filtered[i].Name < filtered[j].Name })
		limit, err := parsePreventLimit(r.URL.Query().Get("limit"), 50)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		page, info, err := paginatePrevent(filtered, limit, r.URL.Query().Get("cursor"), r.URL.Query().Get("navigation"))
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositoryTokensResponse{
			Results:    page,
			PageInfo:   info,
			TotalCount: len(filtered),
		})
	}
}

func handleRegeneratePreventRepositoryToken(catalog controlplane.CatalogStore, authz *auth.Authorizer, prevent store.PreventStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if !validatePreventSessionCSRF(w, r, authz) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		repository := strings.TrimSpace(PathParam(r, "repository"))
		token, ok, err := getPreventPrimaryToken(r, prevent, PathParam(r, "org_slug"), owner, repository)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Prevent repository token.")
			return
		}
		if !ok {
			httputil.WriteError(w, http.StatusNotFound, "Prevent repository token not found.")
			return
		}
		_, raw, err := prevent.RegenerateRepositoryToken(r.Context(), PathParam(r, "org_slug"), owner, repository, token.ID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to regenerate Prevent repository token.")
			return
		}
		if raw == "" {
			httputil.WriteError(w, http.StatusNotFound, "Prevent repository token not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, map[string]string{"token": raw})
	}
}

func handleGetPreventRepositoriesSync(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		if owner == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Owner is required.")
			return
		}
		isSyncing, err := prevent.GetOwnerSyncStatus(r.Context(), PathParam(r, "org_slug"), owner)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Prevent sync status.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositorySyncResponse{IsSyncing: isSyncing})
	}
}

func handleStartPreventRepositoriesSync(catalog controlplane.CatalogStore, authz *auth.Authorizer, prevent store.PreventStore, authenticate authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !authenticate(w, r) {
			return
		}
		if !validatePreventSessionCSRF(w, r, authz) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		if owner == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Owner is required.")
			return
		}
		isSyncing, err := prevent.StartOwnerSync(r.Context(), PathParam(r, "org_slug"), owner)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to start Prevent repository sync.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositorySyncResponse{IsSyncing: isSyncing})
	}
}

func handleListPreventRepositoryBranches(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		repository := strings.TrimSpace(PathParam(r, "repository"))
		repo, ok := loadPreventRepository(w, r, prevent, PathParam(r, "org_slug"), owner, repository)
		if !ok {
			return
		}
		branches, err := prevent.ListRepositoryBranches(r.Context(), PathParam(r, "org_slug"), owner, repository)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent repository branches.")
			return
		}
		term := strings.TrimSpace(r.URL.Query().Get("term"))
		filtered := make([]store.PreventRepositoryBranch, 0, len(branches))
		for _, branch := range branches {
			if matchesPreventTerm(term, branch.Name) {
				filtered = append(filtered, branch)
			}
		}
		sort.Slice(filtered, func(i, j int) bool {
			if filtered[i].IsDefault != filtered[j].IsDefault {
				return filtered[i].IsDefault
			}
			return filtered[i].Name < filtered[j].Name
		})
		limit, err := parsePreventLimit(r.URL.Query().Get("limit"), 25)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		page, info, err := paginatePrevent(filtered, limit, r.URL.Query().Get("cursor"), r.URL.Query().Get("navigation"))
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		results := make([]preventRepositoryBranchResponse, 0, len(page))
		for _, branch := range page {
			results = append(results, preventRepositoryBranchResponse{Name: branch.Name})
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositoryBranchesResponse{
			DefaultBranch: preventDefaultBranch(repo.DefaultBranch),
			Results:       results,
			PageInfo:      info,
			TotalCount:    len(filtered),
		})
	}
}

func handleListPreventRepositoryTestSuites(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		repository := strings.TrimSpace(PathParam(r, "repository"))
		if _, ok := loadPreventRepository(w, r, prevent, PathParam(r, "org_slug"), owner, repository); !ok {
			return
		}
		suites, err := prevent.ListRepositoryTestSuites(r.Context(), PathParam(r, "org_slug"), owner, repository)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent test suites.")
			return
		}
		term := strings.TrimSpace(r.URL.Query().Get("term"))
		names := make([]string, 0, len(suites))
		for _, suite := range suites {
			if matchesPreventTerm(term, suite.Name) {
				names = append(names, suite.Name)
			}
		}
		sort.Strings(names)
		httputil.WriteJSON(w, http.StatusOK, preventRepositoryTestSuitesResponse{TestSuites: names})
	}
}

func handleListPreventRepositoryTestResults(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		repository := strings.TrimSpace(PathParam(r, "repository"))
		repo, ok := loadPreventRepository(w, r, prevent, PathParam(r, "org_slug"), owner, repository)
		if !ok {
			return
		}
		results, err := prevent.ListRepositoryTestResults(r.Context(), PathParam(r, "org_slug"), owner, repository)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent test results.")
			return
		}
		filtered := filterPreventTestResults(results, r)
		stats := buildPreventTestResultStats(filtered)
		sortPreventTestResults(filtered, stats, r.URL.Query().Get("sortBy"), r.URL.Query().Get("filterBy"))
		limit, err := parsePreventLimit(r.URL.Query().Get("limit"), 20)
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		page, info, err := paginatePrevent(filtered, limit, r.URL.Query().Get("cursor"), r.URL.Query().Get("navigation"))
		if err != nil {
			httputil.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		out := make([]preventRepositoryTestResultResponse, 0, len(page))
		for _, result := range page {
			out = append(out, preventTestResultResponse(result, stats))
		}
		httputil.WriteJSON(w, http.StatusOK, preventRepositoryTestResultsResponse{
			DefaultBranch: preventDefaultBranch(repo.DefaultBranch),
			Results:       out,
			PageInfo:      info,
			TotalCount:    len(filtered),
		})
	}
}

func handleListPreventRepositoryTestResultsAggregates(catalog controlplane.CatalogStore, prevent store.PreventStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		if _, ok := getOrganizationFromCatalog(w, r, catalog, PathParam(r, "org_slug")); !ok {
			return
		}
		owner := strings.TrimSpace(PathParam(r, "owner"))
		repository := strings.TrimSpace(PathParam(r, "repository"))
		if _, ok := loadPreventRepository(w, r, prevent, PathParam(r, "org_slug"), owner, repository); !ok {
			return
		}
		results, err := prevent.ListRepositoryTestResults(r.Context(), PathParam(r, "org_slug"), owner, repository)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list Prevent test aggregates.")
			return
		}
		now := time.Now().UTC()
		window := preventIntervalWindow(r.URL.Query().Get("interval"))
		current := filterPreventTestResultsWindow(results, strings.TrimSpace(r.URL.Query().Get("branch")), now.Add(-window), now)
		previous := filterPreventTestResultsWindow(results, strings.TrimSpace(r.URL.Query().Get("branch")), now.Add(-2*window), now.Add(-window))
		currentAgg := buildPreventAggregates(current)
		previousAgg := buildPreventAggregates(previous)
		httputil.WriteJSON(w, http.StatusOK, preventRepositoryTestResultsAggregatesResponse{
			TotalDuration:                     currentAgg.totalDuration,
			TotalDurationPercentChange:        preventPercentChange(currentAgg.totalDuration, previousAgg.totalDuration),
			SlowestTestsDuration:              currentAgg.slowestTestsDuration,
			SlowestTestsDurationPercentChange: preventPercentChange(currentAgg.slowestTestsDuration, previousAgg.slowestTestsDuration),
			TotalSlowTests:                    currentAgg.totalSlowTests,
			TotalSlowTestsPercentChange:       preventPercentChange(float64(currentAgg.totalSlowTests), float64(previousAgg.totalSlowTests)),
			TotalFails:                        currentAgg.totalFails,
			TotalFailsPercentChange:           preventPercentChange(float64(currentAgg.totalFails), float64(previousAgg.totalFails)),
			TotalSkips:                        currentAgg.totalSkips,
			TotalSkipsPercentChange:           preventPercentChange(float64(currentAgg.totalSkips), float64(previousAgg.totalSkips)),
			FlakeCount:                        currentAgg.flakeCount,
			FlakeCountPercentChange:           preventPercentChange(float64(currentAgg.flakeCount), float64(previousAgg.flakeCount)),
			FlakeRate:                         currentAgg.flakeRate,
			FlakeRatePercentChange:            preventPercentChange(currentAgg.flakeRate, previousAgg.flakeRate),
		})
	}
}

func loadPreventRepository(w http.ResponseWriter, r *http.Request, prevent store.PreventStore, orgSlug, owner, repository string) (*store.PreventRepository, bool) {
	repo, err := prevent.GetRepository(r.Context(), orgSlug, owner, repository)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load Prevent repository.")
		return nil, false
	}
	if repo == nil {
		httputil.WriteError(w, http.StatusNotFound, "Prevent repository not found.")
		return nil, false
	}
	return repo, true
}

func getPreventPrimaryToken(r *http.Request, prevent store.PreventStore, orgSlug, owner, repository string) (store.PreventRepositoryToken, bool, error) {
	tokens, err := prevent.ListRepositoryTokens(r.Context(), orgSlug, owner, repository)
	if err != nil {
		return store.PreventRepositoryToken{}, false, err
	}
	token, ok := preventPrimaryToken(tokens)
	return token, ok, nil
}

func preventPrimaryToken(tokens []store.PreventRepositoryToken) (store.PreventRepositoryToken, bool) {
	for _, token := range tokens {
		if token.Status == "active" {
			return token, true
		}
	}
	if len(tokens) == 0 {
		return store.PreventRepositoryToken{}, false
	}
	return tokens[0], true
}

func preventTokenValue(token store.PreventRepositoryToken) string {
	if strings.TrimSpace(token.Token) != "" {
		return token.Token
	}
	return token.TokenPrefix
}

func preventRepositoryUpdatedAt(repo store.PreventRepository) string {
	if repo.LastSyncStartedAt != nil {
		return repo.LastSyncStartedAt.UTC().Format(time.RFC3339)
	}
	if repo.LastSyncedAt != nil {
		return repo.LastSyncedAt.UTC().Format(time.RFC3339)
	}
	if !repo.DateCreated.IsZero() {
		return repo.DateCreated.UTC().Format(time.RFC3339)
	}
	return ""
}

func preventRepositoryLatestCommitAt(repo store.PreventRepository) string {
	if repo.LastSyncedAt != nil {
		return repo.LastSyncedAt.UTC().Format(time.RFC3339)
	}
	return preventRepositoryUpdatedAt(repo)
}

func preventDefaultBranch(branch string) string {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return "main"
	}
	return branch
}

func parsePreventLimit(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit <= 0 {
		return 0, errors.New("provided `limit` parameter must be a positive integer")
	}
	return limit, nil
}

func matchesPreventTerm(term string, values ...string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	if term == "" {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), term) {
			return true
		}
	}
	return false
}

func paginatePrevent[T any](items []T, limit int, rawCursor, navigation string) ([]T, preventPageInfo, error) {
	info := preventPageInfo{}
	if len(items) == 0 {
		return []T{}, info, nil
	}
	start := 0
	if strings.TrimSpace(rawCursor) != "" {
		cursor, err := strconv.Atoi(strings.TrimSpace(rawCursor))
		if err != nil || cursor < 0 {
			return nil, info, errors.New("invalid cursor")
		}
		if strings.EqualFold(strings.TrimSpace(navigation), "prev") {
			start = cursor - limit
			if start < 0 {
				start = 0
			}
		} else {
			start = cursor + 1
		}
	}
	if start >= len(items) {
		info.HasPreviousPage = len(items) > 0
		return []T{}, info, nil
	}
	end := start + limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]T(nil), items[start:end]...)
	info.HasPreviousPage = start > 0
	info.HasNextPage = end < len(items)
	info.StartCursor = preventCursorPtr(start)
	info.EndCursor = preventCursorPtr(end - 1)
	return page, info, nil
}

func preventCursorPtr(v int) *string {
	if v < 0 {
		return nil
	}
	raw := strconv.Itoa(v)
	return &raw
}

func filterPreventTestResults(items []store.PreventRepositoryTestResult, r *http.Request) []store.PreventRepositoryTestResult {
	now := time.Now().UTC()
	branch := strings.TrimSpace(r.URL.Query().Get("branch"))
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	suites := map[string]struct{}{}
	for _, value := range r.URL.Query()["testSuites"] {
		value = strings.TrimSpace(value)
		if value != "" {
			suites[value] = struct{}{}
		}
	}
	start := now.Add(-preventIntervalWindow(r.URL.Query().Get("interval")))
	filterBy := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("filterBy")))
	filtered := make([]store.PreventRepositoryTestResult, 0, len(items))
	for _, item := range items {
		if !item.DateCreated.IsZero() && item.DateCreated.Before(start) {
			continue
		}
		if branch != "" && item.BranchName != branch {
			continue
		}
		if len(suites) > 0 {
			if _, ok := suites[item.SuiteName]; !ok {
				continue
			}
		}
		if !matchesPreventTerm(term, item.SuiteName, item.BranchName, item.CommitSHA, item.ID) {
			continue
		}
		switch filterBy {
		case "FAILED_TESTS":
			if item.FailureCount == 0 {
				continue
			}
		case "SKIPPED_TESTS":
			if item.SkippedCount == 0 {
				continue
			}
		}
		filtered = append(filtered, item)
	}
	if filterBy == "FLAKY_TESTS" {
		stats := buildPreventTestResultStats(filtered)
		flaky := make([]store.PreventRepositoryTestResult, 0, len(filtered))
		for _, item := range filtered {
			if stats[preventTestResultKey(item)].IsFlaky {
				flaky = append(flaky, item)
			}
		}
		return flaky
	}
	return filtered
}

func filterPreventTestResultsWindow(items []store.PreventRepositoryTestResult, branch string, start, end time.Time) []store.PreventRepositoryTestResult {
	filtered := make([]store.PreventRepositoryTestResult, 0, len(items))
	for _, item := range items {
		if branch != "" && item.BranchName != branch {
			continue
		}
		if item.DateCreated.Before(start) || !item.DateCreated.Before(end) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func sortPreventTestResults(items []store.PreventRepositoryTestResult, stats map[string]preventTestResultStats, sortBy, filterBy string) {
	desc := true
	field := strings.TrimSpace(sortBy)
	if field == "" {
		field = "-RUNS_FAILED"
	}
	if strings.HasPrefix(field, "-") {
		field = strings.TrimPrefix(field, "-")
		desc = true
	} else if strings.HasPrefix(field, "+") {
		field = strings.TrimPrefix(field, "+")
		desc = false
	} else {
		desc = false
	}
	field = strings.ToUpper(field)
	if strings.EqualFold(strings.TrimSpace(filterBy), "SLOWEST_TESTS") && strings.TrimSpace(sortBy) == "" {
		field = "AVG_DURATION"
		desc = true
	}
	metric := func(item store.PreventRepositoryTestResult) float64 {
		switch field {
		case "AVG_DURATION":
			if stat, ok := stats[preventTestResultKey(item)]; ok && stat.AvgDuration > 0 {
				return stat.AvgDuration
			}
			return preventAvgDuration(item)
		case "FAILURE_RATE":
			return preventFailureRate(item)
		case "FLAKE_RATE":
			return stats[preventTestResultKey(item)].FlakeRate
		case "UPDATED_AT":
			return float64(item.DateCreated.UnixNano())
		default:
			return float64(item.FailureCount)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		left := metric(items[i])
		right := metric(items[j])
		if left == right {
			if items[i].DateCreated.Equal(items[j].DateCreated) {
				return items[i].ID < items[j].ID
			}
			return items[i].DateCreated.After(items[j].DateCreated)
		}
		if desc {
			return left > right
		}
		return left < right
	})
}

func preventAvgDuration(item store.PreventRepositoryTestResult) float64 {
	if item.TestCount <= 0 {
		return float64(item.DurationMS)
	}
	return float64(item.DurationMS) / float64(item.TestCount)
}

func preventFailureRate(item store.PreventRepositoryTestResult) float64 {
	if item.TestCount <= 0 {
		return 0
	}
	return float64(item.FailureCount) / float64(item.TestCount)
}

func preventTestResultResponse(item store.PreventRepositoryTestResult, stats map[string]preventTestResultStats) preventRepositoryTestResultResponse {
	totalPass := item.TestCount - item.FailureCount - item.SkippedCount
	if totalPass < 0 {
		totalPass = 0
	}
	name := strings.TrimSpace(item.SuiteName)
	if name == "" {
		name = item.ID
	}
	stat := stats[preventTestResultKey(item)]
	avgDuration := preventAvgDuration(item)
	if stat.AvgDuration > 0 {
		avgDuration = stat.AvgDuration
	}
	return preventRepositoryTestResultResponse{
		UpdatedAt:           item.DateCreated.UTC().Format(time.RFC3339),
		AvgDuration:         avgDuration,
		TotalDuration:       float64(item.DurationMS),
		Name:                name,
		FailureRate:         preventFailureRate(item),
		FlakeRate:           stat.FlakeRate,
		TotalFailCount:      item.FailureCount,
		TotalFlakyFailCount: stat.TotalFlakyFailCount,
		TotalSkipCount:      item.SkippedCount,
		TotalPassCount:      totalPass,
		LastDuration:        float64(item.DurationMS),
	}
}

func preventIntervalWindow(raw string) time.Duration {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "INTERVAL_1_DAY":
		return 24 * time.Hour
	case "INTERVAL_7_DAY":
		return 7 * 24 * time.Hour
	default:
		return 30 * 24 * time.Hour
	}
}

type preventAggregateSnapshot struct {
	totalDuration        float64
	slowestTestsDuration float64
	totalSlowTests       int
	totalFails           int
	totalSkips           int
	flakeCount           int
	flakeRate            float64
}

func buildPreventAggregates(items []store.PreventRepositoryTestResult) preventAggregateSnapshot {
	snapshot := preventAggregateSnapshot{}
	stats := buildPreventTestResultStats(items)
	slowest := make([]float64, 0, len(stats))
	for _, item := range items {
		snapshot.totalDuration += float64(item.DurationMS)
		snapshot.totalFails += item.FailureCount
		snapshot.totalSkips += item.SkippedCount
	}
	for _, stat := range stats {
		if stat.AvgDuration >= 1000 {
			snapshot.totalSlowTests++
		}
		if stat.IsFlaky {
			snapshot.flakeCount++
		}
		slowest = append(slowest, stat.AvgDuration)
	}
	sort.Slice(slowest, func(i, j int) bool { return slowest[i] > slowest[j] })
	for i, value := range slowest {
		if i >= 5 {
			break
		}
		snapshot.slowestTestsDuration += value
	}
	if len(stats) > 0 {
		snapshot.flakeRate = float64(snapshot.flakeCount) / float64(len(stats))
	}
	return snapshot
}

type preventTestResultStats struct {
	AvgDuration         float64
	FlakeRate           float64
	TotalFlakyFailCount int
	IsFlaky             bool
}

func buildPreventTestResultStats(items []store.PreventRepositoryTestResult) map[string]preventTestResultStats {
	type accumulator struct {
		durationMS int64
		testCount  int
		failCount  int
		hasPass    bool
		hasFailure bool
	}
	accumulators := make(map[string]*accumulator, len(items))
	for _, item := range items {
		key := preventTestResultKey(item)
		acc := accumulators[key]
		if acc == nil {
			acc = &accumulator{}
			accumulators[key] = acc
		}
		acc.durationMS += item.DurationMS
		acc.testCount += item.TestCount
		acc.failCount += item.FailureCount
		if item.FailureCount > 0 {
			acc.hasFailure = true
		} else {
			acc.hasPass = true
		}
	}
	stats := make(map[string]preventTestResultStats, len(accumulators))
	for key, acc := range accumulators {
		avg := float64(acc.durationMS)
		if acc.testCount > 0 {
			avg = float64(acc.durationMS) / float64(acc.testCount)
		}
		stat := preventTestResultStats{AvgDuration: avg}
		if acc.hasFailure && acc.hasPass {
			stat.IsFlaky = true
			stat.TotalFlakyFailCount = acc.failCount
			if acc.testCount > 0 {
				stat.FlakeRate = float64(acc.failCount) / float64(acc.testCount)
			}
		}
		stats[key] = stat
	}
	return stats
}

func preventTestResultKey(item store.PreventRepositoryTestResult) string {
	name := strings.TrimSpace(item.SuiteName)
	if name == "" {
		name = item.ID
	}
	return name + "\x00" + strings.TrimSpace(item.BranchName)
}

func preventPercentChange(current, previous float64) float64 {
	if previous == 0 {
		return 0
	}
	return ((current - previous) / previous) * 100
}

func validatePreventSessionCSRF(w http.ResponseWriter, r *http.Request, authz *auth.Authorizer) bool {
	principal := auth.PrincipalFromContext(r.Context())
	if principal == nil || principal.Kind != auth.CredentialSession {
		return true
	}
	if authz != nil && authz.ValidateCSRF(r) {
		return true
	}
	httputil.WriteAPIError(w, httputil.APIError{
		Status: http.StatusForbidden,
		Code:   "csrf_failed",
		Detail: "CSRF validation failed.",
	})
	return false
}
