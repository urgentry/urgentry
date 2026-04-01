package selfhostedops

import (
	"fmt"
	"slices"
)

type RepairSurface string

const (
	RepairSurfaceBridgeLag RepairSurface = "bridge_lag"
	RepairSurfaceBackfills RepairSurface = "backfills"
	RepairSurfaceRetention RepairSurface = "retention"
	RepairSurfaceReplay    RepairSurface = "replay"
	RepairSurfaceProfile   RepairSurface = "profile"
	RepairSurfaceQuota     RepairSurface = "quota"
)

var repairSurfaceOrder = []RepairSurface{
	RepairSurfaceBridgeLag,
	RepairSurfaceBackfills,
	RepairSurfaceRetention,
	RepairSurfaceReplay,
	RepairSurfaceProfile,
	RepairSurfaceQuota,
}

type RepairAction string

const (
	RepairActionRestartBackfill   RepairAction = "restart_backfill"
	RepairActionResetBridgeCursor RepairAction = "reset_bridge_cursor"
	RepairActionRebuildReplay     RepairAction = "rebuild_replay"
	RepairActionRebuildProfile    RepairAction = "rebuild_profile"
	RepairActionRestoreBlob       RepairAction = "restore_blob"
	RepairActionClearQuotaWindow  RepairAction = "clear_quota_window"
)

type RepairSurfacePack struct {
	Surface    RepairSurface  `json:"surface"`
	Signals    []string       `json:"signals"`
	Actions    []RepairAction `json:"actions"`
	Safeguards []string       `json:"safeguards"`
}

type RepairPack struct {
	Surfaces []RepairSurfacePack `json:"surfaces"`
}

func DefaultRepairPack() RepairPack {
	return RepairPack{
		Surfaces: []RepairSurfacePack{
			{
				Surface: RepairSurfaceBridgeLag,
				Signals: []string{
					"per-family projector cursor age",
					"bridge lag by organization and surface",
					"last successful rebuild run",
				},
				Actions: []RepairAction{RepairActionRestartBackfill, RepairActionResetBridgeCursor},
				Safeguards: []string{
					"show current cursor before reset",
					"require org or surface scope for resets",
				},
			},
			{
				Surface: RepairSurfaceBackfills,
				Signals: []string{
					"stalled run age",
					"failed item count",
					"current worker lease holder",
				},
				Actions: []RepairAction{RepairActionRestartBackfill},
				Safeguards: []string{
					"deny restart for already-running jobs",
					"audit every restart and cancel action",
				},
			},
			{
				Surface: RepairSurfaceRetention,
				Signals: []string{
					"archive or restore drift by project",
					"missing blob count",
					"partially restored records",
				},
				Actions: []RepairAction{RepairActionRestoreBlob},
				Safeguards: []string{
					"require record id before blob restore",
					"show blob presence before mutating state",
				},
			},
			{
				Surface: RepairSurfaceReplay,
				Signals: []string{
					"replay manifest integrity",
					"timeline index freshness",
				},
				Actions: []RepairAction{RepairActionRebuildReplay},
				Safeguards: []string{
					"require project scope for replay rebuild",
					"audit rebuild requester and record id",
				},
			},
			{
				Surface: RepairSurfaceProfile,
				Signals: []string{
					"profile manifest integrity",
					"materialization lag",
				},
				Actions: []RepairAction{RepairActionRebuildProfile},
				Safeguards: []string{
					"require project scope for profile rebuild",
					"audit rebuild requester and record id",
				},
			},
			{
				Surface: RepairSurfaceQuota,
				Signals: []string{
					"quota deny spikes by org and workload",
					"stale quota windows",
					"current guard backend health",
				},
				Actions: []RepairAction{RepairActionClearQuotaWindow},
				Safeguards: []string{
					"require org and workload scope before clearing quota state",
					"show active window keys before deletion",
				},
			},
		},
	}
}

func (p RepairPack) Validate() error {
	if len(p.Surfaces) != len(repairSurfaceOrder) {
		return fmt.Errorf("expected %d repair surfaces, got %d", len(repairSurfaceOrder), len(p.Surfaces))
	}
	for _, surface := range repairSurfaceOrder {
		item, ok := p.lookupSurface(surface)
		if !ok {
			return fmt.Errorf("missing repair surface %q", surface)
		}
		if len(item.Signals) == 0 {
			return fmt.Errorf("surface %q must define signals", surface)
		}
		if len(item.Actions) == 0 {
			return fmt.Errorf("surface %q must define actions", surface)
		}
		if len(item.Safeguards) == 0 {
			return fmt.Errorf("surface %q must define safeguards", surface)
		}
		for _, action := range item.Actions {
			if !slices.Contains([]RepairAction{
				RepairActionRestartBackfill,
				RepairActionResetBridgeCursor,
				RepairActionRebuildReplay,
				RepairActionRebuildProfile,
				RepairActionRestoreBlob,
				RepairActionClearQuotaWindow,
			}, action) {
				return fmt.Errorf("surface %q has invalid action %q", surface, action)
			}
		}
	}
	return nil
}

func (p RepairPack) lookupSurface(surface RepairSurface) (RepairSurfacePack, bool) {
	for _, item := range p.Surfaces {
		if item.Surface == surface {
			return item, true
		}
	}
	return RepairSurfacePack{}, false
}
