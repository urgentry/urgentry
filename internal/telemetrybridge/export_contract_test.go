package telemetrybridge

import "testing"

func TestDefaultExportContractValidate(t *testing.T) {
	if err := DefaultExportContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestExportContractSupportsCutoverForEvents(t *testing.T) {
	if !DefaultExportContract().Supports(ExportSurfaceEvents, ExportModeCutoverDualWrite) {
		t.Fatal("Supports(events, cutover_dual_write) = false, want true")
	}
}

func TestExportContractKeepsReplayOutOfCutoverMode(t *testing.T) {
	if DefaultExportContract().Supports(ExportSurfaceReplay, ExportModeCutoverDualWrite) {
		t.Fatal("Supports(replay, cutover_dual_write) = true, want false")
	}
}
