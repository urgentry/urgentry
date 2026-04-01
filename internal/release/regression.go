// Package release provides release lifecycle analysis including crash-free
// regression detection between consecutive releases.
package release

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"urgentry/internal/alert"
)

// DefaultRegressionThreshold is the percentage drop in crash-free rate that
// triggers a regression flag (e.g. 10 means flag when rate drops by >10 points).
const DefaultRegressionThreshold = 10.0

// ReleaseHealth captures crash-free metrics for a single release.
type ReleaseHealth struct {
	Version       string  `json:"version"`
	CrashFreeRate float64 `json:"crashFreeRate"`
	SessionCount  int     `json:"sessionCount"`
	AffectedUsers int     `json:"affectedUsers"`
}

// RegressionResult captures the outcome of a regression comparison.
type RegressionResult struct {
	Current          ReleaseHealth `json:"current"`
	Previous         ReleaseHealth `json:"previous"`
	CrashFreeDelta   float64       `json:"crashFreeDelta"`   // negative = regression
	IsRegression     bool          `json:"isRegression"`
	ThresholdUsed    float64       `json:"thresholdUsed"`
	AlertFired       bool          `json:"alertFired"`
}

// HealthProvider returns crash-free health for a release version.
type HealthProvider interface {
	GetCrashFreeRate(ctx context.Context, projectID, version string) (ReleaseHealth, error)
}

// AlertSink accepts alert signals when a regression is detected.
type AlertSink interface {
	FireSignal(ctx context.Context, sig alert.Signal) error
}

// RegressionDetector compares crash-free rates between consecutive releases.
type RegressionDetector struct {
	health    HealthProvider
	alertSink AlertSink
	threshold float64
}

// NewRegressionDetector creates a detector with the default threshold.
func NewRegressionDetector(health HealthProvider, sink AlertSink) *RegressionDetector {
	return &RegressionDetector{
		health:    health,
		alertSink: sink,
		threshold: DefaultRegressionThreshold,
	}
}

// WithThreshold sets a custom regression threshold in percentage points.
func (d *RegressionDetector) WithThreshold(pct float64) *RegressionDetector {
	d.threshold = pct
	return d
}

// Compare checks the crash-free rate of currentVersion against previousVersion
// for the given project. If the rate dropped by more than the threshold,
// it flags a regression and fires an alert signal.
func (d *RegressionDetector) Compare(ctx context.Context, projectID, currentVersion, previousVersion string) (*RegressionResult, error) {
	current, err := d.health.GetCrashFreeRate(ctx, projectID, currentVersion)
	if err != nil {
		return nil, fmt.Errorf("get current release health: %w", err)
	}
	previous, err := d.health.GetCrashFreeRate(ctx, projectID, previousVersion)
	if err != nil {
		return nil, fmt.Errorf("get previous release health: %w", err)
	}

	delta := current.CrashFreeRate - previous.CrashFreeRate
	isRegression := delta < -d.threshold

	result := &RegressionResult{
		Current:        current,
		Previous:       previous,
		CrashFreeDelta: delta,
		IsRegression:   isRegression,
		ThresholdUsed:  d.threshold,
	}

	if isRegression && d.alertSink != nil {
		sig := alert.Signal{
			ProjectID:     projectID,
			EventType:     alert.EventTypeRelease,
			Release:       currentVersion,
			CrashFreeRate: current.CrashFreeRate,
			SessionCount:  current.SessionCount,
			AffectedUsers: current.AffectedUsers,
			IsRegression:  true,
			Timestamp:     time.Now().UTC(),
		}
		if err := d.alertSink.FireSignal(ctx, sig); err != nil {
			log.Warn().Err(err).
				Str("project", projectID).
				Str("release", currentVersion).
				Msg("failed to fire regression alert")
		} else {
			result.AlertFired = true
		}
	}

	if isRegression {
		log.Warn().
			Str("project", projectID).
			Str("current", currentVersion).
			Str("previous", previousVersion).
			Float64("current_rate", current.CrashFreeRate).
			Float64("previous_rate", previous.CrashFreeRate).
			Float64("delta", delta).
			Msg("release regression detected")
	}

	return result, nil
}
