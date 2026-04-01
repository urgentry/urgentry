package discover

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const CurrentVersion = 1

type Dataset string

const (
	DatasetIssues       Dataset = "issues"
	DatasetLogs         Dataset = "logs"
	DatasetTransactions Dataset = "transactions"
)

type ScopeKind string

const (
	ScopeKindOrganization ScopeKind = "organization"
	ScopeKindProject      ScopeKind = "project"
)

type Query struct {
	Version   int          `json:"version"`
	Dataset   Dataset      `json:"dataset"`
	Scope     Scope        `json:"scope"`
	Select    []SelectItem `json:"select,omitempty"`
	Where     *Predicate   `json:"where,omitempty"`
	GroupBy   []Expression `json:"group_by,omitempty"`
	Rollup    *Rollup      `json:"rollup,omitempty"`
	OrderBy   []OrderBy    `json:"order_by,omitempty"`
	TimeRange *TimeRange   `json:"time_range,omitempty"`
	Limit     int          `json:"limit,omitempty"`
	Cursor    string       `json:"cursor,omitempty"`
}

type Scope struct {
	Kind         ScopeKind `json:"kind"`
	Organization string    `json:"organization,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	ProjectIDs   []string  `json:"project_ids,omitempty"`
}

type SelectItem struct {
	Alias string     `json:"alias,omitempty"`
	Expr  Expression `json:"expr"`
}

type Expression struct {
	Field string       `json:"field,omitempty"`
	Call  string       `json:"call,omitempty"`
	Args  []Expression `json:"args,omitempty"`
	Alias string       `json:"alias,omitempty"`
}

type Predicate struct {
	Op     string      `json:"op"`
	Args   []Predicate `json:"args,omitempty"`
	Field  string      `json:"field,omitempty"`
	Value  string      `json:"value,omitempty"`
	Values []string    `json:"values,omitempty"`
}

type OrderBy struct {
	Expr      Expression `json:"expr"`
	Direction string     `json:"direction,omitempty"`
	Nulls     string     `json:"nulls,omitempty"`
}

type TimeRange struct {
	Kind  string `json:"kind"`
	Value string `json:"value,omitempty"`
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type Rollup struct {
	Interval string `json:"interval"`
	Buckets  int    `json:"buckets,omitempty"`
}

type CostClass string

const (
	CostClassCheap       CostClass = "cheap"
	CostClassInteractive CostClass = "interactive"
	CostClassReject      CostClass = "reject"
)

type CostEstimate struct {
	Class  CostClass `json:"class"`
	Score  int       `json:"score"`
	Reason string    `json:"reason,omitempty"`
}

type ValidationError struct {
	Code    string `json:"code"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

type ValidationErrors []ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return ""
	}
	var b strings.Builder
	for i, item := range e {
		if i > 0 {
			b.WriteString("; ")
		}
		if item.Path != "" {
			b.WriteString(item.Path)
			b.WriteString(": ")
		}
		b.WriteString(item.Message)
	}
	return b.String()
}

func MarshalQuery(query Query) ([]byte, error) {
	normalized, _, err := ValidateQuery(query)
	if err != nil {
		return nil, err
	}
	return json.Marshal(normalized)
}

func UnmarshalQuery(data []byte) (Query, CostEstimate, error) {
	var query Query
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&query); err != nil {
		return Query{}, CostEstimate{}, fmt.Errorf("decode discover query: %w", err)
	}
	return ValidateQuery(query)
}

func normalizeQuery(query Query) Query {
	query.Version = CurrentVersion
	query.Dataset = Dataset(strings.ToLower(strings.TrimSpace(string(query.Dataset))))
	query.Scope.Kind = ScopeKind(strings.ToLower(strings.TrimSpace(string(query.Scope.Kind))))
	query.Scope.Organization = strings.TrimSpace(query.Scope.Organization)
	query.Scope.ProjectID = strings.TrimSpace(query.Scope.ProjectID)
	if len(query.Scope.ProjectIDs) > 0 {
		ids := make([]string, 0, len(query.Scope.ProjectIDs))
		seen := make(map[string]struct{}, len(query.Scope.ProjectIDs))
		for _, item := range query.Scope.ProjectIDs {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if _, ok := seen[item]; ok {
				continue
			}
			seen[item] = struct{}{}
			ids = append(ids, item)
		}
		slices.Sort(ids)
		query.Scope.ProjectIDs = ids
	}
	if query.Limit == 0 {
		query.Limit = 50
	}
	for i := range query.Select {
		query.Select[i].Alias = strings.TrimSpace(query.Select[i].Alias)
		query.Select[i].Expr = normalizeExpr(query.Select[i].Expr)
	}
	for i := range query.GroupBy {
		query.GroupBy[i] = normalizeExpr(query.GroupBy[i])
	}
	for i := range query.OrderBy {
		query.OrderBy[i].Expr = normalizeExpr(query.OrderBy[i].Expr)
		query.OrderBy[i].Direction = strings.ToLower(strings.TrimSpace(query.OrderBy[i].Direction))
		if query.OrderBy[i].Direction == "" {
			query.OrderBy[i].Direction = "asc"
		}
		query.OrderBy[i].Nulls = strings.ToLower(strings.TrimSpace(query.OrderBy[i].Nulls))
		if query.OrderBy[i].Nulls == "" {
			query.OrderBy[i].Nulls = "last"
		}
	}
	if query.Where != nil {
		pred := normalizePredicate(*query.Where)
		query.Where = &pred
	}
	if query.TimeRange != nil {
		query.TimeRange.Kind = strings.ToLower(strings.TrimSpace(query.TimeRange.Kind))
		query.TimeRange.Value = strings.TrimSpace(query.TimeRange.Value)
		query.TimeRange.Start = strings.TrimSpace(query.TimeRange.Start)
		query.TimeRange.End = strings.TrimSpace(query.TimeRange.End)
	}
	if query.Rollup != nil {
		query.Rollup.Interval = strings.TrimSpace(query.Rollup.Interval)
	}
	query.Cursor = strings.TrimSpace(query.Cursor)
	return query
}

func normalizeExpr(expr Expression) Expression {
	expr.Field = strings.ToLower(strings.TrimSpace(expr.Field))
	expr.Call = strings.ToLower(strings.TrimSpace(expr.Call))
	expr.Alias = strings.TrimSpace(expr.Alias)
	for i := range expr.Args {
		expr.Args[i] = normalizeExpr(expr.Args[i])
	}
	return expr
}

func normalizePredicate(pred Predicate) Predicate {
	pred.Op = strings.ToLower(strings.TrimSpace(pred.Op))
	pred.Field = strings.ToLower(strings.TrimSpace(pred.Field))
	pred.Value = strings.TrimSpace(pred.Value)
	for i := range pred.Values {
		pred.Values[i] = strings.TrimSpace(pred.Values[i])
	}
	for i := range pred.Args {
		pred.Args[i] = normalizePredicate(pred.Args[i])
	}
	return pred
}
