//go:build !clickhouse

package telemetrycolumnar

import (
	"context"
	"database/sql"
	"fmt"
)

func Open(_ context.Context, _ string) (*sql.DB, error) {
	return nil, fmt.Errorf("clickhouse support is not built in; rebuild with URGENTRY_BUILD_TAGS including clickhouse")
}
