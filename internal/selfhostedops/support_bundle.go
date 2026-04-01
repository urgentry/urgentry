package selfhostedops

import (
	"time"

	"urgentry/internal/store"
)

type SupportBundle = store.OperatorDiagnosticsBundle

func BuildSupportBundle(overview *store.OperatorOverview, capturedAt time.Time) *SupportBundle {
	return store.BuildOperatorDiagnosticsBundle(overview, capturedAt)
}
