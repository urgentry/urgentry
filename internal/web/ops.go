package web

import (
	"net/http"
	"strconv"
	"time"

	"urgentry/internal/auth"
	sharedstore "urgentry/internal/store"
)

type opsData struct {
	Title             string
	Nav               string
	Environment  string   // selected environment ("" = all)
	Environments []string // available environments for global nav
	OrganizationSlug  string
	DiagnosticsURL    string
	Redactions        []string
	Install           *opsInstallState
	Runtime           sharedstore.OperatorRuntime
	QueueDepth        int
	Services          []opsService
	SLOs              []opsSLO
	Alerts            []opsAlert
	Backfills         []opsBackfill
	BridgeFreshness   []opsBridgeFreshness
	RetentionOutcomes []opsRetentionOutcome
	InstallAudits     []opsInstallAudit
	AuditLogs         []opsAuditLog
}

type opsService struct {
	Name   string
	Status string
	Detail string
}

type opsInstallState struct {
	InstallID            string
	Region               string
	Environment          string
	Version              string
	BootstrapCompleted   bool
	BootstrapCompletedAt string
	MaintenanceMode      bool
	MaintenanceReason    string
	MaintenanceStartedAt string
}

type opsSLO struct {
	Plane   string
	Health  string
	Summary string
}

type opsAlert struct {
	Plane    string
	Name     string
	Severity string
	Health   string
	Trigger  string
}

type opsBridgeFreshness struct {
	Family    string
	Pending   string
	Lag       string
	LastError string
}

type opsBackfill struct {
	ID        string
	Kind      string
	Status    string
	Scope     string
	Progress  string
	LastError string
	UpdatedAt string
}

type opsRetentionOutcome struct {
	Project    string
	Surface    string
	RecordType string
	RecordID   string
	BlobState  string
	ArchivedAt string
	RestoredAt string
}

type opsInstallAudit struct {
	Action    string
	Status    string
	Source    string
	Actor     string
	Detail    string
	Metadata  string
	CreatedAt string
}

type opsAuditLog struct {
	Action    string
	Actor     string
	CreatedAt string
}

func (h *Handler) opsPage(w http.ResponseWriter, r *http.Request) {
	if h.operators == nil || h.catalog == nil {
		http.Error(w, "Operator overview unavailable", http.StatusServiceUnavailable)
		return
	}
	projects, err := h.catalog.ListProjects(r.Context(), "")
	if err != nil {
		http.Error(w, "Failed to load operator overview.", http.StatusInternalServerError)
		return
	}
	var current *sharedstore.Project
	for i := range projects {
		if h.authz != nil && h.authz.AuthorizeProject(r, projects[i].ID, auth.ScopeOrgAdmin) != nil {
			continue
		}
		current = &projects[i]
		break
	}
	if current == nil {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	overview, err := h.operators.Overview(r.Context(), current.OrgSlug, 8)
	if err != nil {
		http.Error(w, "Failed to load operator overview.", http.StatusInternalServerError)
		return
	}
	if overview == nil {
		http.Error(w, "Operator overview unavailable", http.StatusNotFound)
		return
	}

	services := make([]opsService, 0, len(overview.Services))
	for _, item := range overview.Services {
		services = append(services, opsService{
			Name:   item.Name,
			Status: item.Status,
			Detail: item.Detail,
		})
	}
	slos := make([]opsSLO, 0, len(overview.SLOs))
	for _, item := range overview.SLOs {
		slos = append(slos, opsSLO{
			Plane:   item.Plane,
			Health:  item.Health,
			Summary: item.Summary,
		})
	}
	alerts := make([]opsAlert, 0, len(overview.Alerts))
	for _, item := range overview.Alerts {
		alerts = append(alerts, opsAlert{
			Plane:    item.Plane,
			Name:     item.Name,
			Severity: item.Severity,
			Health:   item.Health,
			Trigger:  item.Trigger,
		})
	}
	backfills := make([]opsBackfill, 0, len(overview.Backfills))
	for _, item := range overview.Backfills {
		scope := item.ProjectID
		if scope == "" {
			scope = overview.OrganizationSlug
		}
		if item.ReleaseVersion != "" {
			scope = scope + " / " + item.ReleaseVersion
		}
		progress := "pending"
		if item.TotalItems > 0 {
			progress = strconv.FormatInt(item.ProcessedItems, 10) + " / " + strconv.FormatInt(item.TotalItems, 10)
		}
		backfills = append(backfills, opsBackfill{
			ID:        item.ID,
			Kind:      item.Kind,
			Status:    item.Status,
			Scope:     scope,
			Progress:  progress,
			LastError: item.LastError,
			UpdatedAt: timeAgo(item.DateUpdated),
		})
	}
	bridgeFreshness := make([]opsBridgeFreshness, 0, len(overview.BridgeFreshness))
	for _, item := range overview.BridgeFreshness {
		pending := "ok"
		if item.Pending {
			pending = "pending"
		}
		lag := "-"
		if item.Lag > 0 {
			lag = item.Lag.Truncate(time.Second).String()
		}
		bridgeFreshness = append(bridgeFreshness, opsBridgeFreshness{
			Family:    item.Family,
			Pending:   pending,
			Lag:       lag,
			LastError: item.LastError,
		})
	}
	retentionOutcomes := make([]opsRetentionOutcome, 0, len(overview.RetentionOutcomes))
	for _, item := range overview.RetentionOutcomes {
		blobState := "metadata"
		if item.BlobPresent {
			blobState = "blob"
		}
		restoredAt := ""
		if !item.RestoredAt.IsZero() {
			restoredAt = timeAgo(item.RestoredAt)
		}
		retentionOutcomes = append(retentionOutcomes, opsRetentionOutcome{
			Project:    item.ProjectSlug,
			Surface:    item.Surface,
			RecordType: item.RecordType,
			RecordID:   item.RecordID,
			BlobState:  blobState,
			ArchivedAt: timeAgo(item.ArchivedAt),
			RestoredAt: restoredAt,
		})
	}
	installAudits := make([]opsInstallAudit, 0, len(overview.InstallAudits))
	for _, item := range overview.InstallAudits {
		actor := item.Actor
		if actor == "" {
			actor = "system"
		}
		source := item.Source
		if source == "" {
			source = "system"
		}
		installAudits = append(installAudits, opsInstallAudit{
			Action:    item.Action,
			Status:    item.Status,
			Source:    source,
			Actor:     actor,
			Detail:    item.Detail,
			Metadata:  item.MetadataJSON,
			CreatedAt: timeAgo(item.DateCreated),
		})
	}
	auditLogs := make([]opsAuditLog, 0, len(overview.AuditLogs))
	for _, item := range overview.AuditLogs {
		actor := item.UserEmail
		if actor == "" {
			actor = item.UserID
		}
		if actor == "" {
			actor = item.CredentialType
		}
		if actor == "" {
			actor = "system"
		}
		auditLogs = append(auditLogs, opsAuditLog{
			Action:    item.Action,
			Actor:     actor,
			CreatedAt: timeAgo(item.DateCreated),
		})
	}
	var installState *opsInstallState
	if overview.Install != nil {
		bootstrapCompletedAt := ""
		if !overview.Install.BootstrapCompletedAt.IsZero() {
			bootstrapCompletedAt = timeAgo(overview.Install.BootstrapCompletedAt)
		}
		maintenanceStartedAt := ""
		if !overview.Install.MaintenanceStartedAt.IsZero() {
			maintenanceStartedAt = timeAgo(overview.Install.MaintenanceStartedAt)
		}
		installState = &opsInstallState{
			InstallID:            overview.Install.InstallID,
			Region:               overview.Install.Region,
			Environment:          overview.Install.Environment,
			Version:              overview.Install.Version,
			BootstrapCompleted:   overview.Install.BootstrapCompleted,
			BootstrapCompletedAt: bootstrapCompletedAt,
			MaintenanceMode:      overview.Install.MaintenanceMode,
			MaintenanceReason:    overview.Install.MaintenanceReason,
			MaintenanceStartedAt: maintenanceStartedAt,
		}
	}

	h.render(w, "ops.html", opsData{
		Title:             "Operator",
		Nav:               "ops",
		Environment:       readSelectedEnvironment(r),
		Environments:      h.loadEnvironments(r.Context()),
		OrganizationSlug:  overview.OrganizationSlug,
		DiagnosticsURL:    "/api/0/organizations/" + overview.OrganizationSlug + "/ops/diagnostics/",
		Redactions:        []string{"credentials and secrets are never included", "only operator-safe runtime metadata is exported"},
		Install:           installState,
		Runtime:           overview.Runtime,
		QueueDepth:        overview.Queue.Depth,
		Services:          services,
		SLOs:              slos,
		Alerts:            alerts,
		Backfills:         backfills,
		BridgeFreshness:   bridgeFreshness,
		RetentionOutcomes: retentionOutcomes,
		InstallAudits:     installAudits,
		AuditLogs:         auditLogs,
	})
}
