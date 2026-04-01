package telemetryquery

import "fmt"

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

type EnginePlan struct {
	DefaultTier      EngineTier          `json:"defaultTier"`
	OptionalTiers    []EngineTier        `json:"optionalTiers"`
	GraduationTarget EngineTier          `json:"graduationTarget"`
	Triggers         []GraduationTrigger `json:"triggers"`
}

func DefaultEnginePlan() EnginePlan {
	return EnginePlan{
		DefaultTier:      EngineTierBridgePostgres,
		OptionalTiers:    []EngineTier{EngineTierBridgeTimescale},
		GraduationTarget: EngineTierAnalyticsColumnar,
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
	for _, trigger := range p.Triggers {
		if trigger.Name == "" || trigger.Threshold == "" || trigger.Reason == "" {
			return fmt.Errorf("graduation triggers must be complete")
		}
	}
	return nil
}
