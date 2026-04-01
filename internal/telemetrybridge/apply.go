package telemetrybridge

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Executor matches the Exec surface needed to apply bridge migrations.
type Executor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

// Apply runs the bridge migrations for the selected backend in order.
func Apply(ctx context.Context, exec Executor, backend Backend) error {
	if exec == nil {
		return fmt.Errorf("telemetry bridge executor is required")
	}
	for _, migration := range Migrations(backend) {
		if _, err := exec.Exec(ctx, migration.SQL); err != nil {
			return fmt.Errorf("apply telemetry bridge migration %d (%s): %w", migration.Version, migration.Name, err)
		}
	}
	return nil
}
