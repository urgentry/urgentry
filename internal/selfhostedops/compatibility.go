package selfhostedops

import "fmt"

type ClusterRole string

const (
	ClusterRoleAPI       ClusterRole = "api"
	ClusterRoleIngest    ClusterRole = "ingest"
	ClusterRoleWorker    ClusterRole = "worker"
	ClusterRoleScheduler ClusterRole = "scheduler"
)

type NodeVersion struct {
	Role          ClusterRole `json:"role"`
	BinaryVersion int         `json:"binaryVersion"`
}

type ClusterVersionState struct {
	ControlSchemaVersion   int           `json:"controlSchemaVersion"`
	TelemetrySchemaVersion int           `json:"telemetrySchemaVersion"`
	Nodes                  []NodeVersion `json:"nodes"`
}

type VersionViolation struct {
	Component UpgradeComponent `json:"component"`
	Detail    string           `json:"detail"`
}

type ClusterPreflightReport struct {
	Compatible bool               `json:"compatible"`
	Violations []VersionViolation `json:"violations,omitempty"`
}

func CheckMixedVersionCluster(contract UpgradeContract, state ClusterVersionState) (*ClusterPreflightReport, error) {
	if err := contract.Validate(); err != nil {
		return nil, err
	}
	if state.ControlSchemaVersion < 0 || state.TelemetrySchemaVersion < 0 {
		return nil, fmt.Errorf("schema versions must be non-negative")
	}
	appMin, appMax, ok := roleWindow(state.Nodes, ClusterRoleAPI, ClusterRoleIngest)
	if !ok {
		return nil, fmt.Errorf("at least one api or ingest node is required")
	}
	workerMin, workerMax, hasWorkers := roleWindow(state.Nodes, ClusterRoleWorker)
	schedulerMin, schedulerMax, hasSchedulers := roleWindow(state.Nodes, ClusterRoleScheduler)

	var violations []VersionViolation
	if appMax-appMin > 1 {
		violations = append(violations, VersionViolation{
			Component: UpgradeComponentAppBundle,
			Detail:    fmt.Sprintf("app bundle skew %d exceeds one release", appMax-appMin),
		})
	}
	if state.ControlSchemaVersion < appMax || state.ControlSchemaVersion > appMin+1 {
		violations = append(violations, VersionViolation{
			Component: UpgradeComponentControlSchema,
			Detail:    fmt.Sprintf("control schema version %d is incompatible with app bundle range %d-%d", state.ControlSchemaVersion, appMin, appMax),
		})
	}
	if state.TelemetrySchemaVersion < appMax || state.TelemetrySchemaVersion > appMin+1 {
		violations = append(violations, VersionViolation{
			Component: UpgradeComponentTelemetrySchema,
			Detail:    fmt.Sprintf("telemetry schema version %d is incompatible with app bundle range %d-%d", state.TelemetrySchemaVersion, appMin, appMax),
		})
	}
	if hasWorkers {
		if workerMax-workerMin > 1 {
			violations = append(violations, VersionViolation{
				Component: UpgradeComponentWorker,
				Detail:    fmt.Sprintf("worker skew %d exceeds one release", workerMax-workerMin),
			})
		}
		if appMax-workerMin > 1 || workerMax > appMax {
			violations = append(violations, VersionViolation{
				Component: UpgradeComponentWorker,
				Detail:    fmt.Sprintf("worker range %d-%d is incompatible with app bundle range %d-%d", workerMin, workerMax, appMin, appMax),
			})
		}
	}
	if hasSchedulers {
		if schedulerMax-schedulerMin > 1 {
			violations = append(violations, VersionViolation{
				Component: UpgradeComponentScheduler,
				Detail:    fmt.Sprintf("scheduler skew %d exceeds one release", schedulerMax-schedulerMin),
			})
		}
		if appMax-schedulerMin > 1 || schedulerMax > appMax {
			violations = append(violations, VersionViolation{
				Component: UpgradeComponentScheduler,
				Detail:    fmt.Sprintf("scheduler range %d-%d is incompatible with app bundle range %d-%d", schedulerMin, schedulerMax, appMin, appMax),
			})
		}
	}
	return &ClusterPreflightReport{
		Compatible: len(violations) == 0,
		Violations: violations,
	}, nil
}

func roleWindow(nodes []NodeVersion, roles ...ClusterRole) (minVal int, maxVal int, ok bool) {
	for _, node := range nodes {
		if !hasRole(roles, node.Role) {
			continue
		}
		if !ok {
			minVal = node.BinaryVersion
			maxVal = node.BinaryVersion
			ok = true
			continue
		}
		if node.BinaryVersion < minVal {
			minVal = node.BinaryVersion
		}
		if node.BinaryVersion > maxVal {
			maxVal = node.BinaryVersion
		}
	}
	return minVal, maxVal, ok
}

func hasRole(roles []ClusterRole, role ClusterRole) bool {
	for _, item := range roles {
		if item == role {
			return true
		}
	}
	return false
}
