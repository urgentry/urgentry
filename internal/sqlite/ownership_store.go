package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	sharedstore "urgentry/internal/store"
	"urgentry/pkg/id"
)

type OwnershipStore struct {
	db *sql.DB
}

func NewOwnershipStore(db *sql.DB) *OwnershipStore {
	return &OwnershipStore{db: db}
}

func (s *OwnershipStore) ListProjectRules(ctx context.Context, projectID string) ([]sharedstore.OwnershipRule, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, project_id, name, pattern, assignee, created_at, updated_at
		 FROM ownership_rules
		 WHERE project_id = ?
		 ORDER BY created_at ASC`,
		projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []sharedstore.OwnershipRule
	for rows.Next() {
		item, err := scanOwnershipRuleRows(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *OwnershipStore) CreateRule(ctx context.Context, rule sharedstore.OwnershipRule) (*sharedstore.OwnershipRule, error) {
	rule.ProjectID = strings.TrimSpace(rule.ProjectID)
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	rule.Assignee = strings.TrimSpace(rule.Assignee)
	if rule.ProjectID == "" || rule.Pattern == "" || rule.Assignee == "" {
		return nil, fmt.Errorf("project, pattern, and assignee are required")
	}
	if rule.ID == "" {
		rule.ID = id.New()
	}
	now := time.Now().UTC()
	if rule.DateCreated.IsZero() {
		rule.DateCreated = now
	}
	rule.DateUpdated = now
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO ownership_rules (id, project_id, name, pattern, assignee, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		rule.ID, rule.ProjectID, rule.Name, rule.Pattern, rule.Assignee,
		rule.DateCreated.Format(time.RFC3339), rule.DateUpdated.Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	return &rule, nil
}

func (s *OwnershipStore) DeleteRule(ctx context.Context, projectID, ruleID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM ownership_rules WHERE id = ? AND project_id = ?`,
		ruleID, projectID,
	)
	return err
}

func (s *OwnershipStore) ResolveAssignee(ctx context.Context, projectID, title, culprit string, tags map[string]string) (string, error) {
	result, err := s.ResolveOwnership(ctx, projectID, title, culprit, tags)
	if err != nil {
		return "", err
	}
	if result == nil {
		return "", nil
	}
	return result.Assignee, nil
}

func (s *OwnershipStore) ResolveOwnership(ctx context.Context, projectID, title, culprit string, tags map[string]string) (*sharedstore.OwnershipResolveResult, error) {
	rules, err := s.ListProjectRules(ctx, projectID)
	if err != nil {
		return nil, err
	}
	title = strings.ToLower(strings.TrimSpace(title))
	culprit = strings.ToLower(strings.TrimSpace(culprit))
	normalizedTags := make(map[string]string, len(tags))
	for key, value := range tags {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		normalizedTags[key] = strings.ToLower(strings.TrimSpace(value))
	}
	for _, rule := range rules {
		if matchesOwnershipRule(rule.Pattern, title, culprit, normalizedTags) {
			return &sharedstore.OwnershipResolveResult{
				Assignee:   rule.Assignee,
				TeamSlug:   rule.TeamSlug,
				NotifyTeam: rule.NotifyTeam,
			}, nil
		}
	}
	return nil, nil
}

func matchesOwnershipRule(pattern, title, culprit string, tags map[string]string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	lower := strings.ToLower(pattern)
	switch {
	case strings.HasPrefix(lower, "path:"):
		needle := strings.TrimSpace(strings.TrimPrefix(lower, "path:"))
		if needle == "" {
			return false
		}
		base := strings.ToLower(filepath.Base(needle))
		return strings.Contains(culprit, needle) || (base != "" && strings.Contains(culprit, base))
	case strings.HasPrefix(lower, "culprit:"):
		needle := strings.TrimSpace(strings.TrimPrefix(lower, "culprit:"))
		return needle != "" && strings.Contains(culprit, needle)
	case strings.HasPrefix(lower, "title:"):
		needle := strings.TrimSpace(strings.TrimPrefix(lower, "title:"))
		return needle != "" && strings.Contains(title, needle)
	case strings.HasPrefix(lower, "tag:"):
		raw := strings.TrimSpace(strings.TrimPrefix(lower, "tag:"))
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return false
		}
		return tags[strings.TrimSpace(key)] == strings.TrimSpace(value)
	default:
		return strings.Contains(title, lower) || strings.Contains(culprit, lower)
	}
}

func scanOwnershipRuleRows(rows *sql.Rows) (sharedstore.OwnershipRule, error) {
	var item sharedstore.OwnershipRule
	var name, pattern, assignee, createdAt, updatedAt sql.NullString
	if err := rows.Scan(&item.ID, &item.ProjectID, &name, &pattern, &assignee, &createdAt, &updatedAt); err != nil {
		return item, err
	}
	item.Name = nullStr(name)
	item.Pattern = nullStr(pattern)
	item.Assignee = nullStr(assignee)
	item.DateCreated = parseTime(nullStr(createdAt))
	item.DateUpdated = parseTime(nullStr(updatedAt))
	return item, nil
}
