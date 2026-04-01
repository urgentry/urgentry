package hosted

import (
	"fmt"
	"slices"
)

type Plan string

const (
	PlanStarter    Plan = "starter"
	PlanTeam       Plan = "team"
	PlanBusiness   Plan = "business"
	PlanEnterprise Plan = "enterprise"
)

var planOrder = []Plan{
	PlanStarter,
	PlanTeam,
	PlanBusiness,
	PlanEnterprise,
}

func Plans() []Plan {
	out := make([]Plan, len(planOrder))
	copy(out, planOrder)
	return out
}

type SupportTier string

const (
	SupportTierCommunity SupportTier = "community"
	SupportTierStandard  SupportTier = "standard"
	SupportTierPriority  SupportTier = "priority"
	SupportTierDedicated SupportTier = "dedicated"
)

type OverageMode string

const (
	OverageModeBlock          OverageMode = "block"
	OverageModeMetered        OverageMode = "metered"
	OverageModeGraceThenBlock OverageMode = "grace_then_block"
	OverageModeContract       OverageMode = "contract"
)

type PostTrialState string

const (
	PostTrialStateReadOnly PostTrialState = "read_only"
	PostTrialStateSuspend  PostTrialState = "suspended"
)

type UsageDimension string

const (
	UsageMembers               UsageDimension = "members"
	UsageProjects              UsageDimension = "projects"
	UsageMonthlyEvents         UsageDimension = "monthly_events"
	UsageDailyQueryUnits       UsageDimension = "daily_query_units"
	UsageMonthlyReplaySessions UsageDimension = "monthly_replay_sessions"
	UsageMonthlyProfileSamples UsageDimension = "monthly_profile_samples"
	UsageStorageGiB            UsageDimension = "storage_gib"
	UsageMonthlyExportJobs     UsageDimension = "monthly_export_jobs"
	UsageMaxRetentionDays      UsageDimension = "max_retention_days"
)

var usageDimensionOrder = []UsageDimension{
	UsageMembers,
	UsageProjects,
	UsageMonthlyEvents,
	UsageDailyQueryUnits,
	UsageMonthlyReplaySessions,
	UsageMonthlyProfileSamples,
	UsageStorageGiB,
	UsageMonthlyExportJobs,
	UsageMaxRetentionDays,
}

func UsageDimensions() []UsageDimension {
	out := make([]UsageDimension, len(usageDimensionOrder))
	copy(out, usageDimensionOrder)
	return out
}

type Feature string

const (
	FeatureAuditExport         Feature = "audit_export"
	FeaturePrioritySupport     Feature = "priority_support"
	FeatureRegionPinning       Feature = "region_pinning"
	FeatureCustomSSO           Feature = "custom_sso"
	FeaturePrivateConnectivity Feature = "private_connectivity"
)

var featureOrder = []Feature{
	FeatureAuditExport,
	FeaturePrioritySupport,
	FeatureRegionPinning,
	FeatureCustomSSO,
	FeaturePrivateConnectivity,
}

func Features() []Feature {
	out := make([]Feature, len(featureOrder))
	copy(out, featureOrder)
	return out
}

type Limit struct {
	Included     int64       `json:"included"`
	OverageMode  OverageMode `json:"overageMode"`
	GracePercent int         `json:"gracePercent,omitempty"`
	GraceDays    int         `json:"graceDays,omitempty"`
}

type TrialBehavior struct {
	Plan                 Plan           `json:"plan"`
	Days                 int            `json:"days"`
	RequirePaymentMethod bool           `json:"requirePaymentMethod"`
	AllowOnePerAccount   bool           `json:"allowOnePerAccount"`
	GraceDays            int            `json:"graceDays"`
	PostTrialState       PostTrialState `json:"postTrialState"`
}

type PlanSpec struct {
	Plan        Plan                     `json:"plan"`
	DisplayName string                   `json:"displayName"`
	SupportTier SupportTier              `json:"supportTier"`
	Limits      map[UsageDimension]Limit `json:"limits"`
	Features    map[Feature]bool         `json:"features"`
	Notes       []string                 `json:"notes,omitempty"`
}

type Catalog struct {
	Trial TrialBehavior     `json:"trial"`
	Plans map[Plan]PlanSpec `json:"plans"`
}

func DefaultCatalog() Catalog {
	return Catalog{
		Trial: TrialBehavior{
			Plan:                 PlanTeam,
			Days:                 14,
			RequirePaymentMethod: false,
			AllowOnePerAccount:   true,
			GraceDays:            7,
			PostTrialState:       PostTrialStateReadOnly,
		},
		Plans: map[Plan]PlanSpec{
			PlanStarter: {
				Plan:        PlanStarter,
				DisplayName: "Starter",
				SupportTier: SupportTierCommunity,
				Limits: limitSet(
					10,
					20,
					metered(1_000_000),
					grace(25_000, 20, 3),
					grace(2_500, 10, 3),
					grace(25_000, 10, 3),
					grace(50, 10, 3),
					block(30),
					block(30),
				),
				Features: featureSet(false, false, false, false, false),
				Notes: []string{
					"Starter is the default paid landing tier after a trial.",
					"Starter keeps analytics and export ceilings hard enough to protect shared infrastructure.",
				},
			},
			PlanTeam: {
				Plan:        PlanTeam,
				DisplayName: "Team",
				SupportTier: SupportTierStandard,
				Limits: limitSet(
					25,
					75,
					metered(10_000_000),
					metered(150_000),
					grace(15_000, 15, 7),
					grace(150_000, 15, 7),
					grace(250, 15, 7),
					block(250),
					block(90),
				),
				Features: featureSet(true, false, false, false, false),
				Notes: []string{
					"Team is the default trial target.",
					"Team adds audit export before it adds region or network customization.",
				},
			},
			PlanBusiness: {
				Plan:        PlanBusiness,
				DisplayName: "Business",
				SupportTier: SupportTierPriority,
				Limits: limitSet(
					100,
					300,
					metered(100_000_000),
					metered(750_000),
					metered(75_000),
					metered(750_000),
					grace(2_000, 20, 14),
					block(2_000),
					block(365),
				),
				Features: featureSet(true, true, true, false, false),
				Notes: []string{
					"Business is the first hosted tier that can pin a tenant to a region.",
					"Business keeps identity and connectivity simple enough for self-serve sales plus ops support.",
				},
			},
			PlanEnterprise: {
				Plan:        PlanEnterprise,
				DisplayName: "Enterprise",
				SupportTier: SupportTierDedicated,
				Limits: limitSet(
					500,
					1_000,
					contract(1_000_000_000),
					contract(3_000_000),
					contract(300_000),
					contract(3_000_000),
					contract(10_000),
					contract(10_000),
					block(730),
				),
				Features: featureSet(true, true, true, true, true),
				Notes: []string{
					"Enterprise keeps headline defaults for capacity planning, but contracts can override them.",
					"Enterprise is the only tier that enables custom SSO and private connectivity.",
				},
			},
		},
	}
}

func (c Catalog) Lookup(plan Plan) (PlanSpec, bool) {
	spec, ok := c.Plans[plan]
	return spec, ok
}

func (c Catalog) Validate() error {
	if _, ok := c.Plans[c.Trial.Plan]; !ok {
		return fmt.Errorf("trial plan %q is not defined", c.Trial.Plan)
	}
	if c.Trial.Days <= 0 {
		return fmt.Errorf("trial days must be positive")
	}
	if c.Trial.GraceDays < 0 {
		return fmt.Errorf("trial grace days must be non-negative")
	}
	for _, plan := range planOrder {
		spec, ok := c.Plans[plan]
		if !ok {
			return fmt.Errorf("missing plan %q", plan)
		}
		if err := validatePlanSpec(spec); err != nil {
			return fmt.Errorf("%s: %w", plan, err)
		}
	}
	for plan := range c.Plans {
		if !slices.Contains(planOrder, plan) {
			return fmt.Errorf("unknown plan %q", plan)
		}
	}
	return nil
}

func validatePlanSpec(spec PlanSpec) error {
	if spec.DisplayName == "" {
		return fmt.Errorf("display name is required")
	}
	if spec.SupportTier == "" {
		return fmt.Errorf("support tier is required")
	}
	for _, dimension := range usageDimensionOrder {
		limit, ok := spec.Limits[dimension]
		if !ok {
			return fmt.Errorf("missing limit for %s", dimension)
		}
		if limit.Included <= 0 {
			return fmt.Errorf("limit for %s must be positive", dimension)
		}
		switch limit.OverageMode {
		case OverageModeBlock, OverageModeMetered, OverageModeContract:
			if limit.GracePercent != 0 || limit.GraceDays != 0 {
				return fmt.Errorf("%s cannot define grace for %s", limit.OverageMode, dimension)
			}
		case OverageModeGraceThenBlock:
			if limit.GracePercent <= 0 || limit.GraceDays <= 0 {
				return fmt.Errorf("grace_then_block requires positive grace settings for %s", dimension)
			}
		default:
			return fmt.Errorf("unknown overage mode %q for %s", limit.OverageMode, dimension)
		}
	}
	for _, feature := range featureOrder {
		if _, ok := spec.Features[feature]; !ok {
			return fmt.Errorf("missing feature flag for %s", feature)
		}
	}
	for feature := range spec.Features {
		if !slices.Contains(featureOrder, feature) {
			return fmt.Errorf("unknown feature %q", feature)
		}
	}
	return nil
}

func limitSet(
	members int64,
	projects int64,
	events Limit,
	queryUnits Limit,
	replays Limit,
	profiles Limit,
	storage Limit,
	exports Limit,
	retention Limit,
) map[UsageDimension]Limit {
	return map[UsageDimension]Limit{
		UsageMembers:               block(members),
		UsageProjects:              block(projects),
		UsageMonthlyEvents:         events,
		UsageDailyQueryUnits:       queryUnits,
		UsageMonthlyReplaySessions: replays,
		UsageMonthlyProfileSamples: profiles,
		UsageStorageGiB:            storage,
		UsageMonthlyExportJobs:     exports,
		UsageMaxRetentionDays:      retention,
	}
}

func featureSet(auditExport, prioritySupport, regionPinning, customSSO, privateConnectivity bool) map[Feature]bool {
	return map[Feature]bool{
		FeatureAuditExport:         auditExport,
		FeaturePrioritySupport:     prioritySupport,
		FeatureRegionPinning:       regionPinning,
		FeatureCustomSSO:           customSSO,
		FeaturePrivateConnectivity: privateConnectivity,
	}
}

func block(included int64) Limit {
	return Limit{Included: included, OverageMode: OverageModeBlock}
}

func metered(included int64) Limit {
	return Limit{Included: included, OverageMode: OverageModeMetered}
}

func contract(included int64) Limit {
	return Limit{Included: included, OverageMode: OverageModeContract}
}

func grace(included int64, percent, days int) Limit {
	return Limit{
		Included:     included,
		OverageMode:  OverageModeGraceThenBlock,
		GracePercent: percent,
		GraceDays:    days,
	}
}
