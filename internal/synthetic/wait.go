package synthetic

import (
	"database/sql"
	"fmt"
	"time"
)

type WaitCondition string

const (
	WaitEvent             WaitCondition = "event"
	WaitProjectEventCount WaitCondition = "project_event_count"
	WaitTransactionCount  WaitCondition = "transaction_count"
	WaitTrace             WaitCondition = "trace"
	WaitSessionRelease    WaitCondition = "session_release"
	WaitFeedbackName      WaitCondition = "feedback_name"
	WaitCheckInMonitor    WaitCondition = "checkin_monitor"
)

type WaitSpec struct {
	Condition   WaitCondition `yaml:"condition" json:"condition"`
	ProjectID   string        `yaml:"project_id,omitempty" json:"project_id,omitempty"`
	EventID     string        `yaml:"event_id,omitempty" json:"event_id,omitempty"`
	TraceID     string        `yaml:"trace_id,omitempty" json:"trace_id,omitempty"`
	Release     string        `yaml:"release,omitempty" json:"release,omitempty"`
	Name        string        `yaml:"name,omitempty" json:"name,omitempty"`
	MonitorSlug string        `yaml:"monitor_slug,omitempty" json:"monitor_slug,omitempty"`
	Count       int           `yaml:"count,omitempty" json:"count,omitempty"`
	TimeoutMS   int           `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
}

func WaitFor(db *sql.DB, spec WaitSpec) error {
	timeout := time.Duration(spec.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		ok, err := checkWait(db, spec)
		if err == nil && ok {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("wait condition %s timed out", spec.Condition)
}

func checkWait(db *sql.DB, spec WaitSpec) (bool, error) {
	switch spec.Condition {
	case WaitEvent:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE event_id = ?`, spec.EventID).Scan(&count)
		return count > 0, err
	case WaitProjectEventCount:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE project_id = ?`, spec.ProjectID).Scan(&count)
		return count >= spec.Count, err
	case WaitTransactionCount:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE project_id = ?`, spec.ProjectID).Scan(&count)
		return count >= spec.Count, err
	case WaitTrace:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM transactions WHERE project_id = ? AND trace_id = ?`, spec.ProjectID, spec.TraceID).Scan(&count)
		return count > 0, err
	case WaitSessionRelease:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM release_sessions WHERE project_id = ? AND release_version = ?`, spec.ProjectID, spec.Release).Scan(&count)
		return count > 0, err
	case WaitFeedbackName:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM user_feedback WHERE project_id = ? AND name = ?`, spec.ProjectID, spec.Name).Scan(&count)
		return count > 0, err
	case WaitCheckInMonitor:
		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM monitor_checkins WHERE project_id = ? AND monitor_slug = ?`, spec.ProjectID, spec.MonitorSlug).Scan(&count)
		return count > 0, err
	default:
		return false, fmt.Errorf("unsupported wait condition %q", spec.Condition)
	}
}
