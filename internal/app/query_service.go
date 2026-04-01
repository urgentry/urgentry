package app

import (
	"database/sql"

	"urgentry/internal/discoverharness"
	"urgentry/internal/store"
	"urgentry/internal/telemetrybridge"
	"urgentry/internal/telemetryquery"
)

// QueryServiceDeps bundles the pre-constructed stores and collaborators
// needed to create a telemetry query service. All fields are supplied
// by the runtime composition root so no ad hoc store construction
// happens inside the query service factory.
type QueryServiceDeps struct {
	Blobs       store.BlobStore
	IssueSearch telemetryquery.IssueSearchStore
	Web         store.WebStore
	Discover    discoverharness.Executor
	Traces      telemetryquery.TraceReadStore
	Replays     store.ReplayReadStore
	Profiles    store.ProfileReadStore
	Projector   *telemetrybridge.Projector
}

func newTelemetryQueryService(sourceDB, bridgeDB *sql.DB, d QueryServiceDeps) telemetryquery.Service {
	return telemetryquery.NewService(sourceDB, bridgeDB, telemetryquery.Dependencies{
		Blobs:       d.Blobs,
		IssueSearch: d.IssueSearch,
		Web:         d.Web,
		Discover:    d.Discover,
		Traces:      d.Traces,
		Replays:     d.Replays,
		Profiles:    d.Profiles,
		Projector:   d.Projector,
	})
}
