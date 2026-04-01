package store

import (
	"context"
	"strings"
	"time"
)

type OperatorRuntime struct {
	Role         string `json:"role"`
	Env          string `json:"env"`
	Version      string `json:"version,omitempty"`
	AsyncBackend string `json:"asyncBackend,omitempty"`
	CacheBackend string `json:"cacheBackend,omitempty"`
	BlobBackend  string `json:"blobBackend,omitempty"`
}

type OperatorServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type OperatorQueueStatus struct {
	Depth int `json:"depth"`
}

type OperatorPlaneStatus struct {
	Plane   string `json:"plane"`
	Health  string `json:"health"`
	Summary string `json:"summary,omitempty"`
}

type OperatorAlertStatus struct {
	Plane    string `json:"plane"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Health   string `json:"health"`
	Trigger  string `json:"trigger"`
}

type OperatorBackfillStatus struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind"`
	Status         string    `json:"status"`
	ProjectID      string    `json:"projectId,omitempty"`
	ReleaseVersion string    `json:"releaseVersion,omitempty"`
	ProcessedItems int64     `json:"processedItems"`
	TotalItems     int64     `json:"totalItems"`
	FailedItems    int64     `json:"failedItems"`
	LastError      string    `json:"lastError,omitempty"`
	DateCreated    time.Time `json:"dateCreated"`
	DateUpdated    time.Time `json:"dateUpdated"`
}

type OperatorRetentionOutcome struct {
	ID          string    `json:"id"`
	ProjectID   string    `json:"projectId"`
	ProjectSlug string    `json:"projectSlug"`
	Surface     string    `json:"surface"`
	RecordType  string    `json:"recordType"`
	RecordID    string    `json:"recordId"`
	ArchiveKey  string    `json:"archiveKey,omitempty"`
	BlobPresent bool      `json:"blobPresent"`
	ArchivedAt  time.Time `json:"archivedAt"`
	RestoredAt  time.Time `json:"restoredAt,omitempty"`
}

// OperatorBridgeFreshness reports the projection lag for a single telemetry family.
type OperatorBridgeFreshness struct {
	Family    string        `json:"family"`
	Pending   bool          `json:"pending"`
	Lag       time.Duration `json:"lag"`
	LastError string        `json:"lastError,omitempty"`
}

type OperatorOverview struct {
	OrganizationSlug  string                     `json:"organizationSlug"`
	Install           *InstallState              `json:"install,omitempty"`
	Runtime           OperatorRuntime            `json:"runtime"`
	Services          []OperatorServiceStatus    `json:"services"`
	Queue             OperatorQueueStatus        `json:"queue"`
	SLOs              []OperatorPlaneStatus      `json:"slos"`
	Alerts            []OperatorAlertStatus      `json:"alerts"`
	Backfills         []OperatorBackfillStatus   `json:"backfills"`
	BridgeFreshness   []OperatorBridgeFreshness  `json:"bridgeFreshness,omitempty"`
	RetentionOutcomes []OperatorRetentionOutcome `json:"retentionOutcomes"`
	InstallAudits     []OperatorAuditEntry       `json:"installAudits"`
	AuditLogs         []AuditLogEntry            `json:"auditLogs"`
}

type OperatorStore interface {
	Overview(ctx context.Context, orgSlug string, limit int) (*OperatorOverview, error)
}

func BuildOperatorHealth(services []OperatorServiceStatus, queue OperatorQueueStatus, backfills []OperatorBackfillStatus, retention []OperatorRetentionOutcome, freshness ...[]OperatorBridgeFreshness) ([]OperatorPlaneStatus, []OperatorAlertStatus) {
	planeHealth := map[string]string{
		"control":   "ok",
		"async":     "ok",
		"cache":     "ok",
		"blob":      "ok",
		"telemetry": "ok",
	}
	planeSummary := map[string]string{}
	for _, service := range services {
		plane := planeForService(service.Name)
		switch service.Status {
		case "error":
			planeHealth[plane] = "page"
			planeSummary[plane] = service.Detail
		case "warn":
			if planeHealth[plane] == "ok" {
				planeHealth[plane] = "warn"
				planeSummary[plane] = service.Detail
			}
		}
	}
	if queue.Depth > 100 {
		planeHealth["async"] = "page"
		planeSummary["async"] = "queue depth is above the page threshold"
	} else if queue.Depth > 20 && planeHealth["async"] == "ok" {
		planeHealth["async"] = "warn"
		planeSummary["async"] = "queue depth is above the warning threshold"
	}
	for _, item := range backfills {
		if item.FailedItems > 0 && planeHealth["telemetry"] == "ok" {
			planeHealth["telemetry"] = "warn"
			planeSummary["telemetry"] = "one or more backfills have failed items"
		}
	}
	for _, item := range retention {
		if !item.BlobPresent && planeHealth["blob"] == "ok" {
			planeHealth["blob"] = "warn"
			planeSummary["blob"] = "one or more retention outcomes have metadata without a blob"
		}
	}
	if len(freshness) > 0 {
		for _, item := range freshness[0] {
			if item.LastError != "" && planeHealth["telemetry"] != "page" {
				planeHealth["telemetry"] = "page"
				planeSummary["telemetry"] = "bridge projector failure: " + item.LastError
			} else if item.Pending && item.Lag > 10*time.Minute && planeHealth["telemetry"] == "ok" {
				planeHealth["telemetry"] = "warn"
				planeSummary["telemetry"] = "bridge projection lag exceeds 10 minutes"
			}
		}
	}
	slos := []OperatorPlaneStatus{
		{Plane: "control", Health: planeHealth["control"], Summary: planeSummary["control"]},
		{Plane: "async", Health: planeHealth["async"], Summary: planeSummary["async"]},
		{Plane: "cache", Health: planeHealth["cache"], Summary: planeSummary["cache"]},
		{Plane: "blob", Health: planeHealth["blob"], Summary: planeSummary["blob"]},
		{Plane: "telemetry", Health: planeHealth["telemetry"], Summary: planeSummary["telemetry"]},
	}
	alerts := []OperatorAlertStatus{
		{Plane: "control", Name: "control api error spike", Severity: "page", Health: planeHealth["control"], Trigger: "5xx rate > 2% for 10m"},
		{Plane: "async", Name: "async backlog growth", Severity: "page", Health: planeHealth["async"], Trigger: "oldest job age > 15m for 10m"},
		{Plane: "cache", Name: "valkey outage", Severity: "page", Health: planeHealth["cache"], Trigger: "quota or lease command failures > 0 for 2m"},
		{Plane: "blob", Name: "blob read failures", Severity: "page", Health: planeHealth["blob"], Trigger: "blob read failures > 1% for 10m"},
		{Plane: "telemetry", Name: "bridge lag breach", Severity: "page", Health: planeHealth["telemetry"], Trigger: "bridge lag > 10m for 10m"},
	}
	return slos, alerts
}

func planeForService(name string) string {
	lower := strings.ToLower(name)
	switch {
	case strings.Contains(lower, "jetstream"), strings.Contains(lower, "worker"), strings.Contains(lower, "scheduler"):
		return "async"
	case strings.Contains(lower, "valkey"), strings.Contains(lower, "cache"):
		return "cache"
	case strings.Contains(lower, "minio"), strings.Contains(lower, "blob"), strings.Contains(lower, "s3"):
		return "blob"
	case strings.Contains(lower, "telemetry"), strings.Contains(lower, "timescale"), strings.Contains(lower, "bridge"):
		return "telemetry"
	default:
		return "control"
	}
}
