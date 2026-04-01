package hosted

import (
	"testing"
	"time"
)

func TestBuildBillingExportIncludesEveryDimension(t *testing.T) {
	export, err := BuildBillingExport(DefaultCatalog(), BillingExportRequest{
		Account: Account{
			ID:     "acct-1",
			Slug:   "acme",
			Plan:   PlanTeam,
			Status: AccountStatusActive,
		},
		Period: InvoicePeriod{
			Start:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Status: InvoiceStatusOpen,
		},
		GeneratedAt: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildBillingExport() error = %v", err)
	}
	if got, want := len(export.Lines), len(UsageDimensions()); got != want {
		t.Fatalf("len(Lines) = %d, want %d", got, want)
	}
}

func TestBuildBillingExportRollsMeteredUsage(t *testing.T) {
	export, err := BuildBillingExport(DefaultCatalog(), BillingExportRequest{
		Account: Account{
			ID:     "acct-1",
			Slug:   "acme",
			Plan:   PlanTeam,
			Status: AccountStatusActive,
		},
		Period: InvoicePeriod{
			Start:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Status: InvoiceStatusFinalized,
		},
		GeneratedAt: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC),
		Rows: []UsageLedgerRow{
			{
				Dimension:     UsageMonthlyEvents,
				Surface:       QuotaSurfaceIngestEvents,
				RecordedUnits: 10_500_000,
				BillableUnits: 500_000,
				Adjustment:    UsageAdjustmentRecorded,
				RecordedAt:    time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildBillingExport() error = %v", err)
	}
	line := findBillingLine(t, export, UsageMonthlyEvents)
	if got, want := line.RecordedUnits, int64(10_500_000); got != want {
		t.Fatalf("RecordedUnits = %d, want %d", got, want)
	}
	if got, want := line.BillableUnits, int64(500_000); got != want {
		t.Fatalf("BillableUnits = %d, want %d", got, want)
	}
}

func TestBuildBillingExportMarksEnterpriseContractLines(t *testing.T) {
	export, err := BuildBillingExport(DefaultCatalog(), BillingExportRequest{
		Account: Account{
			ID:     "acct-1",
			Slug:   "acme",
			Plan:   PlanEnterprise,
			Status: AccountStatusActive,
		},
		Period: InvoicePeriod{
			Start:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Status: InvoiceStatusOpen,
		},
		GeneratedAt: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("BuildBillingExport() error = %v", err)
	}
	line := findBillingLine(t, export, UsageMonthlyEvents)
	if !line.RequiresContractLimit {
		t.Fatalf("RequiresContractLimit = false, want true, line=%+v", line)
	}
}

func TestBuildBillingExportAppliesEnterpriseContractOverride(t *testing.T) {
	export, err := BuildBillingExport(DefaultCatalog(), BillingExportRequest{
		Account: Account{
			ID:     "acct-1",
			Slug:   "acme",
			Plan:   PlanEnterprise,
			Status: AccountStatusActive,
		},
		Period: InvoicePeriod{
			Start:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Status: InvoiceStatusOpen,
		},
		GeneratedAt: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC),
		ContractLimits: map[UsageDimension]Limit{
			UsageMonthlyEvents: {
				Included:    2_000_000_000,
				OverageMode: OverageModeMetered,
			},
		},
	})
	if err != nil {
		t.Fatalf("BuildBillingExport() error = %v", err)
	}
	line := findBillingLine(t, export, UsageMonthlyEvents)
	if line.RequiresContractLimit {
		t.Fatalf("RequiresContractLimit = true, want false, line=%+v", line)
	}
	if got, want := line.IncludedUnits, int64(2_000_000_000); got != want {
		t.Fatalf("IncludedUnits = %d, want %d", got, want)
	}
	if got, want := line.OverageMode, OverageModeMetered; got != want {
		t.Fatalf("OverageMode = %q, want %q", got, want)
	}
}

func TestBuildBillingExportRejectsSurfaceDimensionMismatch(t *testing.T) {
	_, err := BuildBillingExport(DefaultCatalog(), BillingExportRequest{
		Account: Account{
			ID:     "acct-1",
			Slug:   "acme",
			Plan:   PlanTeam,
			Status: AccountStatusActive,
		},
		Period: InvoicePeriod{
			Start:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			End:    time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
			Status: InvoiceStatusOpen,
		},
		GeneratedAt: time.Date(2026, 4, 1, 1, 0, 0, 0, time.UTC),
		Rows: []UsageLedgerRow{
			{
				Dimension:     UsageMonthlyEvents,
				Surface:       QuotaSurfaceQuery,
				RecordedUnits: 1,
				Adjustment:    UsageAdjustmentRecorded,
				RecordedAt:    time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			},
		},
	})
	if err == nil {
		t.Fatal("BuildBillingExport() error = nil, want mismatch error")
	}
}

func findBillingLine(t *testing.T, export *BillingExport, dimension UsageDimension) BillingLine {
	t.Helper()
	for _, line := range export.Lines {
		if line.Dimension == dimension {
			return line
		}
	}
	t.Fatalf("missing billing line for %s", dimension)
	return BillingLine{}
}
