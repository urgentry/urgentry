package store

import "time"

type OperatorDiagnosticsBundle struct {
	OrganizationSlug  string                     `json:"organizationSlug"`
	CapturedAt        time.Time                  `json:"capturedAt"`
	Summary           OperatorDiagnosticsSummary `json:"summary"`
	Install           *InstallState              `json:"install,omitempty"`
	Runtime           OperatorRuntime            `json:"runtime"`
	FleetNodes        []OperatorFleetNode        `json:"fleetNodes,omitempty"`
	Services          []OperatorServiceStatus    `json:"services"`
	Queue             OperatorQueueStatus        `json:"queue"`
	Backfills         []OperatorBackfillStatus   `json:"backfills"`
	BridgeFreshness   []OperatorBridgeFreshness  `json:"bridgeFreshness,omitempty"`
	RetentionOutcomes []OperatorRetentionOutcome `json:"retentionOutcomes"`
	InstallAudits     []OperatorAuditEntry       `json:"installAudits"`
	AuditLogs         []AuditLogEntry            `json:"auditLogs"`
	Redactions        []string                   `json:"redactions"`
}

type OperatorDiagnosticsSummary struct {
	Health                   string         `json:"health"`
	ServiceCounts            map[string]int `json:"serviceCounts,omitempty"`
	QueueDepth               int            `json:"queueDepth"`
	FleetNodeCount           int            `json:"fleetNodeCount"`
	FailedBackfills          int            `json:"failedBackfills"`
	MissingRetentionBlobs    int            `json:"missingRetentionBlobs"`
	PendingBridgeProjections int            `json:"pendingBridgeProjections,omitempty"`
	BridgeProjectionErrors   int            `json:"bridgeProjectionErrors,omitempty"`
	RecentInstallAudits      int            `json:"recentInstallAudits"`
	RecentAuthAuditLogs      int            `json:"recentAuthAuditLogs"`
	RecommendedInvestigation []string       `json:"recommendedInvestigation,omitempty"`
}

func BuildOperatorDiagnosticsBundle(overview *OperatorOverview, capturedAt time.Time) *OperatorDiagnosticsBundle {
	if overview == nil {
		return nil
	}
	if capturedAt.IsZero() {
		capturedAt = time.Now().UTC()
	}
	var install *InstallState
	if overview.Install != nil {
		copyValue := *overview.Install
		install = &copyValue
	}
	return &OperatorDiagnosticsBundle{
		OrganizationSlug:  overview.OrganizationSlug,
		CapturedAt:        capturedAt.UTC(),
		Summary:           BuildOperatorDiagnosticsSummary(overview),
		Install:           install,
		Runtime:           overview.Runtime,
		FleetNodes:        append([]OperatorFleetNode(nil), overview.FleetNodes...),
		Services:          append([]OperatorServiceStatus(nil), overview.Services...),
		Queue:             overview.Queue,
		Backfills:         append([]OperatorBackfillStatus(nil), overview.Backfills...),
		BridgeFreshness:   append([]OperatorBridgeFreshness(nil), overview.BridgeFreshness...),
		RetentionOutcomes: append([]OperatorRetentionOutcome(nil), overview.RetentionOutcomes...),
		InstallAudits:     append([]OperatorAuditEntry(nil), overview.InstallAudits...),
		AuditLogs:         append([]AuditLogEntry(nil), overview.AuditLogs...),
		Redactions: []string{
			"credentials and secrets are never included in the support bundle",
			"only backend names and operator-safe runtime metadata are exported",
		},
	}
}

func BuildOperatorDiagnosticsSummary(overview *OperatorOverview) OperatorDiagnosticsSummary {
	summary := OperatorDiagnosticsSummary{
		Health:              "ok",
		ServiceCounts:       map[string]int{},
		QueueDepth:          overview.Queue.Depth,
		FleetNodeCount:      len(overview.FleetNodes),
		RecentInstallAudits: len(overview.InstallAudits),
		RecentAuthAuditLogs: len(overview.AuditLogs),
	}
	for _, service := range overview.Services {
		summary.ServiceCounts[service.Status]++
		switch service.Status {
		case "error":
			summary.Health = "page"
			summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "service "+service.Name+" is reporting error")
		case "warn":
			if summary.Health == "ok" {
				summary.Health = "warn"
			}
			summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "service "+service.Name+" is reporting warn")
		}
	}
	if summary.QueueDepth > 100 {
		summary.Health = "page"
		summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "async queue depth is above page threshold")
	} else if summary.QueueDepth > 20 && summary.Health == "ok" {
		summary.Health = "warn"
		summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "async queue depth is above warning threshold")
	}
	for _, backfill := range overview.Backfills {
		if backfill.FailedItems > 0 {
			summary.FailedBackfills++
		}
	}
	if summary.FailedBackfills > 0 && summary.Health == "ok" {
		summary.Health = "warn"
		summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "one or more backfills have failed items")
	}
	for _, outcome := range overview.RetentionOutcomes {
		if !outcome.BlobPresent {
			summary.MissingRetentionBlobs++
		}
	}
	if summary.MissingRetentionBlobs > 0 && summary.Health == "ok" {
		summary.Health = "warn"
		summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "one or more retention outcomes are missing blobs")
	}
	for _, item := range overview.BridgeFreshness {
		if item.Pending {
			summary.PendingBridgeProjections++
		}
		if item.LastError != "" {
			summary.BridgeProjectionErrors++
		}
	}
	if summary.BridgeProjectionErrors > 0 {
		summary.Health = "page"
		summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "bridge projection has recorded errors")
	} else if summary.PendingBridgeProjections > 0 && summary.Health == "ok" {
		summary.Health = "warn"
		summary.RecommendedInvestigation = append(summary.RecommendedInvestigation, "bridge projection has pending work")
	}
	if len(summary.ServiceCounts) == 0 {
		summary.ServiceCounts = nil
	}
	return summary
}
