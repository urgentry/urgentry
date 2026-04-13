package telemetryquery

import (
	"strings"
	"testing"
	"time"

	"urgentry/internal/telemetrybridge"
)

func TestEvaluateSurfaceFreshness(t *testing.T) {
	t.Parallel()

	serveStale := SurfaceExecution{
		Surface:                QuerySurfaceTraces,
		FreshnessMode:          FreshnessModeServeStale,
		StaleBudgetSeconds:     120,
		FailClosedAfterSeconds: 600,
	}
	failClosed := SurfaceExecution{
		Surface:                QuerySurfaceProfiles,
		FreshnessMode:          FreshnessModeFailClosed,
		StaleBudgetSeconds:     120,
		FailClosedAfterSeconds: 600,
	}

	tests := []struct {
		name     string
		policy   SurfaceExecution
		items    []telemetrybridge.FamilyFreshness
		wantLen  int
		contains string
	}{
		{
			name:   "pending without cursor fails",
			policy: serveStale,
			items: []telemetrybridge.FamilyFreshness{{
				Family:  telemetrybridge.FamilyTransactions,
				Pending: true,
			}},
			wantLen:  1,
			contains: "no projection cursor",
		},
		{
			name:   "serve stale budget allows short lag",
			policy: serveStale,
			items: []telemetrybridge.FamilyFreshness{{
				Family:      telemetrybridge.FamilyTransactions,
				CursorFound: true,
				Pending:     true,
				Lag:         90 * time.Second,
			}},
		},
		{
			name:   "serve stale fails after fail closed budget",
			policy: serveStale,
			items: []telemetrybridge.FamilyFreshness{{
				Family:      telemetrybridge.FamilyTransactions,
				CursorFound: true,
				Pending:     true,
				Lag:         11 * time.Minute,
			}},
			wantLen:  1,
			contains: "fail-closed budget",
		},
		{
			name:   "fail closed mode rejects stale budget breach",
			policy: failClosed,
			items: []telemetrybridge.FamilyFreshness{{
				Family:      telemetrybridge.FamilyProfiles,
				CursorFound: true,
				Pending:     true,
				Lag:         3 * time.Minute,
			}},
			wantLen:  1,
			contains: "stale budget",
		},
		{
			name:   "pending with last error fails immediately",
			policy: serveStale,
			items: []telemetrybridge.FamilyFreshness{{
				Family:      telemetrybridge.FamilyReplayTimeline,
				CursorFound: true,
				Pending:     true,
				LastError:   "boom",
			}},
			wantLen:  1,
			contains: "stalled after error",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reasons := evaluateSurfaceFreshness(tc.policy, tc.items)
			if len(reasons) != tc.wantLen {
				t.Fatalf("len(reasons) = %d, want %d (%v)", len(reasons), tc.wantLen, reasons)
			}
			if tc.contains != "" && (len(reasons) == 0 || !strings.Contains(reasons[0], tc.contains)) {
				t.Fatalf("reasons[0] = %q, want substring %q", firstReason(reasons), tc.contains)
			}
		})
	}
}

func firstReason(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return items[0]
}
