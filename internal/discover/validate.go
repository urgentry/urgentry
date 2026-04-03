package discover

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func ValidateQuery(query Query) (Query, CostEstimate, error) {
	query = normalizeQuery(query)
	var errs ValidationErrors

	if query.Version != CurrentVersion {
		errs = append(errs, ValidationError{Code: "invalid_version", Path: "version", Message: fmt.Sprintf("unsupported version %d", query.Version)})
	}
	if _, ok := datasetCatalog[query.Dataset]; !ok {
		errs = append(errs, ValidationError{Code: "invalid_dataset", Path: "dataset", Message: fmt.Sprintf("unsupported dataset %q", query.Dataset)})
	}

	switch query.Scope.Kind {
	case ScopeKindOrganization:
		if query.Scope.Organization == "" {
			errs = append(errs, ValidationError{Code: "missing_scope", Path: "scope.organization", Message: "organization scope requires an organization slug"})
		}
	case ScopeKindProject:
		if query.Scope.ProjectID == "" {
			errs = append(errs, ValidationError{Code: "missing_scope", Path: "scope.project_id", Message: "project scope requires a project id"})
		}
	default:
		errs = append(errs, ValidationError{Code: "invalid_scope", Path: "scope.kind", Message: fmt.Sprintf("unsupported scope kind %q", query.Scope.Kind)})
	}

	if query.Limit <= 0 {
		query.Limit = 50
	}
	if query.Limit > 200 {
		errs = append(errs, ValidationError{Code: "limit_too_large", Path: "limit", Message: "limit cannot exceed 200"})
	}

	groupedFields := make(map[string]struct{}, len(query.GroupBy))
	for i, expr := range query.GroupBy {
		typ, aggregate, fieldName, exprErrs := validateExpression(query.Dataset, expr, fmt.Sprintf("group_by[%d]", i), false)
		errs = append(errs, exprErrs...)
		if aggregate {
			errs = append(errs, ValidationError{Code: "aggregate_not_allowed", Path: fmt.Sprintf("group_by[%d]", i), Message: "group_by cannot contain aggregate expressions"})
			continue
		}
		if typ == valueTypeNumber {
			if spec, ok := lookupField(query.Dataset, fieldName); ok && spec.Measure {
				errs = append(errs, ValidationError{Code: "measure_not_groupable", Path: fmt.Sprintf("group_by[%d]", i), Message: "measures cannot be used in group_by"})
			}
		}
		groupedFields[fieldName] = struct{}{}
	}

	aliases := make(map[string]struct{}, len(query.Select))
	hasAggregate := false
	hasDimension := false
	for i, item := range query.Select {
		path := fmt.Sprintf("select[%d]", i)
		if item.Alias != "" {
			if _, exists := aliases[item.Alias]; exists {
				errs = append(errs, ValidationError{Code: "duplicate_alias", Path: path + ".alias", Message: fmt.Sprintf("duplicate alias %q", item.Alias)})
			}
			aliases[item.Alias] = struct{}{}
		}
		_, aggregate, fieldName, exprErrs := validateExpression(query.Dataset, item.Expr, path+".expr", false)
		errs = append(errs, exprErrs...)
		if aggregate {
			hasAggregate = true
			continue
		}
		hasDimension = true
		if len(groupedFields) > 0 {
			if _, ok := groupedFields[fieldName]; !ok {
				errs = append(errs, ValidationError{Code: "missing_group_by", Path: path, Message: fmt.Sprintf("field %q must appear in group_by when aggregates are selected", fieldName)})
			}
		}
	}
	if hasAggregate && hasDimension && len(groupedFields) == 0 {
		errs = append(errs, ValidationError{Code: "missing_group_by", Path: "select", Message: "mixed aggregate and field selection requires group_by"})
	}
	if len(query.Select) == 0 && (len(query.GroupBy) > 0 || len(query.OrderBy) > 0) {
		errs = append(errs, ValidationError{Code: "missing_select", Path: "select", Message: "select is required when group_by or order_by is present"})
	}

	for i, order := range query.OrderBy {
		path := fmt.Sprintf("order_by[%d]", i)
		_, _, _, orderErrs := validateOrderExpression(query.Dataset, order.Expr, path+".expr", aliases)
		errs = append(errs, orderErrs...)
		if order.Direction != "asc" && order.Direction != "desc" {
			errs = append(errs, ValidationError{Code: "invalid_order_direction", Path: path + ".direction", Message: "direction must be asc or desc"})
		}
		if order.Nulls != "first" && order.Nulls != "last" {
			errs = append(errs, ValidationError{Code: "invalid_nulls", Path: path + ".nulls", Message: "nulls must be first or last"})
		}
	}

	if query.Where != nil {
		errs = append(errs, validatePredicate(query.Dataset, *query.Where, "where")...)
	}
	if query.TimeRange != nil {
		errs = append(errs, validateTimeRange(*query.TimeRange)...)
	}
	if query.Rollup != nil {
		errs = append(errs, validateRollup(*query.Rollup, query.TimeRange != nil)...)
		if len(query.GroupBy) > 0 {
			errs = append(errs, ValidationError{Code: "unsupported_group_by", Path: "group_by", Message: "phase-1 rollups support only the time bucket dimension"})
		}
	}

	cost := estimateCost(query)
	if cost.Class == CostClassReject {
		errs = append(errs, ValidationError{Code: "query_cost_reject", Path: "query", Message: cost.Reason})
	}
	if len(errs) > 0 {
		return Query{}, cost, errs
	}
	return query, cost, nil
}

func validateExpression(dataset Dataset, expr Expression, path string, allowAlias bool) (valueType, bool, string, ValidationErrors) {
	var errs ValidationErrors
	populated := 0
	if expr.Field != "" {
		populated++
	}
	if expr.Call != "" {
		populated++
	}
	if expr.Alias != "" {
		populated++
	}
	if populated != 1 {
		errs = append(errs, ValidationError{Code: "invalid_expression", Path: path, Message: "expression must set exactly one of field, call, or alias"})
		return "", false, "", errs
	}
	if expr.Alias != "" {
		if !allowAlias {
			errs = append(errs, ValidationError{Code: "alias_not_allowed", Path: path, Message: "alias references are only allowed in order_by"})
			return "", false, "", errs
		}
		return valueTypeString, false, "", nil
	}
	if expr.Field != "" {
		spec, ok := lookupField(dataset, expr.Field)
		if !ok {
			errs = append(errs, ValidationError{Code: "unknown_field", Path: path, Message: fmt.Sprintf("unknown field %q for dataset %q", expr.Field, dataset)})
			return "", false, expr.Field, errs
		}
		return spec.Type, false, expr.Field, nil
	}
	def, ok := aggregateCatalog[expr.Call]
	if !ok {
		errs = append(errs, ValidationError{Code: "unknown_function", Path: path, Message: fmt.Sprintf("unknown aggregate %q", expr.Call)})
		return "", true, "", errs
	}
	if _, ok := def.Datasets[dataset]; !ok {
		errs = append(errs, ValidationError{Code: "unsupported_function", Path: path, Message: fmt.Sprintf("aggregate %q is not supported for dataset %q", expr.Call, dataset)})
	}
	if len(expr.Args) != def.Args {
		errs = append(errs, ValidationError{Code: "invalid_argument_count", Path: path, Message: fmt.Sprintf("aggregate %q expects %d args", expr.Call, def.Args)})
		return valueTypeNumber, true, "", errs
	}
	if def.Args == 1 {
		argType, aggregate, fieldName, argErrs := validateExpression(dataset, expr.Args[0], path+".args[0]", false)
		errs = append(errs, argErrs...)
		if aggregate {
			errs = append(errs, ValidationError{Code: "nested_aggregate", Path: path, Message: "aggregate arguments cannot contain aggregates"})
		}
		if def.Field != "" && fieldName != def.Field {
			errs = append(errs, ValidationError{Code: "invalid_measure", Path: path, Message: fmt.Sprintf("aggregate %q requires field %q", expr.Call, def.Field)})
		}
		if argType != valueTypeNumber {
			errs = append(errs, ValidationError{Code: "invalid_argument_type", Path: path, Message: fmt.Sprintf("aggregate %q requires a numeric field", expr.Call)})
		}
	}
	return valueTypeNumber, true, "", errs
}

func validateOrderExpression(dataset Dataset, expr Expression, path string, aliases map[string]struct{}) (valueType, bool, string, ValidationErrors) {
	if expr.Alias != "" {
		if _, ok := aliases[expr.Alias]; !ok {
			return "", false, "", ValidationErrors{{Code: "unknown_alias", Path: path, Message: fmt.Sprintf("unknown alias %q", expr.Alias)}}
		}
		return valueTypeString, false, "", nil
	}
	return validateExpression(dataset, expr, path, true)
}

func validatePredicate(dataset Dataset, pred Predicate, path string) ValidationErrors {
	switch pred.Op {
	case "and", "or":
		if len(pred.Args) == 0 {
			return ValidationErrors{{Code: "missing_args", Path: path, Message: fmt.Sprintf("%s requires args", pred.Op)}}
		}
		var errs ValidationErrors
		for i, item := range pred.Args {
			errs = append(errs, validatePredicate(dataset, item, fmt.Sprintf("%s.args[%d]", path, i))...)
		}
		return errs
	case "not":
		if len(pred.Args) != 1 {
			return ValidationErrors{{Code: "invalid_not", Path: path, Message: "not requires exactly one child predicate"}}
		}
		return validatePredicate(dataset, pred.Args[0], path+".args[0]")
	}
	spec, ok := lookupField(dataset, pred.Field)
	if !ok {
		return ValidationErrors{{Code: "unknown_field", Path: path, Message: fmt.Sprintf("unknown field %q for dataset %q", pred.Field, dataset)}}
	}
	switch pred.Op {
	case "=", "!=", "contains", "prefix", ">", ">=", "<", "<=", "is_null", "not_null", "in":
	default:
		return ValidationErrors{{Code: "unsupported_operator", Path: path, Message: fmt.Sprintf("unsupported operator %q", pred.Op)}}
	}
	switch pred.Op {
	case "contains", "prefix":
		if spec.Type != valueTypeString && spec.Type != valueTypeEnum {
			return ValidationErrors{{Code: "invalid_operator_type", Path: path, Message: fmt.Sprintf("%s requires a string field", pred.Op)}}
		}
		if pred.Value == "" {
			return ValidationErrors{{Code: "missing_value", Path: path, Message: fmt.Sprintf("%s requires a value", pred.Op)}}
		}
	case ">", ">=", "<", "<=":
		switch spec.Type {
		case valueTypeNumber:
			if _, err := strconv.ParseFloat(pred.Value, 64); err != nil {
				return ValidationErrors{{Code: "invalid_number", Path: path, Message: fmt.Sprintf("invalid numeric value %q", pred.Value)}}
			}
		case valueTypeTimestamp:
			if _, err := time.Parse(time.RFC3339, pred.Value); err != nil {
				return ValidationErrors{{Code: "invalid_timestamp", Path: path, Message: fmt.Sprintf("invalid timestamp %q", pred.Value)}}
			}
		default:
			return ValidationErrors{{Code: "invalid_operator_type", Path: path, Message: fmt.Sprintf("%s is not supported for field %q", pred.Op, pred.Field)}}
		}
	case "in":
		if len(pred.Values) == 0 {
			return ValidationErrors{{Code: "missing_value", Path: path, Message: "in requires values"}}
		}
	case "is_null", "not_null":
		return nil
	default:
		if pred.Value == "" {
			return ValidationErrors{{Code: "missing_value", Path: path, Message: fmt.Sprintf("%s requires a value", pred.Op)}}
		}
	}
	return nil
}

func validateTimeRange(value TimeRange) ValidationErrors {
	switch value.Kind {
	case "relative":
		if strings.TrimSpace(value.Value) == "" {
			return ValidationErrors{{Code: "missing_time_range", Path: "time_range.value", Message: "relative time_range requires a value"}}
		}
	case "absolute":
		if strings.TrimSpace(value.Start) == "" || strings.TrimSpace(value.End) == "" {
			return ValidationErrors{{Code: "missing_time_range", Path: "time_range", Message: "absolute time_range requires start and end"}}
		}
		if _, err := time.Parse(time.RFC3339, value.Start); err != nil {
			return ValidationErrors{{Code: "invalid_timestamp", Path: "time_range.start", Message: fmt.Sprintf("invalid timestamp %q", value.Start)}}
		}
		if _, err := time.Parse(time.RFC3339, value.End); err != nil {
			return ValidationErrors{{Code: "invalid_timestamp", Path: "time_range.end", Message: fmt.Sprintf("invalid timestamp %q", value.End)}}
		}
	default:
		return ValidationErrors{{Code: "invalid_time_range", Path: "time_range.kind", Message: fmt.Sprintf("unsupported time_range kind %q", value.Kind)}}
	}
	return nil
}

func validateRollup(value Rollup, hasTimeRange bool) ValidationErrors {
	if !hasTimeRange {
		return ValidationErrors{{Code: "missing_time_range", Path: "rollup", Message: "rollup requires an explicit time_range"}}
	}
	if strings.TrimSpace(value.Interval) == "" {
		return ValidationErrors{{Code: "invalid_rollup", Path: "rollup.interval", Message: "rollup interval is required"}}
	}
	return nil
}

func estimateCost(query Query) CostEstimate {
	score := 0
	switch query.Dataset {
	case DatasetIssues:
		score++
	case DatasetLogs:
		score += 4
	case DatasetTransactions:
		score += 4
	}
	projectCount := 1
	if query.Scope.Kind == ScopeKindOrganization {
		switch {
		case len(query.Scope.ProjectIDs) > 0:
			projectCount = len(query.Scope.ProjectIDs)
		case query.Dataset == DatasetLogs || query.Dataset == DatasetTransactions:
			projectCount = 3
		default:
			projectCount = 1
		}
	}
	score += projectCount
	if len(query.Select) > 1 {
		score += len(query.Select) - 1
	}
	score += len(query.GroupBy)
	if query.Rollup != nil {
		score += 2
	}
	if query.Limit > 50 {
		score += query.Limit / 25
	}
	if query.TimeRange == nil && query.Scope.Kind == ScopeKindOrganization && (query.Dataset == DatasetLogs || query.Dataset == DatasetTransactions) {
		return CostEstimate{
			Class:  CostClassReject,
			Score:  score + 10,
			Reason: "organization-scoped logs and transactions queries require an explicit bounded time range",
		}
	}
	switch {
	case score >= 14:
		return CostEstimate{Class: CostClassReject, Score: score, Reason: "query exceeds the phase-1 interactive cost budget"}
	case score >= 7:
		return CostEstimate{Class: CostClassInteractive, Score: score}
	default:
		return CostEstimate{Class: CostClassCheap, Score: score}
	}
}
