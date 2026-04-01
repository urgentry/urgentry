package telemetrybridge

import (
	"fmt"
	"slices"
)

type ExportMode string

const (
	ExportModeSnapshot         ExportMode = "snapshot_export"
	ExportModeShadowDualWrite  ExportMode = "shadow_dual_write"
	ExportModeCutoverDualWrite ExportMode = "cutover_dual_write"
)

var exportModeOrder = []ExportMode{
	ExportModeSnapshot,
	ExportModeShadowDualWrite,
	ExportModeCutoverDualWrite,
}

type ExportSurface string

const (
	ExportSurfaceEvents  ExportSurface = "events"
	ExportSurfaceLogs    ExportSurface = "logs"
	ExportSurfaceTraces  ExportSurface = "traces"
	ExportSurfaceReplay  ExportSurface = "replay"
	ExportSurfaceProfile ExportSurface = "profile"
)

var exportSurfaceOrder = []ExportSurface{
	ExportSurfaceEvents,
	ExportSurfaceLogs,
	ExportSurfaceTraces,
	ExportSurfaceReplay,
	ExportSurfaceProfile,
}

type ExportSurfaceContract struct {
	Surface                  ExportSurface `json:"surface"`
	Modes                    []ExportMode  `json:"modes"`
	RequiresCursorCheckpoint bool          `json:"requiresCursorCheckpoint"`
	RequiresIdempotencyKey   bool          `json:"requiresIdempotencyKey"`
	VerifyShadowReads        bool          `json:"verifyShadowReads"`
}

type ExportContract struct {
	Surfaces []ExportSurfaceContract `json:"surfaces"`
}

func DefaultExportContract() ExportContract {
	return ExportContract{
		Surfaces: []ExportSurfaceContract{
			{Surface: ExportSurfaceEvents, Modes: []ExportMode{ExportModeSnapshot, ExportModeShadowDualWrite, ExportModeCutoverDualWrite}, RequiresCursorCheckpoint: true, RequiresIdempotencyKey: true, VerifyShadowReads: true},
			{Surface: ExportSurfaceLogs, Modes: []ExportMode{ExportModeSnapshot, ExportModeShadowDualWrite, ExportModeCutoverDualWrite}, RequiresCursorCheckpoint: true, RequiresIdempotencyKey: true, VerifyShadowReads: true},
			{Surface: ExportSurfaceTraces, Modes: []ExportMode{ExportModeSnapshot, ExportModeShadowDualWrite, ExportModeCutoverDualWrite}, RequiresCursorCheckpoint: true, RequiresIdempotencyKey: true, VerifyShadowReads: true},
			{Surface: ExportSurfaceReplay, Modes: []ExportMode{ExportModeSnapshot, ExportModeShadowDualWrite}, RequiresCursorCheckpoint: true, RequiresIdempotencyKey: true, VerifyShadowReads: true},
			{Surface: ExportSurfaceProfile, Modes: []ExportMode{ExportModeSnapshot, ExportModeShadowDualWrite}, RequiresCursorCheckpoint: true, RequiresIdempotencyKey: true, VerifyShadowReads: true},
		},
	}
}

func (c ExportContract) Validate() error {
	if len(c.Surfaces) != len(exportSurfaceOrder) {
		return fmt.Errorf("expected %d export surfaces, got %d", len(exportSurfaceOrder), len(c.Surfaces))
	}
	for _, surface := range exportSurfaceOrder {
		item, ok := c.lookup(surface)
		if !ok {
			return fmt.Errorf("missing export surface %q", surface)
		}
		if len(item.Modes) == 0 {
			return fmt.Errorf("surface %q must define at least one export mode", surface)
		}
		for _, mode := range item.Modes {
			if !slices.Contains(exportModeOrder, mode) {
				return fmt.Errorf("surface %q has invalid export mode %q", surface, mode)
			}
		}
		if !item.RequiresCursorCheckpoint {
			return fmt.Errorf("surface %q must require cursor checkpoints", surface)
		}
		if !item.RequiresIdempotencyKey {
			return fmt.Errorf("surface %q must require idempotency keys", surface)
		}
	}
	return nil
}

func (c ExportContract) Supports(surface ExportSurface, mode ExportMode) bool {
	item, ok := c.lookup(surface)
	if !ok {
		return false
	}
	return slices.Contains(item.Modes, mode)
}

func (c ExportContract) lookup(surface ExportSurface) (ExportSurfaceContract, bool) {
	for _, item := range c.Surfaces {
		if item.Surface == surface {
			return item, true
		}
	}
	return ExportSurfaceContract{}, false
}
