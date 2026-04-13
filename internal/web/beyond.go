package web

import (
	"context"
	"fmt"
	"time"
)

// ---------------------------------------------------------------------------
// Beyond-Sentry Feature: Time-to-First-Event
// ---------------------------------------------------------------------------

// timeToFirstEvent returns a human-readable string describing how long after
// server start the first real event was received. Returns "" if no events exist.
func (h *Handler) timeToFirstEvent(ctx context.Context) string {
	if h.webStore == nil {
		return ""
	}
	firstTime, err := h.webStore.FirstEventAt(ctx)
	if err != nil || firstTime == nil || firstTime.IsZero() {
		return ""
	}

	d := firstTime.Sub(h.startedAt)
	if d < 0 {
		// Event predates this server boot — show absolute time.
		return fmt.Sprintf("First event: %s", firstTime.Format("Jan 2 15:04"))
	}

	switch {
	case d < time.Minute:
		return fmt.Sprintf("First event received %ds after start", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("First event received %dm after start", int(d.Minutes()))
	default:
		return fmt.Sprintf("First event received %dh after start", int(d.Hours()))
	}
}

// ---------------------------------------------------------------------------
// Beyond-Sentry Feature: Error Budget
// ---------------------------------------------------------------------------

// ErrorBudgetData holds the computed error budget for display on the dashboard.
type ErrorBudgetData struct {
	TargetPct   float64 // configured target, e.g. 1.0 = 1%
	ActualPct   float64 // actual error rate as percentage
	FillWidth   int     // 0-100 for CSS width
	IsOver      bool    // true if over budget
	TotalEvents int
	ErrorEvents int
	Label       string // e.g. "0.8% of 1.0% budget"
}

// computeErrorBudget calculates the error budget for the dashboard.
// Target default: 1% of events should be errors.
func (h *Handler) computeErrorBudget(ctx context.Context) *ErrorBudgetData {
	if h.webStore == nil {
		return nil
	}

	totalEvents, _ := h.webStore.CountEvents(ctx)
	if totalEvents == 0 {
		return &ErrorBudgetData{
			TargetPct:   1.0,
			ActualPct:   0,
			FillWidth:   0,
			IsOver:      false,
			TotalEvents: 0,
			ErrorEvents: 0,
			Label:       "No events yet",
		}
	}

	// Count error-level events (error + fatal).
	errorEvents, err := h.webStore.CountErrorLevelEvents(ctx)
	if err != nil {
		return nil
	}

	target := 1.0 // 1% error budget
	actual := float64(errorEvents) / float64(totalEvents) * 100.0
	fillWidth := int(actual / target * 100)
	if fillWidth > 100 {
		fillWidth = 100
	}

	return &ErrorBudgetData{
		TargetPct:   target,
		ActualPct:   actual,
		FillWidth:   fillWidth,
		IsOver:      actual > target,
		TotalEvents: totalEvents,
		ErrorEvents: errorEvents,
		Label:       fmt.Sprintf("%.1f%% of %.1f%% budget", actual, target),
	}
}

// ---------------------------------------------------------------------------
// Beyond-Sentry Feature: Issue Diff
// ---------------------------------------------------------------------------

// IssueDiffEntry represents a change between the first and latest event.
type IssueDiffEntry struct {
	Key      string
	OldValue string
	NewValue string
}

// computeIssueDiff compares the first and latest events for a group,
// returning any differences in tags, user, release, environment, etc.
func (h *Handler) computeIssueDiff(ctx context.Context, groupID string) []IssueDiffEntry {
	if h.webStore == nil {
		return nil
	}

	first, latest, err := h.webStore.IssueDiffBase(ctx, groupID)
	if err != nil || first == nil || latest == nil {
		return nil
	}

	var diffs []IssueDiffEntry

	if first.Level != latest.Level && first.Level != "" {
		diffs = append(diffs, IssueDiffEntry{Key: "Level", OldValue: first.Level, NewValue: latest.Level})
	}
	if first.Release != latest.Release && first.Release != "" {
		diffs = append(diffs, IssueDiffEntry{Key: "Release", OldValue: first.Release, NewValue: latest.Release})
	}
	if first.Environment != latest.Environment && first.Environment != "" {
		diffs = append(diffs, IssueDiffEntry{Key: "Environment", OldValue: first.Environment, NewValue: latest.Environment})
	}
	if first.UserIdentifier != latest.UserIdentifier && first.UserIdentifier != "" {
		diffs = append(diffs, IssueDiffEntry{Key: "User", OldValue: first.UserIdentifier, NewValue: latest.UserIdentifier})
	}

	return diffs
}
