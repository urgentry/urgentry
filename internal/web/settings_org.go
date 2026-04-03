package web

import (
	"errors"
	"net/http"

	"urgentry/internal/auth"
)

// ---------------------------------------------------------------------------
// Shared org settings base
// ---------------------------------------------------------------------------

type orgSettingsData struct {
	Title        string
	Nav          string
	Environment  string
	Environments []string
	ActiveTab    string // "general" | "members" | "teams" | "auth" | "audit-log"
	OrgName      string
	OrgSlug      string
}

func (h *Handler) orgSettingsBase(r *http.Request, tab string) (orgSettingsData, string) {
	ctx := r.Context()
	data := orgSettingsData{
		Nav:          "settings",
		ActiveTab:    tab,
		Environment:  readSelectedEnvironment(r),
		Environments: h.loadEnvironments(ctx),
	}

	if h.catalog != nil {
		orgs, err := h.catalog.ListOrganizations(ctx)
		if err == nil && len(orgs) > 0 {
			data.OrgName = orgs[0].Name
			data.OrgSlug = orgs[0].Slug
		}
	}

	return data, data.OrgSlug
}

// ---------------------------------------------------------------------------
// GET /settings/org/ — general org settings (name, slug)
// ---------------------------------------------------------------------------

type orgGeneralData struct {
	orgSettingsData
}

func (h *Handler) orgSettingsPage(w http.ResponseWriter, r *http.Request) {
	base, _ := h.orgSettingsBase(r, "general")
	base.Title = "Organization Settings"

	data := orgGeneralData{orgSettingsData: base}
	h.render(w, "settings-org.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/org/members/ — member list
// ---------------------------------------------------------------------------

type orgMemberRow struct {
	ID        string
	Email     string
	Name      string
	Role      string
	CreatedAt string
}

type orgMembersData struct {
	orgSettingsData
	Members []orgMemberRow
}

func (h *Handler) orgMembersPage(w http.ResponseWriter, r *http.Request) {
	base, orgSlug := h.orgSettingsBase(r, "members")
	base.Title = "Members — Organization Settings"

	var members []orgMemberRow
	if h.admin != nil && orgSlug != "" {
		records, err := h.admin.ListOrgMembers(r.Context(), orgSlug)
		if err == nil {
			members = make([]orgMemberRow, 0, len(records))
			for _, rec := range records {
				members = append(members, orgMemberRow{
					ID:        rec.ID,
					Email:     rec.Email,
					Name:      rec.Name,
					Role:      rec.Role,
					CreatedAt: timeAgo(rec.CreatedAt),
				})
			}
		}
	}

	data := orgMembersData{
		orgSettingsData: base,
		Members:         members,
	}
	h.render(w, "settings-org-members.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/org/teams/ — team list
// ---------------------------------------------------------------------------

type orgTeamRow struct {
	ID        string
	Slug      string
	Name      string
	CreatedAt string
}

type orgTeamsData struct {
	orgSettingsData
	Teams []orgTeamRow
}

func (h *Handler) orgTeamsPage(w http.ResponseWriter, r *http.Request) {
	base, orgSlug := h.orgSettingsBase(r, "teams")
	base.Title = "Teams — Organization Settings"

	var teams []orgTeamRow
	if h.admin != nil && orgSlug != "" {
		records, err := h.admin.ListTeams(r.Context(), orgSlug)
		if err == nil {
			teams = make([]orgTeamRow, 0, len(records))
			for _, rec := range records {
				teams = append(teams, orgTeamRow{
					ID:        rec.ID,
					Slug:      rec.Slug,
					Name:      rec.Name,
					CreatedAt: timeAgo(rec.CreatedAt),
				})
			}
		}
	}

	data := orgTeamsData{
		orgSettingsData: base,
		Teams:           teams,
	}
	h.render(w, "settings-org-teams.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/org/auth/ — auth provider settings (OIDC status)
// ---------------------------------------------------------------------------

type orgOIDCStatus struct {
	Configured bool
	Enabled    bool
	Issuer     string
	ClientID   string
	Scopes     string
}

type orgAuthData struct {
	orgSettingsData
	OIDC orgOIDCStatus
}

func (h *Handler) orgAuthPage(w http.ResponseWriter, r *http.Request) {
	base, _ := h.orgSettingsBase(r, "auth")
	base.Title = "Auth — Organization Settings"

	var oidc orgOIDCStatus
	if h.oidcConfigs != nil && base.OrgSlug != "" {
		orgID := ""
		if h.catalog != nil {
			if org, err := h.catalog.GetOrganization(r.Context(), base.OrgSlug); err == nil && org != nil {
				orgID = org.ID
			}
		}
		if orgID != "" {
			cfg, err := h.oidcConfigs.GetOIDCConfig(r.Context(), orgID)
			if err == nil && cfg != nil {
				oidc = orgOIDCStatus{
					Configured: true,
					Enabled:    cfg.Enabled,
					Issuer:     cfg.Issuer,
					ClientID:   cfg.ClientID,
					Scopes:     cfg.EffectiveScopes(),
				}
			} else if err != nil && !errors.Is(err, auth.ErrOIDCNotConfigured) {
				writeWebInternal(w, r, "Failed to load auth settings.")
				return
			}
		}
	}

	data := orgAuthData{
		orgSettingsData: base,
		OIDC:            oidc,
	}
	h.render(w, "settings-org-auth.html", data)
}

// ---------------------------------------------------------------------------
// GET /settings/org/audit-log/ — audit log viewer
// ---------------------------------------------------------------------------

type orgAuditLogRow struct {
	Action    string
	Actor     string
	Source    string
	Status    string
	Detail    string
	CreatedAt string
}

type orgAuditLogData struct {
	orgSettingsData
	Entries []orgAuditLogRow
}

func (h *Handler) orgAuditLogPage(w http.ResponseWriter, r *http.Request) {
	base, orgSlug := h.orgSettingsBase(r, "audit-log")
	base.Title = "Audit Log — Organization Settings"

	var entries []orgAuditLogRow
	if h.operatorAudits != nil && orgSlug != "" {
		records, err := h.operatorAudits.List(r.Context(), orgSlug, 100)
		if err == nil {
			entries = make([]orgAuditLogRow, 0, len(records))
			for _, rec := range records {
				actor := rec.Actor
				if actor == "" {
					actor = "system"
				}
				entries = append(entries, orgAuditLogRow{
					Action:    rec.Action,
					Actor:     actor,
					Source:    rec.Source,
					Status:    rec.Status,
					Detail:    rec.Detail,
					CreatedAt: timeAgo(rec.DateCreated),
				})
			}
		}
	}

	data := orgAuditLogData{
		orgSettingsData: base,
		Entries:         entries,
	}
	h.render(w, "settings-org-audit-log.html", data)
}
