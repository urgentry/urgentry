package alert

import (
	"context"
	"math"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// anomalyBaselineDays is the number of days used to compute the moving
	// average baseline for anomaly detection.
	anomalyBaselineDays = 7

	// anomalyDeviationFactor is the number of standard deviations above the
	// baseline mean that triggers an anomaly flag.
	anomalyDeviationFactor = 2.0
)

// AnomalyEvent records a detected anomaly for a project.
type AnomalyEvent struct {
	ProjectID  string    `json:"projectId"`
	Metric     string    `json:"metric"`
	Current    float64   `json:"current"`
	Mean       float64   `json:"mean"`
	StdDev     float64   `json:"stdDev"`
	Threshold  float64   `json:"threshold"`
	DetectedAt time.Time `json:"detectedAt"`
}

// DailyCountProvider supplies daily event counts used by the anomaly
// detector to build a baseline. Implementations query the event store
// for a specific project.
type DailyCountProvider interface {
	// DailyErrorCounts returns the error count for each of the last N days,
	// ordered oldest-first. Each element represents one calendar day.
	DailyErrorCounts(ctx context.Context, projectID string, days int, asOf time.Time) ([]float64, error)
}

// ProjectLister lists all project IDs so the detector can scan every
// project on each scheduler tick.
type ProjectLister interface {
	ListProjectIDs(ctx context.Context) ([]string, error)
}

// AnomalyStore persists detected anomaly events.
type AnomalyStore interface {
	RecordAnomaly(ctx context.Context, evt AnomalyEvent) error
}

// AnomalyDetector evaluates whether any project's current error rate
// deviates significantly from its recent baseline.
type AnomalyDetector struct {
	Counts   DailyCountProvider
	Projects ProjectLister
	Store    AnomalyStore
}

// EvaluateAll scans every project and flags anomalies where the current
// day's error count exceeds the 7-day moving average by more than 2
// standard deviations. It returns all detected anomaly events.
func (d *AnomalyDetector) EvaluateAll(ctx context.Context) ([]AnomalyEvent, error) {
	if d.Counts == nil || d.Projects == nil {
		return nil, nil
	}

	projectIDs, err := d.Projects.ListProjectIDs(ctx)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	var anomalies []AnomalyEvent

	for _, pid := range projectIDs {
		evt, err := d.evaluateProject(ctx, pid, now)
		if err != nil {
			log.Warn().Err(err).Str("project_id", pid).Msg("anomaly: evaluation failed for project")
			continue
		}
		if evt != nil {
			anomalies = append(anomalies, *evt)
			if d.Store != nil {
				if sErr := d.Store.RecordAnomaly(ctx, *evt); sErr != nil {
					log.Error().Err(sErr).Str("project_id", pid).Msg("anomaly: failed to persist anomaly event")
				}
			}
		}
	}

	return anomalies, nil
}

// evaluateProject checks a single project for anomalous error rates.
// It requests anomalyBaselineDays+1 days of counts: the first N are
// baseline, the last is today's count being evaluated.
func (d *AnomalyDetector) evaluateProject(ctx context.Context, projectID string, now time.Time) (*AnomalyEvent, error) {
	// Request baseline days + 1 (today).
	counts, err := d.Counts.DailyErrorCounts(ctx, projectID, anomalyBaselineDays+1, now)
	if err != nil {
		return nil, err
	}
	if len(counts) < 2 {
		// Not enough data to establish a baseline.
		return nil, nil
	}

	// The last element is "today"; everything before it is baseline.
	baseline := counts[:len(counts)-1]
	current := counts[len(counts)-1]

	mean, stddev := meanStdDev(baseline)

	// Need at least some variance to detect anomalies. If stddev is zero,
	// only flag if the current value is strictly greater than the mean and
	// the mean is nonzero.
	threshold := mean + anomalyDeviationFactor*stddev
	if stddev == 0 {
		if mean == 0 || current <= mean {
			return nil, nil
		}
		// With zero variance, any increase above zero baseline is anomalous
		// only if it represents at least a doubling.
		if current < mean*2 {
			return nil, nil
		}
	} else if current <= threshold {
		return nil, nil
	}

	return &AnomalyEvent{
		ProjectID:  projectID,
		Metric:     MetricErrorCount,
		Current:    current,
		Mean:       mean,
		StdDev:     stddev,
		Threshold:  threshold,
		DetectedAt: now,
	}, nil
}

// meanStdDev computes the arithmetic mean and population standard deviation
// for a slice of float64 values.
func meanStdDev(vals []float64) (float64, float64) {
	if len(vals) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range vals {
		sum += v
	}
	mean := sum / float64(len(vals))

	variance := 0.0
	for _, v := range vals {
		d := v - mean
		variance += d * d
	}
	variance /= float64(len(vals))
	return mean, math.Sqrt(variance)
}
