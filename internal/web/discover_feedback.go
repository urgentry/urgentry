package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"urgentry/internal/discover"
	"urgentry/internal/sqlite"
)

type discoverExplainView struct {
	CostLabel string
	QueryJSON string
	Notes     []string
}

func buildDiscoverExplain(query discover.Query) discoverExplainView {
	view := discoverExplainView{}
	normalized, cost, err := discover.ValidateQuery(query)
	if err == nil {
		query = normalized
	}
	if raw, marshalErr := json.MarshalIndent(query, "", "  "); marshalErr == nil {
		view.QueryJSON = string(raw)
	}
	if cost.Class != "" {
		view.CostLabel = fmt.Sprintf("Estimated planner cost: %s (%d)", cost.Class, cost.Score)
		if strings.TrimSpace(cost.Reason) != "" {
			view.Notes = append(view.Notes, cost.Reason)
		}
	}
	var validationErrs discover.ValidationErrors
	if errors.As(err, &validationErrs) {
		for _, item := range validationErrs {
			view.Notes = append(view.Notes, discoverValidationLine(item))
		}
	}
	return view
}

func discoverErrorFeedback(err error) (string, []string) {
	if err == nil {
		return "", nil
	}
	var validationErrs discover.ValidationErrors
	if errors.As(err, &validationErrs) {
		details := make([]string, 0, len(validationErrs))
		for _, item := range validationErrs {
			details = append(details, discoverValidationLine(item))
		}
		summary := "Query validation failed."
		if len(validationErrs) == 1 {
			summary = validationErrs[0].Message
		}
		return summary, details
	}
	return err.Error(), []string{err.Error()}
}

func discoverGuardFeedback(decision sqlite.QueryGuardDecision) (string, []string) {
	summary := strings.TrimSpace(decision.Reason)
	if summary == "" {
		summary = "Query blocked by guardrails."
	}
	var details []string
	if decision.Cost > 0 {
		details = append(details, fmt.Sprintf("Estimated guard cost: %d units.", decision.Cost))
	}
	if decision.RetryAfter > 0 {
		details = append(details, fmt.Sprintf("Retry after %ds.", int(math.Ceil(decision.RetryAfter.Seconds()))))
	}
	return summary, details
}

func discoverValidationLine(item discover.ValidationError) string {
	if strings.TrimSpace(item.Path) == "" {
		return item.Message
	}
	return item.Path + ": " + item.Message
}
