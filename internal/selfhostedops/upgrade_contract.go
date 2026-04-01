package selfhostedops

import "fmt"

type UpgradeComponent string

const (
	UpgradeComponentAppBundle       UpgradeComponent = "app_bundle"
	UpgradeComponentControlSchema   UpgradeComponent = "control_schema"
	UpgradeComponentTelemetrySchema UpgradeComponent = "telemetry_schema"
	UpgradeComponentWorker          UpgradeComponent = "worker"
	UpgradeComponentScheduler       UpgradeComponent = "scheduler"
)

type VersionSkewRule struct {
	Component         UpgradeComponent `json:"component"`
	MaxVersionsAhead  int              `json:"maxVersionsAhead"`
	MaxVersionsBehind int              `json:"maxVersionsBehind"`
	Notes             []string         `json:"notes"`
}

type UpgradeContract struct {
	Rules              []VersionSkewRule `json:"rules"`
	CanaryStages       []string          `json:"canaryStages"`
	RollbackSafeguards []string          `json:"rollbackSafeguards"`
}

func DefaultUpgradeContract() UpgradeContract {
	return UpgradeContract{
		Rules: []VersionSkewRule{
			{
				Component:         UpgradeComponentAppBundle,
				MaxVersionsAhead:  0,
				MaxVersionsBehind: 1,
				Notes: []string{
					"new app bundle may run against expanded schemas only",
					"contract cleanup never ships in the same upgrade wave",
				},
			},
			{
				Component:         UpgradeComponentControlSchema,
				MaxVersionsAhead:  1,
				MaxVersionsBehind: 0,
				Notes: []string{
					"control schema expands before app rollout",
				},
			},
			{
				Component:         UpgradeComponentTelemetrySchema,
				MaxVersionsAhead:  1,
				MaxVersionsBehind: 0,
				Notes: []string{
					"telemetry schema expands before query nodes move",
				},
			},
			{
				Component:         UpgradeComponentWorker,
				MaxVersionsAhead:  0,
				MaxVersionsBehind: 1,
				Notes: []string{
					"workers may lag one release during canary but not more",
				},
			},
			{
				Component:         UpgradeComponentScheduler,
				MaxVersionsAhead:  0,
				MaxVersionsBehind: 1,
				Notes: []string{
					"scheduler handoff must stay valid across one-release skew only",
				},
			},
		},
		CanaryStages: []string{
			"expand schemas",
			"roll one canary node",
			"verify queue, bridge, and query health",
			"roll the rest of the region",
			"run rollback proof before contract cleanup",
		},
		RollbackSafeguards: []string{
			"capture rollback artifacts before the first schema change",
			"allow app rollback after schema expand only while schemas stay backward compatible",
			"require explicit restore proof before any schema restore",
		},
	}
}

func (c UpgradeContract) Validate() error {
	if len(c.Rules) != 5 {
		return fmt.Errorf("expected 5 upgrade rules, got %d", len(c.Rules))
	}
	if len(c.CanaryStages) == 0 {
		return fmt.Errorf("canary stages must not be empty")
	}
	if len(c.RollbackSafeguards) == 0 {
		return fmt.Errorf("rollback safeguards must not be empty")
	}
	for _, rule := range c.Rules {
		if rule.Component == "" {
			return fmt.Errorf("upgrade component is required")
		}
		if rule.MaxVersionsAhead < 0 || rule.MaxVersionsBehind < 0 {
			return fmt.Errorf("version skew must be non-negative")
		}
		if len(rule.Notes) == 0 {
			return fmt.Errorf("component %q must define notes", rule.Component)
		}
	}
	return nil
}
