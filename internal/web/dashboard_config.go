package web

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

var dashboardNumberPattern = regexp.MustCompile(`-?\d+(?:\.\d+)?`)

type dashboardPresentationConfig struct {
	RefreshSeconds int                         `json:"refreshSeconds,omitempty"`
	Filters        dashboardFilterConfig       `json:"filters,omitempty"`
	Annotations    []dashboardAnnotationConfig `json:"annotations,omitempty"`
}

type dashboardFilterConfig struct {
	Environment string `json:"environment,omitempty"`
	Release     string `json:"release,omitempty"`
	Transaction string `json:"transaction,omitempty"`
}

type dashboardAnnotationConfig struct {
	Level string `json:"level,omitempty"`
	Text  string `json:"text"`
}

type widgetPresentationConfig struct {
	Thresholds dashboardThresholdConfig `json:"thresholds,omitempty"`
}

type dashboardThresholdConfig struct {
	Warning   float64 `json:"warning,omitempty"`
	Critical  float64 `json:"critical,omitempty"`
	Direction string  `json:"direction,omitempty"`
}

func decodeDashboardConfig(raw json.RawMessage) dashboardPresentationConfig {
	var cfg dashboardPresentationConfig
	if len(raw) == 0 {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return dashboardPresentationConfig{}
	}
	cfg.RefreshSeconds = normalizeDashboardRefresh(cfg.RefreshSeconds)
	cfg.Filters.Environment = strings.TrimSpace(cfg.Filters.Environment)
	cfg.Filters.Release = strings.TrimSpace(cfg.Filters.Release)
	cfg.Filters.Transaction = strings.TrimSpace(cfg.Filters.Transaction)
	out := make([]dashboardAnnotationConfig, 0, len(cfg.Annotations))
	for _, item := range cfg.Annotations {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		out = append(out, dashboardAnnotationConfig{
			Level: normalizeDashboardAnnotationLevel(item.Level),
			Text:  text,
		})
	}
	cfg.Annotations = out
	return cfg
}

func decodeWidgetConfig(raw json.RawMessage) widgetPresentationConfig {
	var cfg widgetPresentationConfig
	if len(raw) == 0 {
		return cfg
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return widgetPresentationConfig{}
	}
	cfg.Thresholds.Direction = normalizeDashboardThresholdDirection(cfg.Thresholds.Direction)
	if cfg.Thresholds.Warning < 0 {
		cfg.Thresholds.Warning = 0
	}
	if cfg.Thresholds.Critical < 0 {
		cfg.Thresholds.Critical = 0
	}
	return cfg
}

func encodeDashboardConfigFromForm(form url.Values) json.RawMessage {
	cfg := dashboardPresentationConfig{
		RefreshSeconds: normalizeDashboardRefresh(parsePositiveInt(form.Get("refresh_seconds"), 0)),
		Filters: dashboardFilterConfig{
			Environment: strings.TrimSpace(form.Get("filter_environment")),
			Release:     strings.TrimSpace(form.Get("filter_release")),
			Transaction: strings.TrimSpace(form.Get("filter_transaction")),
		},
		Annotations: dashboardAnnotationsFromText(form.Get("annotations")),
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func encodeDashboardWidgetConfigFromForm(form url.Values) json.RawMessage {
	cfg := widgetPresentationConfig{
		Thresholds: dashboardThresholdConfig{
			Warning:   parseDashboardFloat(form.Get("threshold_warning")),
			Critical:  parseDashboardFloat(form.Get("threshold_critical")),
			Direction: normalizeDashboardThresholdDirection(form.Get("threshold_direction")),
		},
	}
	if cfg.Thresholds.Warning == 0 && cfg.Thresholds.Critical == 0 {
		return json.RawMessage(`{}`)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

func dashboardAnnotationsFromText(raw string) []dashboardAnnotationConfig {
	lines := strings.Split(raw, "\n")
	out := make([]dashboardAnnotationConfig, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		level := "note"
		text := line
		if left, right, ok := strings.Cut(line, "|"); ok {
			if normalized := normalizeDashboardAnnotationLevel(left); normalized != "note" || strings.TrimSpace(left) == "note" {
				level = normalized
				text = strings.TrimSpace(right)
			}
		}
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		out = append(out, dashboardAnnotationConfig{Level: level, Text: text})
	}
	return out
}

func dashboardAnnotationsText(items []dashboardAnnotationConfig) string {
	lines := make([]string, 0, len(items))
	for _, item := range items {
		text := strings.TrimSpace(item.Text)
		if text == "" {
			continue
		}
		level := normalizeDashboardAnnotationLevel(item.Level)
		if level == "note" {
			lines = append(lines, text)
			continue
		}
		lines = append(lines, level+"|"+text)
	}
	return strings.Join(lines, "\n")
}

func dashboardQueryWithFilters(query discover.Query, cfg dashboardPresentationConfig) discover.Query {
	if discover.SupportsField(query.Dataset, "environment") {
		query.Where = appendDashboardPredicate(query.Where, cfg.Filters.Environment, "environment")
	}
	if discover.SupportsField(query.Dataset, "release") {
		query.Where = appendDashboardPredicate(query.Where, cfg.Filters.Release, "release")
	}
	if discover.SupportsField(query.Dataset, "transaction") {
		query.Where = appendDashboardPredicate(query.Where, cfg.Filters.Transaction, "transaction")
	}
	return query
}

func appendDashboardPredicate(where *discover.Predicate, value, field string) *discover.Predicate {
	value = strings.TrimSpace(value)
	if value == "" {
		return where
	}
	predicate := discover.Predicate{Op: "=", Field: field, Value: value}
	if where == nil {
		return &predicate
	}
	if strings.EqualFold(where.Op, "and") {
		args := append([]discover.Predicate(nil), where.Args...)
		args = append(args, predicate)
		return &discover.Predicate{Op: "and", Args: args}
	}
	return &discover.Predicate{Op: "and", Args: []discover.Predicate{*where, predicate}}
}

func dashboardFilterSummary(cfg dashboardPresentationConfig) []string {
	var out []string
	if cfg.Filters.Environment != "" {
		out = append(out, "env:"+cfg.Filters.Environment)
	}
	if cfg.Filters.Release != "" {
		out = append(out, "release:"+cfg.Filters.Release)
	}
	if cfg.Filters.Transaction != "" {
		out = append(out, "transaction:"+cfg.Filters.Transaction)
	}
	return out
}

func dashboardRefreshLabel(seconds int) string {
	switch normalizeDashboardRefresh(seconds) {
	case 30:
		return "30 seconds"
	case 60:
		return "1 minute"
	case 300:
		return "5 minutes"
	default:
		return "Manual"
	}
}

func widgetThresholdView(widget sqlite.DashboardWidget, result discoverResultView) (string, string) {
	cfg := decodeWidgetConfig(widget.Config)
	thresholds := cfg.Thresholds
	if result.Type != "stat" || (thresholds.Warning == 0 && thresholds.Critical == 0) {
		return "", ""
	}
	value, ok := parseDashboardNumeric(result.StatValue)
	if !ok {
		return "", thresholdSummary(thresholds)
	}
	switch normalizeDashboardThresholdDirection(thresholds.Direction) {
	case "below":
		if thresholds.Critical > 0 && value <= thresholds.Critical {
			return "threshold-critical", thresholdSummary(thresholds)
		}
		if thresholds.Warning > 0 && value <= thresholds.Warning {
			return "threshold-warning", thresholdSummary(thresholds)
		}
	default:
		if thresholds.Critical > 0 && value >= thresholds.Critical {
			return "threshold-critical", thresholdSummary(thresholds)
		}
		if thresholds.Warning > 0 && value >= thresholds.Warning {
			return "threshold-warning", thresholdSummary(thresholds)
		}
	}
	return "", thresholdSummary(thresholds)
}

func thresholdSummary(cfg dashboardThresholdConfig) string {
	direction := normalizeDashboardThresholdDirection(cfg.Direction)
	operator := ">="
	if direction == "below" {
		operator = "<="
	}
	parts := make([]string, 0, 2)
	if cfg.Warning > 0 {
		parts = append(parts, "warn "+operator+" "+formatDashboardFloat(cfg.Warning))
	}
	if cfg.Critical > 0 {
		parts = append(parts, "critical "+operator+" "+formatDashboardFloat(cfg.Critical))
	}
	return strings.Join(parts, " · ")
}

func parseDashboardNumeric(raw string) (float64, bool) {
	match := dashboardNumberPattern.FindString(strings.TrimSpace(raw))
	if match == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(match, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

func parseDashboardFloat(raw string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || value < 0 {
		return 0
	}
	return value
}

func formatDashboardFloat(value float64) string {
	if value == float64(int64(value)) {
		return strconv.FormatInt(int64(value), 10)
	}
	return strconv.FormatFloat(value, 'f', 2, 64)
}

func normalizeDashboardRefresh(seconds int) int {
	switch seconds {
	case 30, 60, 300:
		return seconds
	default:
		return 0
	}
}

func normalizeDashboardAnnotationLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "info":
		return "info"
	case "warning", "warn":
		return "warning"
	case "critical", "error":
		return "critical"
	default:
		return "note"
	}
}

func normalizeDashboardThresholdDirection(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "below":
		return "below"
	default:
		return "above"
	}
}
