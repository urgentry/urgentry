package selfhostedops

import "fmt"

type MigrationCompatibilityGate struct {
	MaxBinaryLag          int      `json:"maxBinaryLag"`
	MaxControlSchemaLag   int      `json:"maxControlSchemaLag"`
	MaxTelemetrySchemaLag int      `json:"maxTelemetrySchemaLag"`
	RequiredProofs        []string `json:"requiredProofs"`
}

type MigrationCompatibilityRequest struct {
	TargetBinaryVersion   int `json:"targetBinaryVersion"`
	TargetControlSchema   int `json:"targetControlSchema"`
	TargetTelemetrySchema int `json:"targetTelemetrySchema"`
	OldestBinaryVersion   int `json:"oldestBinaryVersion"`
	OldestControlSchema   int `json:"oldestControlSchema"`
	OldestTelemetrySchema int `json:"oldestTelemetrySchema"`
}

type MigrationCompatibilityReport struct {
	Compatible         bool     `json:"compatible"`
	BinaryLag          int      `json:"binaryLag"`
	ControlSchemaLag   int      `json:"controlSchemaLag"`
	TelemetrySchemaLag int      `json:"telemetrySchemaLag"`
	SupportsNMinusOne  bool     `json:"supportsNMinusOne"`
	SupportsNMinusTwo  bool     `json:"supportsNMinusTwo"`
	Violations         []string `json:"violations,omitempty"`
}

func DefaultMigrationCompatibilityGate() MigrationCompatibilityGate {
	return MigrationCompatibilityGate{
		MaxBinaryLag:          2,
		MaxControlSchemaLag:   2,
		MaxTelemetrySchemaLag: 2,
		RequiredProofs: []string{
			"prove mixed-version cluster preflight before rolling the second wave",
			"prove rollback safety before contract cleanup",
			"prove backup verification from the same upgrade window",
		},
	}
}

func (g MigrationCompatibilityGate) Validate() error {
	if g.MaxBinaryLag <= 0 || g.MaxControlSchemaLag <= 0 || g.MaxTelemetrySchemaLag <= 0 {
		return fmt.Errorf("compatibility lags must be positive")
	}
	if len(g.RequiredProofs) == 0 {
		return fmt.Errorf("required proofs must not be empty")
	}
	return nil
}

func (g MigrationCompatibilityGate) Evaluate(req MigrationCompatibilityRequest) (*MigrationCompatibilityReport, error) {
	if err := g.Validate(); err != nil {
		return nil, err
	}
	if req.TargetBinaryVersion < 0 || req.TargetControlSchema < 0 || req.TargetTelemetrySchema < 0 {
		return nil, fmt.Errorf("target versions must be non-negative")
	}
	if req.OldestBinaryVersion < 0 || req.OldestControlSchema < 0 || req.OldestTelemetrySchema < 0 {
		return nil, fmt.Errorf("oldest versions must be non-negative")
	}
	report := &MigrationCompatibilityReport{
		BinaryLag:          req.TargetBinaryVersion - req.OldestBinaryVersion,
		ControlSchemaLag:   req.TargetControlSchema - req.OldestControlSchema,
		TelemetrySchemaLag: req.TargetTelemetrySchema - req.OldestTelemetrySchema,
	}
	if report.BinaryLag < 0 || report.ControlSchemaLag < 0 || report.TelemetrySchemaLag < 0 {
		return nil, fmt.Errorf("oldest versions must not be ahead of targets")
	}
	report.SupportsNMinusOne = report.BinaryLag <= 1 && report.ControlSchemaLag <= 1 && report.TelemetrySchemaLag <= 1
	report.SupportsNMinusTwo = report.BinaryLag <= 2 && report.ControlSchemaLag <= 2 && report.TelemetrySchemaLag <= 2
	if report.BinaryLag > g.MaxBinaryLag {
		report.Violations = append(report.Violations, fmt.Sprintf("binary lag %d exceeds max %d", report.BinaryLag, g.MaxBinaryLag))
	}
	if report.ControlSchemaLag > g.MaxControlSchemaLag {
		report.Violations = append(report.Violations, fmt.Sprintf("control schema lag %d exceeds max %d", report.ControlSchemaLag, g.MaxControlSchemaLag))
	}
	if report.TelemetrySchemaLag > g.MaxTelemetrySchemaLag {
		report.Violations = append(report.Violations, fmt.Sprintf("telemetry schema lag %d exceeds max %d", report.TelemetrySchemaLag, g.MaxTelemetrySchemaLag))
	}
	report.Compatible = len(report.Violations) == 0
	return report, nil
}
