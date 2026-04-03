package hosted

import (
	"testing"
	"time"
)

func TestDefaultUsageLedgerPolicyValidate(t *testing.T) {
	if err := DefaultUsageLedgerPolicy().Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func assertUsageLedgerWindow(t *testing.T, entry UsageLedgerEntry, wantKind UsageWindowKind, wantStart, wantEnd time.Time) {
	t.Helper()
	if got := entry.WindowKind; got != wantKind {
		t.Fatalf("WindowKind = %q, want %q", got, wantKind)
	}
	if got := entry.Window.Start; !got.Equal(wantStart) {
		t.Fatalf("Window.Start = %s, want %s", got, wantStart)
	}
	if got := entry.Window.End; !got.Equal(wantEnd) {
		t.Fatalf("Window.End = %s, want %s", got, wantEnd)
	}
}

func TestBuildUsageLedgerEntryUsesDailyWindowForQueries(t *testing.T) {
	policy := DefaultUsageLedgerPolicy()
	account := Account{ID: "acct_1", Plan: PlanTeam}
	recordedAt := time.Date(2026, time.March, 30, 15, 4, 5, 0, time.UTC)
	entry, err := policy.BuildEntry(account, UsageLedgerRow{
		Dimension:     UsageDailyQueryUnits,
		Surface:       QuotaSurfaceQuery,
		RecordedUnits: 12,
		Adjustment:    UsageAdjustmentRecorded,
		RecordedAt:    recordedAt,
	}, "query-1")
	if err != nil {
		t.Fatalf("BuildEntry() error = %v", err)
	}
	assertUsageLedgerWindow(t, entry,
		UsageWindowDaily,
		time.Date(2026, time.March, 30, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.March, 31, 0, 0, 0, 0, time.UTC),
	)
}

func TestBuildUsageLedgerEntryUsesMonthlyWindowForEvents(t *testing.T) {
	policy := DefaultUsageLedgerPolicy()
	account := Account{ID: "acct_1", Plan: PlanTeam}
	recordedAt := time.Date(2026, time.March, 30, 15, 4, 5, 0, time.UTC)
	entry, err := policy.BuildEntry(account, UsageLedgerRow{
		Dimension:     UsageMonthlyEvents,
		Surface:       QuotaSurfaceIngestEvents,
		RecordedUnits: 42,
		Adjustment:    UsageAdjustmentRecorded,
		RecordedAt:    recordedAt,
	}, "events-1")
	if err != nil {
		t.Fatalf("BuildEntry() error = %v", err)
	}
	assertUsageLedgerWindow(t, entry,
		UsageWindowMonthly,
		time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, time.April, 1, 0, 0, 0, 0, time.UTC),
	)
}

func TestBuildUsageLedgerEntryRejectsWrongSurfaceMapping(t *testing.T) {
	policy := DefaultUsageLedgerPolicy()
	account := Account{ID: "acct_1", Plan: PlanTeam}
	_, err := policy.BuildEntry(account, UsageLedgerRow{
		Dimension:     UsageMonthlyEvents,
		Surface:       QuotaSurfaceQuery,
		RecordedUnits: 1,
		Adjustment:    UsageAdjustmentRecorded,
		RecordedAt:    time.Now().UTC(),
	}, "bad-1")
	if err == nil {
		t.Fatal("BuildEntry() error = nil, want surface mapping failure")
	}
}
