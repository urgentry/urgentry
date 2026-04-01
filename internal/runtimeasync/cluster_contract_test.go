package runtimeasync

import "testing"

func TestDefaultClusterContractValidate(t *testing.T) {
	if err := DefaultClusterContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultClusterContractFailsClosedForQuotasAndLeases(t *testing.T) {
	contract := DefaultClusterContract()
	for _, item := range contract.Primitives {
		switch item.Primitive {
		case ClusterPrimitiveIngestQuota, ClusterPrimitiveQueryQuota, ClusterPrimitiveLease:
			if item.FailureMode != FailureModeFailClosed {
				t.Fatalf("primitive %q failure mode = %q, want %q", item.Primitive, item.FailureMode, FailureModeFailClosed)
			}
		}
	}
}
