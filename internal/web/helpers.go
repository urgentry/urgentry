package web

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func timeAgoClass(t time.Time) string {
	d := time.Since(t)
	if d < 5*time.Minute {
		return "recent"
	}
	if d < time.Hour {
		return "medium"
	}
	return ""
}

func levelColor(level string) string {
	switch level {
	case "fatal":
		return "#FF2D2D"
	case "error":
		return "#FF5555"
	case "warning":
		return "#FFB347"
	case "info":
		return "#5BA4F5"
	default:
		return "#9CA0B0"
	}
}

func formatNumber(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000.0)
	}
	return fmt.Sprintf("%d", n)
}

// timeAgoCompact returns a compact relative time like "3wk", "2mo", "1yr".
// Used for the Age column in the issue table.
func timeAgoCompact(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dwk", int(d.Hours()/(24*7)))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dyr", int(d.Hours()/(24*365)))
	}
}

// statusLabel returns a human-friendly label for a status string.
func statusLabel(status string) string {
	switch status {
	case "resolved":
		return "Resolved"
	case "ignored":
		return "Ignored"
	case "unresolved":
		return "Ongoing"
	default:
		return "Ongoing"
	}
}

func issueStatusLabel(status, resolutionSubstatus, resolvedInRelease string) string {
	if strings.TrimSpace(resolutionSubstatus) == "next_release" {
		if release := strings.TrimSpace(resolvedInRelease); release != "" {
			return "Resolved in " + release
		}
		return "Resolved in next release"
	}
	return statusLabel(status)
}

func issueResolutionLabel(resolutionSubstatus, resolvedInRelease string) string {
	if strings.TrimSpace(resolutionSubstatus) == "next_release" {
		if release := strings.TrimSpace(resolvedInRelease); release != "" {
			return "This issue will be resolved in " + release
		}
		return "This issue will be resolved in the next release"
	}
	return ""
}

// priorityLabel returns a human-friendly label for a priority integer.
func priorityLabel(p int) string {
	switch p {
	case 0:
		return "Critical"
	case 1:
		return "High"
	case 2:
		return "Medium"
	case 3:
		return "Low"
	default:
		return "Medium"
	}
}
