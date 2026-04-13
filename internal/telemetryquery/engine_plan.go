package telemetryquery

import (
	"fmt"
	"slices"
)

type EngineTier string

const (
	EngineTierBridgePostgres    EngineTier = "bridge_postgres"
	EngineTierBridgeTimescale   EngineTier = "bridge_timescale"
	EngineTierAnalyticsColumnar EngineTier = "analytics_columnar"
)

type GraduationTrigger struct {
	Name      string `json:"name"`
	Threshold string `json:"threshold"`
	Reason    string `json:"reason"`
}

type EngineCapability struct {
	Tier          EngineTier `json:"tier"`
	Backend       string     `json:"backend,omitempty"`
	Supported     bool       `json:"supported"`
	Reason        string     `json:"reason"`
	RequiredProof []string   `json:"requiredProof,omitempty"`
}

type EnginePlan struct {
	DefaultTier      EngineTier          `json:"defaultTier"`
	OptionalTiers    []EngineTier        `json:"optionalTiers"`
	GraduationTarget EngineTier          `json:"graduationTarget"`
	Triggers         []GraduationTrigger `json:"triggers"`
	Capabilities     []EngineCapability  `json:"capabilities"`
}

func DefaultEnginePlan() EnginePlan {
	return EnginePlan{
		DefaultTier:      EngineTierBridgePostgres,
		OptionalTiers:    []EngineTier{EngineTierBridgeTimescale},
		GraduationTarget: EngineTierAnalyticsColumnar,
		Capabilities: []EngineCapability{
			{
				Tier:      EngineTierBridgePostgres,
				Backend:   "postgres",
				Supported: true,
				Reason:    "default serious self-hosted telemetry bridge",
			},
			{
				Tier:      EngineTierBridgeTimescale,
				Backend:   "timescale",
				Supported: true,
				Reason:    "optional bridge backend that preserves the same logical table contract",
			},
			{
				Tier:      EngineTierAnalyticsColumnar,
				Supported: false,
				Reason:    "graduation target is intentionally not implemented until the bridge triggers are proven in production-like evidence",
				RequiredProof: []string{
					"selected engine and migration path",
					"query executor contract for logs, discover, traces, replay, and profiles",
					"backfill and rollback plan from the bridge tables",
					"benchmark showing bridge saturation that normal tuning cannot fix",
				},
			},
		},
		Triggers: []GraduationTrigger{
			{
				Name:      "retention growth",
				Threshold: "operators need query-serving telemetry retention well beyond the bridge comfort window",
				Reason:    "long retention changes storage and compaction cost more than bridge tuning can hide",
			},
			{
				Name:      "query saturation",
				Threshold: "interactive logs, discover, traces, replay, and profile reads stay slow after index and guard tuning",
				Reason:    "the bridge stops being predictable once mixed workloads keep colliding",
			},
			{
				Name:      "ingest and rebuild overlap",
				Threshold: "projector lag and rebuild work stay high during normal ingest",
				Reason:    "query-serving facts need their own engine once rebuild work crowds live reads",
			},
			{
				Name:      "operator cost pressure",
				Threshold: "operators have to overprovision Postgres just to keep telemetry queries interactive",
				Reason:    "that is the point where the bridge is solving the wrong problem",
			},
		},
	}
}

func (p EnginePlan) Validate() error {
	if p.DefaultTier == "" {
		return fmt.Errorf("default tier is required")
	}
	if p.GraduationTarget == "" {
		return fmt.Errorf("graduation target is required")
	}
	if len(p.OptionalTiers) == 0 {
		return fmt.Errorf("optional tiers must not be empty")
	}
	if len(p.Triggers) == 0 {
		return fmt.Errorf("graduation triggers must not be empty")
	}
	if len(p.Capabilities) == 0 {
		return fmt.Errorf("engine capabilities must not be empty")
	}
	for _, trigger := range p.Triggers {
		if trigger.Name == "" || trigger.Threshold == "" || trigger.Reason == "" {
			return fmt.Errorf("graduation triggers must be complete")
		}
	}
	requiredTiers := []EngineTier{p.DefaultTier, p.GraduationTarget}
	requiredTiers = append(requiredTiers, p.OptionalTiers...)
	for _, tier := range requiredTiers {
		capability, ok := p.CapabilityForTier(tier)
		if !ok {
			return fmt.Errorf("missing engine capability for %q", tier)
		}
		if capability.Reason == "" {
			return fmt.Errorf("engine capability %q must define a reason", tier)
		}
		if !capability.Supported && len(capability.RequiredProof) == 0 {
			return fmt.Errorf("unsupported engine capability %q must define required proof", tier)
		}
	}
	return nil
}

func (p EnginePlan) CapabilityForTier(tier EngineTier) (EngineCapability, bool) {
	for _, capability := range p.Capabilities {
		if capability.Tier == tier {
			return capability, true
		}
	}
	return EngineCapability{}, false
}

func (p EnginePlan) SupportedTiers() []EngineTier {
	out := []EngineTier{}
	for _, capability := range p.Capabilities {
		if capability.Supported && !slices.Contains(out, capability.Tier) {
			out = append(out, capability.Tier)
		}
	}
	return out
}
