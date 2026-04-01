package selfhostedops

import "testing"

func TestCheckMixedVersionClusterAllowsOneReleaseCanary(t *testing.T) {
	report, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   10,
		TelemetrySchemaVersion: 10,
		Nodes: []NodeVersion{
			{Role: ClusterRoleAPI, BinaryVersion: 9},
			{Role: ClusterRoleAPI, BinaryVersion: 10},
			{Role: ClusterRoleIngest, BinaryVersion: 10},
			{Role: ClusterRoleWorker, BinaryVersion: 9},
			{Role: ClusterRoleScheduler, BinaryVersion: 9},
		},
	})
	if err != nil {
		t.Fatalf("CheckMixedVersionCluster() error = %v", err)
	}
	if !report.Compatible {
		t.Fatalf("Compatible = false, want true, violations = %+v", report.Violations)
	}
}

func TestCheckMixedVersionClusterRejectsSchemaTooFarAhead(t *testing.T) {
	report, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   11,
		TelemetrySchemaVersion: 11,
		Nodes: []NodeVersion{
			{Role: ClusterRoleAPI, BinaryVersion: 9},
			{Role: ClusterRoleIngest, BinaryVersion: 9},
		},
	})
	if err != nil {
		t.Fatalf("CheckMixedVersionCluster() error = %v", err)
	}
	if report.Compatible {
		t.Fatalf("Compatible = true, want false")
	}
}

func TestCheckMixedVersionClusterRejectsWorkersAheadOfApp(t *testing.T) {
	report, err := CheckMixedVersionCluster(DefaultUpgradeContract(), ClusterVersionState{
		ControlSchemaVersion:   10,
		TelemetrySchemaVersion: 10,
		Nodes: []NodeVersion{
			{Role: ClusterRoleAPI, BinaryVersion: 10},
			{Role: ClusterRoleWorker, BinaryVersion: 11},
		},
	})
	if err != nil {
		t.Fatalf("CheckMixedVersionCluster() error = %v", err)
	}
	if report.Compatible {
		t.Fatalf("Compatible = true, want false")
	}
}
