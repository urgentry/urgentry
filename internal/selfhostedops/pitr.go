package selfhostedops

import "fmt"

type PostgresRecoverySurface string

const (
	PostgresRecoverySurfaceControl   PostgresRecoverySurface = "control"
	PostgresRecoverySurfaceTelemetry PostgresRecoverySurface = "telemetry"
)

type PITRRequirement struct {
	Surface             PostgresRecoverySurface `json:"surface"`
	RequiresWALArchive  bool                    `json:"requiresWalArchive"`
	BaseBackupInterval  string                  `json:"baseBackupInterval"`
	RecoveryTargetTypes []string                `json:"recoveryTargetTypes"`
}

type PITRContract struct {
	Requirements []PITRRequirement `json:"requirements"`
	Workflow     []string          `json:"workflow"`
	Boundaries   []string          `json:"boundaries"`
}

func DefaultPITRContract() PITRContract {
	return PITRContract{
		Requirements: []PITRRequirement{
			{
				Surface:             PostgresRecoverySurfaceControl,
				RequiresWALArchive:  true,
				BaseBackupInterval:  "24h",
				RecoveryTargetTypes: []string{"timestamp", "named restore point"},
			},
			{
				Surface:             PostgresRecoverySurfaceTelemetry,
				RequiresWALArchive:  true,
				BaseBackupInterval:  "24h",
				RecoveryTargetTypes: []string{"timestamp", "named restore point"},
			},
		},
		Workflow: []string{
			"capture a fresh backup manifest and current self-hosted status before the incident response starts",
			"choose one recovery target timestamp or restore point for control and telemetry before restoring either database",
			"restore base backup plus WAL to a staging location first and prove target visibility before cutting traffic",
			"run self-hosted preflight and status against the staged restore before promoting it",
			"replay operator smoke checks after promotion and record the actual recovery point used",
		},
		Boundaries: []string{
			"control and telemetry must recover to the same operator-approved point in time",
			"blob, cache, and async systems may need separate drift checks after Postgres recovery",
			"PITR does not replace backup verification or rollback-plan capture",
		},
	}
}

func (c PITRContract) Validate() error {
	if len(c.Requirements) != 2 {
		return fmt.Errorf("expected 2 pitr requirements, got %d", len(c.Requirements))
	}
	if len(c.Workflow) == 0 {
		return fmt.Errorf("pitr workflow must not be empty")
	}
	if len(c.Boundaries) == 0 {
		return fmt.Errorf("pitr boundaries must not be empty")
	}
	for _, item := range c.Requirements {
		if item.Surface == "" {
			return fmt.Errorf("recovery surface is required")
		}
		if !item.RequiresWALArchive {
			return fmt.Errorf("surface %q must require wal archiving", item.Surface)
		}
		if item.BaseBackupInterval == "" {
			return fmt.Errorf("surface %q must define a base backup interval", item.Surface)
		}
		if len(item.RecoveryTargetTypes) == 0 {
			return fmt.Errorf("surface %q must define recovery target types", item.Surface)
		}
	}
	return nil
}
