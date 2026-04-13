package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"urgentry/internal/auth"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Settings Page
// ---------------------------------------------------------------------------

type settingsData struct {
	Title                   string
	Nav                     string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	ProjectID               string
	ProjectName             string
	ProjectSlug             string
	Platform                string
	ProjectStatus           string
	EventRetentionDays      int
	AttachmentRetentionDays int
	DebugRetentionDays      int
	DSN                     string
	Keys                    []settingsKey
	EventCount              string
	GroupCount              string
	DBSize                  string
	TelemetryPolicies       []settingsTelemetryPolicy
	ReplayPolicy            settingsReplayPolicy
	OwnershipRules          []settingsOwnershipRule
	CodeMappings            []settingsCodeMapping
	AuditLogs               []settingsAuditLog
	FormError               string
}

type settingsCodeMapping struct {
	ID            string
	StackRoot     string
	SourceRoot    string
	DefaultBranch string
	RepoURL       string
	CreatedAt     string
}

type settingsKey struct {
	PublicKey string
	Status    string
	CreatedAt string
}

type settingsAuditLog struct {
	Action    string
	Actor     string
	CreatedAt string
}

type settingsOwnershipRule struct {
	ID         string
	Name       string
	Pattern    string
	Assignee   string
	TeamSlug   string
	NotifyTeam bool
	CreatedAt  string
}

type settingsTelemetryPolicy struct {
	Surface         string
	Label           string
	RetentionDays   int
	StorageTier     string
	ArchiveDays     int
	SupportsArchive bool
}

type settingsReplayPolicy struct {
	SampleRate     float64
	MaxBytes       int64
	ScrubFields    string
	ScrubSelectors string
}

func (h *Handler) settingsPage(w http.ResponseWriter, r *http.Request) {
	if h.webStore == nil {
		writeWebUnavailable(w, r, "Web UI unavailable")
		return
	}
	ctx := r.Context()
	overview, err := h.webStore.SettingsOverview(ctx, 8)
	if err != nil {
		writeWebInternal(w, r, "Failed to load settings.")
		return
	}

	projectName := "Default Project"
	projectSlug := "default"
	platform := "go"
	projectStatus := "active"
	eventRetentionDays := 90
	attachmentRetentionDays := 30
	debugRetentionDays := 180
	dsn := ""
	currentProject := overview.Project
	var currentSettings *sharedstore.ProjectSettings
	var catalogProjects []sharedstore.Project
	if h.catalog != nil {
		catalogProjects, err = h.catalog.ListProjects(ctx, "")
		if err != nil {
			writeWebInternal(w, r, "Failed to load settings.")
			return
		}
		if len(catalogProjects) > 0 {
			currentProject = &catalogProjects[0]
		}
	}
	if currentProject != nil {
		projectName = currentProject.Name
		projectSlug = currentProject.Slug
		if currentProject.Platform != "" {
			platform = currentProject.Platform
		}
		if currentProject.Status != "" {
			projectStatus = currentProject.Status
		}
		if currentProject.EventRetentionDays > 0 {
			eventRetentionDays = currentProject.EventRetentionDays
		}
		if currentProject.AttachRetentionDays > 0 {
			attachmentRetentionDays = currentProject.AttachRetentionDays
		}
		if currentProject.DebugRetentionDays > 0 {
			debugRetentionDays = currentProject.DebugRetentionDays
		}
		if h.catalog != nil {
			currentSettings, err = h.catalog.GetProjectSettings(ctx, currentProject.OrgSlug, currentProject.Slug)
			if err != nil {
				writeWebInternal(w, r, "Failed to load settings.")
				return
			}
			if currentSettings != nil {
				if currentSettings.EventRetentionDays > 0 {
					eventRetentionDays = currentSettings.EventRetentionDays
				}
				if currentSettings.AttachmentRetentionDays > 0 {
					attachmentRetentionDays = currentSettings.AttachmentRetentionDays
				}
				if currentSettings.DebugFileRetentionDays > 0 {
					debugRetentionDays = currentSettings.DebugFileRetentionDays
				}
			}
		}
	}

	projectKeys := overview.ProjectKeys
	if h.catalog != nil && currentProject != nil {
		projectKeys, err = h.catalog.ListProjectKeys(ctx, currentProject.OrgSlug, currentProject.Slug)
		if err != nil {
			writeWebInternal(w, r, "Failed to load settings.")
			return
		}
		if len(projectKeys) == 0 {
			for _, project := range catalogProjects {
				keys, err := h.catalog.ListProjectKeys(ctx, project.OrgSlug, project.Slug)
				if err != nil {
					writeWebInternal(w, r, "Failed to load settings.")
					return
				}
				if len(keys) == 0 {
					continue
				}
				currentProject = &project
				projectName = project.Name
				projectSlug = project.Slug
				if project.Platform != "" {
					platform = project.Platform
				}
				if project.Status != "" {
					projectStatus = project.Status
				}
				projectKeys = keys
				currentSettings, err = h.catalog.GetProjectSettings(ctx, project.OrgSlug, project.Slug)
				if err != nil {
					writeWebInternal(w, r, "Failed to load settings.")
					return
				}
				if currentSettings != nil {
					eventRetentionDays = currentSettings.EventRetentionDays
					attachmentRetentionDays = currentSettings.AttachmentRetentionDays
					debugRetentionDays = currentSettings.DebugFileRetentionDays
				}
				break
			}
		}
	}
	keys := make([]settingsKey, 0, len(projectKeys))
	for _, key := range projectKeys {
		status := key.Status
		if status == "" {
			status = "active"
		}
		keys = append(keys, settingsKey{
			PublicKey: key.PublicKey,
			Status:    status,
			CreatedAt: key.DateCreated.Format("2006-01-02 15:04:05"),
		})
	}
	if len(keys) > 0 && currentProject != nil && currentProject.ID != "" {
		dsn = projectStoreDSN(r, keys[0].PublicKey, currentProject.ID)
	}

	auditSource := overview.AuditLogs
	if h.catalog != nil && currentProject != nil && currentProject.OrgSlug != "" {
		auditSource, err = h.catalog.ListOrganizationAuditLogs(ctx, currentProject.OrgSlug, 8)
		if err != nil {
			writeWebInternal(w, r, "Failed to load settings.")
			return
		}
	}
	auditLogs := make([]settingsAuditLog, 0, len(auditSource))
	for _, row := range auditSource {
		actor := row.UserEmail
		if actor == "" {
			actor = row.UserID
		}
		if actor == "" {
			actor = row.CredentialType
		}
		if actor == "" {
			actor = "system"
		}
		auditLogs = append(auditLogs, settingsAuditLog{
			Action:    row.Action,
			Actor:     actor,
			CreatedAt: timeAgo(row.DateCreated),
		})
	}

	var ownershipRules []settingsOwnershipRule
	if currentProject != nil {
		if rules, rulesErr := h.ownership.ListProjectRules(ctx, currentProject.ID); rulesErr == nil {
			ownershipRules = make([]settingsOwnershipRule, 0, len(rules))
			for _, rule := range rules {
				ownershipRules = append(ownershipRules, settingsOwnershipRule{
					ID:        rule.ID,
					Name:      rule.Name,
					Pattern:   rule.Pattern,
					Assignee:  rule.Assignee,
					CreatedAt: timeAgo(rule.DateCreated),
				})
			}
		}
	}

	var codeMappings []settingsCodeMapping
	if currentProject != nil && h.codeMappings != nil {
		if mappings, mapErr := h.codeMappings.ListCodeMappings(ctx, currentProject.ID); mapErr == nil {
			codeMappings = make([]settingsCodeMapping, 0, len(mappings))
			for _, m := range mappings {
				codeMappings = append(codeMappings, settingsCodeMapping{
					ID:            m.ID,
					StackRoot:     m.StackRoot,
					SourceRoot:    m.SourceRoot,
					DefaultBranch: m.DefaultBranch,
					RepoURL:       m.RepoURL,
					CreatedAt:     timeAgo(m.CreatedAt),
				})
			}
		}
	}

	data := settingsData{
		Title:                   "Settings",
		Nav:                     "settings",
		Environment:             readSelectedEnvironment(r),
		Environments:            h.loadEnvironments(ctx),
		ProjectID:               currentProjectID(currentProject),
		ProjectName:             projectName,
		ProjectSlug:             projectSlug,
		Platform:                platform,
		ProjectStatus:           projectStatus,
		EventRetentionDays:      eventRetentionDays,
		AttachmentRetentionDays: attachmentRetentionDays,
		DebugRetentionDays:      debugRetentionDays,
		DSN:                     dsn,
		Keys:                    keys,
		EventCount:              formatNumber(overview.EventCount),
		GroupCount:              formatNumber(overview.GroupCount),
		DBSize:                  formatBytes(h.databaseFileSize()),
		TelemetryPolicies:       settingsTelemetryPolicies(currentSettings, eventRetentionDays, attachmentRetentionDays, debugRetentionDays),
		ReplayPolicy:            settingsReplayPolicyFromProject(currentSettings),
		OwnershipRules:          ownershipRules,
		CodeMappings:            codeMappings,
		AuditLogs:               auditLogs,
	}

	h.render(w, "settings.html", data)
}

func projectStoreDSN(r *http.Request, publicKey, projectID string) string {
	publicKey = strings.TrimSpace(publicKey)
	projectID = strings.TrimSpace(projectID)
	if publicKey == "" || projectID == "" {
		return ""
	}
	base := strings.TrimSpace(os.Getenv("URGENTRY_BASE_URL"))
	if base == "" && r != nil {
		scheme := "http"
		if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); forwarded != "" {
			scheme = forwarded
		} else if r.TLS != nil {
			scheme = "https"
		}
		if host := strings.TrimSpace(r.Host); host != "" {
			base = scheme + "://" + host
		}
	}
	if base == "" {
		base = "http://localhost:8080"
	}
	u, err := url.Parse(base)
	if err != nil || strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return fmt.Sprintf("http://%s@localhost:8080/api/%s/store/", publicKey, projectID)
	}
	u.User = url.User(publicKey)
	basePath := strings.TrimRight(u.Path, "/")
	u.Path = basePath + "/api/" + projectID + "/store/"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (h *Handler) updateProjectSettings(w http.ResponseWriter, r *http.Request) {
	if h.catalog == nil {
		writeWebUnavailable(w, r, "Settings unavailable")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}

	name := strings.TrimSpace(r.FormValue("name"))
	platform := strings.TrimSpace(r.FormValue("platform"))
	status := strings.TrimSpace(r.FormValue("status"))
	eventRetentionDays := parseRetentionDays(r.FormValue("event_retention_days"), 90)
	attachmentRetentionDays := parseRetentionDays(r.FormValue("attachment_retention_days"), 30)
	debugRetentionDays := parseRetentionDays(r.FormValue("debug_retention_days"), 180)
	telemetryPolicies, err := parseTelemetryPoliciesFromForm(r, eventRetentionDays, attachmentRetentionDays, debugRetentionDays)
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	replayPolicy, err := parseReplayPolicyFromForm(r)
	if err != nil {
		writeWebBadRequest(w, r, err.Error())
		return
	}
	if name == "" {
		writeWebBadRequest(w, r, "Project name is required")
		return
	}
	if status == "" {
		status = "active"
	}
	if status != "active" && status != "disabled" {
		writeWebBadRequest(w, r, "Invalid project status")
		return
	}

	projects, err := h.catalog.ListProjects(r.Context(), "")
	if err != nil {
		writeWebInternal(w, r, "Failed to load settings.")
		return
	}
	if len(projects) == 0 {
		writeWebNotFound(w, r, "Project not found")
		return
	}
	project := projects[0]
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, project.ID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	_, err = h.catalog.UpdateProjectSettings(r.Context(), project.OrgSlug, project.Slug, sharedstore.ProjectSettingsUpdate{
		Name:                    name,
		Platform:                platform,
		Status:                  status,
		EventRetentionDays:      eventRetentionDays,
		AttachmentRetentionDays: attachmentRetentionDays,
		DebugFileRetentionDays:  debugRetentionDays,
		TelemetryPolicies:       telemetryPolicies,
		ReplayPolicy:            replayPolicy,
	})
	if err != nil {
		if sharedstore.IsInvalidReplayPolicy(err) {
			writeWebBadRequest(w, r, err.Error())
			return
		}
		writeWebInternal(w, r, "Failed to update settings.")
		return
	}

	http.Redirect(w, r, "/settings/", http.StatusSeeOther)
}

func (h *Handler) createOwnershipRule(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		writeWebBadRequest(w, r, "Project ID is required")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	_, err := h.ownership.CreateRule(r.Context(), sharedstore.OwnershipRule{
		ProjectID: projectID,
		Name:      strings.TrimSpace(r.FormValue("name")),
		Pattern:   strings.TrimSpace(r.FormValue("pattern")),
		Assignee:  strings.TrimSpace(r.FormValue("assignee")),
	})
	if err != nil {
		writeWebBadRequest(w, r, "Failed to create ownership rule")
		return
	}
	http.Redirect(w, r, "/settings/", http.StatusSeeOther)
}

func (h *Handler) deleteOwnershipRule(w http.ResponseWriter, r *http.Request) {
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		writeWebBadRequest(w, r, "Project ID is required")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := h.ownership.DeleteRule(r.Context(), projectID, r.PathValue("id")); err != nil {
		writeWebInternal(w, r, "Failed to delete ownership rule.")
		return
	}
	http.Redirect(w, r, "/settings/", http.StatusSeeOther)
}

func (h *Handler) createCodeMapping(w http.ResponseWriter, r *http.Request) {
	if h.codeMappings == nil {
		writeWebUnavailable(w, r, "Code mappings unavailable")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		writeWebBadRequest(w, r, "Project ID is required")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	repoURL := strings.TrimSpace(r.FormValue("repo_url"))
	if repoURL == "" {
		writeWebBadRequest(w, r, "Repository URL is required")
		return
	}
	branch := strings.TrimSpace(r.FormValue("default_branch"))
	if branch == "" {
		branch = "main"
	}
	if err := h.codeMappings.CreateCodeMapping(r.Context(), &sharedstore.CodeMapping{
		ProjectID:     projectID,
		StackRoot:     strings.TrimSpace(r.FormValue("stack_root")),
		SourceRoot:    strings.TrimSpace(r.FormValue("source_root")),
		DefaultBranch: branch,
		RepoURL:       repoURL,
	}); err != nil {
		writeWebBadRequest(w, r, "Failed to create code mapping")
		return
	}
	http.Redirect(w, r, "/settings/", http.StatusSeeOther)
}

func (h *Handler) deleteCodeMapping(w http.ResponseWriter, r *http.Request) {
	if h.codeMappings == nil {
		writeWebUnavailable(w, r, "Code mappings unavailable")
		return
	}
	if h.authz != nil && !h.authz.ValidateCSRF(r) {
		writeWebForbidden(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeWebBadRequest(w, r, "Invalid form")
		return
	}
	projectID := strings.TrimSpace(r.FormValue("project_id"))
	if projectID == "" {
		writeWebBadRequest(w, r, "Project ID is required")
		return
	}
	if h.authz != nil {
		if err := h.authz.AuthorizeProject(r, projectID, auth.ScopeProjectWrite); err != nil {
			writeWebForbidden(w, r)
			return
		}
	}
	if err := h.codeMappings.DeleteCodeMapping(r.Context(), projectID, r.PathValue("id")); err != nil {
		writeWebInternal(w, r, "Failed to delete code mapping.")
		return
	}
	http.Redirect(w, r, "/settings/", http.StatusSeeOther)
}

// formatBytes formats bytes into a human-readable string.
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func parseRetentionDays(raw string, fallback int) int {
	value := strings.TrimSpace(raw)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func settingsTelemetryPolicies(current *sharedstore.ProjectSettings, eventDays, attachmentDays, debugDays int) []settingsTelemetryPolicy {
	policies := []sharedstore.TelemetryRetentionPolicy(nil)
	if current != nil {
		policies = current.TelemetryPolicies
	}
	canonical, err := sharedstore.CanonicalTelemetryPolicies(policies, eventDays, attachmentDays, debugDays)
	if err != nil {
		canonical, _ = sharedstore.CanonicalTelemetryPolicies(nil, eventDays, attachmentDays, debugDays)
	}
	rows := make([]settingsTelemetryPolicy, 0, len(canonical))
	for _, policy := range canonical {
		rows = append(rows, settingsTelemetryPolicy{
			Surface:         string(policy.Surface),
			Label:           telemetrySurfaceLabel(policy.Surface),
			RetentionDays:   policy.RetentionDays,
			StorageTier:     string(policy.StorageTier),
			ArchiveDays:     policy.ArchiveRetentionDays,
			SupportsArchive: sharedstore.SupportsArchiveTelemetrySurface(policy.Surface),
		})
	}
	return rows
}

func settingsReplayPolicyFromProject(current *sharedstore.ProjectSettings) settingsReplayPolicy {
	policy := sqlite.DefaultReplayIngestPolicy()
	if current != nil {
		policy = current.ReplayPolicy
	}
	return settingsReplayPolicy{
		SampleRate:     policy.SampleRate,
		MaxBytes:       policy.MaxBytes,
		ScrubFields:    strings.Join(policy.ScrubFields, ", "),
		ScrubSelectors: strings.Join(policy.ScrubSelectors, ", "),
	}
}

func parseTelemetryPoliciesFromForm(r *http.Request, eventDays, attachmentDays, debugDays int) ([]sharedstore.TelemetryRetentionPolicy, error) {
	input := make([]sharedstore.TelemetryRetentionPolicy, 0, len(sharedstore.TelemetrySurfaces()))
	for _, surface := range sharedstore.TelemetrySurfaces() {
		policy := sharedstore.TelemetryRetentionPolicy{
			Surface:              surface,
			RetentionDays:        parseRetentionDays(r.FormValue("telemetry_"+string(surface)+"_days"), defaultTelemetryDays(surface, eventDays, attachmentDays, debugDays)),
			StorageTier:          sharedstore.TelemetryStorageTier(strings.TrimSpace(r.FormValue("telemetry_" + string(surface) + "_tier"))),
			ArchiveRetentionDays: parseRetentionDays(r.FormValue("telemetry_"+string(surface)+"_archive_days"), 0),
		}
		if policy.ArchiveRetentionDays == 0 && policy.StorageTier == sharedstore.TelemetryStorageTierArchive {
			policy.ArchiveRetentionDays = policy.RetentionDays * 2
		}
		input = append(input, policy)
	}
	return sharedstore.CanonicalTelemetryPolicies(input, eventDays, attachmentDays, debugDays)
}

func parseReplayPolicyFromForm(r *http.Request) (sharedstore.ReplayIngestPolicy, error) {
	policy := sharedstore.ReplayIngestPolicy{}
	if raw := strings.TrimSpace(r.FormValue("replay_sample_rate")); raw != "" {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return sharedstore.ReplayIngestPolicy{}, sharedstore.ErrInvalidReplayPolicy("sampleRate must be between 0 and 1")
		}
		policy.SampleRate = value
	}
	if raw := strings.TrimSpace(r.FormValue("replay_max_bytes")); raw != "" {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return sharedstore.ReplayIngestPolicy{}, sharedstore.ErrInvalidReplayPolicy("maxBytes must be a positive integer")
		}
		policy.MaxBytes = value
	}
	if raw := strings.TrimSpace(r.FormValue("replay_scrub_fields")); raw != "" {
		policy.ScrubFields = strings.Split(raw, ",")
	}
	if raw := strings.TrimSpace(r.FormValue("replay_scrub_selectors")); raw != "" {
		policy.ScrubSelectors = strings.Split(raw, ",")
	}
	return policy, nil
}

func telemetrySurfaceLabel(surface sharedstore.TelemetrySurface) string {
	switch surface {
	case sharedstore.TelemetrySurfaceErrors:
		return "Errors"
	case sharedstore.TelemetrySurfaceLogs:
		return "Logs"
	case sharedstore.TelemetrySurfaceTraces:
		return "Traces"
	case sharedstore.TelemetrySurfaceReplays:
		return "Replays"
	case sharedstore.TelemetrySurfaceProfiles:
		return "Profiles"
	case sharedstore.TelemetrySurfaceOutcomes:
		return "Outcomes"
	case sharedstore.TelemetrySurfaceAttachments:
		return "Attachments"
	case sharedstore.TelemetrySurfaceDebugFiles:
		return "Debug Files"
	default:
		return string(surface)
	}
}

func defaultTelemetryDays(surface sharedstore.TelemetrySurface, eventDays, attachmentDays, debugDays int) int {
	switch surface {
	case sharedstore.TelemetrySurfaceAttachments, sharedstore.TelemetrySurfaceReplays:
		return attachmentDays
	case sharedstore.TelemetrySurfaceDebugFiles:
		return debugDays
	default:
		return eventDays
	}
}

func currentProjectID(project *sharedstore.Project) string {
	if project == nil {
		return ""
	}
	return project.ID
}
