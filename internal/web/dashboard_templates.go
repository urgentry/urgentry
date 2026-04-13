package web

import (
	"fmt"
	"strings"

	"urgentry/internal/sqlite"
)

type dashboardTemplate struct {
	Slug        string
	Name        string
	Description string
	IdealFor    string
	Widgets     []dashboardTemplateWidget
}

type dashboardTemplateWidget struct {
	Title       string
	Description string
	Kind        sqlite.DashboardWidgetKind
	State       discoverBuilderState
}

type dashboardTemplateCard struct {
	Slug        string
	Name        string
	Description string
	IdealFor    string
	WidgetCount int
	Highlights  []string
}

func tinyDashboardTemplates() []dashboardTemplate {
	return []dashboardTemplate{
		{
			Slug:        "ops-triage",
			Name:        "Ops triage starter",
			Description: "Start with the main issue and log views a small team needs every day.",
			IdealFor:    "Teams replacing a hand-built error triage board.",
			Widgets: []dashboardTemplateWidget{
				{
					Title:       "Unresolved issues",
					Description: "Current unresolved issue count.",
					Kind:        sqlite.DashboardWidgetKindStat,
					State: discoverBuilderState{
						Dataset:       "issues",
						Query:         "is:unresolved",
						Visualization: "stat",
						Aggregate:     "count",
					},
				},
				{
					Title:       "Production issues by release",
					Description: "Recent issue count grouped by release.",
					Kind:        sqlite.DashboardWidgetKindTable,
					State: discoverBuilderState{
						Dataset:       "issues",
						Environment:   "production",
						Visualization: "table",
						Aggregate:     "count",
						GroupBy:       "release",
					},
				},
				{
					Title:       "Recent api logs",
					Description: "Latest log rows for fast incident context.",
					Kind:        sqlite.DashboardWidgetKindTable,
					State: discoverBuilderState{
						Dataset:       "logs",
						Query:         "api",
						Visualization: "table",
					},
				},
			},
		},
		{
			Slug:        "release-watch",
			Name:        "Release watch",
			Description: "Track new failures after a deploy without building each widget by hand.",
			IdealFor:    "Teams that ship often and want a release check page.",
			Widgets: []dashboardTemplateWidget{
				{
					Title:       "Production error trend",
					Description: "Hourly issue volume for the last day.",
					Kind:        sqlite.DashboardWidgetKindSeries,
					State: discoverBuilderState{
						Dataset:       "issues",
						Environment:   "production",
						Visualization: "series",
						Aggregate:     "count",
						TimeRange:     "24h",
						Rollup:        "1h",
					},
				},
				{
					Title:       "Errors by release",
					Description: "Issue count grouped by release.",
					Kind:        sqlite.DashboardWidgetKindTable,
					State: discoverBuilderState{
						Dataset:       "issues",
						Environment:   "production",
						Visualization: "table",
						Aggregate:     "count",
						GroupBy:       "release",
					},
				},
				{
					Title:       "Logs by logger",
					Description: "Top production loggers right now.",
					Kind:        sqlite.DashboardWidgetKindTable,
					State: discoverBuilderState{
						Dataset:       "logs",
						Environment:   "production",
						Visualization: "table",
						Aggregate:     "count",
						GroupBy:       "logger",
					},
				},
			},
		},
		{
			Slug:        "performance-pulse",
			Name:        "Performance pulse",
			Description: "Give transactions and profile-heavy teams a useful default view on day one.",
			IdealFor:    "Teams already sending traces and profiles.",
			Widgets: []dashboardTemplateWidget{
				{
					Title:       "P95 transaction duration",
					Description: "Overall p95 latency from recent transactions.",
					Kind:        sqlite.DashboardWidgetKindStat,
					State: discoverBuilderState{
						Dataset:       "transactions",
						Visualization: "stat",
						Aggregate:     "p95(duration.ms)",
						TimeRange:     "24h",
					},
				},
				{
					Title:       "Slow transactions",
					Description: "Transactions ranked by p95 duration.",
					Kind:        sqlite.DashboardWidgetKindTable,
					State: discoverBuilderState{
						Dataset:       "transactions",
						Visualization: "table",
						Aggregate:     "p95(duration.ms)",
						GroupBy:       "transaction",
						TimeRange:     "24h",
					},
				},
				{
					Title:       "Transaction volume",
					Description: "Hourly transaction count.",
					Kind:        sqlite.DashboardWidgetKindSeries,
					State: discoverBuilderState{
						Dataset:       "transactions",
						Visualization: "series",
						Aggregate:     "count",
						TimeRange:     "24h",
						Rollup:        "1h",
					},
				},
			},
		},
	}
}

func dashboardTemplateCards() []dashboardTemplateCard {
	templates := tinyDashboardTemplates()
	cards := make([]dashboardTemplateCard, 0, len(templates))
	for _, item := range templates {
		card := dashboardTemplateCard{
			Slug:        item.Slug,
			Name:        item.Name,
			Description: item.Description,
			IdealFor:    item.IdealFor,
			WidgetCount: len(item.Widgets),
		}
		for _, widget := range item.Widgets {
			card.Highlights = append(card.Highlights, widget.Title)
		}
		cards = append(cards, card)
	}
	return cards
}

func lookupDashboardTemplate(slug string) (dashboardTemplate, bool) {
	for _, item := range tinyDashboardTemplates() {
		if item.Slug == slug {
			return item, true
		}
	}
	return dashboardTemplate{}, false
}

func buildDashboardTemplateInputs(orgSlug string, item dashboardTemplate) ([]sqlite.DashboardWidgetInput, error) {
	inputs := make([]sqlite.DashboardWidgetInput, 0, len(item.Widgets))
	for index, widget := range item.Widgets {
		state := widget.State
		if strings.TrimSpace(state.Visualization) == "" {
			state.Visualization = string(widget.Kind)
		}
		queryDoc, err := buildDiscoverQuery(orgSlug, state, 50)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", widget.Title, err)
		}
		kind := widget.Kind
		if kind == "" {
			kind = inferDashboardWidgetKind(queryDoc)
		}
		inputs = append(inputs, sqlite.DashboardWidgetInput{
			Title:       widget.Title,
			Description: widget.Description,
			Kind:        kind,
			Position:    index + 1,
			Width:       1,
			Height:      1,
			QueryDoc:    queryDoc,
		})
	}
	return inputs, nil
}
