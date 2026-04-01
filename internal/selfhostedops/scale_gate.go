package selfhostedops

import "fmt"

type GateCheck string

const (
	GateCheckBackupRestore   GateCheck = "backup_restore"
	GateCheckRollingUpgrade  GateCheck = "rolling_upgrade"
	GateCheckFailover        GateCheck = "failover"
	GateCheckSoak            GateCheck = "soak"
	GateCheckQuotaIsolation  GateCheck = "quota_isolation"
	GateCheckBridgeRebuild   GateCheck = "bridge_rebuild"
	GateCheckKubernetesSmoke GateCheck = "kubernetes_smoke"
)

type GateEntry struct {
	Check      GateCheck `json:"check"`
	Command    string    `json:"command"`
	Artifact   string    `json:"artifact"`
	SuccessBar string    `json:"successBar"`
}

type ScaleValidationGate struct {
	Checks []GateEntry `json:"checks"`
}

func DefaultScaleValidationGate() ScaleValidationGate {
	return ScaleValidationGate{
		Checks: []GateEntry{
			{
				Check:      GateCheckBackupRestore,
				Command:    "bash eval/run-selfhosted.sh",
				Artifact:   "eval/reports/selfhosted/scorecard.md",
				SuccessBar: "backup, restore, and operator artifacts pass in the self-hosted scorecard",
			},
			{
				Check:      GateCheckRollingUpgrade,
				Command:    "apps/urgentry/deploy/compose/upgrade.sh",
				Artifact:   "operator-artifacts/preflight-before.json",
				SuccessBar: "upgrade captures pre and post status plus rollback artifacts",
			},
			{
				Check:      GateCheckFailover,
				Command:    "apps/urgentry/deploy/compose/drills.sh split-role-restart",
				Artifact:   "eval/reports/selfhosted/scorecard.json",
				SuccessBar: "worker and scheduler failover drills stay green",
			},
			{
				Check:      GateCheckSoak,
				Command:    "cd apps/urgentry && make selfhosted-bench",
				Artifact:   "eval/reports/selfhosted-performance/capacity-summary.md",
				SuccessBar: "steady-state and node-churn soak budgets stay inside the published ceiling",
			},
			{
				Check:      GateCheckQuotaIsolation,
				Command:    "cd apps/urgentry && go test ./internal/sqlite -run QueryGuard",
				Artifact:   "eval/reports/selfhosted-performance/capacity-summary.json",
				SuccessBar: "shared quota enforcement survives node churn and tenant pressure",
			},
			{
				Check:      GateCheckBridgeRebuild,
				Command:    "apps/urgentry/deploy/compose/drills.sh bridge-rebuild",
				Artifact:   "eval/reports/selfhosted/scorecard.md",
				SuccessBar: "bridge rebuild and lag-recovery drills stay green",
			},
			{
				Check:      GateCheckKubernetesSmoke,
				Command:    "apps/urgentry/deploy/k8s/smoke.sh",
				Artifact:   "apps/urgentry/deploy/k8s/secret.yaml",
				SuccessBar: "cluster-oriented smoke stays aligned with the shipped placeholders and bootstrap path",
			},
		},
	}
}

func (g ScaleValidationGate) Validate() error {
	if len(g.Checks) != 7 {
		return fmt.Errorf("expected 7 scale-gate checks, got %d", len(g.Checks))
	}
	seen := map[GateCheck]struct{}{}
	for _, item := range g.Checks {
		if item.Check == "" {
			return fmt.Errorf("gate check id is required")
		}
		if _, ok := seen[item.Check]; ok {
			return fmt.Errorf("duplicate gate check %q", item.Check)
		}
		seen[item.Check] = struct{}{}
		if item.Command == "" || item.Artifact == "" || item.SuccessBar == "" {
			return fmt.Errorf("gate check %q is incomplete", item.Check)
		}
	}
	return nil
}
