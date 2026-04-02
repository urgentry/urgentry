package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/pkg/id"
)

// PreprodArtifactStore persists mobile build metadata used by the
// preprod-artifact parity endpoints.
type PreprodArtifactStore struct {
	db *sql.DB
}

// PreprodArtifact describes one stored mobile build artifact.
type PreprodArtifact struct {
	ID                    string
	OrganizationID        string
	ProjectID             string
	BuildID               string
	State                 string
	AppInfo               PreprodArtifactAppInfo
	GitInfo               *PreprodArtifactGitInfo
	Platform              string
	BuildConfiguration    string
	IsInstallable         bool
	InstallURL            string
	DownloadCount         int
	ReleaseNotes          string
	InstallGroups         []string
	IsCodeSignatureValid  *bool
	ProfileName           string
	CodesigningType       string
	MainBinaryIdentifier  string
	DefaultBaseArtifactID string
	AnalysisState         string
	AnalysisErrorCode     string
	AnalysisErrorMessage  string
	DownloadSize          *int64
	InstallSize           *int64
	AnalysisDuration      *float64
	AnalysisVersion       string
	Insights              map[string]any
	AppComponents         []PreprodArtifactComponent
	CreatedAt             time.Time
}

type PreprodArtifactAppInfo struct {
	AppID        string
	Name         string
	Version      string
	BuildNumber  *int
	ArtifactType string
	DateAdded    string
	DateBuilt    string
}

type PreprodArtifactGitInfo struct {
	HeadSHA      string
	BaseSHA      string
	Provider     string
	HeadRepoName string
	BaseRepoName string
	HeadRef      string
	BaseRef      string
	PRNumber     *int
}

type PreprodArtifactComponent struct {
	ComponentType string
	Name          string
	AppID         string
	Path          string
	DownloadSize  int64
	InstallSize   int64
}

// NewPreprodArtifactStore creates a mobile-build metadata store backed by SQLite.
func NewPreprodArtifactStore(db *sql.DB) *PreprodArtifactStore {
	return &PreprodArtifactStore{db: db}
}

// Save upserts one preprod artifact.
func (s *PreprodArtifactStore) Save(ctx context.Context, artifact *PreprodArtifact) error {
	if artifact == nil {
		return errors.New("preprod artifact is nil")
	}
	if s == nil || s.db == nil {
		return errors.New("preprod artifact store is not configured")
	}
	if strings.TrimSpace(artifact.ProjectID) == "" {
		return errors.New("preprod artifact project_id is required")
	}
	if strings.TrimSpace(artifact.OrganizationID) == "" {
		orgID, err := s.projectOrganizationID(ctx, artifact.ProjectID)
		if err != nil {
			return err
		}
		artifact.OrganizationID = orgID
	}
	if artifact.ID == "" {
		artifact.ID = id.New()
	}
	if artifact.BuildID == "" {
		artifact.BuildID = artifact.ID
	}
	if artifact.State == "" {
		artifact.State = "PROCESSED"
	}
	if artifact.AnalysisState == "" {
		artifact.AnalysisState = "NOT_RAN"
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO preprod_artifacts (
			id, organization_id, project_id, build_id, state,
			app_id, app_name, app_version, build_number, artifact_type, app_date_added, app_date_built,
			git_info_json, platform, build_configuration, is_installable, install_url, download_count,
			release_notes, install_groups_json, is_code_signature_valid, profile_name, codesigning_type,
			main_binary_identifier, default_base_artifact_id, analysis_state, analysis_error_code,
			analysis_error_message, download_size, install_size, analysis_duration, analysis_version,
			insights_json, app_components_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID,
		artifact.OrganizationID,
		artifact.ProjectID,
		artifact.BuildID,
		artifact.State,
		nullIfEmpty(artifact.AppInfo.AppID),
		nullIfEmpty(artifact.AppInfo.Name),
		nullIfEmpty(artifact.AppInfo.Version),
		nullableIntValue(artifact.AppInfo.BuildNumber),
		nullIfEmpty(artifact.AppInfo.ArtifactType),
		nullIfEmpty(artifact.AppInfo.DateAdded),
		nullIfEmpty(artifact.AppInfo.DateBuilt),
		marshalNullableJSON(artifact.GitInfo),
		nullIfEmpty(artifact.Platform),
		nullIfEmpty(artifact.BuildConfiguration),
		boolToInt(artifact.IsInstallable),
		nullIfEmpty(artifact.InstallURL),
		artifact.DownloadCount,
		nullIfEmpty(artifact.ReleaseNotes),
		marshalNullableJSON(artifact.InstallGroups),
		nullableBoolValue(artifact.IsCodeSignatureValid),
		nullIfEmpty(artifact.ProfileName),
		nullIfEmpty(artifact.CodesigningType),
		nullIfEmpty(artifact.MainBinaryIdentifier),
		nullIfEmpty(artifact.DefaultBaseArtifactID),
		artifact.AnalysisState,
		nullIfEmpty(artifact.AnalysisErrorCode),
		nullIfEmpty(artifact.AnalysisErrorMessage),
		nullableInt64Value(artifact.DownloadSize),
		nullableInt64Value(artifact.InstallSize),
		nullableFloat64Value(artifact.AnalysisDuration),
		nullIfEmpty(artifact.AnalysisVersion),
		marshalNullableJSON(artifact.Insights),
		marshalNullableJSON(artifact.AppComponents),
		artifact.CreatedAt.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("save preprod artifact: %w", err)
	}
	return nil
}

// Get loads one artifact by ID.
func (s *PreprodArtifactStore) Get(ctx context.Context, artifactID string) (*PreprodArtifact, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("preprod artifact store is not configured")
	}
	row := s.db.QueryRowContext(ctx, preprodArtifactSelectSQL+` WHERE id = ?`, artifactID)
	artifact, err := scanPreprodArtifact(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load preprod artifact: %w", err)
	}
	return artifact, nil
}

// ListByProject loads all stored artifacts for one project.
func (s *PreprodArtifactStore) ListByProject(ctx context.Context, projectID string) ([]*PreprodArtifact, error) {
	if s == nil || s.db == nil {
		return nil, errors.New("preprod artifact store is not configured")
	}
	rows, err := s.db.QueryContext(ctx, preprodArtifactSelectSQL+` WHERE project_id = ? ORDER BY created_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list preprod artifacts: %w", err)
	}
	defer rows.Close()

	items := make([]*PreprodArtifact, 0, 8)
	for rows.Next() {
		artifact, err := scanPreprodArtifact(rows)
		if err != nil {
			return nil, fmt.Errorf("scan preprod artifact: %w", err)
		}
		items = append(items, artifact)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate preprod artifacts: %w", err)
	}
	return items, nil
}

const preprodArtifactSelectSQL = `
	SELECT
		id, organization_id, project_id, build_id, state,
		app_id, app_name, app_version, build_number, artifact_type, app_date_added, app_date_built,
		git_info_json, platform, build_configuration, is_installable, install_url, download_count,
		release_notes, install_groups_json, is_code_signature_valid, profile_name, codesigning_type,
		main_binary_identifier, default_base_artifact_id, analysis_state, analysis_error_code,
		analysis_error_message, download_size, install_size, analysis_duration, analysis_version,
		insights_json, app_components_json, created_at
	FROM preprod_artifacts`

type preprodScanner interface {
	Scan(dest ...any) error
}

func scanPreprodArtifact(row preprodScanner) (*PreprodArtifact, error) {
	var artifact PreprodArtifact
	var (
		appID, appName, appVersion, artifactType                     sql.NullString
		dateAdded, dateBuilt, gitInfoJSON, platform                  sql.NullString
		buildConfiguration, installURL, releaseNotes                 sql.NullString
		installGroupsJSON, profileName                               sql.NullString
		codesigningType, mainBinaryIdentifier, defaultBaseArtifactID sql.NullString
		analysisErrorCode, analysisErrorMessage, analysisVersion     sql.NullString
		insightsJSON, appComponentsJSON, createdAt                   sql.NullString
		buildNumber, codeSigningValid                                sql.NullInt64
		downloadSize, installSize                                    sql.NullInt64
		analysisDuration                                             sql.NullFloat64
		isInstallable                                                int
	)
	if err := row.Scan(
		&artifact.ID,
		&artifact.OrganizationID,
		&artifact.ProjectID,
		&artifact.BuildID,
		&artifact.State,
		&appID,
		&appName,
		&appVersion,
		&buildNumber,
		&artifactType,
		&dateAdded,
		&dateBuilt,
		&gitInfoJSON,
		&platform,
		&buildConfiguration,
		&isInstallable,
		&installURL,
		&artifact.DownloadCount,
		&releaseNotes,
		&installGroupsJSON,
		&codeSigningValid,
		&profileName,
		&codesigningType,
		&mainBinaryIdentifier,
		&defaultBaseArtifactID,
		&artifact.AnalysisState,
		&analysisErrorCode,
		&analysisErrorMessage,
		&downloadSize,
		&installSize,
		&analysisDuration,
		&analysisVersion,
		&insightsJSON,
		&appComponentsJSON,
		&createdAt,
	); err != nil {
		return nil, err
	}

	artifact.IsInstallable = isInstallable != 0
	artifact.Platform = nullStr(platform)
	artifact.BuildConfiguration = nullStr(buildConfiguration)
	artifact.InstallURL = nullStr(installURL)
	artifact.ReleaseNotes = nullStr(releaseNotes)
	artifact.ProfileName = nullStr(profileName)
	artifact.CodesigningType = nullStr(codesigningType)
	artifact.MainBinaryIdentifier = nullStr(mainBinaryIdentifier)
	artifact.DefaultBaseArtifactID = nullStr(defaultBaseArtifactID)
	artifact.AnalysisErrorCode = nullStr(analysisErrorCode)
	artifact.AnalysisErrorMessage = nullStr(analysisErrorMessage)
	artifact.AnalysisVersion = nullStr(analysisVersion)
	artifact.CreatedAt = parseTime(nullStr(createdAt))
	artifact.AppInfo = PreprodArtifactAppInfo{
		AppID:        nullStr(appID),
		Name:         nullStr(appName),
		Version:      nullStr(appVersion),
		BuildNumber:  nullableIntPtr(buildNumber),
		ArtifactType: nullStr(artifactType),
		DateAdded:    nullStr(dateAdded),
		DateBuilt:    nullStr(dateBuilt),
	}
	artifact.DownloadSize = nullableInt64Ptr(downloadSize)
	artifact.InstallSize = nullableInt64Ptr(installSize)
	artifact.AnalysisDuration = nullableFloat64Ptr(analysisDuration)
	artifact.IsCodeSignatureValid = nullableBoolPtr(codeSigningValid)

	gitInfo, err := parseNullableJSON[*PreprodArtifactGitInfo](nullStr(gitInfoJSON))
	if err != nil {
		return nil, err
	}
	artifact.GitInfo = gitInfo

	installGroups, err := parseNullableJSON[[]string](nullStr(installGroupsJSON))
	if err != nil {
		return nil, err
	}
	artifact.InstallGroups = installGroups

	insights, err := parseNullableJSON[map[string]any](nullStr(insightsJSON))
	if err != nil {
		return nil, err
	}
	artifact.Insights = insights

	components, err := parseNullableJSON[[]PreprodArtifactComponent](nullStr(appComponentsJSON))
	if err != nil {
		return nil, err
	}
	artifact.AppComponents = components

	return &artifact, nil
}

func (s *PreprodArtifactStore) projectOrganizationID(ctx context.Context, projectID string) (string, error) {
	var organizationID string
	if err := s.db.QueryRowContext(ctx, `SELECT organization_id FROM projects WHERE id = ?`, projectID).Scan(&organizationID); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("project %q not found", projectID)
		}
		return "", fmt.Errorf("load project organization: %w", err)
	}
	return organizationID, nil
}

func marshalNullableJSON(value any) string {
	if value == nil {
		return "null"
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(data)
}

func parseNullableJSON[T any](raw string) (T, error) {
	var value T
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return value, nil
	}
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return value, fmt.Errorf("decode preprod artifact json: %w", err)
	}
	return value, nil
}

func nullableIntValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableIntPtr(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}
	converted := int(value.Int64)
	return &converted
}

func nullableInt64Value(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	converted := value.Int64
	return &converted
}

func nullableFloat64Value(value *float64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableFloat64Ptr(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}
	converted := value.Float64
	return &converted
}

func nullableBoolValue(value *bool) any {
	if value == nil {
		return nil
	}
	return boolToInt(*value)
}

func nullableBoolPtr(value sql.NullInt64) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Int64 != 0
	return &result
}
