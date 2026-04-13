package api

import (
	"crypto/sha256"
	"fmt"
	"net/http"

	"urgentry/internal/controlplane"
	"urgentry/internal/httputil"
	"urgentry/internal/store"
)

// defaultOrgFeatures is the default set of feature flags for all organizations.
var defaultOrgFeatures = []string{
	"organizations:discover",
	"organizations:events",
	"organizations:monitors",
	"organizations:performance",
	"organizations:replays",
	"organizations:profiling",
	"organizations:dashboards",
	"organizations:alerts",
	"organizations:releases",
	"organizations:issue-details-replay",
}

// ownerAccess is the full set of permissions granted to organization owners.
var ownerAccess = []string{
	"org:read", "org:write", "org:admin", "org:integrations",
	"team:read", "team:write", "team:admin",
	"project:read", "project:write", "project:admin", "project:releases",
	"member:read", "member:write", "member:admin",
	"event:read", "event:write", "event:admin",
	"alerts:read", "alerts:write",
}

// managerAccess is the set of permissions granted to managers.
var managerAccess = []string{
	"org:read", "org:write", "org:integrations",
	"team:read", "team:write", "team:admin",
	"project:read", "project:write", "project:admin", "project:releases",
	"member:read", "member:write",
	"event:read", "event:write", "event:admin",
	"alerts:read", "alerts:write",
}

// memberAccess is the set of permissions granted to regular members.
var memberAccess = []string{
	"org:read",
	"team:read",
	"project:read", "project:releases",
	"member:read",
	"event:read",
	"alerts:read",
}

// accessForRole returns the permission set for a given organization role.
func accessForRole(role string) []string {
	switch role {
	case "owner":
		return ownerAccess
	case "manager":
		return managerAccess
	default:
		return memberAccess
	}
}

// buildOrganizationDetail enriches a store Organization into a full API response.
func buildOrganizationDetail(org *store.Organization, teams []store.Team, projects []store.Project) *OrganizationDetail {
	orgTeams := make([]OrgTeamResponse, 0, len(teams))
	for _, t := range teams {
		orgTeams = append(orgTeams, OrgTeamResponse{
			ID:          t.ID,
			Slug:        t.Slug,
			Name:        t.Name,
			DateCreated: t.DateCreated,
			IsMember:    true,
			MemberCount: 0,
			Avatar:      teamAvatar{Type: "letter_avatar"},
			HasAccess:   true,
			IsPending:   false,
		})
	}

	orgProjects := make([]OrgProjectResponse, 0, len(projects))
	for _, p := range projects {
		orgProjects = append(orgProjects, OrgProjectResponse{
			ID:           p.ID,
			Slug:         p.Slug,
			Name:         p.Name,
			Platform:     p.Platform,
			Status:       p.Status,
			DateCreated:  p.DateCreated,
			HasAccess:    true,
			IsBookmarked: false,
			IsMember:     true,
		})
	}

	return &OrganizationDetail{
		ID:                    org.ID,
		Slug:                  org.Slug,
		Name:                  org.Name,
		DateCreated:           org.DateCreated,
		Features:              defaultOrgFeatures,
		Access:                accessForRole("owner"),
		AllowMemberInvite:     true,
		AllowMemberProjectCreation: true,
		AllowSuperuserAccess:  false,
		Teams:                 orgTeams,
		Projects:              orgProjects,
		Avatar:                OrgAvatar{Type: "letter_avatar"},
		HasAuthProvider:       false,
		Links: OrgLinks{
			OrganizationURL: "/organizations/" + org.Slug + "/",
			RegionURL:       "/",
		},
		Require2FA:            false,
		ExtraOptions:          map[string]any{},
		Status: OrgStatus{
			ID:   "active",
			Name: "active",
		},
		IsEarlyAdopter:        false,
		AllowJoinRequests:     false,
		OpenMembership:        false,
		DefaultRole:           "member",
		EnhancedPrivacy:       false,
		DataScrubber:          false,
		DataScrubberDefaults:  false,
		SensitiveFields:       []string{},
		SafeFields:            []string{},
		ScrubIPAddresses:      false,
		StoreCrashReports:     0,
		RelayPiiConfig:        "",
		AllowSharedIssues:     true,
		TrustedRelays:         []string{},
		OnboardingTasks:       []any{},
	}
}

// handleListOrgs handles GET /api/0/organizations/.
func handleListOrgs(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		orgs, err := catalog.ListOrganizations(r.Context())
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list organizations.")
			return
		}
		page := Paginate(w, r, orgs)
		if page == nil {
			page = []Organization{}
		}
		httputil.WriteJSON(w, http.StatusOK, page)
	}
}

// handleGetOrg handles GET /api/0/organizations/{org_slug}/.
func handleGetOrg(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		slug := PathParam(r, "org_slug")
		rec, err := catalog.GetOrganization(r.Context(), slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to load organization.")
			return
		}
		if rec == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}

		teams, _ := catalog.ListTeams(r.Context(), slug)
		if teams == nil {
			teams = []store.Team{}
		}
		projects, _ := catalog.ListProjects(r.Context(), slug)
		if projects == nil {
			projects = []store.Project{}
		}

		httputil.WriteJSON(w, http.StatusOK, buildOrganizationDetail(rec, teams, projects))
	}
}

// handleUpdateOrg handles PUT /api/0/organizations/{org_slug}/.
func handleUpdateOrg(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		slug := PathParam(r, "org_slug")
		org, ok := getOrganizationFromCatalog(w, r, catalog, slug)
		if !ok {
			return
		}
		_ = org

		var body store.OrganizationUpdate
		if err := decodeJSON(r, &body); err != nil {
			httputil.WriteError(w, http.StatusBadRequest, "Invalid JSON body.")
			return
		}
		// Accept the update as long as the JSON body was valid.
		// The catalog layer preserves existing values for empty fields.

		updated, err := catalog.UpdateOrganization(r.Context(), slug, body)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to update organization.")
			return
		}
		if updated == nil {
			httputil.WriteError(w, http.StatusNotFound, "Organization not found.")
			return
		}

		useSlug := updated.Slug
		teams, _ := catalog.ListTeams(r.Context(), useSlug)
		if teams == nil {
			teams = []store.Team{}
		}
		projects, _ := catalog.ListProjects(r.Context(), useSlug)
		if projects == nil {
			projects = []store.Project{}
		}
		httputil.WriteJSON(w, http.StatusOK, buildOrganizationDetail(updated, teams, projects))
	}
}

// environmentEntry is the JSON response shape for a single environment.
type environmentEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// handleListOrgEnvironments handles GET /api/0/organizations/{org_slug}/environments/.
func handleListOrgEnvironments(catalog controlplane.CatalogStore, auth authFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		slug := PathParam(r, "org_slug")
		_, ok := getOrganizationFromCatalog(w, r, catalog, slug)
		if !ok {
			return
		}

		envNames, err := catalog.ListEnvironments(r.Context(), slug)
		if err != nil {
			httputil.WriteError(w, http.StatusInternalServerError, "Failed to list environments.")
			return
		}

		out := make([]environmentEntry, 0, len(envNames))
		for _, name := range envNames {
			out = append(out, environmentEntry{
				ID:   envNameToID(name),
				Name: name,
			})
		}
		httputil.WriteJSON(w, http.StatusOK, out)
	}
}

// envNameToID produces a stable deterministic ID from an environment name.
func envNameToID(name string) string {
	h := sha256.Sum256([]byte(name))
	return fmt.Sprintf("%x", h[:8])
}
