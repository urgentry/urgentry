package web

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// ---------------------------------------------------------------------------
// Breadcrumb timeline parser
// ---------------------------------------------------------------------------

// breadcrumbPayload mirrors the Sentry breadcrumb JSON wire format.
type breadcrumbPayload struct {
	Breadcrumbs *struct {
		Values []struct {
			Timestamp interface{}            `json:"timestamp"`
			Category  string                 `json:"category"`
			Message   string                 `json:"message"`
			Level     string                 `json:"level"`
			Type      string                 `json:"type"`
			Data      map[string]interface{} `json:"data"`
		} `json:"values"`
	} `json:"breadcrumbs"`
}

// parseBreadcrumbs extracts breadcrumbs from the normalized event JSON and
// computes relative timestamps against the event time.
func parseBreadcrumbs(payloadJSON []byte, eventTime time.Time) []breadcrumb {
	if len(payloadJSON) == 0 {
		return nil
	}
	var payload breadcrumbPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil || payload.Breadcrumbs == nil {
		return nil
	}

	bcs := make([]breadcrumb, 0, len(payload.Breadcrumbs.Values))
	for _, v := range payload.Breadcrumbs.Values {
		bc := breadcrumb{
			Category: v.Category,
			Message:  v.Message,
			Level:    v.Level,
			Type:     v.Type,
		}
		if bc.Level == "" {
			bc.Level = "info"
		}
		if bc.Type == "" {
			bc.Type = "default"
		}

		// Parse timestamp (string RFC3339 or float64 unix epoch).
		var ts time.Time
		switch raw := v.Timestamp.(type) {
		case string:
			if t, err := time.Parse(time.RFC3339, raw); err == nil {
				ts = t
			} else if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				ts = t
			}
		case float64:
			sec := int64(raw)
			nsec := int64((raw - float64(sec)) * 1e9)
			ts = time.Unix(sec, nsec).UTC()
		}

		if !ts.IsZero() {
			bc.Time = ts.Format("15:04:05")
			if !eventTime.IsZero() {
				bc.TimestampRel = relativeTimeBefore(ts, eventTime)
			}
		}

		// Flatten Data map to string values for display.
		if len(v.Data) > 0 {
			bc.Data = make(map[string]string, len(v.Data))
			for k, val := range v.Data {
				bc.Data[k] = fmt.Sprintf("%v", val)
			}
		}

		bcs = append(bcs, bc)
	}
	return bcs
}

// relativeTimeBefore returns a human-readable relative time string like
// "2.3s before" or "150ms before". If the breadcrumb is after the event
// it returns "at event time" or "after event".
func relativeTimeBefore(bcTime, eventTime time.Time) string {
	diff := eventTime.Sub(bcTime)
	if diff < 0 {
		// Breadcrumb after event time — unusual but possible.
		absDiff := -diff
		if absDiff < 10*time.Millisecond {
			return "at event time"
		}
		return formatDuration(absDiff) + " after"
	}
	if diff < 10*time.Millisecond {
		return "at event time"
	}
	return formatDuration(diff) + " before"
}

// formatDuration returns a compact human-readable duration: "150ms", "2.3s", "1m 5s", etc.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		secs := d.Seconds()
		// Show one decimal for sub-10s durations.
		if secs < 10 {
			return fmt.Sprintf("%.1fs", secs)
		}
		return fmt.Sprintf("%.0fs", math.Round(secs))
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm %ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	if mins == 0 {
		return fmt.Sprintf("%dh", hours)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}

// breadcrumbCategoryClass returns a CSS class for category badge coloring.
func breadcrumbCategoryClass(category string) string {
	switch category {
	case "navigation":
		return "bc-cat-navigation"
	case "http":
		return "bc-cat-http"
	case "console":
		return "bc-cat-console"
	case "ui", "ui.click", "ui.input":
		return "bc-cat-ui"
	case "user":
		return "bc-cat-user"
	default:
		return "bc-cat-default"
	}
}
