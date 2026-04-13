package telemetryquery

import (
	"encoding/json"
	"fmt"
	"time"

	"urgentry/internal/discover"
	"urgentry/internal/store"
)

const bridgeReadCacheTTL = 250 * time.Millisecond

type cachedReplayRecord struct {
	expiresAt time.Time
	record    *store.ReplayRecord
}

type cachedProfileRecord struct {
	expiresAt time.Time
	record    *store.ProfileRecord
}

type cachedTransactions struct {
	expiresAt time.Time
	items     []*store.StoredTransaction
}

type cachedSpans struct {
	expiresAt time.Time
	items     []store.StoredSpan
}

type cachedLogs struct {
	expiresAt time.Time
	items     []store.DiscoverLog
}

type cachedTableResult struct {
	expiresAt time.Time
	result    discover.TableResult
}

func replayCacheKey(projectID, replayID string) string {
	return fmt.Sprintf("%s|%s", projectID, replayID)
}

func profileCacheKey(projectID, profileID string) string {
	return fmt.Sprintf("%s|%s", projectID, profileID)
}

func traceCacheKey(projectID, traceID string) string {
	return fmt.Sprintf("%s|%s", projectID, traceID)
}

func logsCacheKey(orgSlug, rawQuery string, limit int) string {
	return fmt.Sprintf("%s|%s|%d", orgSlug, rawQuery, limit)
}

func tableCacheKey(query discover.Query) string {
	body, err := json.Marshal(query)
	if err != nil {
		return ""
	}
	return string(body)
}

func (s *bridgeService) cachedReplay(projectID, replayID string) (*store.ReplayRecord, bool) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.replayCache == nil {
		return nil, false
	}
	item, ok := s.replayCache[replayCacheKey(projectID, replayID)]
	if !ok || time.Now().UTC().After(item.expiresAt) || item.record == nil {
		return nil, false
	}
	return cloneReplayRecord(item.record), true
}

func (s *bridgeService) storeReplayCache(projectID, replayID string, record *store.ReplayRecord) {
	if record == nil {
		return
	}
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.replayCache == nil {
		s.replayCache = make(map[string]cachedReplayRecord, 8)
	}
	s.replayCache[replayCacheKey(projectID, replayID)] = cachedReplayRecord{
		expiresAt: time.Now().UTC().Add(bridgeReadCacheTTL),
		record:    cloneReplayRecord(record),
	}
}

func (s *bridgeService) cachedProfile(projectID, profileID string) (*store.ProfileRecord, bool) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.profileCache == nil {
		return nil, false
	}
	item, ok := s.profileCache[profileCacheKey(projectID, profileID)]
	if !ok || time.Now().UTC().After(item.expiresAt) || item.record == nil {
		return nil, false
	}
	return cloneProfileRecord(item.record), true
}

func (s *bridgeService) storeProfileCache(projectID, profileID string, record *store.ProfileRecord) {
	if record == nil {
		return
	}
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.profileCache == nil {
		s.profileCache = make(map[string]cachedProfileRecord, 8)
	}
	s.profileCache[profileCacheKey(projectID, profileID)] = cachedProfileRecord{
		expiresAt: time.Now().UTC().Add(bridgeReadCacheTTL),
		record:    cloneProfileRecord(record),
	}
}

func (s *bridgeService) cachedTraceTransactions(projectID, traceID string) ([]*store.StoredTransaction, bool) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.traceTxnCache == nil {
		return nil, false
	}
	item, ok := s.traceTxnCache[traceCacheKey(projectID, traceID)]
	if !ok || time.Now().UTC().After(item.expiresAt) {
		return nil, false
	}
	return cloneTransactions(item.items), true
}

func (s *bridgeService) storeTraceTransactions(projectID, traceID string, items []*store.StoredTransaction) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.traceTxnCache == nil {
		s.traceTxnCache = make(map[string]cachedTransactions, 8)
	}
	s.traceTxnCache[traceCacheKey(projectID, traceID)] = cachedTransactions{
		expiresAt: time.Now().UTC().Add(bridgeReadCacheTTL),
		items:     cloneTransactions(items),
	}
}

func (s *bridgeService) cachedTraceSpans(projectID, traceID string) ([]store.StoredSpan, bool) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.traceSpanCache == nil {
		return nil, false
	}
	item, ok := s.traceSpanCache[traceCacheKey(projectID, traceID)]
	if !ok || time.Now().UTC().After(item.expiresAt) {
		return nil, false
	}
	return cloneSpans(item.items), true
}

func (s *bridgeService) storeTraceSpans(projectID, traceID string, items []store.StoredSpan) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.traceSpanCache == nil {
		s.traceSpanCache = make(map[string]cachedSpans, 8)
	}
	s.traceSpanCache[traceCacheKey(projectID, traceID)] = cachedSpans{
		expiresAt: time.Now().UTC().Add(bridgeReadCacheTTL),
		items:     cloneSpans(items),
	}
}

func (s *bridgeService) cachedLogsResult(orgSlug, rawQuery string, limit int) ([]store.DiscoverLog, bool) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.logsCache == nil {
		return nil, false
	}
	item, ok := s.logsCache[logsCacheKey(orgSlug, rawQuery, limit)]
	if !ok || time.Now().UTC().After(item.expiresAt) {
		return nil, false
	}
	return cloneLogs(item.items), true
}

func (s *bridgeService) storeLogsResult(orgSlug, rawQuery string, limit int, items []store.DiscoverLog) {
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.logsCache == nil {
		s.logsCache = make(map[string]cachedLogs, 8)
	}
	s.logsCache[logsCacheKey(orgSlug, rawQuery, limit)] = cachedLogs{
		expiresAt: time.Now().UTC().Add(bridgeReadCacheTTL),
		items:     cloneLogs(items),
	}
}

func (s *bridgeService) cachedTable(query discover.Query) (discover.TableResult, bool) {
	key := tableCacheKey(query)
	if key == "" {
		return discover.TableResult{}, false
	}
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.tableCache == nil {
		return discover.TableResult{}, false
	}
	item, ok := s.tableCache[key]
	if !ok || time.Now().UTC().After(item.expiresAt) {
		return discover.TableResult{}, false
	}
	return cloneTableResult(item.result), true
}

func (s *bridgeService) storeTable(query discover.Query, result discover.TableResult) {
	key := tableCacheKey(query)
	if key == "" {
		return
	}
	s.readCacheMu.Lock()
	defer s.readCacheMu.Unlock()
	if s.tableCache == nil {
		s.tableCache = make(map[string]cachedTableResult, 8)
	}
	s.tableCache[key] = cachedTableResult{
		expiresAt: time.Now().UTC().Add(bridgeReadCacheTTL),
		result:    cloneTableResult(result),
	}
}

func cloneReplayRecord(in *store.ReplayRecord) *store.ReplayRecord {
	if in == nil {
		return nil
	}
	out := *in
	out.Manifest = in.Manifest
	out.Manifest.TraceIDs = append([]string(nil), in.Manifest.TraceIDs...)
	out.Manifest.LinkedEventIDs = append([]string(nil), in.Manifest.LinkedEventIDs...)
	out.Manifest.LinkedIssueIDs = append([]string(nil), in.Manifest.LinkedIssueIDs...)
	out.Assets = append([]store.ReplayAssetRef(nil), in.Assets...)
	if len(in.Timeline) > 0 {
		out.Timeline = make([]store.ReplayTimelineItem, len(in.Timeline))
		for i := range in.Timeline {
			out.Timeline[i] = in.Timeline[i]
			out.Timeline[i].MetaJSON = append([]byte(nil), in.Timeline[i].MetaJSON...)
		}
	}
	out.Payload = append([]byte(nil), in.Payload...)
	return &out
}

func cloneProfileRecord(in *store.ProfileRecord) *store.ProfileRecord {
	if in == nil {
		return nil
	}
	out := *in
	out.RawPayload = append([]byte(nil), in.RawPayload...)
	out.Threads = append([]store.ProfileThread(nil), in.Threads...)
	out.Frames = append([]store.ProfileFrame(nil), in.Frames...)
	out.Stacks = append([]store.ProfileStack(nil), in.Stacks...)
	out.StackFrames = append([]store.ProfileStackFrame(nil), in.StackFrames...)
	out.Samples = append([]store.ProfileSample(nil), in.Samples...)
	out.TopFrames = append([]store.ProfileBreakdown(nil), in.TopFrames...)
	out.TopFunctions = append([]store.ProfileBreakdown(nil), in.TopFunctions...)
	return &out
}

func cloneTransactions(items []*store.StoredTransaction) []*store.StoredTransaction {
	if len(items) == 0 {
		return nil
	}
	out := make([]*store.StoredTransaction, 0, len(items))
	for _, item := range items {
		if item == nil {
			out = append(out, nil)
			continue
		}
		cloned := *item
		cloned.Tags = cloneStringMap(item.Tags)
		cloned.Measurements = cloneMeasurements(item.Measurements)
		cloned.Spans = cloneSpans(item.Spans)
		out = append(out, &cloned)
	}
	return out
}

func cloneSpans(items []store.StoredSpan) []store.StoredSpan {
	if len(items) == 0 {
		return nil
	}
	out := make([]store.StoredSpan, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].Tags = cloneStringMap(items[i].Tags)
		out[i].Data = cloneAnyMap(items[i].Data)
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneMeasurements(in map[string]store.StoredMeasurement) map[string]store.StoredMeasurement {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]store.StoredMeasurement, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneLogs(items []store.DiscoverLog) []store.DiscoverLog {
	if len(items) == 0 {
		return nil
	}
	out := make([]store.DiscoverLog, len(items))
	for i := range items {
		out[i] = items[i]
		out[i].Tags = cloneStringMap(items[i].Tags)
	}
	return out
}

func cloneTableResult(in discover.TableResult) discover.TableResult {
	out := in
	out.Columns = append([]discover.Column(nil), in.Columns...)
	if len(in.Rows) > 0 {
		out.Rows = make([]discover.TableRow, len(in.Rows))
		for i := range in.Rows {
			row := make(discover.TableRow, len(in.Rows[i]))
			for k, v := range in.Rows[i] {
				row[k] = v
			}
			out.Rows[i] = row
		}
	}
	return out
}
