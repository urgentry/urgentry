package telemetrybridge

import "fmt"

type FanoutMode string

const (
	FanoutModeOutbox FanoutMode = "outbox"
	FanoutModeCDC    FanoutMode = "cdc"
)

type DeliveryGuarantee string

const (
	DeliveryGuaranteeAtLeastOnce DeliveryGuarantee = "at_least_once"
)

type FanoutContract struct {
	Mode                FanoutMode        `json:"mode"`
	DeliveryGuarantee   DeliveryGuarantee `json:"deliveryGuarantee"`
	LagBudgetSeconds    int               `json:"lagBudgetSeconds"`
	IdempotencyKey      string            `json:"idempotencyKey"`
	RequiredEvents      []string          `json:"requiredEvents"`
	RebuildHandoffSteps []string          `json:"rebuildHandoffSteps"`
}

func DefaultFanoutContract() FanoutContract {
	return FanoutContract{
		Mode:              FanoutModeOutbox,
		DeliveryGuarantee: DeliveryGuaranteeAtLeastOnce,
		LagBudgetSeconds:  120,
		IdempotencyKey:    "organization_id + project_id + surface + logical_event_id",
		RequiredEvents: []string{
			"event accepted",
			"transaction accepted",
			"log accepted",
			"replay indexed",
			"profile materialized",
			"release mutated",
			"retention archive restored",
		},
		RebuildHandoffSteps: []string{
			"mark the affected family stale",
			"enqueue a resumable rebuild run",
			"keep the idempotency key stable across retries",
			"clear stale state only after the rebuild catches up",
		},
	}
}

func (c FanoutContract) Validate() error {
	if c.Mode == "" {
		return fmt.Errorf("fanout mode is required")
	}
	if c.DeliveryGuarantee == "" {
		return fmt.Errorf("delivery guarantee is required")
	}
	if c.LagBudgetSeconds <= 0 {
		return fmt.Errorf("lag budget must be positive")
	}
	if c.IdempotencyKey == "" {
		return fmt.Errorf("idempotency key is required")
	}
	if len(c.RequiredEvents) == 0 {
		return fmt.Errorf("required events must not be empty")
	}
	if len(c.RebuildHandoffSteps) == 0 {
		return fmt.Errorf("rebuild handoff steps must not be empty")
	}
	return nil
}
