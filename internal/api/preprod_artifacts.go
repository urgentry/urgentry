package api

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
)

type preprodArtifactResponse struct {
	BuildID              string                  `json:"buildId"`
	State                string                  `json:"state"`
	AppInfo              preprodArtifactAppInfo  `json:"appInfo"`
	GitInfo              *preprodArtifactGitInfo `json:"gitInfo"`
	Platform             *string                 `json:"platform"`
	ProjectID            string                  `json:"projectId"`
	ProjectSlug          string                  `json:"projectSlug"`
	BuildConfiguration   *string                 `json:"buildConfiguration"`
	IsInstallable        bool                    `json:"isInstallable"`
	InstallURL           *string                 `json:"installUrl"`
	DownloadCount        int                     `json:"downloadCount"`
	ReleaseNotes         *string                 `json:"releaseNotes"`
	InstallGroups        []string                `json:"installGroups"`
	IsCodeSignatureValid *bool                   `json:"isCodeSignatureValid"`
	ProfileName          *string                 `json:"profileName"`
	CodesigningType      *string                 `json:"codesigningType"`
}

type preprodArtifactAppInfo struct {
	AppID        *string `json:"appId"`
	Name         *string `json:"name"`
	Version      *string `json:"version"`
	BuildNumber  *int    `json:"buildNumber"`
	ArtifactType *string `json:"artifactType"`
	DateAdded    *string `json:"dateAdded"`
	DateBuilt    *string `json:"dateBuilt"`
}

type preprodArtifactGitInfo struct {
	HeadSHA      *string `json:"headSha"`
	BaseSHA      *string `json:"baseSha"`
	Provider     *string `json:"provider"`
	HeadRepoName *string `json:"headRepoName"`
	BaseRepoName *string `json:"baseRepoName"`
	HeadRef      *string `json:"headRef"`
	BaseRef      *string `json:"baseRef"`
	PRNumber     *int    `json:"prNumber"`
}

type preprodSizeAnalysisResponse struct {
	BuildID          string                          `json:"buildId"`
	State            string                          `json:"state"`
	AppInfo          preprodArtifactAppInfo          `json:"appInfo"`
	GitInfo          *preprodArtifactGitInfo         `json:"gitInfo"`
	ErrorCode        *string                         `json:"errorCode"`
	ErrorMessage     *string                         `json:"errorMessage"`
	DownloadSize     *int64                          `json:"downloadSize"`
	InstallSize      *int64                          `json:"installSize"`
	AnalysisDuration *float64                        `json:"analysisDuration"`
	AnalysisVersion  *string                         `json:"analysisVersion"`
	BaseBuildID      *string                         `json:"baseBuildId"`
	BaseAppInfo      *preprodArtifactAppInfo         `json:"baseAppInfo"`
	Insights         map[string]any                  `json:"insights"`
	AppComponents    []preprodAppComponentResponse   `json:"appComponents"`
	Comparisons      []preprodSizeComparisonResponse `json:"comparisons"`
}

type preprodAppComponentResponse struct {
	ComponentType string `json:"componentType"`
	Name          string `json:"name"`
	AppID         string `json:"appId"`
	Path          string `json:"path"`
	DownloadSize  int64  `json:"downloadSize"`
	InstallSize   int64  `json:"installSize"`
}

type preprodSizeComparisonResponse struct {
	MetricsArtifactType string                           `json:"metricsArtifactType"`
	Identifier          *string                          `json:"identifier"`
	State               string                           `json:"state"`
	ErrorCode           *string                          `json:"errorCode"`
	ErrorMessage        *string                          `json:"errorMessage"`
	SizeMetricDiff      *preprodSizeMetricDiffResponse   `json:"sizeMetricDiff"`
	DiffItems           []preprodDiffItemResponse        `json:"diffItems"`
	InsightDiffItems    []preprodInsightDiffItemResponse `json:"insightDiffItems"`
}

type preprodSizeMetricDiffResponse struct {
	MetricsArtifactType string  `json:"metricsArtifactType"`
	Identifier          *string `json:"identifier"`
	HeadInstallSize     int64   `json:"headInstallSize"`
	HeadDownloadSize    int64   `json:"headDownloadSize"`
	BaseInstallSize     int64   `json:"baseInstallSize"`
	BaseDownloadSize    int64   `json:"baseDownloadSize"`
}

type preprodDiffItemResponse struct {
	SizeDiff  int64                     `json:"sizeDiff"`
	HeadSize  *int64                    `json:"headSize"`
	BaseSize  *int64                    `json:"baseSize"`
	Path      string                    `json:"path"`
	ItemType  *string                   `json:"itemType"`
	Type      string                    `json:"type"`
	DiffItems []preprodDiffItemResponse `json:"diffItems"`
}

type preprodInsightDiffItemResponse struct {
	InsightType        string                    `json:"insightType"`
	Status             string                    `json:"status"`
	TotalSavingsChange int64                     `json:"totalSavingsChange"`
	FileDiffs          []preprodDiffItemResponse `json:"fileDiffs"`
	GroupDiffs         []preprodDiffItemResponse `json:"groupDiffs"`
}

type preprodLatestResponse struct {
	LatestArtifact  *preprodArtifactResponse `json:"latestArtifact"`
	CurrentArtifact *preprodArtifactResponse `json:"currentArtifact"`
}

func handleGetPreprodArtifactInstallDetails(db *sql.DB, artifacts *sqlite.PreprodArtifactStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		artifact, err := artifacts.Get(r.Context(), PathParam(r, "artifact_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load preprod artifact.")
			return
		}
		if artifact == nil || artifact.OrganizationID != org.ID {
			httputil.WriteError(w, http.StatusNotFound, "Preprod artifact not found.")
			return
		}
		projectSlug, err := preprodArtifactProjectSlug(r.Context(), db, artifact.ProjectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapPreprodArtifactResponse(artifact, projectSlug))
	}
}

func handleGetPreprodArtifactSizeAnalysis(db *sql.DB, artifacts *sqlite.PreprodArtifactStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		head, err := artifacts.Get(r.Context(), PathParam(r, "artifact_id"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load preprod artifact.")
			return
		}
		if head == nil || head.OrganizationID != org.ID {
			httputil.WriteError(w, http.StatusNotFound, "Preprod artifact not found.")
			return
		}

		base, explicitBaseMissing, err := resolvePreprodBaseArtifact(r, artifacts, head)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load preprod artifact.")
			return
		}
		if explicitBaseMissing {
			httputil.WriteError(w, http.StatusNotFound, "Base preprod artifact not found.")
			return
		}

		response := preprodSizeAnalysisResponse{
			BuildID:          head.BuildID,
			State:            valueOrDefault(head.AnalysisState, "NOT_RAN"),
			AppInfo:          mapPreprodAppInfo(head.AppInfo),
			GitInfo:          mapPreprodGitInfo(head.GitInfo),
			ErrorCode:        nullableString(head.AnalysisErrorCode),
			ErrorMessage:     nullableString(head.AnalysisErrorMessage),
			DownloadSize:     head.DownloadSize,
			InstallSize:      head.InstallSize,
			AnalysisDuration: head.AnalysisDuration,
			AnalysisVersion:  nullableString(head.AnalysisVersion),
			Insights:         head.Insights,
			AppComponents:    mapPreprodAppComponents(head.AppComponents),
		}
		if base != nil {
			response.BaseBuildID = nullableString(base.BuildID)
			baseInfo := mapPreprodAppInfo(base.AppInfo)
			response.BaseAppInfo = &baseInfo
			response.Comparisons = buildPreprodSizeComparisons(head, base)
		}
		httputil.WriteJSON(w, http.StatusOK, response)
	}
}

func handleGetLatestPreprodArtifact(db *sql.DB, artifacts *sqlite.PreprodArtifactStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectID(w, r, db)
		if !ok {
			return
		}

		filter, err := parsePreprodLatestFilter(r)
		if err != nil {
			httputil.WriteAPIError(w, httputil.APIError{
				Status: http.StatusBadRequest,
				Detail: err.Error(),
			})
			return
		}

		items, err := artifacts.ListByProject(r.Context(), projectID)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load preprod artifacts.")
			return
		}
		sort.SliceStable(items, func(i, j int) bool {
			left := preprodArtifactSortTime(items[i])
			right := preprodArtifactSortTime(items[j])
			if left.Equal(right) {
				if items[i].AppInfo.BuildNumber != nil && items[j].AppInfo.BuildNumber != nil && *items[i].AppInfo.BuildNumber != *items[j].AppInfo.BuildNumber {
					return *items[i].AppInfo.BuildNumber > *items[j].AppInfo.BuildNumber
				}
				return items[i].ID > items[j].ID
			}
			return left.After(right)
		})

		var latest *sqlite.PreprodArtifact
		for _, item := range items {
			if item.IsInstallable && matchesPreprodLatestFilter(item, filter) {
				latest = item
				break
			}
		}

		var current *sqlite.PreprodArtifact
		if filter.buildVersion != "" {
			for _, item := range items {
				if matchesPreprodLatestFilter(item, filter) && item.AppInfo.Version == filter.buildVersion {
					if filter.buildNumber != nil && (item.AppInfo.BuildNumber == nil || *item.AppInfo.BuildNumber != *filter.buildNumber) {
						continue
					}
					if filter.mainBinaryIdentifier != "" && item.MainBinaryIdentifier != filter.mainBinaryIdentifier {
						continue
					}
					current = item
					break
				}
			}
		}

		projectSlug := PathParam(r, "proj_slug")
		httputil.WriteJSON(w, http.StatusOK, preprodLatestResponse{
			LatestArtifact:  mapPreprodArtifactResponse(latest, projectSlug),
			CurrentArtifact: mapPreprodArtifactResponse(current, projectSlug),
		})
	}
}

type preprodLatestFilter struct {
	appID                string
	platform             string
	buildVersion         string
	buildNumber          *int
	mainBinaryIdentifier string
	buildConfiguration   string
	codesigningType      string
	installGroups        []string
}

func parsePreprodLatestFilter(r *http.Request) (preprodLatestFilter, error) {
	query := r.URL.Query()
	appID := strings.TrimSpace(query.Get("appId"))
	if appID == "" {
		return preprodLatestFilter{}, errPreprodQuery("Missing required query parameter: appId.")
	}
	platform, err := normalizePreprodPlatform(strings.TrimSpace(query.Get("platform")))
	if err != nil {
		return preprodLatestFilter{}, err
	}

	filter := preprodLatestFilter{
		appID:                appID,
		platform:             platform,
		buildVersion:         strings.TrimSpace(query.Get("buildVersion")),
		mainBinaryIdentifier: strings.TrimSpace(query.Get("mainBinaryIdentifier")),
		buildConfiguration:   strings.TrimSpace(query.Get("buildConfiguration")),
		codesigningType:      strings.TrimSpace(query.Get("codesigningType")),
		installGroups:        preprodInstallGroupsFromQuery(query["installGroups"]),
	}
	if buildNumber := strings.TrimSpace(query.Get("buildNumber")); buildNumber != "" {
		value, parseErr := parsePositiveInt(buildNumber)
		if parseErr != nil {
			return preprodLatestFilter{}, errPreprodQuery("Invalid query parameter: buildNumber.")
		}
		filter.buildNumber = &value
	}
	if filter.buildVersion != "" && filter.buildNumber == nil && filter.mainBinaryIdentifier == "" {
		return preprodLatestFilter{}, errPreprodQuery("buildVersion requires buildNumber or mainBinaryIdentifier.")
	}
	return filter, nil
}

func resolvePreprodBaseArtifact(r *http.Request, artifacts *sqlite.PreprodArtifactStore, head *sqlite.PreprodArtifact) (*sqlite.PreprodArtifact, bool, error) {
	explicitBaseID := strings.TrimSpace(r.URL.Query().Get("baseArtifactId"))
	if explicitBaseID != "" {
		base, err := artifacts.Get(r.Context(), explicitBaseID)
		if err != nil {
			return nil, false, err
		}
		if base == nil || base.ProjectID != head.ProjectID || base.OrganizationID != head.OrganizationID {
			return nil, true, nil
		}
		return base, false, nil
	}
	if strings.TrimSpace(head.DefaultBaseArtifactID) != "" {
		base, err := artifacts.Get(r.Context(), head.DefaultBaseArtifactID)
		if err != nil {
			return nil, false, err
		}
		if base != nil && base.ProjectID == head.ProjectID && base.OrganizationID == head.OrganizationID {
			return base, false, nil
		}
	}
	items, err := artifacts.ListByProject(r.Context(), head.ProjectID)
	if err != nil {
		return nil, false, err
	}
	sort.SliceStable(items, func(i, j int) bool {
		return preprodArtifactSortTime(items[i]).After(preprodArtifactSortTime(items[j]))
	})
	for _, candidate := range items {
		if candidate.ID == head.ID {
			continue
		}
		if candidate.OrganizationID != head.OrganizationID {
			continue
		}
		if candidate.AppInfo.AppID != head.AppInfo.AppID {
			continue
		}
		if !strings.EqualFold(candidate.Platform, head.Platform) {
			continue
		}
		return candidate, false, nil
	}
	return nil, false, nil
}

func mapPreprodArtifactResponse(artifact *sqlite.PreprodArtifact, projectSlug string) *preprodArtifactResponse {
	if artifact == nil {
		return nil
	}
	return &preprodArtifactResponse{
		BuildID:              artifact.BuildID,
		State:                valueOrDefault(artifact.State, "PROCESSED"),
		AppInfo:              mapPreprodAppInfo(artifact.AppInfo),
		GitInfo:              mapPreprodGitInfo(artifact.GitInfo),
		Platform:             nullableString(artifact.Platform),
		ProjectID:            artifact.ProjectID,
		ProjectSlug:          projectSlug,
		BuildConfiguration:   nullableString(artifact.BuildConfiguration),
		IsInstallable:        artifact.IsInstallable,
		InstallURL:           nullableString(artifact.InstallURL),
		DownloadCount:        artifact.DownloadCount,
		ReleaseNotes:         nullableString(artifact.ReleaseNotes),
		InstallGroups:        artifact.InstallGroups,
		IsCodeSignatureValid: artifact.IsCodeSignatureValid,
		ProfileName:          nullableString(artifact.ProfileName),
		CodesigningType:      nullableString(artifact.CodesigningType),
	}
}

func mapPreprodAppInfo(info sqlite.PreprodArtifactAppInfo) preprodArtifactAppInfo {
	return preprodArtifactAppInfo{
		AppID:        nullableString(info.AppID),
		Name:         nullableString(info.Name),
		Version:      nullableString(info.Version),
		BuildNumber:  info.BuildNumber,
		ArtifactType: nullableString(info.ArtifactType),
		DateAdded:    nullableString(info.DateAdded),
		DateBuilt:    nullableString(info.DateBuilt),
	}
}

func mapPreprodGitInfo(info *sqlite.PreprodArtifactGitInfo) *preprodArtifactGitInfo {
	if info == nil {
		return nil
	}
	return &preprodArtifactGitInfo{
		HeadSHA:      nullableString(info.HeadSHA),
		BaseSHA:      nullableString(info.BaseSHA),
		Provider:     nullableString(info.Provider),
		HeadRepoName: nullableString(info.HeadRepoName),
		BaseRepoName: nullableString(info.BaseRepoName),
		HeadRef:      nullableString(info.HeadRef),
		BaseRef:      nullableString(info.BaseRef),
		PRNumber:     info.PRNumber,
	}
}

func mapPreprodAppComponents(components []sqlite.PreprodArtifactComponent) []preprodAppComponentResponse {
	if components == nil {
		return nil
	}
	items := make([]preprodAppComponentResponse, 0, len(components))
	for _, component := range components {
		items = append(items, preprodAppComponentResponse{
			ComponentType: component.ComponentType,
			Name:          component.Name,
			AppID:         component.AppID,
			Path:          component.Path,
			DownloadSize:  component.DownloadSize,
			InstallSize:   component.InstallSize,
		})
	}
	return items
}

func buildPreprodSizeComparisons(head, base *sqlite.PreprodArtifact) []preprodSizeComparisonResponse {
	if head == nil || base == nil || head.DownloadSize == nil || head.InstallSize == nil || base.DownloadSize == nil || base.InstallSize == nil {
		return nil
	}
	metricsArtifactType := valueOrDefault(head.AppInfo.ArtifactType, "APP")
	return []preprodSizeComparisonResponse{
		{
			MetricsArtifactType: metricsArtifactType,
			Identifier:          nullableString(head.MainBinaryIdentifier),
			State:               "COMPLETED",
			SizeMetricDiff: &preprodSizeMetricDiffResponse{
				MetricsArtifactType: metricsArtifactType,
				Identifier:          nullableString(head.MainBinaryIdentifier),
				HeadInstallSize:     *head.InstallSize,
				HeadDownloadSize:    *head.DownloadSize,
				BaseInstallSize:     *base.InstallSize,
				BaseDownloadSize:    *base.DownloadSize,
			},
		},
	}
}

func matchesPreprodLatestFilter(artifact *sqlite.PreprodArtifact, filter preprodLatestFilter) bool {
	if artifact == nil {
		return false
	}
	if artifact.AppInfo.AppID != filter.appID {
		return false
	}
	if !strings.EqualFold(artifact.Platform, filter.platform) {
		return false
	}
	if filter.buildConfiguration != "" && artifact.BuildConfiguration != filter.buildConfiguration {
		return false
	}
	if filter.codesigningType != "" && artifact.CodesigningType != filter.codesigningType {
		return false
	}
	if len(filter.installGroups) == 0 {
		return true
	}
	if len(artifact.InstallGroups) == 0 {
		return false
	}
	allowed := make(map[string]struct{}, len(artifact.InstallGroups))
	for _, group := range artifact.InstallGroups {
		group = strings.TrimSpace(group)
		if group != "" {
			allowed[group] = struct{}{}
		}
	}
	for _, group := range filter.installGroups {
		if _, ok := allowed[group]; ok {
			return true
		}
	}
	return false
}

func preprodArtifactProjectSlug(ctx context.Context, db *sql.DB, projectID string) (string, error) {
	var slug string
	if err := db.QueryRowContext(ctx, `SELECT slug FROM projects WHERE id = ?`, projectID).Scan(&slug); err != nil {
		return "", err
	}
	return slug, nil
}

func preprodArtifactSortTime(artifact *sqlite.PreprodArtifact) time.Time {
	for _, raw := range []string{artifact.AppInfo.DateAdded, artifact.AppInfo.DateBuilt} {
		if ts, ok := parsePreprodTime(raw); ok {
			return ts
		}
	}
	return artifact.CreatedAt
}

func parsePreprodTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

func preprodInstallGroupsFromQuery(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	groups := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				groups = append(groups, part)
			}
		}
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

func normalizePreprodPlatform(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "apple":
		return "APPLE", nil
	case "android":
		return "ANDROID", nil
	case "":
		return "", errPreprodQuery("Missing required query parameter: platform.")
	default:
		return "", errPreprodQuery("Invalid query parameter: platform.")
	}
}

func parsePositiveInt(raw string) (int, error) {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return 0, err
	}
	return value, nil
}

func errPreprodQuery(detail string) error {
	return preprodQueryError(detail)
}

type preprodQueryError string

func (e preprodQueryError) Error() string { return string(e) }

func nullableString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
