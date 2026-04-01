package telemetrybridge

import "testing"

func TestDefaultFanoutContractValidate(t *testing.T) {
	if err := DefaultFanoutContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultFanoutContractUsesOutboxAndIdempotency(t *testing.T) {
	contract := DefaultFanoutContract()
	if got, want := contract.Mode, FanoutModeOutbox; got != want {
		t.Fatalf("Mode = %q, want %q", got, want)
	}
	if contract.IdempotencyKey == "" {
		t.Fatal("IdempotencyKey = empty, want populated")
	}
	if got, want := len(contract.RequiredEvents) > 0, true; got != want {
		t.Fatalf("RequiredEvents empty = %t, want %t", !got, !want)
	}
}
