package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"urgentry/internal/migration"
)

type organizationPayloadExport struct {
	projects    []migration.ProjectImport
	releases    []migration.ReleaseImport
	issues      []migration.IssueImport
	events      []migration.EventImport
	projectKeys []migration.ProjectKeyImport
	alertRules  []migration.AlertRuleImport
	members     []migration.MemberImport
	artifacts   []migration.ArtifactImport
}

type execQuerier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type importValidationError struct {
	msg string
}

func (e *importValidationError) Error() string {
	return e.msg
}

func validationErrorf(format string, args ...any) error {
	return &importValidationError{msg: fmt.Sprintf(format, args...)}
}

func IsImportValidationError(err error) bool {
	var target *importValidationError
	return errors.As(err, &target)
}

func nullOrDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func parseOptionalTimeString(v string) time.Time {
	if strings.TrimSpace(v) == "" {
		return time.Time{}
	}
	return dbParseTime(v)
}

func nullableInt(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

func nullableIntDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

func sanitizeImportKeySegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(s)
}

func dbNullStr(ns sql.NullString) string {
	return nullStr(ns)
}

func dbParseTime(s string) time.Time {
	return parseTime(s)
}
