package discover

import (
	"fmt"
	"strings"
)

type LegacyInput struct {
	Dataset      Dataset
	Organization string
	ProjectID    string
	ProjectIDs   []string
	Filter       string
	Query        string
	Environment  string
	TimeRange    string
	Limit        int
}

func ParseLegacy(input LegacyInput) (Query, CostEstimate, error) {
	query := Query{
		Version: CurrentVersion,
		Dataset: input.Dataset,
		Scope:   buildLegacyScope(input),
		Limit:   input.Limit,
	}
	if strings.TrimSpace(input.TimeRange) != "" {
		query.TimeRange = &TimeRange{Kind: "relative", Value: strings.TrimSpace(input.TimeRange)}
	}

	var errs ValidationErrors
	var predicates []Predicate

	if status := normalizeLegacyStatus(input.Filter); status != "" {
		predicates = append(predicates, Predicate{Op: "=", Field: "status", Value: status})
	} else if filter := strings.TrimSpace(input.Filter); filter != "" && !strings.EqualFold(filter, "all") {
		errs = append(errs, ValidationError{
			Code:    "invalid_filter",
			Path:    "filter",
			Message: fmt.Sprintf("unsupported filter %q", filter),
		})
	}

	selectedEnv := strings.TrimSpace(input.Environment)
	for _, token := range strings.Fields(strings.TrimSpace(input.Query)) {
		pred, consumed, err := parseLegacyToken(input.Dataset, token, &selectedEnv)
		if err != nil {
			errs = append(errs, *err)
			continue
		}
		if !consumed {
			predicates = append(predicates, defaultSearchPredicate(input.Dataset, token))
			continue
		}
		if pred.Op != "" {
			predicates = append(predicates, pred)
		}
	}
	if selectedEnv != "" {
		predicates = append(predicates, Predicate{Op: "=", Field: "environment", Value: selectedEnv})
	}
	if len(predicates) == 1 {
		query.Where = &predicates[0]
	} else if len(predicates) > 1 {
		query.Where = &Predicate{Op: "and", Args: predicates}
	}
	if len(errs) > 0 {
		return Query{}, CostEstimate{}, errs
	}
	return ValidateQuery(query)
}

func buildLegacyScope(input LegacyInput) Scope {
	projectID := strings.TrimSpace(input.ProjectID)
	if projectID != "" {
		return Scope{Kind: ScopeKindProject, ProjectID: projectID}
	}
	return Scope{
		Kind:         ScopeKindOrganization,
		Organization: strings.TrimSpace(input.Organization),
		ProjectIDs:   append([]string(nil), input.ProjectIDs...),
	}
}

func parseLegacyToken(dataset Dataset, token string, selectedEnv *string) (Predicate, bool, *ValidationError) {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return Predicate{}, false, nil
	}
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "is:"):
		status := normalizeLegacyStatus(trimmed[3:])
		if status == "" {
			return Predicate{}, true, &ValidationError{Code: "invalid_status", Path: "query", Message: fmt.Sprintf("unsupported status token %q", trimmed)}
		}
		return Predicate{Op: "=", Field: "status", Value: status}, true, nil
	case strings.HasPrefix(lower, "release:"):
		value := strings.TrimSpace(trimmed[len("release:"):])
		if value == "" {
			return Predicate{}, true, &ValidationError{Code: "missing_value", Path: "query", Message: "release token requires a value"}
		}
		return Predicate{Op: "=", Field: "release", Value: value}, true, nil
	case strings.HasPrefix(lower, "environment:"):
		return consumeLegacyEnvironment(trimmed[len("environment:"):], selectedEnv)
	case strings.HasPrefix(lower, "env:"):
		return consumeLegacyEnvironment(trimmed[len("env:"):], selectedEnv)
	case strings.HasPrefix(lower, "level:"):
		value := strings.ToLower(strings.TrimSpace(trimmed[len("level:"):]))
		if value == "" {
			return Predicate{}, true, &ValidationError{Code: "missing_value", Path: "query", Message: "level token requires a value"}
		}
		return Predicate{Op: "=", Field: "level", Value: value}, true, nil
	case strings.HasPrefix(lower, "event.type:"):
		value := strings.ToLower(strings.TrimSpace(trimmed[len("event.type:"):]))
		if value == "" {
			return Predicate{}, true, &ValidationError{Code: "missing_value", Path: "query", Message: "event.type token requires a value"}
		}
		return Predicate{Op: "=", Field: "event.type", Value: value}, true, nil
	case strings.HasPrefix(lower, "type:"):
		value := strings.ToLower(strings.TrimSpace(trimmed[len("type:"):]))
		if value == "" {
			return Predicate{}, true, &ValidationError{Code: "missing_value", Path: "query", Message: "type token requires a value"}
		}
		return Predicate{Op: "=", Field: "event.type", Value: value}, true, nil
	case strings.Contains(trimmed, ":"):
		return Predicate{}, true, &ValidationError{Code: "unsupported_token", Path: "query", Message: fmt.Sprintf("unsupported discover token %q", trimmed)}
	default:
		return Predicate{}, false, nil
	}
}

func consumeLegacyEnvironment(raw string, selectedEnv *string) (Predicate, bool, *ValidationError) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return Predicate{}, true, &ValidationError{Code: "missing_value", Path: "query", Message: "environment token requires a value"}
	}
	if *selectedEnv != "" && !strings.EqualFold(*selectedEnv, value) {
		return Predicate{}, true, &ValidationError{Code: "environment_conflict", Path: "query", Message: "environment token conflicts with selected environment"}
	}
	*selectedEnv = value
	return Predicate{}, true, nil
}

func defaultSearchPredicate(dataset Dataset, term string) Predicate {
	fields := []string{}
	switch dataset {
	case DatasetIssues:
		fields = []string{"title", "culprit"}
	case DatasetLogs:
		fields = []string{"title", "message", "logger", "culprit"}
	case DatasetTransactions:
		fields = []string{"transaction", "op", "status", "trace.id", "project"}
	}
	if len(fields) == 0 {
		return Predicate{Op: "contains", Field: "title", Value: term}
	}
	args := make([]Predicate, 0, len(fields))
	for _, field := range fields {
		args = append(args, Predicate{Op: "contains", Field: field, Value: term})
	}
	if len(args) == 1 {
		return args[0]
	}
	return Predicate{Op: "or", Args: args}
}

func normalizeLegacyStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "open", "unresolved":
		return "unresolved"
	case "resolved", "closed":
		return "resolved"
	case "ignored":
		return "ignored"
	default:
		return ""
	}
}
