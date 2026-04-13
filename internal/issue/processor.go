package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"urgentry/internal/grouping"
	"urgentry/internal/metrics"
	"urgentry/internal/nativesym"
	"urgentry/internal/normalize"
	"urgentry/internal/store"
	"urgentry/pkg/id"

	"github.com/rs/zerolog/log"
)

// ReleaseEnsurer creates release records on the fly. Implemented by
// sqlite.ReleaseStore but defined here to avoid an import cycle.
type ReleaseEnsurer interface {
	EnsureRelease(ctx context.Context, orgID, version string) error
}

// SourceMapResolver resolves minified JS source locations to original source
// using uploaded source maps. Implemented by sourcemap.Resolver.
type SourceMapResolver interface {
	Resolve(ctx context.Context, projectID, release, filename string, line, col int) (origFile string, origLine int, origFunc string, err error)
}

// ProGuardResolver resolves obfuscated Java/Android frames using uploaded mappings.
type ProGuardResolver interface {
	Resolve(ctx context.Context, projectID, release, module, function string, line int) (origModule, origFile, origFunc string, origLine int, err error)
}

// NativeSymbolResolver resolves native instruction addresses using uploaded symbol files.
type NativeSymbolResolver interface {
	Resolve(ctx context.Context, req nativesym.LookupRequest) (nativesym.LookupResult, error)
}

// TraceStore persists transactions and spans separately from error issues.
type TraceStore interface {
	SaveTransaction(ctx context.Context, txn *store.StoredTransaction) error
}

// OwnershipResolver resolves issue ownership from project rules.
// When a rule has a team slug and notify_team enabled, the result
// carries both the assignee and the team routing information.
type OwnershipResolver interface {
	ResolveAssignee(ctx context.Context, projectID, title, culprit string, tags map[string]string) (string, error)
	ResolveOwnership(ctx context.Context, projectID, title, culprit string, tags map[string]string) (*store.OwnershipResolveResult, error)
}

type eventUpserter interface {
	UpsertEvent(ctx context.Context, evt *store.StoredEvent) error
}

// Processor ties normalization, grouping, and storage together.
// It takes raw event payloads and produces persisted events and groups.
type Processor struct {
	Events     store.EventStore
	Groups     GroupStore
	Blobs      store.BlobStore
	Releases   ReleaseEnsurer    // nil = release tracking disabled
	SourceMaps SourceMapResolver // nil = source map resolution disabled
	ProGuard   ProGuardResolver  // nil = ProGuard resolution disabled
	Native     NativeSymbolResolver
	Traces     TraceStore
	Ownership  OwnershipResolver
	Metrics    *metrics.Metrics
}

// ProcessResult is the output of processing a single event.
type ProcessResult struct {
	EventID      string
	GroupID      string
	IsNewGroup   bool
	IsRegression bool
	EventType    string
	TraceID      string
	Transaction  string
	DurationMS   float64
	Status       string
}

// Process takes a raw event payload, normalizes it, computes grouping,
// persists the event to blob and event stores, and upserts the group.
func (p *Processor) Process(ctx context.Context, projectID string, raw []byte) (*ProcessResult, error) {
	return p.process(ctx, projectID, raw, "")
}

// ProcessExisting reuses an already-created event row and overwrites it with
// the fully processed event payload.
func (p *Processor) ProcessExisting(ctx context.Context, projectID string, raw []byte, eventRowID string) (*ProcessResult, error) {
	return p.process(ctx, projectID, raw, eventRowID)
}

func (p *Processor) process(ctx context.Context, projectID string, raw []byte, eventRowID string) (*ProcessResult, error) {
	// 1. Normalize
	normalizeStarted := time.Now()
	evt, err := normalize.Normalize(raw)
	if p.Metrics != nil {
		p.Metrics.RecordStage(metrics.StageNormalize, time.Since(normalizeStarted), err)
	}
	if err != nil {
		return nil, fmt.Errorf("normalize: %w", err)
	}

	if evt.EventType() == "transaction" {
		if strings.TrimSpace(eventRowID) != "" {
			return nil, fmt.Errorf("existing event rows are not supported for transactions")
		}
		return p.processTransaction(ctx, projectID, evt, raw)
	}

	ApplyEventResolvers(ctx, projectID, evt, p.SourceMaps, p.ProGuard, p.Native)

	// 2. Compute grouping
	groupingStarted := time.Now()
	gr := grouping.ComputeGrouping(evt)
	if p.Metrics != nil {
		p.Metrics.RecordStage(metrics.StageGrouping, time.Since(groupingStarted), nil)
	}

	// 3. Serialize normalized event
	normalizedJSON, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized event: %w", err)
	}

	// 4. Store raw payload in blob store
	payloadKey := fmt.Sprintf("raw/%s/%s.json", projectID, evt.EventID)
	blobStarted := time.Now()
	err = p.Blobs.Put(ctx, payloadKey, raw)
	if p.Metrics != nil {
		p.Metrics.RecordStage(metrics.StageBlobWrite, time.Since(blobStarted), err)
	}
	if err != nil {
		return nil, fmt.Errorf("blob put: %w", err)
	}

	// 5. Generate internal IDs
	internalEventID := strings.TrimSpace(eventRowID)
	if internalEventID == "" {
		internalEventID = id.New()
	}
	groupID := id.New()
	now := time.Now().UTC()

	// 6. Upsert the group (this may merge with an existing group)
	group := &Group{
		ID:              groupID,
		ProjectID:       projectID,
		GroupingVersion: gr.Version,
		GroupingKey:     gr.GroupingKey,
		Title:           evt.Title(),
		Culprit:         evt.Culprit(),
		Level:           evt.Level,
		FirstSeen:       evt.Timestamp,
		LastSeen:        evt.Timestamp,
		LastEventID:     evt.EventID,
	}

	// Check if group already exists to determine IsNewGroup and regression.
	groupLookupStarted := time.Now()
	existing, err := p.Groups.GetGroupByKey(ctx, projectID, gr.Version, gr.GroupingKey)
	lookupErr := err
	if errors.Is(lookupErr, store.ErrNotFound) {
		lookupErr = nil
	}
	if p.Metrics != nil {
		p.Metrics.RecordStage(metrics.StageGroupLookup, time.Since(groupLookupStarted), lookupErr)
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Errorf("lookup group by key: %w", err)
	}
	if errors.Is(err, store.ErrNotFound) {
		existing = nil
	}
	isNew := existing == nil
	isRegression := existing != nil && existing.Status == "resolved"

	groupUpsertStarted := time.Now()
	err = p.Groups.UpsertGroup(ctx, group)
	if p.Metrics != nil {
		p.Metrics.RecordStage(metrics.StageGroupUpsert, time.Since(groupUpsertStarted), err)
	}
	if err != nil {
		return nil, fmt.Errorf("upsert group: %w", err)
	}

	// After upsert, group.ID is the real ID (existing or newly created)
	realGroupID := group.ID

	// If a resolved issue received a new event, reopen it.
	if isRegression {
		if err := p.Groups.UpdateStatus(ctx, realGroupID, "unresolved"); err != nil {
			log.Warn().Err(err).Str("group_id", realGroupID).Msg("failed to reopen regressed group")
		}
	}
	if p.Ownership != nil && (existing == nil || strings.TrimSpace(existing.Assignee) == "") {
		result, ownershipErr := p.Ownership.ResolveOwnership(ctx, projectID, evt.Title(), evt.Culprit(), evt.Tags)
		if ownershipErr != nil {
			log.Warn().Err(ownershipErr).Str("group_id", realGroupID).Msg("failed to resolve ownership rule")
		} else if result != nil && strings.TrimSpace(result.Assignee) != "" {
			assignee := result.Assignee
			// When the rule routes to a team, prefix the assignee with the team slug.
			if strings.TrimSpace(result.TeamSlug) != "" {
				assignee = "team:" + result.TeamSlug
			}
			if err := p.Groups.UpdateAssignee(ctx, realGroupID, assignee); err != nil {
				log.Warn().Err(err).Str("group_id", realGroupID).Msg("failed to apply ownership rule")
			}
			if result.NotifyTeam && strings.TrimSpace(result.TeamSlug) != "" {
				log.Info().
					Str("group_id", realGroupID).
					Str("team", result.TeamSlug).
					Str("title", evt.Title()).
					Msg("team ownership notification triggered")
			}
		}
	}

	// 7. Track release if present
	releaseVersion := ""
	if evt.Release != "" {
		releaseVersion = evt.Release
		if p.Releases != nil {
			// Use projectID as a stand-in for orgID since events don't carry org info.
			// In a full deployment the project->org mapping would be resolved here.
			if err := p.Releases.EnsureRelease(ctx, projectID, evt.Release); err != nil {
				log.Warn().Err(err).Str("release", evt.Release).Msg("failed to ensure release record")
			}
		}
	}

	// 8. Extract user identifier
	userID := ""
	if evt.User != nil {
		userID = evt.User.ID
		if userID == "" {
			userID = evt.User.Email
		}
		if userID == "" {
			userID = evt.User.IPAddress
		}
	}

	// 9. Save event
	storedEvt := &store.StoredEvent{
		ID:               internalEventID,
		ProjectID:        projectID,
		EventID:          evt.EventID,
		GroupID:          realGroupID,
		ReleaseID:        releaseVersion,
		Environment:      evt.Environment,
		Platform:         evt.Platform,
		Level:            evt.Level,
		EventType:        evt.EventType(),
		OccurredAt:       evt.Timestamp,
		IngestedAt:       now,
		Message:          evt.Message,
		Title:            evt.Title(),
		Culprit:          evt.Culprit(),
		Fingerprint:      evt.Fingerprint,
		Tags:             evt.Tags,
		NormalizedJSON:   normalizedJSON,
		PayloadKey:       payloadKey,
		UserIdentifier:   userID,
		ProcessingStatus: store.EventProcessingStatusCompleted,
		IngestError:      "",
	}

	if strings.TrimSpace(eventRowID) != "" {
		upserter, ok := p.Events.(eventUpserter)
		if !ok {
			return nil, fmt.Errorf("event store does not support event row reuse")
		}
		eventWriteStarted := time.Now()
		err = upserter.UpsertEvent(ctx, storedEvt)
		if p.Metrics != nil {
			p.Metrics.RecordStage(metrics.StageEventWrite, time.Since(eventWriteStarted), err)
		}
		if err != nil {
			return nil, fmt.Errorf("upsert event: %w", err)
		}
	} else {
		eventWriteStarted := time.Now()
		err = p.Events.SaveEvent(ctx, storedEvt)
		if p.Metrics != nil {
			p.Metrics.RecordStage(metrics.StageEventWrite, time.Since(eventWriteStarted), err)
		}
		if err != nil {
			return nil, fmt.Errorf("save event: %w", err)
		}
	}

	return &ProcessResult{
		EventID:      evt.EventID,
		GroupID:      realGroupID,
		IsNewGroup:   isNew,
		IsRegression: isRegression,
		EventType:    evt.EventType(),
		Status:       evt.Level,
	}, nil
}

func (p *Processor) processTransaction(ctx context.Context, projectID string, evt *normalize.Event, raw []byte) (*ProcessResult, error) {
	if p.Traces == nil {
		return nil, fmt.Errorf("trace store is not configured")
	}
	normalizedJSON, err := json.Marshal(evt)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized transaction: %w", err)
	}
	storedJSON, err := marshalStoredTransactionPayload(normalizedJSON, evt.Tags)
	if err != nil {
		return nil, fmt.Errorf("marshal stored transaction payload: %w", err)
	}
	payloadKey := fmt.Sprintf("raw/%s/%s.json", projectID, evt.EventID)
	if err := p.Blobs.Put(ctx, payloadKey, raw); err != nil {
		return nil, fmt.Errorf("blob put: %w", err)
	}

	trace := evt.TraceContext()
	if trace.TraceID == "" {
		trace.TraceID = evt.EventID
	}
	if trace.SpanID == "" {
		trace.SpanID = shortTraceSpanID(evt.EventID)
	}

	startedAt := evt.Timestamp
	if evt.StartTimestamp != nil {
		startedAt = *evt.StartTimestamp
	}
	endedAt := evt.Timestamp
	if endedAt.IsZero() {
		endedAt = startedAt
	}
	stored := &store.StoredTransaction{
		ID:             id.New(),
		ProjectID:      projectID,
		EventID:        evt.EventID,
		TraceID:        trace.TraceID,
		SpanID:         trace.SpanID,
		ParentSpanID:   trace.ParentSpanID,
		Transaction:    evt.Transaction,
		Op:             trace.Op,
		Status:         trace.Status,
		Platform:       evt.Platform,
		Environment:    evt.Environment,
		ReleaseID:      evt.Release,
		StartTimestamp: startedAt,
		EndTimestamp:   endedAt,
		DurationMS:     endedAt.Sub(startedAt).Seconds() * 1000,
		Tags:           evt.Tags,
		Measurements:   make(map[string]store.StoredMeasurement, len(evt.Measurements)),
		NormalizedJSON: storedJSON,
		PayloadKey:     payloadKey,
	}
	for key, measurement := range evt.Measurements {
		stored.Measurements[key] = store.StoredMeasurement{
			Value: measurement.Value,
			Unit:  measurement.Unit,
		}
	}
	for _, span := range evt.Spans {
		start := span.StartTimestamp
		end := span.Timestamp
		if start.IsZero() || end.IsZero() {
			continue
		}
		stored.Spans = append(stored.Spans, store.StoredSpan{
			ID:                 id.New(),
			ProjectID:          projectID,
			TransactionEventID: evt.EventID,
			TraceID:            firstNonEmpty(span.TraceID, trace.TraceID),
			SpanID:             span.SpanID,
			ParentSpanID:       span.ParentSpanID,
			Op:                 span.Op,
			Description:        span.Description,
			Status:             span.Status,
			StartTimestamp:     start,
			EndTimestamp:       end,
			DurationMS:         end.Sub(start).Seconds() * 1000,
			Tags:               span.Tags,
			Data:               span.Data,
		})
	}
	if err := p.Traces.SaveTransaction(ctx, stored); err != nil {
		return nil, fmt.Errorf("save transaction: %w", err)
	}
	return &ProcessResult{
		EventID:     evt.EventID,
		EventType:   "transaction",
		TraceID:     trace.TraceID,
		Transaction: evt.Transaction,
		DurationMS:  stored.DurationMS,
		Status:      trace.Status,
	}, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func shortTraceSpanID(eventID string) string {
	if len(eventID) <= 16 {
		return eventID
	}
	return eventID[:16]
}

func marshalStoredTransactionPayload(normalizedJSON []byte, tags map[string]string) (json.RawMessage, error) {
	if len(tags) == 0 {
		return json.RawMessage(normalizedJSON), nil
	}

	var payload map[string]any
	if err := json.Unmarshal(normalizedJSON, &payload); err != nil {
		return nil, err
	}
	payload["tags"] = sentryTagList(tags)

	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

func sentryTagList(tags map[string]string) []map[string]string {
	keys := make([]string, 0, len(tags))
	for key := range tags {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]map[string]string, 0, len(keys))
	for _, key := range keys {
		items = append(items, map[string]string{
			"key":   key,
			"value": tags[key],
		})
	}
	return items
}
