package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"urgentry/internal/auth"
)

const selectedProjectCookie = "urgentry_project"

type projectSwitcherProject struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	OrgSlug     string `json:"orgSlug"`
	TeamSlug    string `json:"teamSlug,omitempty"`
	Platform    string `json:"platform,omitempty"`
	Status      string `json:"status"`
	Value       string `json:"value"`
	SettingsURL string `json:"settingsUrl"`
}

type projectSwitcherResponse struct {
	Selected string                   `json:"selected"`
	Projects []projectSwitcherProject `json:"projects"`
}

func selectedProjectSwitcherValue(r *http.Request) string {
	if r == nil {
		return ""
	}
	cookie, err := r.Cookie(selectedProjectCookie)
	if err != nil {
		return ""
	}
	value, err := url.QueryUnescape(cookie.Value)
	if err != nil {
		value = cookie.Value
	}
	return strings.TrimSpace(value)
}

func projectSwitcherValue(orgSlug, projectSlug string) string {
	orgSlug = strings.TrimSpace(orgSlug)
	projectSlug = strings.TrimSpace(projectSlug)
	if orgSlug == "" || projectSlug == "" {
		return ""
	}
	return orgSlug + "/" + projectSlug
}

func splitProjectSwitcherValue(value string) (string, string, bool) {
	value = strings.Trim(strings.TrimSpace(value), "/")
	parts := strings.Split(value, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func setSelectedProjectCookie(w http.ResponseWriter, orgSlug, projectSlug string) {
	value := projectSwitcherValue(orgSlug, projectSlug)
	if value == "" {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     selectedProjectCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour).Seconds()),
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *Handler) selectedProjectScope(ctx context.Context, value string) (pageScope, bool, error) {
	if h == nil || h.db == nil {
		return pageScope{}, false, nil
	}
	orgSlug, projectSlug, ok := splitProjectSwitcherValue(value)
	if !ok {
		return pageScope{}, false, nil
	}

	var scope pageScope
	principal := auth.PrincipalFromContext(ctx)
	if principal != nil && principal.User != nil && principal.User.ID != "" {
		err := h.db.QueryRowContext(ctx,
			`SELECT p.id, p.slug, o.id, o.slug
			 FROM projects p
			 JOIN organizations o ON o.id = p.organization_id
			 JOIN organization_members m ON m.organization_id = o.id
			 WHERE o.slug = ? AND p.slug = ? AND m.user_id = ?
			 LIMIT 1`,
			orgSlug, projectSlug, principal.User.ID,
		).Scan(&scope.ProjectID, &scope.ProjectSlug, &scope.OrganizationID, &scope.OrganizationSlug)
		if err == sql.ErrNoRows {
			return pageScope{}, false, nil
		}
		return scope, err == nil, err
	}

	err := h.db.QueryRowContext(ctx,
		`SELECT p.id, p.slug, o.id, o.slug
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 WHERE o.slug = ? AND p.slug = ?
		 LIMIT 1`,
		orgSlug, projectSlug,
	).Scan(&scope.ProjectID, &scope.ProjectSlug, &scope.OrganizationID, &scope.OrganizationSlug)
	if err == sql.ErrNoRows {
		return pageScope{}, false, nil
	}
	return scope, err == nil, err
}

func (h *Handler) listUIProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.accessibleProjectSwitcherProjects(r)
	if err != nil {
		writeWebInternal(w, r, "Failed to load projects.")
		return
	}
	selected := selectedProjectSwitcherValue(r)
	if selected != "" {
		if _, _, ok := findProjectSwitcherProject(projects, selected); !ok {
			selected = ""
		}
	}
	if selected == "" {
		scope, err := h.defaultPageScope(r.Context())
		if err != nil {
			writeWebInternal(w, r, "Failed to resolve selected project.")
			return
		}
		selected = projectSwitcherValue(scope.OrganizationSlug, scope.ProjectSlug)
	}
	if selected == "" && len(projects) > 0 {
		selected = projects[0].Value
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(projectSwitcherResponse{
		Selected: selected,
		Projects: projects,
	})
}

func findProjectSwitcherProject(projects []projectSwitcherProject, value string) (projectSwitcherProject, int, bool) {
	for i, project := range projects {
		if project.Value == value {
			return project, i, true
		}
	}
	return projectSwitcherProject{}, -1, false
}

func (h *Handler) accessibleProjectSwitcherProjects(r *http.Request) ([]projectSwitcherProject, error) {
	if h == nil || h.db == nil {
		return nil, nil
	}
	ctx := r.Context()
	principal := auth.PrincipalFromContext(ctx)
	if principal != nil && principal.User != nil && principal.User.ID != "" {
		rows, err := h.db.QueryContext(ctx,
			`SELECT p.id, p.name, p.slug, o.slug, COALESCE(t.slug, ''), COALESCE(p.platform, ''), COALESCE(p.status, 'active')
			 FROM projects p
			 JOIN organizations o ON o.id = p.organization_id
			 JOIN organization_members m ON m.organization_id = o.id
			 LEFT JOIN teams t ON t.id = p.team_id
			 WHERE m.user_id = ?
			 ORDER BY COALESCE(p.created_at, ''), p.name, p.id`,
			principal.User.ID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanProjectSwitcherProjects(rows)
	}

	rows, err := h.db.QueryContext(ctx,
		`SELECT p.id, p.name, p.slug, o.slug, COALESCE(t.slug, ''), COALESCE(p.platform, ''), COALESCE(p.status, 'active')
		 FROM projects p
		 JOIN organizations o ON o.id = p.organization_id
		 LEFT JOIN teams t ON t.id = p.team_id
		 ORDER BY COALESCE(p.created_at, ''), p.name, p.id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanProjectSwitcherProjects(rows)
}

func scanProjectSwitcherProjects(rows *sql.Rows) ([]projectSwitcherProject, error) {
	projects := []projectSwitcherProject{}
	for rows.Next() {
		var project projectSwitcherProject
		if err := rows.Scan(&project.ID, &project.Name, &project.Slug, &project.OrgSlug, &project.TeamSlug, &project.Platform, &project.Status); err != nil {
			return nil, err
		}
		project.Value = projectSwitcherValue(project.OrgSlug, project.Slug)
		project.SettingsURL = "/settings/project/" + project.Slug + "/general/"
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return projects, nil
}

func normalizeProjectSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastHyphen := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '-' || r == '_' || r == '.' || r == ' ':
			if b.Len() > 0 && !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
