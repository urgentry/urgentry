package web

import (
	"fmt"
	"net/http"

	sharedstore "urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Project settings sub-routes
// ---------------------------------------------------------------------------

// projectSettingsData is the shared data shape passed to all project settings
// sub-page templates.  Each sub-page only consumes the fields it needs.
type projectSettingsData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	ActiveTab    string

	// Project identity
	ProjectID     string
	ProjectName   string
	ProjectSlug   string
	OrgSlug       string
	Platform      string
	ProjectStatus string

	// Keys / DSN (keys sub-page)
	DSN  string
	Keys []settingsKey

	// Ownership (ownership sub-page)
	OwnershipRules []settingsOwnershipRule

	// Environments list (environments sub-page)
	ProjectEnvironments []projectEnvRow

	// Retention (retention sub-page)
	EventRetentionDays      int
	AttachmentRetentionDays int
	DebugRetentionDays      int
	TelemetryPolicies       []settingsTelemetryPolicy
	ReplayPolicy            settingsReplayPolicy

	// Filters (filters sub-page)
	InboundFilters []inboundFilterRow

	FormError string
}

type projectEnvRow struct {
	Name     string
	IsHidden bool
}

type inboundFilterRow struct {
	Name    string
	Label   string
	Enabled bool
}

// resolveProjectBySlug finds the project and its settings by slug, iterating
// all projects since the catalog does not expose a direct slug-lookup.
func (h *Handler) resolveProjectBySlug(r *http.Request, slug string) (*sharedstore.Project, *sharedstore.ProjectSettings, error) {
	if h.catalog == nil {
		return nil, nil, fmt.Errorf("catalog unavailable")
	}
	ctx := r.Context()
	projects, err := h.catalog.ListProjects(ctx, "")
	if err != nil {
		return nil, nil, err
	}
	for i := range projects {
		p := &projects[i]
		if p.Slug == slug {
			settings, settErr := h.catalog.GetProjectSettings(ctx, p.OrgSlug, p.Slug)
			if settErr != nil {
				return nil, nil, settErr
			}
			return p, settings, nil
		}
	}
	return nil, nil, nil // not found
}

// baseProjectSettingsData builds the shared fields from a resolved project
// and its settings.
func (h *Handler) baseProjectSettingsData(r *http.Request, project *sharedstore.Project, settings *sharedstore.ProjectSettings, activeTab string) projectSettingsData {
	ctx := r.Context()

	platform := "go"
	if project.Platform != "" {
		platform = project.Platform
	}
	status := "active"
	if project.Status != "" {
		status = project.Status
	}

	eventRetentionDays := 90
	attachmentRetentionDays := 30
	debugRetentionDays := 180
	if project.EventRetentionDays > 0 {
		eventRetentionDays = project.EventRetentionDays
	}
	if project.AttachRetentionDays > 0 {
		attachmentRetentionDays = project.AttachRetentionDays
	}
	if project.DebugRetentionDays > 0 {
		debugRetentionDays = project.DebugRetentionDays
	}
	if settings != nil {
		if settings.EventRetentionDays > 0 {
			eventRetentionDays = settings.EventRetentionDays
		}
		if settings.AttachmentRetentionDays > 0 {
			attachmentRetentionDays = settings.AttachmentRetentionDays
		}
		if settings.DebugFileRetentionDays > 0 {
			debugRetentionDays = settings.DebugFileRetentionDays
		}
	}

	return projectSettingsData{
		Nav:                     "settings",
		Environment:             readSelectedEnvironment(r),
		Environments:            h.loadEnvironments(ctx),
		ActiveTab:               activeTab,
		ProjectID:               project.ID,
		ProjectName:             project.Name,
		ProjectSlug:             project.Slug,
		OrgSlug:                 project.OrgSlug,
		Platform:                platform,
		ProjectStatus:           status,
		EventRetentionDays:      eventRetentionDays,
		AttachmentRetentionDays: attachmentRetentionDays,
		DebugRetentionDays:      debugRetentionDays,
		TelemetryPolicies:       settingsTelemetryPolicies(settings, eventRetentionDays, attachmentRetentionDays, debugRetentionDays),
		ReplayPolicy:            settingsReplayPolicyFromProject(settings),
	}
}

// ---------------------------------------------------------------------------
// GET /settings/project/{slug}/general/
// ---------------------------------------------------------------------------

func (h *Handler) projectSettingsGeneralPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	project, settings, err := h.resolveProjectBySlug(r, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load project settings.")
		return
	}
	if project == nil {
		writeWebNotFound(w, r, "Project not found.")
		return
	}

	data := h.baseProjectSettingsData(r, project, settings, "general")
	data.Title = project.Name + " — General Settings"
	h.render(w, "settings-project-general.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/project/{slug}/keys/
// ---------------------------------------------------------------------------

func (h *Handler) projectSettingsKeysPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	project, settings, err := h.resolveProjectBySlug(r, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load project settings.")
		return
	}
	if project == nil {
		writeWebNotFound(w, r, "Project not found.")
		return
	}

	ctx := r.Context()
	rawKeys, keysErr := h.catalog.ListProjectKeys(ctx, project.OrgSlug, project.Slug)
	if keysErr != nil {
		writeWebInternal(w, r, "Failed to load project keys.")
		return
	}

	keys := make([]settingsKey, 0, len(rawKeys))
	dsn := ""
	for _, key := range rawKeys {
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
	if len(keys) > 0 && project.ID != "" {
		dsn = projectStoreDSN(r, keys[0].PublicKey, project.ID)
	}

	data := h.baseProjectSettingsData(r, project, settings, "keys")
	data.Title = project.Name + " — Keys & DSN"
	data.Keys = keys
	data.DSN = dsn
	h.render(w, "settings-project-keys.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/project/{slug}/ownership/
// ---------------------------------------------------------------------------

func (h *Handler) projectSettingsOwnershipPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	project, settings, err := h.resolveProjectBySlug(r, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load project settings.")
		return
	}
	if project == nil {
		writeWebNotFound(w, r, "Project not found.")
		return
	}

	ctx := r.Context()
	var ownershipRules []settingsOwnershipRule
	if h.ownership != nil {
		if rules, rulesErr := h.ownership.ListProjectRules(ctx, project.ID); rulesErr == nil {
			ownershipRules = make([]settingsOwnershipRule, 0, len(rules))
			for _, rule := range rules {
				ownershipRules = append(ownershipRules, settingsOwnershipRule{
					ID:        rule.ID,
					Name:      rule.Name,
					Pattern:   rule.Pattern,
					Assignee:  rule.Assignee,
					TeamSlug:  rule.TeamSlug,
					CreatedAt: timeAgo(rule.DateCreated),
				})
			}
		}
	}

	data := h.baseProjectSettingsData(r, project, settings, "ownership")
	data.Title = project.Name + " — Ownership Rules"
	data.OwnershipRules = ownershipRules
	h.render(w, "settings-project-ownership.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/project/{slug}/environments/
// ---------------------------------------------------------------------------

func (h *Handler) projectSettingsEnvironmentsPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	project, settings, err := h.resolveProjectBySlug(r, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load project settings.")
		return
	}
	if project == nil {
		writeWebNotFound(w, r, "Project not found.")
		return
	}

	ctx := r.Context()
	var envRows []projectEnvRow
	rawEnvs, envErr := h.catalog.ListProjectEnvironments(ctx, project.OrgSlug, project.Slug)
	if envErr == nil {
		envRows = make([]projectEnvRow, 0, len(rawEnvs))
		for _, e := range rawEnvs {
			envRows = append(envRows, projectEnvRow{
				Name:     e.Name,
				IsHidden: e.IsHidden,
			})
		}
	}

	data := h.baseProjectSettingsData(r, project, settings, "environments")
	data.Title = project.Name + " — Environments"
	data.ProjectEnvironments = envRows
	h.render(w, "settings-project-environments.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/project/{slug}/retention/
// ---------------------------------------------------------------------------

func (h *Handler) projectSettingsRetentionPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	project, settings, err := h.resolveProjectBySlug(r, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load project settings.")
		return
	}
	if project == nil {
		writeWebNotFound(w, r, "Project not found.")
		return
	}

	data := h.baseProjectSettingsData(r, project, settings, "retention")
	data.Title = project.Name + " — Retention Policy"
	h.render(w, "settings-project-retention.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/project/{slug}/filters/
// ---------------------------------------------------------------------------

// staticInboundFilters lists the named inbound data filters that Urgentry
// supports. There is no persistence layer for these yet; the page is
// read-only and shows the default (disabled) state for each filter.
func staticInboundFilters() []inboundFilterRow {
	return []inboundFilterRow{
		{Name: "browser_extensions", Label: "Filter out errors known to be caused by browser extensions"},
		{Name: "localhost", Label: "Filter out events originating from localhost"},
		{Name: "web_crawlers", Label: "Filter out known web crawlers and bots"},
		{Name: "legacy_browsers", Label: "Filter out events from legacy browsers"},
		{Name: "invalid_csp", Label: "Filter out invalid CSP reports"},
	}
}

func (h *Handler) projectSettingsFiltersPage(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	project, settings, err := h.resolveProjectBySlug(r, slug)
	if err != nil {
		writeWebInternal(w, r, "Failed to load project settings.")
		return
	}
	if project == nil {
		writeWebNotFound(w, r, "Project not found.")
		return
	}

	data := h.baseProjectSettingsData(r, project, settings, "filters")
	data.Title = project.Name + " — Inbound Filters"
	data.InboundFilters = staticInboundFilters()
	h.render(w, "settings-project-filters.html", data)
}
