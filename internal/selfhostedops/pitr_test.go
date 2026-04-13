package selfhostedops

import (
	"strings"
	"testing"
)

func TestDefaultPITRContractValidate(t *testing.T) {
	if err := DefaultPITRContract().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestDefaultPITRContractRequiresWalArchive(t *testing.T) {
	contract := DefaultPITRContract()
	for _, item := range contract.Requirements {
		if !item.RequiresWALArchive {
			t.Fatalf("surface %q does not require wal archive", item.Surface)
		}
	}
}

func TestPITRContractValidateRejectsInvalidContracts(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*PITRContract)
		wantErr string
	}{
		{
			name: "wrong-requirement-count",
			mutate: func(contract *PITRContract) {
				contract.Requirements = contract.Requirements[:1]
			},
			wantErr: "expected 2 pitr requirements",
		},
		{
			name: "missing-workflow",
			mutate: func(contract *PITRContract) {
				contract.Workflow = nil
			},
			wantErr: "workflow",
		},
		{
			name: "missing-boundaries",
			mutate: func(contract *PITRContract) {
				contract.Boundaries = nil
			},
			wantErr: "boundaries",
		},
		{
			name: "missing-surface",
			mutate: func(contract *PITRContract) {
				contract.Requirements[0].Surface = ""
			},
			wantErr: "recovery surface is required",
		},
		{
			name: "missing-wal-archive",
			mutate: func(contract *PITRContract) {
				contract.Requirements[0].RequiresWALArchive = false
			},
			wantErr: "must require wal archiving",
		},
		{
			name: "missing-base-backup-interval",
			mutate: func(contract *PITRContract) {
				contract.Requirements[0].BaseBackupInterval = ""
			},
			wantErr: "base backup interval",
		},
		{
			name: "missing-recovery-target-types",
			mutate: func(contract *PITRContract) {
				contract.Requirements[0].RecoveryTargetTypes = nil
			},
			wantErr: "recovery target types",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			contract := DefaultPITRContract()
			tt.mutate(&contract)
			err := contract.Validate()
			if err == nil {
				t.Fatal("Validate() error = nil, want failure")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}
