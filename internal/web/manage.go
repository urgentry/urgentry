package web

import (
	"database/sql"
	"net/http"
	"runtime"
	"time"

	"urgentry/internal/auth"
	sharedstore "urgentry/internal/store"
)

// ---------------------------------------------------------------------------
// Shared manage guard helper
// ---------------------------------------------------------------------------

// manageGuard checks that the request is from an org admin. Returns the first
// organization the caller is authorized to administer, or writes an error
// response and returns nil.
func (h *Handler) manageGuard(w http.ResponseWriter, r *http.Request) *sharedstore.Organization {
	if h.catalog == nil {
		http.Error(w, "Admin console unavailable", http.StatusServiceUnavailable)
		return nil
	}
	orgs, err := h.catalog.ListOrganizations(r.Context())
	if err != nil || len(orgs) == 0 {
		http.Error(w, "Admin console unavailable", http.StatusServiceUnavailable)
		return nil
	}
	// Require org:admin on at least one org.
	for i := range orgs {
		projects, pErr := h.catalog.ListProjects(r.Context(), orgs[i].Slug)
		if pErr != nil || len(projects) == 0 {
			continue
		}
		if h.authz != nil && h.authz.AuthorizeProject(r, projects[0].ID, auth.ScopeOrgAdmin) != nil {
			continue
		}
		return &orgs[i]
	}
	http.Error(w, "Forbidden", http.StatusForbidden)
	return nil
}

// ---------------------------------------------------------------------------
// Shared page data base
// ---------------------------------------------------------------------------

type manageBase struct {
	Title        string
	Nav          string
	ManageNav    string // active sub-nav key
	Environment  string
	Environments []string
}

// ---------------------------------------------------------------------------
// GET /manage/ — admin dashboard overview
// ---------------------------------------------------------------------------

type manageDashboardData struct {
	manageBase
	OrgCount     int
	ProjectCount int
	UserCount    int
	DBSizeBytes  int64
	DBSizeFmt    string
	Uptime       string
}

func (h *Handler) manageDashboardPage(w http.ResponseWriter, r *http.Request) {
	if h.manageGuard(w, r) == nil {
		return
	}

	orgs, _ := h.catalog.ListOrganizations(r.Context())
	projects, _ := h.catalog.ListProjects(r.Context(), "")
	userCount := countUsers(h.db, r)
	dbSize := h.databaseFileSize()

	h.render(w, "manage-dashboard.html", manageDashboardData{
		manageBase: manageBase{
			Title:        "Admin Console",
			Nav:          "manage",
			ManageNav:    "dashboard",
			Environment:  readSelectedEnvironment(r),
			Environments: h.loadEnvironments(r.Context()),
		},
		OrgCount:     len(orgs),
		ProjectCount: len(projects),
		UserCount:    userCount,
		DBSizeBytes:  dbSize,
		DBSizeFmt:    formatBytes(dbSize),
		Uptime:       time.Since(h.startedAt).Truncate(time.Second).String(),
	})
}

// ---------------------------------------------------------------------------
// GET /manage/organizations/ — list all organizations
// ---------------------------------------------------------------------------

type manageOrg struct {
	ID           string
	Slug         string
	Name         string
	DateCreated  string
	ProjectCount int
}

type manageOrgsData struct {
	manageBase
	Organizations []manageOrg
}

func (h *Handler) manageOrganizationsPage(w http.ResponseWriter, r *http.Request) {
	if h.manageGuard(w, r) == nil {
		return
	}

	orgs, err := h.catalog.ListOrganizations(r.Context())
	if err != nil {
		http.Error(w, "Failed to load organizations", http.StatusInternalServerError)
		return
	}

	items := make([]manageOrg, 0, len(orgs))
	for _, org := range orgs {
		projects, _ := h.catalog.ListProjects(r.Context(), org.Slug)
		items = append(items, manageOrg{
			ID:           org.ID,
			Slug:         org.Slug,
			Name:         org.Name,
			DateCreated:  timeAgo(org.DateCreated),
			ProjectCount: len(projects),
		})
	}

	h.render(w, "manage-organizations.html", manageOrgsData{
		manageBase: manageBase{
			Title:        "Organizations — Admin Console",
			Nav:          "manage",
			ManageNav:    "organizations",
			Environment:  readSelectedEnvironment(r),
			Environments: h.loadEnvironments(r.Context()),
		},
		Organizations: items,
	})
}

// ---------------------------------------------------------------------------
// GET /manage/projects/ — list all projects across orgs
// ---------------------------------------------------------------------------

type manageProject struct {
	ID          string
	Slug        string
	Name        string
	OrgSlug     string
	Platform    string
	Status      string
	DateCreated string
}

type manageProjectsData struct {
	manageBase
	Projects []manageProject
}

func (h *Handler) manageProjectsPage(w http.ResponseWriter, r *http.Request) {
	if h.manageGuard(w, r) == nil {
		return
	}

	// ListProjects with empty org returns all projects.
	projects, err := h.catalog.ListProjects(r.Context(), "")
	if err != nil {
		http.Error(w, "Failed to load projects", http.StatusInternalServerError)
		return
	}

	items := make([]manageProject, 0, len(projects))
	for _, p := range projects {
		items = append(items, manageProject{
			ID:          p.ID,
			Slug:        p.Slug,
			Name:        p.Name,
			OrgSlug:     p.OrgSlug,
			Platform:    p.Platform,
			Status:      p.Status,
			DateCreated: timeAgo(p.DateCreated),
		})
	}

	h.render(w, "manage-projects.html", manageProjectsData{
		manageBase: manageBase{
			Title:        "Projects — Admin Console",
			Nav:          "manage",
			ManageNav:    "projects",
			Environment:  readSelectedEnvironment(r),
			Environments: h.loadEnvironments(r.Context()),
		},
		Projects: items,
	})
}

// ---------------------------------------------------------------------------
// GET /manage/users/ — list all users with role badges
// ---------------------------------------------------------------------------

type manageUser struct {
	ID          string
	Email       string
	Name        string
	OrgRoles    []manageUserOrgRole
	CreatedAt   string
}

type manageUserOrgRole struct {
	OrgSlug string
	Role    string
}

type manageUsersData struct {
	manageBase
	Users []manageUser
}

func (h *Handler) manageUsersPage(w http.ResponseWriter, r *http.Request) {
	if h.manageGuard(w, r) == nil {
		return
	}

	rows, err := h.db.QueryContext(r.Context(),
		`SELECT u.id, u.email, u.display_name, u.created_at FROM users u WHERE u.is_active = 1 ORDER BY u.created_at ASC`,
	)
	if err != nil {
		http.Error(w, "Failed to load users", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	type rawUser struct {
		id        string
		email     string
		name      string
		createdAt string
	}
	var rawUsers []rawUser
	for rows.Next() {
		var u rawUser
		var createdAt sql.NullString
		if err := rows.Scan(&u.id, &u.email, &u.name, &createdAt); err != nil {
			continue
		}
		if createdAt.Valid {
			u.createdAt = createdAt.String
		}
		rawUsers = append(rawUsers, u)
	}

	// Load org roles per user.
	roleRows, err := h.db.QueryContext(r.Context(),
		`SELECT m.user_id, o.slug, m.role
		 FROM organization_members m
		 JOIN organizations o ON o.id = m.organization_id
		 ORDER BY o.slug, m.role`,
	)
	orgRoles := map[string][]manageUserOrgRole{}
	if err == nil {
		defer roleRows.Close()
		for roleRows.Next() {
			var userID, orgSlug, role string
			if scanErr := roleRows.Scan(&userID, &orgSlug, &role); scanErr == nil {
				orgRoles[userID] = append(orgRoles[userID], manageUserOrgRole{OrgSlug: orgSlug, Role: role})
			}
		}
	}

	users := make([]manageUser, 0, len(rawUsers))
	for _, u := range rawUsers {
		users = append(users, manageUser{
			ID:        u.id,
			Email:     u.email,
			Name:      u.name,
			OrgRoles:  orgRoles[u.id],
			CreatedAt: timeAgo(parseDBTime(u.createdAt)),
		})
	}

	h.render(w, "manage-users.html", manageUsersData{
		manageBase: manageBase{
			Title:        "Users — Admin Console",
			Nav:          "manage",
			ManageNav:    "users",
			Environment:  readSelectedEnvironment(r),
			Environments: h.loadEnvironments(r.Context()),
		},
		Users: users,
	})
}

// ---------------------------------------------------------------------------
// GET /manage/settings/ — system-level settings (retention, quotas)
// ---------------------------------------------------------------------------

type manageSettingsProject struct {
	Slug                    string
	Name                    string
	OrgSlug                 string
	EventRetentionDays      int
	AttachmentRetentionDays int
	DebugRetentionDays      int
}

type manageSettingsData struct {
	manageBase
	Projects []manageSettingsProject
}

func (h *Handler) manageSettingsPage(w http.ResponseWriter, r *http.Request) {
	if h.manageGuard(w, r) == nil {
		return
	}

	projects, err := h.catalog.ListProjects(r.Context(), "")
	if err != nil {
		http.Error(w, "Failed to load settings", http.StatusInternalServerError)
		return
	}

	items := make([]manageSettingsProject, 0, len(projects))
	for _, p := range projects {
		settings, sErr := h.catalog.GetProjectSettings(r.Context(), p.OrgSlug, p.Slug)
		if sErr != nil || settings == nil {
			items = append(items, manageSettingsProject{
				Slug:    p.Slug,
				Name:    p.Name,
				OrgSlug: p.OrgSlug,
			})
			continue
		}
		items = append(items, manageSettingsProject{
			Slug:                    p.Slug,
			Name:                    p.Name,
			OrgSlug:                 p.OrgSlug,
			EventRetentionDays:      settings.EventRetentionDays,
			AttachmentRetentionDays: settings.AttachmentRetentionDays,
			DebugRetentionDays:      settings.DebugFileRetentionDays,
		})
	}

	h.render(w, "manage-settings.html", manageSettingsData{
		manageBase: manageBase{
			Title:        "Settings — Admin Console",
			Nav:          "manage",
			ManageNav:    "settings",
			Environment:  readSelectedEnvironment(r),
			Environments: h.loadEnvironments(r.Context()),
		},
		Projects: items,
	})
}

// ---------------------------------------------------------------------------
// GET /manage/status/ — environment info, Go version, database stats
// ---------------------------------------------------------------------------

type manageStatusData struct {
	manageBase
	GoVersion    string
	GOOS         string
	GOARCH       string
	NumCPU       int
	NumGoroutine int
	DBSizeBytes  int64
	DBSizeFmt    string
	DBOpenConns  int
	DBIdleConns  int
	Uptime       string
	StartedAt    string
}

func (h *Handler) manageStatusPage(w http.ResponseWriter, r *http.Request) {
	if h.manageGuard(w, r) == nil {
		return
	}

	dbStats := h.db.Stats()
	dbSize := h.databaseFileSize()

	h.render(w, "manage-status.html", manageStatusData{
		manageBase: manageBase{
			Title:        "Status — Admin Console",
			Nav:          "manage",
			ManageNav:    "status",
			Environment:  readSelectedEnvironment(r),
			Environments: h.loadEnvironments(r.Context()),
		},
		GoVersion:    runtime.Version(),
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		NumCPU:       runtime.NumCPU(),
		NumGoroutine: runtime.NumGoroutine(),
		DBSizeBytes:  dbSize,
		DBSizeFmt:    formatBytes(dbSize),
		DBOpenConns:  dbStats.OpenConnections,
		DBIdleConns:  dbStats.Idle,
		Uptime:       time.Since(h.startedAt).Truncate(time.Second).String(),
		StartedAt:    h.startedAt.UTC().Format(time.RFC3339),
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func countUsers(db *sql.DB, r *http.Request) int {
	var n int
	_ = db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users WHERE is_active = 1`).Scan(&n)
	return n
}
