package telemetrybridge

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"urgentry/internal/profilequery"
	"urgentry/internal/sqlutil"
	"urgentry/internal/store"
)

func scanBridgeProfileManifest(scanner interface{ Scan(dest ...any) error }) (store.ProfileManifest, string, error) {
	var (
		item        store.ProfileManifest
		startedAt   sql.NullTime
		endedAt     sql.NullTime
		createdAt   sql.NullTime
		payloadJSON sql.NullString
	)
	err := scanner.Scan(
		&item.ID,
		&item.ProjectID,
		&item.ProfileID,
		&item.EventID,
		&item.TraceID,
		&item.Transaction,
		&item.Release,
		&item.Environment,
		&item.Platform,
		&item.ProfileKind,
		&startedAt,
		&endedAt,
		&item.DurationNS,
		&item.ThreadCount,
		&item.SampleCount,
		&item.FrameCount,
		&item.FunctionCount,
		&item.StackCount,
		&item.ProcessingStatus,
		&item.IngestError,
		&item.RawBlobKey,
		&createdAt,
		&payloadJSON,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return store.ProfileManifest{}, "", store.ErrNotFound
		}
		return store.ProfileManifest{}, "", err
	}
	if startedAt.Valid {
		item.StartedAt = startedAt.Time.UTC()
	}
	if endedAt.Valid {
		item.EndedAt = endedAt.Time.UTC()
	}
	if createdAt.Valid {
		item.DateCreated = createdAt.Time.UTC()
	}
	return item, payloadJSON.String, nil
}

func hydrateBridgeProfileRecord(manifest store.ProfileManifest, payload []byte) (*store.ProfileRecord, error) {
	if len(payload) == 0 {
		return &store.ProfileRecord{Manifest: manifest}, nil
	}
	parsed, err := normalizeBridgeProfilePayload(payload)
	if err != nil {
		return nil, err
	}
	record := &store.ProfileRecord{
		Manifest:    mergeBridgeProfileManifest(manifest, parsed.Manifest),
		RawPayload:  json.RawMessage(append([]byte(nil), payload...)),
		Threads:     parsed.Threads,
		Frames:      parsed.Frames,
		Stacks:      parsed.Stacks,
		StackFrames: parsed.StackFrames,
		Samples:     parsed.Samples,
	}
	record.TopFrames = profilequery.Breakdowns(record, true, 10)
	record.TopFunctions = profilequery.Breakdowns(record, false, 10)
	return record, nil
}

func mergeBridgeProfileManifest(base, parsed store.ProfileManifest) store.ProfileManifest {
	item := base
	if item.EventID == "" {
		item.EventID = parsed.EventID
	}
	if item.ProfileID == "" {
		item.ProfileID = parsed.ProfileID
	}
	if item.TraceID == "" {
		item.TraceID = parsed.TraceID
	}
	if item.Transaction == "" {
		item.Transaction = parsed.Transaction
	}
	if item.Release == "" {
		item.Release = parsed.Release
	}
	if item.Environment == "" {
		item.Environment = parsed.Environment
	}
	if item.Platform == "" {
		item.Platform = parsed.Platform
	}
	if item.ProfileKind == "" {
		item.ProfileKind = parsed.ProfileKind
	}
	if item.StartedAt.IsZero() {
		item.StartedAt = parsed.StartedAt
	}
	if item.EndedAt.IsZero() {
		item.EndedAt = parsed.EndedAt
	}
	if item.DurationNS == 0 {
		item.DurationNS = parsed.DurationNS
	}
	if item.ThreadCount == 0 {
		item.ThreadCount = parsed.ThreadCount
	}
	if item.SampleCount == 0 {
		item.SampleCount = parsed.SampleCount
	}
	if item.FrameCount == 0 {
		item.FrameCount = parsed.FrameCount
	}
	if item.FunctionCount == 0 {
		item.FunctionCount = parsed.FunctionCount
	}
	if item.StackCount == 0 {
		item.StackCount = parsed.StackCount
	}
	if item.ProcessingStatus == "" {
		item.ProcessingStatus = parsed.ProcessingStatus
	}
	if item.IngestError == "" {
		item.IngestError = parsed.IngestError
	}
	if item.DateCreated.IsZero() {
		item.DateCreated = parsed.DateCreated
	}
	return item
}

type bridgeNormalizedProfile struct {
	Manifest    store.ProfileManifest
	Threads     []store.ProfileThread
	Frames      []store.ProfileFrame
	Stacks      []store.ProfileStack
	StackFrames []store.ProfileStackFrame
	Samples     []store.ProfileSample
}

type profileFrameInput struct {
	FrameLabel    string
	FunctionLabel string
	FunctionName  string
	ModuleName    string
	PackageName   string
	Filename      string
	Lineno        int
	InApp         bool
	ImageRef      string
}

type profileSampleInput struct {
	ThreadKey   string
	ThreadName  string
	IsMain      bool
	TSNS        int64
	Weight      int
	WallTimeNS  int64
	QueueTimeNS int64
	CPUTimeNS   int64
	IsIdle      bool
	Frames      []any
}

type profileThreadMeta struct {
	Name   string
	IsMain bool
}

func normalizeBridgeProfilePayload(raw []byte) (bridgeNormalizedProfile, error) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return bridgeNormalizedProfile{}, fmt.Errorf("parse profile payload: %w", err)
	}

	profileID := strings.TrimSpace(firstJSONString(payload["profile_id"]))
	eventID := strings.TrimSpace(firstJSONString(payload["event_id"]))
	if profileID == "" {
		profileID = eventID
	}
	if eventID == "" {
		eventID = profileID
	}
	if profileID == "" {
		return bridgeNormalizedProfile{}, fmt.Errorf("profile id is required")
	}

	profileObj := firstJSONObject(payload["profile"])
	if profileObj == nil {
		profileObj = payload
	}
	frames := extractProfileFrameInputs(profileObj)
	samples := extractProfileSampleInputs(profileObj, payload)

	result := bridgeNormalizedProfile{
		Manifest: store.ProfileManifest{
			EventID:          eventID,
			ProfileID:        profileID,
			TraceID:          strings.TrimSpace(firstJSONString(payload["trace_id"])),
			Transaction:      strings.TrimSpace(firstJSONString(payload["transaction"])),
			Release:          strings.TrimSpace(firstJSONString(payload["release"])),
			Environment:      strings.TrimSpace(firstJSONString(payload["environment"])),
			Platform:         firstNonEmptyText(strings.TrimSpace(firstJSONString(payload["platform"])), "profile"),
			ProfileKind:      firstNonEmptyText(strings.TrimSpace(firstJSONString(profileObj["profile_type"])), strings.TrimSpace(firstJSONString(payload["profile_type"])), "sampled"),
			StartedAt:        parseOptionalTimeString(firstJSONString(payload["timestamp"])),
			ProcessingStatus: store.ProfileProcessingStatusCompleted,
			DateCreated:      parseOptionalTimeString(firstJSONString(payload["timestamp"])),
		},
	}
	result.Manifest.DurationNS = parseInt64Any(payload["duration_ns"])

	threadMeta := extractProfileThreadMeta(profileObj, payload)
	threadRows := map[string]*store.ProfileThread{}
	frameRows := map[string]*store.ProfileFrame{}
	stackRows := map[string]*store.ProfileStack{}
	functionLabels := map[string]struct{}{}
	var maxSampleTS int64
	invalidReason := ""

	for index, sample := range samples {
		if len(sample.Frames) == 0 {
			invalidReason = "profile sample is missing frames"
			break
		}
		resolvedFrames, ok := resolveSampleFrameRows(sample.Frames, frames, frameRows, profileID)
		if !ok || len(resolvedFrames) == 0 {
			invalidReason = "profile sample references invalid frames"
			break
		}
		threadKey := normalizeThreadKey(sample.ThreadKey, sample.ThreadName)
		meta := threadMeta[threadKey]
		threadName := firstNonEmptyText(sample.ThreadName, meta.Name, threadKey)
		isMain := sample.IsMain || meta.IsMain
		threadRole := inferThreadRole(threadName, isMain)
		threadRow := threadRows[threadKey]
		if threadRow == nil {
			threadRow = &store.ProfileThread{
				ID:         scopedProfileID(profileID, "thread", threadKey),
				ThreadKey:  threadKey,
				ThreadName: threadName,
				ThreadRole: threadRole,
				IsMain:     isMain,
			}
			threadRows[threadKey] = threadRow
		}
		threadRow.SampleCount += sample.Weight
		if sample.TSNS > threadRow.DurationNS {
			threadRow.DurationNS = sample.TSNS
		}
		if sample.TSNS > maxSampleTS {
			maxSampleTS = sample.TSNS
		}

		frameIDs := make([]string, 0, len(resolvedFrames))
		for _, frame := range resolvedFrames {
			frameIDs = append(frameIDs, frame.ID)
			if frame.FunctionLabel != "" {
				functionLabels[frame.FunctionLabel] = struct{}{}
			}
		}
		stackKey := strings.Join(frameIDs, ",")
		stackRow := stackRows[stackKey]
		if stackRow == nil {
			stackRow = &store.ProfileStack{
				ID:          scopedProfileID(profileID, "stack", stackKey),
				StackKey:    stackKey,
				LeafFrameID: frameIDs[0],
				RootFrameID: frameIDs[len(frameIDs)-1],
				Depth:       len(frameIDs),
			}
			stackRows[stackKey] = stackRow
			for i, frameID := range frameIDs {
				result.StackFrames = append(result.StackFrames, store.ProfileStackFrame{
					StackID:  stackRow.ID,
					Position: i,
					FrameID:  frameID,
				})
			}
		}

		result.Samples = append(result.Samples, store.ProfileSample{
			ID:          scopedProfileID(profileID, "sample", strconv.Itoa(index)),
			ThreadRowID: threadRow.ID,
			StackID:     stackRow.ID,
			TSNS:        sample.TSNS,
			Weight:      sample.Weight,
			WallTimeNS:  sample.WallTimeNS,
			QueueTimeNS: sample.QueueTimeNS,
			CPUTimeNS:   sample.CPUTimeNS,
			IsIdle:      sample.IsIdle,
		})
		result.Manifest.SampleCount += sample.Weight
	}

	for _, item := range threadRows {
		result.Threads = append(result.Threads, *item)
	}
	for _, item := range frameRows {
		result.Frames = append(result.Frames, *item)
	}
	for _, item := range stackRows {
		result.Stacks = append(result.Stacks, *item)
	}
	sort.Slice(result.Threads, func(i, j int) bool { return result.Threads[i].ThreadKey < result.Threads[j].ThreadKey })
	sort.Slice(result.Frames, func(i, j int) bool { return result.Frames[i].FrameLabel < result.Frames[j].FrameLabel })
	sort.Slice(result.Stacks, func(i, j int) bool { return result.Stacks[i].StackKey < result.Stacks[j].StackKey })

	result.Manifest.ThreadCount = len(result.Threads)
	result.Manifest.FrameCount = len(result.Frames)
	result.Manifest.FunctionCount = len(functionLabels)
	result.Manifest.StackCount = len(result.Stacks)
	if result.Manifest.DurationNS <= 0 {
		result.Manifest.DurationNS = maxSampleTS
	}
	if result.Manifest.StartedAt.IsZero() {
		result.Manifest.StartedAt = time.Now().UTC()
	}
	if result.Manifest.DateCreated.IsZero() {
		result.Manifest.DateCreated = result.Manifest.StartedAt
	}
	if result.Manifest.DurationNS > 0 {
		result.Manifest.EndedAt = result.Manifest.StartedAt.Add(time.Duration(result.Manifest.DurationNS))
	}
	if invalidReason != "" {
		failNormalizedBridgeProfile(&result, invalidReason)
		return result, nil
	}
	if result.Manifest.SampleCount == 0 || result.Manifest.FrameCount == 0 || result.Manifest.StackCount == 0 {
		failNormalizedBridgeProfile(&result, "profile graph is incomplete")
	}
	return result, nil
}

func failNormalizedBridgeProfile(result *bridgeNormalizedProfile, reason string) {
	result.Manifest.ProcessingStatus = store.ProfileProcessingStatusFailed
	result.Manifest.IngestError = reason
	result.Threads = nil
	result.Frames = nil
	result.Stacks = nil
	result.StackFrames = nil
	result.Samples = nil
	result.Manifest.ThreadCount = 0
	result.Manifest.FrameCount = 0
	result.Manifest.FunctionCount = 0
	result.Manifest.StackCount = 0
	result.Manifest.SampleCount = 0
}

func extractProfileFrameInputs(obj map[string]any) []profileFrameInput {
	rawFrames, _ := obj["frames"].([]any)
	frames := make([]profileFrameInput, 0, len(rawFrames))
	for _, raw := range rawFrames {
		input := normalizeProfileFrameInput(raw)
		if input.FrameLabel == "" {
			continue
		}
		frames = append(frames, input)
	}
	return frames
}

func extractProfileSampleInputs(profileObj, payload map[string]any) []profileSampleInput {
	rawSamples := extractProfileSamples(profileObj)
	if len(rawSamples) == 0 {
		rawSamples = extractProfileSamples(payload)
	}
	samples := make([]profileSampleInput, 0, len(rawSamples))
	for _, raw := range rawSamples {
		samples = append(samples, normalizeProfileSampleInput(raw))
	}
	return samples
}

func extractProfileSamples(obj map[string]any) []any {
	for _, key := range []string{"samples", "stacks"} {
		if raw, ok := obj[key].([]any); ok {
			return raw
		}
	}
	return nil
}

func extractProfileThreadMeta(profileObj, payload map[string]any) map[string]profileThreadMeta {
	result := map[string]profileThreadMeta{}
	for _, source := range []map[string]any{profileObj, payload} {
		raw, ok := source["thread_metadata"]
		if !ok {
			continue
		}
		metaMap, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for key, value := range metaMap {
			obj, ok := value.(map[string]any)
			if !ok {
				continue
			}
			threadKey := normalizeThreadKey(key, firstJSONString(obj["name"]))
			result[threadKey] = profileThreadMeta{
				Name:   firstNonEmptyText(firstJSONString(obj["name"]), firstJSONString(obj["thread_name"])),
				IsMain: parseBoolAny(obj["is_main"]),
			}
		}
	}
	return result
}

func resolveSampleFrameRows(rawFrames []any, indexed []profileFrameInput, rows map[string]*store.ProfileFrame, profileID string) ([]*store.ProfileFrame, bool) {
	frames := make([]*store.ProfileFrame, 0, len(rawFrames))
	for _, raw := range rawFrames {
		var input profileFrameInput
		if idx, ok := intFromAny(raw); ok {
			if idx < 0 || idx >= len(indexed) {
				return nil, false
			}
			input = indexed[idx]
		} else {
			input = normalizeProfileFrameInput(raw)
		}
		if input.FrameLabel == "" {
			return nil, false
		}
		key := profileFrameKey(input)
		row := rows[key]
		if row == nil {
			row = &store.ProfileFrame{
				ID:            scopedProfileID(profileID, "frame", key),
				FrameKey:      key,
				FrameLabel:    input.FrameLabel,
				FunctionLabel: input.FunctionLabel,
				FunctionName:  input.FunctionName,
				ModuleName:    input.ModuleName,
				PackageName:   input.PackageName,
				Filename:      input.Filename,
				Lineno:        input.Lineno,
				InApp:         input.InApp,
				ImageRef:      input.ImageRef,
			}
			rows[key] = row
		}
		frames = append(frames, row)
	}
	return frames, len(frames) > 0
}

func normalizeProfileFrameInput(raw any) profileFrameInput {
	obj, ok := raw.(map[string]any)
	if !ok {
		return profileFrameInput{}
	}
	function := strings.TrimSpace(firstNonEmptyText(firstJSONString(obj["function"]), firstJSONString(obj["name"])))
	module := strings.TrimSpace(firstJSONString(obj["module"]))
	pkg := strings.TrimSpace(firstJSONString(obj["package"]))
	filename := strings.TrimSpace(firstNonEmptyText(firstJSONString(obj["filename"]), firstJSONString(obj["file"]), firstJSONString(obj["abs_path"])))
	lineno := int(parseInt64Any(firstNonNil(obj["lineno"], obj["line"], obj["lineNo"])))
	inApp := parseBoolAny(obj["in_app"])
	imageRef := strings.TrimSpace(firstNonEmptyText(firstJSONString(obj["image_addr"]), firstJSONString(obj["image_ref"]), firstJSONString(obj["instruction_addr"])))
	functionLabel := firstNonEmptyText(function, module, pkg, filename, "unknown")
	frameLabel := functionLabel
	if filename != "" {
		frameLabel = functionLabel + " @ " + filename
		if lineno > 0 {
			frameLabel += ":" + strconv.Itoa(lineno)
		}
	} else if lineno > 0 {
		frameLabel = functionLabel + ":" + strconv.Itoa(lineno)
	}
	return profileFrameInput{
		FrameLabel:    frameLabel,
		FunctionLabel: functionLabel,
		FunctionName:  function,
		ModuleName:    module,
		PackageName:   pkg,
		Filename:      filename,
		Lineno:        lineno,
		InApp:         inApp,
		ImageRef:      imageRef,
	}
}

func normalizeProfileSampleInput(raw any) profileSampleInput {
	obj, ok := raw.(map[string]any)
	if !ok {
		if rawFrames, ok := raw.([]any); ok {
			return profileSampleInput{Frames: rawFrames, Weight: 1}
		}
		return profileSampleInput{}
	}
	frames := sampleFramesAny(obj)
	weight := int(parseInt64Any(obj["weight"]))
	if weight <= 0 {
		weight = 1
	}
	return profileSampleInput{
		ThreadKey:   firstNonEmptyText(firstJSONString(obj["thread_id"]), firstJSONString(obj["threadId"]), firstJSONString(obj["thread"])),
		ThreadName:  firstNonEmptyText(firstJSONString(obj["thread_name"]), firstJSONString(obj["threadName"]), firstJSONString(obj["name"])),
		IsMain:      parseBoolAny(obj["is_main"]),
		TSNS:        parseInt64Any(firstNonNil(obj["elapsed_since_start_ns"], obj["elapsedSinceStartNS"], obj["timestamp_ns"], obj["ts"])),
		Weight:      weight,
		WallTimeNS:  parseInt64Any(obj["wall_time_ns"]),
		QueueTimeNS: parseInt64Any(obj["queue_time_ns"]),
		CPUTimeNS:   parseInt64Any(obj["cpu_time_ns"]),
		IsIdle:      parseBoolAny(obj["is_idle"]),
		Frames:      frames,
	}
}

func sampleFramesAny(obj map[string]any) []any {
	for _, key := range []string{"frames", "stack"} {
		switch raw := obj[key].(type) {
		case []any:
			return raw
		case nil:
		default:
			return []any{raw}
		}
	}
	return nil
}

func profileFrameKey(input profileFrameInput) string {
	return strings.Join([]string{
		input.FunctionName,
		input.ModuleName,
		input.PackageName,
		input.Filename,
		strconv.Itoa(input.Lineno),
		strconv.FormatBool(input.InApp),
		input.ImageRef,
	}, "|")
}

func scopedProfileID(profileID, kind, value string) string {
	return profileID + ":" + kind + ":" + value
}

func firstJSONObject(raw any) map[string]any {
	obj, _ := raw.(map[string]any)
	return obj
}

func firstJSONString(raw any) string {
	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case json.Number:
		return strings.TrimSpace(value.String())
	case float64:
		return strconv.FormatInt(int64(value), 10)
	default:
		return ""
	}
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseOptionalTimeString(v string) time.Time {
	if strings.TrimSpace(v) == "" {
		return time.Time{}
	}
	return sqlutil.ParseDBTime(v)
}

func parseBoolAny(raw any) bool {
	switch value := raw.(type) {
	case bool:
		return value
	case string:
		value = strings.TrimSpace(strings.ToLower(value))
		return value == "true" || value == "1" || value == "yes"
	case float64:
		return value != 0
	default:
		return false
	}
}

func parseInt64Any(raw any) int64 {
	switch value := raw.(type) {
	case nil:
		return 0
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	case json.Number:
		i, _ := value.Int64()
		return i
	case string:
		i, _ := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return i
	default:
		return 0
	}
}

func intFromAny(raw any) (int, bool) {
	switch value := raw.(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	case json.Number:
		i, err := value.Int64()
		if err == nil {
			return int(i), true
		}
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return i, true
		}
	}
	return 0, false
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstNonZeroTime(values ...time.Time) time.Time {
	for _, value := range values {
		if !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func normalizeThreadKey(threadKey, threadName string) string {
	threadKey = strings.TrimSpace(threadKey)
	if threadKey != "" {
		return threadKey
	}
	threadName = strings.TrimSpace(threadName)
	if threadName != "" {
		return threadName
	}
	return "main"
}

func inferThreadRole(name string, isMain bool) string {
	if isMain {
		return "main"
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.Contains(lower, "main"):
		return "main"
	case strings.Contains(lower, "worker"):
		return "worker"
	case strings.Contains(lower, "background"), strings.HasPrefix(lower, "bg"):
		return "background"
	default:
		return "unknown"
	}
}
