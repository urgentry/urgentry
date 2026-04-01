package web

import (
	"fmt"
	"strings"

	"urgentry/internal/discover"
)

type analyticsStarterView struct {
	Slug        string
	Name        string
	Description string
	Nav         string
	State       discoverBuilderState
	Where       *discover.Predicate
	ProjectOnly bool
	Dataset     string
	OpenURL     string
}

type analyticsStarterViewCard struct {
	Name        string
	Description string
	Dataset     string
	OpenURL     string
}

func analyticsStarterViewCards(nav string) []analyticsStarterViewCard {
	views := tinyAnalyticsStarterViews()
	cards := make([]analyticsStarterViewCard, 0, len(views))
	for _, item := range views {
		if strings.TrimSpace(nav) != "" && item.Nav != nav {
			continue
		}
		cards = append(cards, analyticsStarterViewCard{
			Name:        item.Name,
			Description: item.Description,
			Dataset:     item.Dataset,
			OpenURL:     item.OpenURL,
		})
	}
	return cards
}

func tinyAnalyticsStarterViews() []analyticsStarterView {
	return []analyticsStarterView{
		{
			Slug:        "slow-endpoints",
			Name:        "Slow endpoints",
			Description: "Transactions ranked by p95 latency over the last day.",
			Nav:         "discover",
			State: discoverBuilderState{
				Dataset:       "transactions",
				Visualization: "table",
				Aggregate:     "p95(duration.ms)",
				GroupBy:       "transaction",
				TimeRange:     "24h",
			},
			ProjectOnly: true,
			Dataset:     "transactions",
			OpenURL:     "/discover/starters/slow-endpoints/",
		},
		{
			Slug:        "noisy-loggers",
			Name:        "Noisy loggers",
			Description: "Production loggers with the highest line volume right now.",
			Nav:         "logs",
			State: discoverBuilderState{
				Dataset:       "logs",
				Visualization: "table",
				Aggregate:     "count",
				GroupBy:       "logger",
				TimeRange:     "24h",
				Environment:   "production",
			},
			ProjectOnly: true,
			Dataset:     "logs",
			OpenURL:     "/logs/starters/noisy-loggers/",
		},
		{
			Slug:        "top-failing-endpoints",
			Name:        "Top failing endpoints",
			Description: "Transactions with internal or explicit error statuses over the last day.",
			Nav:         "discover",
			State: discoverBuilderState{
				Dataset:       "transactions",
				Visualization: "table",
				Aggregate:     "count",
				GroupBy:       "transaction",
				TimeRange:     "24h",
			},
			Where: &discover.Predicate{
				Op: "or",
				Args: []discover.Predicate{
					{Op: "=", Field: "status", Value: "internal_error"},
					{Op: "=", Field: "status", Value: "error"},
				},
			},
			ProjectOnly: true,
			Dataset:     "transactions",
			OpenURL:     "/discover/starters/top-failing-endpoints/",
		},
	}
}

func lookupAnalyticsStarterView(slug string) (analyticsStarterView, bool) {
	slug = strings.TrimSpace(slug)
	for _, item := range tinyAnalyticsStarterViews() {
		if item.Slug == slug {
			return item, true
		}
	}
	return analyticsStarterView{}, false
}

func buildAnalyticsStarterQuery(scope pageScope, view analyticsStarterView, limit int) (discover.Query, error) {
	dataset := discover.Dataset(strings.ToLower(strings.TrimSpace(view.State.Dataset)))
	timeRange := strings.TrimSpace(view.State.TimeRange)
	if timeRange == "" && (dataset == discover.DatasetLogs || dataset == discover.DatasetTransactions) {
		timeRange = "24h"
	}
	input := discover.LegacyInput{
		Dataset:      dataset,
		Organization: scope.OrganizationSlug,
		Filter:       view.State.Filter,
		Query:        view.State.Query,
		Environment:  view.State.Environment,
		TimeRange:    timeRange,
		Limit:        limit,
	}
	if view.ProjectOnly {
		if strings.TrimSpace(scope.ProjectID) == "" {
			return discover.Query{}, fmt.Errorf("starter view %q requires a default project scope", view.Slug)
		}
		input.Organization = ""
		input.ProjectID = scope.ProjectID
	}
	base, _, err := discover.ParseLegacy(input)
	if err != nil {
		return discover.Query{}, err
	}
	if timeRange != "" {
		base.TimeRange = &discover.TimeRange{Kind: "relative", Value: timeRange}
	}
	base.Where = mergeStarterPredicate(base.Where, view.Where)
	return applyDiscoverBuilderState(base, dataset, view.State)
}

func mergeStarterPredicate(base, extra *discover.Predicate) *discover.Predicate {
	if extra == nil {
		return base
	}
	if base == nil {
		copy := *extra
		return &copy
	}
	return &discover.Predicate{Op: "and", Args: []discover.Predicate{*base, *extra}}
}
