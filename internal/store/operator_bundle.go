package store

import "time"

type OperatorDiagnosticsBundle struct {
	OrganizationSlug  string                     `json:"organizationSlug"`
	CapturedAt        time.Time                  `json:"capturedAt"`
	Install           *InstallState              `json:"install,omitempty"`
	Runtime           OperatorRuntime            `json:"runtime"`
	Services          []OperatorServiceStatus    `json:"services"`
	Queue             OperatorQueueStatus        `json:"queue"`
	Backfills         []OperatorBackfillStatus   `json:"backfills"`
	BridgeFreshness   []OperatorBridgeFreshness  `json:"bridgeFreshness,omitempty"`
	RetentionOutcomes []OperatorRetentionOutcome `json:"retentionOutcomes"`
	InstallAudits     []OperatorAuditEntry       `json:"installAudits"`
	AuditLogs         []AuditLogEntry            `json:"auditLogs"`
	Redactions        []string                   `json:"redactions"`
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
		Install:           install,
		Runtime:           overview.Runtime,
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
