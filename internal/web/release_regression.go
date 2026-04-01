package web

import (
	"strings"

	sharedstore "urgentry/internal/store"
)

type releaseRegressionData struct {
	HasPrevious          bool
	PreviousVersion      string
	ComparisonMetrics    []releaseRegressionMetricRow
	EnvironmentMovements []releaseEnvironmentRegressionRow
	TransactionMovements []releaseTransactionRegressionRow
	DeployImpact         *releaseDeployImpactData
}

type releaseRegressionMetricRow struct {
	Label      string
	Current    string
	Previous   string
	Delta      string
	TrendClass string
}

type releaseEnvironmentRegressionRow struct {
	Environment string
	Current     string
	Previous    string
	Delta       string
	TrendClass  string
}

type releaseTransactionRegressionRow struct {
	Transaction   string
	CurrentP95    string
	PreviousP95   string
	DeltaP95      string
	CurrentCount  string
	PreviousCount string
	DeltaCount    string
	TrendClass    string
}

type releaseDeployImpactData struct {
	Environment        string
	Name               string
	URL                string
	AnchorAt           string
	Window             string
	ErrorsBefore       string
	ErrorsAfter        string
	ErrorsDelta        string
	ErrorTrendClass    string
	TransactionsBefore string
	TransactionsAfter  string
	TransactionsDelta  string
	P95Before          string
	P95After           string
	P95Delta           string
	P95TrendClass      string
}

func newReleaseRegressionData(summary *sharedstore.ReleaseRegressionSummary) *releaseRegressionData {
	if summary == nil {
		return nil
	}
	data := &releaseRegressionData{}
	if summary.Previous != nil {
		data.HasPrevious = true
		data.PreviousVersion = summary.Previous.Version
		data.ComparisonMetrics = []releaseRegressionMetricRow{
			{
				Label:      "Errored Sessions",
				Current:    formatNumber(summary.Current.ErroredSessions),
				Previous:   formatNumber(summary.Previous.ErroredSessions),
				Delta:      formatSignedInt(summary.Current.ErroredSessions - summary.Previous.ErroredSessions),
				TrendClass: regressionTrendClass(float64(summary.Current.ErroredSessions-summary.Previous.ErroredSessions), false),
			},
			{
				Label:      "Affected Users",
				Current:    formatNumber(summary.Current.AffectedUsers),
				Previous:   formatNumber(summary.Previous.AffectedUsers),
				Delta:      formatSignedInt(summary.Current.AffectedUsers - summary.Previous.AffectedUsers),
				TrendClass: regressionTrendClass(float64(summary.Current.AffectedUsers-summary.Previous.AffectedUsers), false),
			},
			{
				Label:      "Crash-Free",
				Current:    formatPercent(summary.Current.CrashFreeRate),
				Previous:   formatPercent(summary.Previous.CrashFreeRate),
				Delta:      formatSignedPercent(summary.Current.CrashFreeRate - summary.Previous.CrashFreeRate),
				TrendClass: regressionTrendClass(summary.Current.CrashFreeRate-summary.Previous.CrashFreeRate, true),
			},
			{
				Label:      "Event Volume",
				Current:    formatNumber(summary.Current.EventCount),
				Previous:   formatNumber(summary.Previous.EventCount),
				Delta:      formatSignedInt(summary.Current.EventCount - summary.Previous.EventCount),
				TrendClass: "neutral",
			},
		}
		for _, item := range summary.EnvironmentMovements {
			data.EnvironmentMovements = append(data.EnvironmentMovements, releaseEnvironmentRegressionRow{
				Environment: item.Environment,
				Current:     formatNumber(item.CurrentErrors),
				Previous:    formatNumber(item.PreviousErrors),
				Delta:       formatSignedInt(item.DeltaErrors),
				TrendClass:  regressionTrendClass(float64(item.DeltaErrors), false),
			})
		}
		for _, item := range summary.TransactionMovements {
			data.TransactionMovements = append(data.TransactionMovements, releaseTransactionRegressionRow{
				Transaction:   item.Transaction,
				CurrentP95:    formatTraceDuration(item.CurrentP95),
				PreviousP95:   formatTraceDuration(item.PreviousP95),
				DeltaP95:      formatSignedDuration(item.DeltaP95),
				CurrentCount:  formatNumber(item.CurrentCount),
				PreviousCount: formatNumber(item.PreviousCount),
				DeltaCount:    formatSignedInt(item.DeltaCount),
				TrendClass:    regressionTrendClass(item.DeltaP95, false),
			})
		}
	}
	if summary.LatestDeployImpact != nil {
		data.DeployImpact = &releaseDeployImpactData{
			Environment:        firstNonEmptyText(summary.LatestDeployImpact.Deploy.Environment, "default"),
			Name:               firstNonEmptyText(summary.LatestDeployImpact.Deploy.Name, summary.LatestDeployImpact.Deploy.ReleaseVersion),
			URL:                summary.LatestDeployImpact.Deploy.URL,
			AnchorAt:           formatReleaseTimestamp(summary.LatestDeployImpact.AnchorAt),
			Window:             formatNumber(summary.LatestDeployImpact.WindowHours) + "h before vs after",
			ErrorsBefore:       formatNumber(summary.LatestDeployImpact.ErrorsBefore),
			ErrorsAfter:        formatNumber(summary.LatestDeployImpact.ErrorsAfter),
			ErrorsDelta:        formatSignedInt(summary.LatestDeployImpact.ErrorDelta),
			ErrorTrendClass:    regressionTrendClass(float64(summary.LatestDeployImpact.ErrorDelta), false),
			TransactionsBefore: formatNumber(summary.LatestDeployImpact.TransactionsBefore),
			TransactionsAfter:  formatNumber(summary.LatestDeployImpact.TransactionsAfter),
			TransactionsDelta:  formatSignedInt(summary.LatestDeployImpact.TransactionDelta),
			P95Before:          formatTraceDuration(summary.LatestDeployImpact.P95Before),
			P95After:           formatTraceDuration(summary.LatestDeployImpact.P95After),
			P95Delta:           formatSignedDuration(summary.LatestDeployImpact.P95Delta),
			P95TrendClass:      regressionTrendClass(summary.LatestDeployImpact.P95Delta, false),
		}
	}
	if !data.HasPrevious && data.DeployImpact == nil {
		return nil
	}
	return data
}

func regressionTrendClass(delta float64, positiveGood bool) string {
	switch {
	case delta == 0:
		return "neutral"
	case positiveGood && delta > 0:
		return "good"
	case positiveGood:
		return "bad"
	case delta > 0:
		return "bad"
	default:
		return "good"
	}
}

func formatSignedInt(value int) string {
	switch {
	case value > 0:
		return "+" + formatNumber(value)
	case value < 0:
		return "-" + formatNumber(-value)
	default:
		return "0"
	}
}

func formatSignedPercent(value float64) string {
	switch {
	case value > 0:
		return "+" + strings.TrimSpace(formatPercent(value))
	case value < 0:
		return "-" + strings.TrimSpace(formatPercent(-value))
	default:
		return "0%"
	}
}

func formatSignedDuration(value float64) string {
	switch {
	case value > 0:
		return "+" + formatTraceDuration(value)
	case value < 0:
		return "-" + strings.TrimPrefix(formatTraceDuration(-value), "-")
	default:
		return "0 ms"
	}
}
