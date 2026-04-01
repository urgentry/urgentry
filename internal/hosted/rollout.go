package hosted

import (
	"fmt"
	"slices"
)

type ReleaseArtifact string

const (
	ArtifactAppVersion             ReleaseArtifact = "app_version"
	ArtifactControlSchemaTarget    ReleaseArtifact = "control_schema_target"
	ArtifactTelemetrySchemaTarget  ReleaseArtifact = "telemetry_schema_target"
	ArtifactConfigBundleVersion    ReleaseArtifact = "config_bundle_version"
	ArtifactSecretBundleVersion    ReleaseArtifact = "secret_bundle_version"
	ArtifactRollbackArtifactBundle ReleaseArtifact = "rollback_artifact_bundle"
)

var artifactOrder = []ReleaseArtifact{
	ArtifactAppVersion,
	ArtifactControlSchemaTarget,
	ArtifactTelemetrySchemaTarget,
	ArtifactConfigBundleVersion,
	ArtifactSecretBundleVersion,
	ArtifactRollbackArtifactBundle,
}

type RolloutStage string

const (
	RolloutStageFreezeRelease    RolloutStage = "freeze_release"
	RolloutStagePreflight        RolloutStage = "preflight"
	RolloutStageCaptureRollback  RolloutStage = "capture_rollback"
	RolloutStageExpandSchemas    RolloutStage = "expand_schemas"
	RolloutStageCanaryCell       RolloutStage = "canary_cell"
	RolloutStagePrimaryRegion    RolloutStage = "primary_region"
	RolloutStageRemainingRegions RolloutStage = "remaining_regions"
)

var rolloutStageOrder = []RolloutStage{
	RolloutStageFreezeRelease,
	RolloutStagePreflight,
	RolloutStageCaptureRollback,
	RolloutStageExpandSchemas,
	RolloutStageCanaryCell,
	RolloutStagePrimaryRegion,
	RolloutStageRemainingRegions,
}

type RolloutScope string

const (
	RolloutScopeEnvironment RolloutScope = "environment"
	RolloutScopeCell        RolloutScope = "cell"
	RolloutScopeRegion      RolloutScope = "region"
	RolloutScopeFleet       RolloutScope = "fleet"
)

type TrafficMode string

const (
	TrafficModeReadWrite   TrafficMode = "read_write"
	TrafficModeDrainWrites TrafficMode = "drain_writes"
	TrafficModeReadOnly    TrafficMode = "read_only"
)

type Gate string

const (
	GatePreflightGreen        Gate = "preflight_green"
	GateMigrationTarget       Gate = "migration_target_reached"
	GateAppHealthGreen        Gate = "app_health_green"
	GateSmokeGreen            Gate = "smoke_green"
	GateAsyncBacklogStable    Gate = "async_backlog_stable"
	GateBridgeLagWithinBudget Gate = "bridge_lag_within_budget"
	GateErrorBudgetHealthy    Gate = "error_budget_healthy"
	GateUsageWritesHealthy    Gate = "usage_and_quota_writes_healthy"
)

var gateOrder = []Gate{
	GatePreflightGreen,
	GateMigrationTarget,
	GateAppHealthGreen,
	GateSmokeGreen,
	GateAsyncBacklogStable,
	GateBridgeLagWithinBudget,
	GateErrorBudgetHealthy,
	GateUsageWritesHealthy,
}

type RollbackClass string

const (
	RollbackClassImageOrConfig RollbackClass = "image_or_config"
	RollbackClassCompatibleApp RollbackClass = "compatible_app"
	RollbackClassSchemaRestore RollbackClass = "schema_restore"
)

type RolloutStep struct {
	Stage           RolloutStage    `json:"stage"`
	Scope           RolloutScope    `json:"scope"`
	TrafficMode     TrafficMode     `json:"trafficMode"`
	RequiredGates   []Gate          `json:"requiredGates"`
	AllowedRollback []RollbackClass `json:"allowedRollback"`
	Notes           []string        `json:"notes,omitempty"`
}

type RolloutContract struct {
	ReleaseArtifacts []ReleaseArtifact `json:"releaseArtifacts"`
	Steps            []RolloutStep     `json:"steps"`
	RequiredSignals  []string          `json:"requiredSignals"`
	RequiredDrills   []string          `json:"requiredDrills"`
}

func DefaultRolloutContract() RolloutContract {
	return RolloutContract{
		ReleaseArtifacts: append([]ReleaseArtifact(nil), artifactOrder...),
		Steps: []RolloutStep{
			{
				Stage:           RolloutStageFreezeRelease,
				Scope:           RolloutScopeEnvironment,
				TrafficMode:     TrafficModeReadWrite,
				AllowedRollback: []RollbackClass{RollbackClassImageOrConfig},
				Notes: []string{
					"Freeze the app, schema, config, secret, and rollback bundle versions together.",
				},
			},
			{
				Stage:           RolloutStagePreflight,
				Scope:           RolloutScopeEnvironment,
				TrafficMode:     TrafficModeReadWrite,
				RequiredGates:   []Gate{GatePreflightGreen},
				AllowedRollback: []RollbackClass{RollbackClassImageOrConfig},
				Notes: []string{
					"Stop before touching a cell if config, secrets, or schema status is unhealthy.",
				},
			},
			{
				Stage:           RolloutStageCaptureRollback,
				Scope:           RolloutScopeEnvironment,
				TrafficMode:     TrafficModeReadWrite,
				RequiredGates:   []Gate{GatePreflightGreen},
				AllowedRollback: []RollbackClass{RollbackClassImageOrConfig, RollbackClassSchemaRestore},
				Notes: []string{
					"Capture the current release, schema, config, secret, and backup proof before the first production change.",
				},
			},
			{
				Stage:           RolloutStageExpandSchemas,
				Scope:           RolloutScopeEnvironment,
				TrafficMode:     TrafficModeDrainWrites,
				RequiredGates:   []Gate{GatePreflightGreen, GateMigrationTarget},
				AllowedRollback: []RollbackClass{RollbackClassCompatibleApp, RollbackClassSchemaRestore},
				Notes: []string{
					"Expand before contract. Destructive cleanup never ships in the same release.",
				},
			},
			{
				Stage:       RolloutStageCanaryCell,
				Scope:       RolloutScopeCell,
				TrafficMode: TrafficModeDrainWrites,
				RequiredGates: []Gate{
					GateMigrationTarget,
					GateAppHealthGreen,
					GateSmokeGreen,
					GateAsyncBacklogStable,
					GateBridgeLagWithinBudget,
					GateErrorBudgetHealthy,
					GateUsageWritesHealthy,
				},
				AllowedRollback: []RollbackClass{RollbackClassImageOrConfig, RollbackClassCompatibleApp},
				Notes: []string{
					"Start with one low-risk cell and prove login, ingest, issue creation, query, replay, profile, quota, and usage writes.",
				},
			},
			{
				Stage:       RolloutStagePrimaryRegion,
				Scope:       RolloutScopeRegion,
				TrafficMode: TrafficModeDrainWrites,
				RequiredGates: []Gate{
					GateAppHealthGreen,
					GateSmokeGreen,
					GateAsyncBacklogStable,
					GateBridgeLagWithinBudget,
					GateErrorBudgetHealthy,
					GateUsageWritesHealthy,
				},
				AllowedRollback: []RollbackClass{RollbackClassImageOrConfig, RollbackClassCompatibleApp},
				Notes: []string{
					"Roll the rest of the first region only after the canary cell holds steady.",
				},
			},
			{
				Stage:       RolloutStageRemainingRegions,
				Scope:       RolloutScopeFleet,
				TrafficMode: TrafficModeDrainWrites,
				RequiredGates: []Gate{
					GateAppHealthGreen,
					GateSmokeGreen,
					GateAsyncBacklogStable,
					GateBridgeLagWithinBudget,
					GateErrorBudgetHealthy,
					GateUsageWritesHealthy,
				},
				AllowedRollback: []RollbackClass{RollbackClassImageOrConfig, RollbackClassCompatibleApp, RollbackClassSchemaRestore},
				Notes: []string{
					"Promote one region at a time. Do not fan out globally before the prior region is green.",
				},
			},
		},
		RequiredSignals: []string{
			"release version by cell",
			"config bundle version by cell",
			"secret bundle version by cell",
			"control and telemetry schema version by environment",
			"request error rate and latency by cell",
			"ingest acceptance rate by cell",
			"worker backlog and scheduler lease health",
			"bridge lag and rebuild status",
		},
		RequiredDrills: []string{
			"bad canary image rollback",
			"config-only rollback",
			"secret bundle rollback",
			"region drain and reopen",
			"scheduler handoff during rollout",
			"worker backlog drain during rollback",
			"schema expand with app rollback",
			"schema rollback from captured artifact bundle",
		},
	}
}

func (c RolloutContract) Validate() error {
	if len(c.ReleaseArtifacts) != len(artifactOrder) {
		return fmt.Errorf("expected %d release artifacts, got %d", len(artifactOrder), len(c.ReleaseArtifacts))
	}
	for _, artifact := range artifactOrder {
		if !slices.Contains(c.ReleaseArtifacts, artifact) {
			return fmt.Errorf("missing release artifact %q", artifact)
		}
	}
	if len(c.Steps) != len(rolloutStageOrder) {
		return fmt.Errorf("expected %d rollout steps, got %d", len(rolloutStageOrder), len(c.Steps))
	}
	for i, stage := range rolloutStageOrder {
		step := c.Steps[i]
		if step.Stage != stage {
			return fmt.Errorf("step %d stage = %q, want %q", i, step.Stage, stage)
		}
		if step.Scope == "" {
			return fmt.Errorf("%s scope is required", step.Stage)
		}
		if step.TrafficMode == "" {
			return fmt.Errorf("%s traffic mode is required", step.Stage)
		}
		for _, gate := range step.RequiredGates {
			if !slices.Contains(gateOrder, gate) {
				return fmt.Errorf("%s has unknown gate %q", step.Stage, gate)
			}
		}
		if len(step.AllowedRollback) == 0 {
			return fmt.Errorf("%s must allow at least one rollback class", step.Stage)
		}
	}
	if len(c.RequiredSignals) == 0 {
		return fmt.Errorf("required signals are empty")
	}
	if len(c.RequiredDrills) == 0 {
		return fmt.Errorf("required drills are empty")
	}
	return nil
}
