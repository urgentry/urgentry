package api

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/sqlite"
	sharedstore "urgentry/internal/store"
)

// handleListOrgMonitors lists monitors across all projects in an organization.
func handleListOrgMonitors(db *sql.DB, catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		org, err := getOrganizationFromDB(r, db, PathParam(r, "org_slug"))
		if err != nil || org == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}
		projectItems, err := catalog.ListProjects(r.Context(), org.Slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
			return
		}
		projectRefs := make(map[string]ProjectRef, len(projectItems))
		for i := range projectItems {
			project := projectItems[i]
			projectRefs[project.ID] = apiProjectRefFromProject(&project)
		}
		items, err := monitors.ListOrgMonitors(r.Context(), org.ID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list monitors.")
			return
		}
		resp := make([]Monitor, 0, len(items))
		for _, item := range items {
			ref := projectRefs[item.ProjectID]
			if ref.ID == "" {
				ref = ProjectRef{ID: item.ProjectID}
			}
			resp = append(resp, mapMonitor(item, ref))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleListMonitors(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}
		items, err := monitors.ListMonitors(r.Context(), project.ID, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list monitors.")
			return
		}
		ref := apiProjectRefFromProject(project)
		resp := make([]Monitor, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapMonitor(item, ref))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

type monitorRequest struct {
	Name        string               `json:"name"`
	Slug        string               `json:"slug"`
	Project     string               `json:"project"`
	Status      string               `json:"status"`
	IsMuted     *bool                `json:"is_muted,omitempty"`
	Environment string               `json:"environment"`
	Config      sqlite.MonitorConfig `json:"config"`
}

func handleCreateMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}
		var body monitorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		monitor := &sqlite.Monitor{
			ProjectID:   project.ID,
			Slug:        monitorRequestSlug(body),
			Status:      monitorRequestStatus(body),
			Environment: strings.TrimSpace(body.Environment),
			Config:      body.Config,
			DateCreated: time.Now().UTC(),
		}
		if monitor.Slug == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Slug is required.")
			return
		}
		item, err := monitors.UpsertMonitor(r.Context(), monitor)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapMonitor(*item, apiProjectRefFromProject(project)))
	}
}

func handleGetMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}
		item, err := monitors.GetMonitor(r.Context(), project.ID, PathParam(r, "monitor_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load monitor.")
			return
		}
		if item == nil {
			httputil.WriteError(w, http.StatusNotFound, "Monitor not found.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapMonitor(*item, apiProjectRefFromProject(project)))
	}
}

func handleUpdateMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		project, ok := getProjectFromCatalog(w, r, catalog, PathParam(r, "org_slug"), PathParam(r, "proj_slug"))
		if !ok {
			return
		}
		existing, err := monitors.GetMonitor(r.Context(), project.ID, PathParam(r, "monitor_slug"))
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load monitor.")
			return
		}
		if existing == nil {
			httputil.WriteError(w, http.StatusNotFound, "Monitor not found.")
			return
		}
		var body monitorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if slug := strings.TrimSpace(body.Slug); slug != "" {
			existing.Slug = slug
		}
		existing.Status = normalizeMonitorStatus(body.Status)
		if body.IsMuted != nil && *body.IsMuted {
			existing.Status = "disabled"
		}
		if strings.TrimSpace(body.Environment) != "" {
			existing.Environment = strings.TrimSpace(body.Environment)
		}
		existing.Config = body.Config
		item, err := monitors.UpsertMonitor(r.Context(), existing)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapMonitor(*item, apiProjectRefFromProject(project)))
	}
}

func handleDeleteMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		if err := monitors.DeleteMonitor(r.Context(), projectID, PathParam(r, "monitor_slug")); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete monitor.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListMonitorCheckIns(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		projectID, ok := resolveProjectIDWithCatalog(w, r, catalog)
		if !ok {
			return
		}
		items, err := monitors.ListCheckIns(r.Context(), projectID, PathParam(r, "monitor_slug"), 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list check-ins.")
			return
		}
		resp := make([]MonitorCheckIn, 0, len(items))
		for _, item := range items {
			resp = append(resp, mapMonitorCheckIn(item))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func handleCreateOrgMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		var body monitorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		project, ok := orgMonitorProjectFromBody(w, r, catalog, strings.TrimSpace(body.Project))
		if !ok {
			return
		}
		monitor := &sqlite.Monitor{
			ProjectID:   project.ID,
			Slug:        monitorRequestSlug(body),
			Status:      monitorRequestStatus(body),
			Environment: strings.TrimSpace(body.Environment),
			Config:      body.Config,
			DateCreated: time.Now().UTC(),
		}
		if monitor.Slug == "" {
			httputil.WriteError(w, http.StatusBadRequest, "Slug is required.")
			return
		}
		item, err := monitors.UpsertMonitor(r.Context(), monitor)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to create monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusCreated, mapMonitor(*item, apiProjectRefFromProject(project)))
	}
}

func handleGetOrgMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		item, project, ok := findOrganizationMonitor(w, r, catalog, monitors, PathParam(r, "monitor_slug"))
		if !ok {
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapMonitor(*item, apiProjectRefFromProject(project)))
	}
}

func handleUpdateOrgMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		existing, project, ok := findOrganizationMonitor(w, r, catalog, monitors, PathParam(r, "monitor_slug"))
		if !ok {
			return
		}
		var body monitorRequest
		if err := decodeJSON(r, &body); err != nil {
			writeDecodeJSONError(w, err)
			return
		}
		if slug := monitorRequestSlug(body); slug != "" {
			existing.Slug = slug
		}
		existing.Status = monitorRequestStatus(body)
		if strings.TrimSpace(body.Environment) != "" {
			existing.Environment = strings.TrimSpace(body.Environment)
		}
		existing.Config = body.Config
		item, err := monitors.UpsertMonitor(r.Context(), existing)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update monitor.")
			return
		}
		httputil.WriteJSON(w, http.StatusOK, mapMonitor(*item, apiProjectRefFromProject(project)))
	}
}

func handleDeleteOrgMonitor(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		item, _, ok := findOrganizationMonitor(w, r, catalog, monitors, PathParam(r, "monitor_slug"))
		if !ok {
			return
		}
		if err := monitors.DeleteMonitor(r.Context(), item.ProjectID, item.Slug); err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to delete monitor.")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleListOrgMonitorCheckIns(catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		item, _, ok := findOrganizationMonitor(w, r, catalog, monitors, PathParam(r, "monitor_slug"))
		if !ok {
			return
		}
		items, err := monitors.ListCheckIns(r.Context(), item.ProjectID, item.Slug, 100)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list check-ins.")
			return
		}
		resp := make([]MonitorCheckIn, 0, len(items))
		for _, checkIn := range items {
			resp = append(resp, mapMonitorCheckIn(checkIn))
		}
		httputil.WriteJSON(w, http.StatusOK, resp)
	}
}

func findOrganizationMonitor(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore, monitors controlplane.MonitorStore, slug string) (*controlplane.Monitor, *sharedstore.Project, bool) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		httputil.WriteError(w, http.StatusBadRequest, "Monitor slug is required.")
		return nil, nil, false
	}
	projects, ok := orgProjectsByID(w, r, catalog)
	if !ok {
		return nil, nil, false
	}
	for projectID, project := range projects {
		item, err := monitors.GetMonitor(r.Context(), projectID, slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load monitor.")
			return nil, nil, false
		}
		if item != nil {
			return item, project, true
		}
	}
	httputil.WriteError(w, http.StatusNotFound, "Monitor not found.")
	return nil, nil, false
}

func orgMonitorProjectFromBody(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore, projectSlug string) (*sharedstore.Project, bool) {
	projectSlug = strings.TrimSpace(projectSlug)
	if projectSlug == "" {
		httputil.WriteError(w, http.StatusBadRequest, "Project is required.")
		return nil, false
	}
	project, err := catalog.GetProject(r.Context(), PathParam(r, "org_slug"), projectSlug)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to load project.")
		return nil, false
	}
	if project == nil {
		httputil.WriteError(w, http.StatusNotFound, "Project not found.")
		return nil, false
	}
	return project, true
}

func orgProjectsByID(w http.ResponseWriter, r *http.Request, catalog controlplane.CatalogStore) (map[string]*sharedstore.Project, bool) {
	projects, err := catalog.ListProjects(r.Context(), PathParam(r, "org_slug"))
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "Failed to list projects.")
		return nil, false
	}
	index := make(map[string]*sharedstore.Project, len(projects))
	for i := range projects {
		project := projects[i]
		index[project.ID] = &project
	}
	return index, true
}

func monitorRequestSlug(body monitorRequest) string {
	if slug := normalizeMonitorSlugString(body.Slug); slug != "" {
		return slug
	}
	return normalizeMonitorSlugString(body.Name)
}

func monitorRequestStatus(body monitorRequest) string {
	if body.IsMuted != nil && *body.IsMuted {
		return "disabled"
	}
	return normalizeMonitorStatus(body.Status)
}

func normalizeMonitorSlugString(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
			lastDash = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_':
			if b.Len() > 0 && !lastDash {
				b.WriteRune(r)
				lastDash = true
			}
		case r == ' ':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-_")
}

func mapMonitor(item sqlite.Monitor, project ProjectRef) Monitor {
	name := item.Slug
	env := item.Environment
	if env == "" {
		env = "production"
	}
	resp := Monitor{
		ID:            item.ID,
		ProjectID:     item.ProjectID,
		Name:          name,
		Slug:          item.Slug,
		Status:        item.Status,
		IsMuted:       item.Status == "disabled",
		Environment:   env,
		Environments:  []string{env},
		Project:       project,
		AlertRule:     nil,
		LastCheckInID: item.LastCheckInID,
		LastStatus:    item.LastStatus,
		Config: MonitorConfig{
			Schedule: MonitorSchedule{
				Type:    item.Config.Schedule.Type,
				Value:   item.Config.Schedule.Value,
				Unit:    item.Config.Schedule.Unit,
				Crontab: item.Config.Schedule.Crontab,
			},
			CheckInMargin: item.Config.CheckInMargin,
			MaxRuntime:    item.Config.MaxRuntime,
			Timezone:      item.Config.Timezone,
		},
		DateCreated: item.DateCreated,
		DateUpdated: item.UpdatedAt,
	}
	if !item.LastCheckInAt.IsZero() {
		resp.LastCheckInAt = &item.LastCheckInAt
	}
	if !item.NextCheckInAt.IsZero() {
		resp.NextCheckInAt = &item.NextCheckInAt
	}
	return resp
}

func normalizeMonitorStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active":
		return "active"
	case "disabled", "paused":
		return "disabled"
	default:
		return "active"
	}
}

func mapMonitorCheckIn(item sqlite.MonitorCheckIn) MonitorCheckIn {
	resp := MonitorCheckIn{
		ID:          item.ID,
		MonitorID:   item.MonitorID,
		ProjectID:   item.ProjectID,
		CheckInID:   item.CheckInID,
		MonitorSlug: item.MonitorSlug,
		Status:      item.Status,
		Duration:    item.Duration,
		Release:     item.Release,
		Environment: item.Environment,
		Payload:     item.PayloadJSON,
		DateCreated: item.DateCreated,
	}
	if !item.ScheduledFor.IsZero() {
		resp.ScheduledFor = &item.ScheduledFor
	}
	return resp
}
