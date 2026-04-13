package selfhostedops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/postgrescontrol"
	"urgentry/internal/store"
)

type OperatorActionReceipt struct {
	OrganizationID string    `json:"organizationId,omitempty"`
	ProjectID      string    `json:"projectId,omitempty"`
	Action         string    `json:"action"`
	Status         string    `json:"status"`
	Source         string    `json:"source"`
	Actor          string    `json:"actor"`
	Detail         string    `json:"detail,omitempty"`
	MetadataJSON   string    `json:"metadataJson,omitempty"`
	RecordedAt     time.Time `json:"recordedAt"`
}

func RecordOperatorAction(ctx context.Context, controlDSN string, record store.OperatorAuditRecord) (*OperatorActionReceipt, error) {
	audits, closeDB, err := openOperatorAuditStore(ctx, controlDSN)
	if err != nil {
		return nil, err
	}
	defer closeDB()

	record, err = normalizeOperatorAuditRecord(record)
	if err != nil {
		return nil, err
	}
	recordedAt := time.Now().UTC()
	if err := audits.Record(ctx, record); err != nil {
		return nil, err
	}
	return &OperatorActionReceipt{
		OrganizationID: record.OrganizationID,
		ProjectID:      record.ProjectID,
		Action:         record.Action,
		Status:         record.Status,
		Source:         record.Source,
		Actor:          record.Actor,
		Detail:         record.Detail,
		MetadataJSON:   record.MetadataJSON,
		RecordedAt:     recordedAt,
	}, nil
}

func openOperatorStores(ctx context.Context, controlDSN string) (store.LifecycleStore, store.OperatorAuditStore, func(), error) {
	db, err := openPing(ctx, controlDSN)
	if err != nil {
		return nil, nil, nil, err
	}
	return postgrescontrol.NewLifecycleStore(db), postgrescontrol.NewOperatorAuditStore(db), func() { _ = db.Close() }, nil
}

func openOperatorAuditStore(ctx context.Context, controlDSN string) (store.OperatorAuditStore, func(), error) {
	_, audits, closeDB, err := openOperatorStores(ctx, controlDSN)
	if err != nil {
		return nil, nil, err
	}
	return audits, closeDB, nil
}

func normalizeOperatorAuditRecord(record store.OperatorAuditRecord) (store.OperatorAuditRecord, error) {
	record.OrganizationID = strings.TrimSpace(record.OrganizationID)
	record.ProjectID = strings.TrimSpace(record.ProjectID)
	record.Action = strings.TrimSpace(record.Action)
	record.Status = strings.TrimSpace(record.Status)
	record.Source = strings.TrimSpace(record.Source)
	record.Actor = strings.TrimSpace(record.Actor)
	record.Detail = strings.TrimSpace(record.Detail)
	record.MetadataJSON = strings.TrimSpace(record.MetadataJSON)

	if record.Action == "" {
		return record, fmt.Errorf("operator audit action is required")
	}
	if record.Status == "" {
		record.Status = "succeeded"
	}
	if record.Source == "" {
		record.Source = "cli"
	}
	if record.Actor == "" {
		record.Actor = "system"
	}
	if record.MetadataJSON == "" {
		record.MetadataJSON = "{}"
	}
	if !json.Valid([]byte(record.MetadataJSON)) {
		return record, fmt.Errorf("operator audit metadata must be valid JSON")
	}
	return record, nil
}
