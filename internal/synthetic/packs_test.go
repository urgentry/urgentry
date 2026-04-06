package synthetic

import (
	"context"
	"testing"
)

func TestRunPackByNameCorePack(t *testing.T) {
	result, err := RunPackByName(context.Background(), "core-pack")
	if err != nil {
		t.Fatalf("RunPackByName: %v", err)
	}
	if !result.Passed {
		t.Fatalf("pack did not pass: %+v", result)
	}
	if result.Pack != "core-pack" {
		t.Fatalf("pack = %q", result.Pack)
	}
	if len(result.Results) != 1 {
		t.Fatalf("len(results)=%d", len(result.Results))
	}
}

func TestRunPackByNameFullP0(t *testing.T) {
	result, err := RunPackByName(context.Background(), "full-p0")
	if err != nil {
		t.Fatalf("RunPackByName: %v", err)
	}
	if !result.Passed {
		t.Fatalf("pack did not pass: %+v", result)
	}
}

func TestRunPackByNameQueryReadAnalytics(t *testing.T) {
	result, err := RunPackByName(context.Background(), "query-read-analytics")
	if err != nil {
		t.Fatalf("RunPackByName: %v", err)
	}
	if !result.Passed {
		t.Fatalf("pack did not pass: %+v", result)
	}
}

func TestRunPackByNameAdminControlLifecycle(t *testing.T) {
	result, err := RunPackByName(context.Background(), "admin-control-lifecycle")
	if err != nil {
		t.Fatalf("RunPackByName: %v", err)
	}
	if !result.Passed {
		t.Fatalf("pack did not pass: %+v", result)
	}
}

func TestAuditHasCoverageData(t *testing.T) {
	report, err := Audit(RepoRoot())
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if report.PayloadCaseCount == 0 || report.ArtifactCaseCount == 0 || report.DeepCaseCount == 0 {
		t.Fatalf("unexpected case counts: %+v", report)
	}
	if len(report.Coverage.RouteSections.Covered) == 0 {
		t.Fatalf("expected covered route sections: %+v", report.Coverage.RouteSections)
	}
	if len(report.Coverage.Datasets.Covered) == 0 {
		t.Fatalf("expected covered datasets: %+v", report.Coverage.Datasets)
	}
}
