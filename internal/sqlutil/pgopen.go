package sqlutil

import (
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

// OpenPostgres opens a Postgres connection with automatic per-connection
// statement caching. Each connection caches up to 256 prepared statements,
// avoiding re-parse overhead on hot queries.
func OpenPostgres(dsn string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	cfg.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	cfg.StatementCacheCapacity = 256
	return stdlib.OpenDB(*cfg), nil
}
