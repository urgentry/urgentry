package discovershared

import (
	"fmt"
	"strings"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/sqlutil"
)

type ArgBuilder interface {
	Add(value any) string
	AddAll(values []string) string
}

// SQLiteArgBuilder produces ? placeholders and formats time.Time as RFC3339.
type SQLiteArgBuilder struct {
	Args []any
}

func (b *SQLiteArgBuilder) Add(value any) string {
	if ts, ok := value.(time.Time); ok {
		b.Args = append(b.Args, ts.UTC().Format(time.RFC3339))
		return "?"
	}
	b.Args = append(b.Args, value)
	return "?"
}

func (b *SQLiteArgBuilder) AddAll(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, b.Add(value))
	}
	return strings.Join(items, ", ")
}

// PostgresArgBuilder produces $N numbered placeholders.
type PostgresArgBuilder struct {
	Args []any
}

func (b *PostgresArgBuilder) Add(value any) string {
	b.Args = append(b.Args, value)
	return fmt.Sprintf("$%d", len(b.Args))
}

func (b *PostgresArgBuilder) AddAll(values []string) string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		items = append(items, b.Add(value))
	}
	return strings.Join(items, ", ")
}

// DefaultFetchLimit normalises the per-query fetch limit used by both
// SQLite and bridge discover paths.
func DefaultFetchLimit(queryLimit, scanLimit int) int {
	limit := scanLimit
	if limit <= 0 {
		limit = queryLimit
	}
	if limit <= 0 {
		limit = 50
	}
	return limit
}

// Clamp restricts value to [minValue, maxValue].
func Clamp(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}

type PredicateOverride func(pred discover.Predicate) (sql string, handled bool, err error)

type PredicateDialect struct {
	Builder             ArgBuilder
	FieldExpr           func(string) (string, bool)
	Override            PredicateOverride
	CaseInsensitiveLike bool
}

func CompilePredicate(pred discover.Predicate, dialect PredicateDialect) (string, error) {
	switch pred.Op {
	case "and", "or":
		parts := make([]string, 0, len(pred.Args))
		for _, child := range pred.Args {
			sqlText, err := CompilePredicate(child, dialect)
			if err != nil {
				return "", err
			}
			parts = append(parts, "("+sqlText+")")
		}
		return strings.Join(parts, " "+strings.ToUpper(pred.Op)+" "), nil
	case "not":
		sqlText, err := CompilePredicate(pred.Args[0], dialect)
		if err != nil {
			return "", err
		}
		return "NOT (" + sqlText + ")", nil
	}
	if dialect.Override != nil {
		sqlText, handled, err := dialect.Override(pred)
		if err != nil {
			return "", err
		}
		if handled {
			return sqlText, nil
		}
	}
	sqlExpr, ok := dialect.FieldExpr(pred.Field)
	if !ok {
		return "", fmt.Errorf("unsupported discover field %q", pred.Field)
	}
	likeOp := "LIKE"
	if dialect.CaseInsensitiveLike {
		likeOp = "ILIKE"
	}
	switch pred.Op {
	case "=":
		return sqlExpr + ` = ` + dialect.Builder.Add(pred.Value), nil
	case "!=":
		return sqlExpr + ` != ` + dialect.Builder.Add(pred.Value), nil
	case "contains":
		return sqlExpr + ` ` + likeOp + ` ` + dialect.Builder.Add("%"+sqlutil.EscapeLike(pred.Value)+"%") + ` ESCAPE '\'`, nil
	case "prefix":
		return sqlExpr + ` ` + likeOp + ` ` + dialect.Builder.Add(sqlutil.EscapeLike(pred.Value)+"%") + ` ESCAPE '\'`, nil
	case ">":
		return sqlExpr + ` > ` + dialect.Builder.Add(pred.Value), nil
	case ">=":
		return sqlExpr + ` >= ` + dialect.Builder.Add(pred.Value), nil
	case "<":
		return sqlExpr + ` < ` + dialect.Builder.Add(pred.Value), nil
	case "<=":
		return sqlExpr + ` <= ` + dialect.Builder.Add(pred.Value), nil
	case "is_null":
		return sqlExpr + ` = ''`, nil
	case "not_null":
		return sqlExpr + ` != ''`, nil
	case "in":
		return sqlExpr + ` IN (` + dialect.Builder.AddAll(pred.Values) + `)`, nil
	default:
		return "", fmt.Errorf("unsupported predicate op %q", pred.Op)
	}
}
