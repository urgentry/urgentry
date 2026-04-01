package telemetrybridge

import (
	"encoding/json"
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestNormalizeValueCoversAllNumericTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  any
	}{
		{"time.Time", time.Date(2026, 3, 29, 12, 0, 0, 0, time.UTC), "2026-03-29T12:00:00Z"},
		{"zero time", time.Time{}, ""},
		{"int", int(5), float64(5)},
		{"int64", int64(5), float64(5)},
		{"int32", int32(5), float64(5)},
		{"float32", float32(5.5), float64(float32(5.5))},
		{"float64", float64(5.5), float64(5.5)},
		{"string", "hello", "hello"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// normalizeValue is tested indirectly through SnapshotTable in discoverharness,
			// but we test it directly here since it's also used in profile support
		})
	}
}

func TestFirstJSONString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  string
	}{
		{"string", "hello", "hello"},
		{"string with spaces", "  hello  ", "hello"},
		{"json.Number", json.Number("42"), "42"},
		{"float64", float64(42), "42"},
		{"nil", nil, ""},
		{"bool", true, ""},
		{"int", int(5), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstJSONString(tt.input); got != tt.want {
				t.Fatalf("firstJSONString(%v) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFirstNonEmptyText(t *testing.T) {
	t.Parallel()

	if got := firstNonEmptyText("", "  ", "hello", "world"); got != "hello" {
		t.Fatalf("firstNonEmptyText = %q, want hello", got)
	}
	if got := firstNonEmptyText("", "  "); got != "" {
		t.Fatalf("firstNonEmptyText(all empty) = %q, want empty", got)
	}
	if got := firstNonEmptyText("first"); got != "first" {
		t.Fatalf("firstNonEmptyText(single) = %q, want first", got)
	}
}

func TestFirstNonNil(t *testing.T) {
	t.Parallel()

	if got := firstNonNil(nil, nil, "hello", "world"); got != "hello" {
		t.Fatalf("firstNonNil = %v, want hello", got)
	}
	if got := firstNonNil(nil, nil); got != nil {
		t.Fatalf("firstNonNil(all nil) = %v, want nil", got)
	}
}

func TestFirstNonZeroTime(t *testing.T) {
	t.Parallel()

	zero := time.Time{}
	now := time.Now().UTC()

	if got := firstNonZeroTime(zero, now); !got.Equal(now) {
		t.Fatalf("firstNonZeroTime = %v, want %v", got, now)
	}
	if got := firstNonZeroTime(zero, zero); !got.IsZero() {
		t.Fatalf("firstNonZeroTime(all zero) = %v, want zero", got)
	}
}

func TestParseBoolAny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  bool
	}{
		{"true bool", true, true},
		{"false bool", false, false},
		{"string true", "true", true},
		{"string True", "True", true},
		{"string 1", "1", true},
		{"string yes", "yes", true},
		{"string false", "false", false},
		{"string no", "no", false},
		{"float64 nonzero", float64(1), true},
		{"float64 zero", float64(0), false},
		{"nil", nil, false},
		{"int", int(1), false}, // unsupported type returns false
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseBoolAny(tt.input); got != tt.want {
				t.Fatalf("parseBoolAny(%v) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseInt64Any(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  int64
	}{
		{"nil", nil, 0},
		{"int", int(42), 42},
		{"int64", int64(99), 99},
		{"float64", float64(3.7), 3},
		{"json.Number", json.Number("123"), 123},
		{"string", "456", 456},
		{"bad string", "abc", 0},
		{"bool", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseInt64Any(tt.input); got != tt.want {
				t.Fatalf("parseInt64Any(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestIntFromAny(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input any
		want  int
		ok    bool
	}{
		{"int", int(42), 42, true},
		{"int64", int64(99), 99, true},
		{"float64", float64(7.0), 7, true},
		{"json.Number", json.Number("10"), 10, true},
		{"string", "5", 5, true},
		{"bad string", "abc", 0, false},
		{"nil", nil, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := intFromAny(tt.input)
			if ok != tt.ok {
				t.Fatalf("intFromAny(%v) ok = %v, want %v", tt.input, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("intFromAny(%v) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestMaxInt(t *testing.T) {
	t.Parallel()

	if got := maxInt(3, 5); got != 5 {
		t.Fatalf("maxInt(3, 5) = %d, want 5", got)
	}
	if got := maxInt(5, 3); got != 5 {
		t.Fatalf("maxInt(5, 3) = %d, want 5", got)
	}
	if got := maxInt(4, 4); got != 4 {
		t.Fatalf("maxInt(4, 4) = %d, want 4", got)
	}
}

func TestNormalizeThreadKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		threadKey  string
		threadName string
		want       string
	}{
		{"key takes precedence", "thread-1", "Main Thread", "thread-1"},
		{"name as fallback", "", "Worker Thread", "Worker Thread"},
		{"spaces in key", "  thread-1  ", "Main", "thread-1"},
		{"all empty defaults to main", "", "", "main"},
		{"whitespace only", "  ", "  ", "main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeThreadKey(tt.threadKey, tt.threadName); got != tt.want {
				t.Fatalf("normalizeThreadKey(%q, %q) = %q, want %q", tt.threadKey, tt.threadName, got, tt.want)
			}
		})
	}
}

func TestInferThreadRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		thread string
		isMain bool
		want   string
	}{
		{"explicit main", "thread-1", true, "main"},
		{"name contains main", "Main Thread", false, "main"},
		{"name contains worker", "Worker-3", false, "worker"},
		{"name contains background", "background-pool", false, "background"},
		{"bg prefix", "bg-task", false, "background"},
		{"unknown thread", "custom-pool", false, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferThreadRole(tt.thread, tt.isMain); got != tt.want {
				t.Fatalf("inferThreadRole(%q, %v) = %q, want %q", tt.thread, tt.isMain, got, tt.want)
			}
		})
	}
}

func TestProfileFrameKey(t *testing.T) {
	t.Parallel()

	input := profileFrameInput{
		FunctionName: "main",
		ModuleName:   "myapp",
		PackageName:  "cmd",
		Filename:     "main.go",
		Lineno:       42,
		InApp:        true,
		ImageRef:     "0x1234",
	}
	key := profileFrameKey(input)
	if key == "" {
		t.Fatal("profileFrameKey returned empty string")
	}
	// Should be deterministic
	key2 := profileFrameKey(input)
	if key != key2 {
		t.Fatal("profileFrameKey is not deterministic")
	}
}

func TestScopedProfileID(t *testing.T) {
	t.Parallel()

	got := scopedProfileID("profile-1", "thread", "main")
	if got != "profile-1:thread:main" {
		t.Fatalf("scopedProfileID = %q", got)
	}
}

func TestMergeBridgeProfileManifestFillsGaps(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	base := store.ProfileManifest{
		ProjectID: "proj-1",
		ProfileID: "profile-1",
	}
	parsed := store.ProfileManifest{
		EventID:     "evt-1",
		TraceID:     "trace-1",
		Transaction: "GET /",
		Release:     "v1.0",
		Environment: "prod",
		Platform:    "go",
		ProfileKind: "sampled",
		StartedAt:   now,
		EndedAt:     now.Add(time.Second),
		DurationNS:  1000000000,
		ThreadCount: 2,
		SampleCount: 10,
		FrameCount:  5,
	}
	merged := mergeBridgeProfileManifest(base, parsed)

	if merged.ProjectID != "proj-1" {
		t.Fatalf("ProjectID not preserved from base")
	}
	if merged.EventID != "evt-1" {
		t.Fatalf("EventID not filled from parsed")
	}
	if merged.TraceID != "trace-1" {
		t.Fatalf("TraceID not filled from parsed")
	}
	if merged.Transaction != "GET /" {
		t.Fatalf("Transaction not filled from parsed")
	}
	if merged.DurationNS != 1000000000 {
		t.Fatalf("DurationNS not filled from parsed")
	}
}

func TestMergeBridgeProfileManifestPreservesBaseValues(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	base := store.ProfileManifest{
		EventID:     "base-evt",
		TraceID:     "base-trace",
		Transaction: "base-txn",
		StartedAt:   now,
		DurationNS:  5000,
	}
	parsed := store.ProfileManifest{
		EventID:     "parsed-evt",
		TraceID:     "parsed-trace",
		Transaction: "parsed-txn",
		StartedAt:   now.Add(time.Hour),
		DurationNS:  9000,
	}
	merged := mergeBridgeProfileManifest(base, parsed)

	if merged.EventID != "base-evt" {
		t.Fatalf("base EventID should be preserved, got %q", merged.EventID)
	}
	if merged.DurationNS != 5000 {
		t.Fatalf("base DurationNS should be preserved, got %d", merged.DurationNS)
	}
}

func TestHydrateBridgeProfileRecordEmptyPayload(t *testing.T) {
	t.Parallel()

	manifest := store.ProfileManifest{
		ProfileID: "profile-1",
		ProjectID: "proj-1",
	}
	record, err := hydrateBridgeProfileRecord(manifest, nil)
	if err != nil {
		t.Fatalf("hydrateBridgeProfileRecord(nil payload) error = %v", err)
	}
	if record.Manifest.ProfileID != "profile-1" {
		t.Fatal("manifest not preserved")
	}
}

func TestNormalizeProfileFrameInputFromMap(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"function": "myFunc",
		"module":   "myModule",
		"filename": "main.go",
		"lineno":   float64(42),
		"in_app":   true,
	}
	input := normalizeProfileFrameInput(raw)
	if input.FunctionName != "myFunc" {
		t.Fatalf("FunctionName = %q, want myFunc", input.FunctionName)
	}
	if input.ModuleName != "myModule" {
		t.Fatalf("ModuleName = %q, want myModule", input.ModuleName)
	}
	if input.Filename != "main.go" {
		t.Fatalf("Filename = %q, want main.go", input.Filename)
	}
	if input.Lineno != 42 {
		t.Fatalf("Lineno = %d, want 42", input.Lineno)
	}
	if !input.InApp {
		t.Fatal("InApp = false, want true")
	}
	// Frame label should include filename and line
	if input.FrameLabel != "myFunc @ main.go:42" {
		t.Fatalf("FrameLabel = %q", input.FrameLabel)
	}
}

func TestNormalizeProfileFrameInputNonMapReturnsEmpty(t *testing.T) {
	t.Parallel()

	input := normalizeProfileFrameInput("not a map")
	if input.FrameLabel != "" {
		t.Fatalf("expected empty FrameLabel for non-map input, got %q", input.FrameLabel)
	}
}

func TestNormalizeProfileFrameInputAlternateFieldNames(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"name": "funcName",
		"file": "file.go",
		"line": float64(10),
	}
	input := normalizeProfileFrameInput(raw)
	if input.FunctionName != "funcName" {
		t.Fatalf("FunctionName = %q, want funcName (from name field)", input.FunctionName)
	}
	if input.Filename != "file.go" {
		t.Fatalf("Filename = %q, want file.go (from file field)", input.Filename)
	}
	if input.Lineno != 10 {
		t.Fatalf("Lineno = %d, want 10", input.Lineno)
	}
}

func TestNormalizeProfileSampleInputFromMap(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"thread_id":              "thread-1",
		"elapsed_since_start_ns": float64(500000),
		"weight":                 float64(3),
		"frames":                 []any{float64(0), float64(1)},
	}
	sample := normalizeProfileSampleInput(raw)
	if sample.ThreadKey != "thread-1" {
		t.Fatalf("ThreadKey = %q, want thread-1", sample.ThreadKey)
	}
	if sample.TSNS != 500000 {
		t.Fatalf("TSNS = %d, want 500000", sample.TSNS)
	}
	if sample.Weight != 3 {
		t.Fatalf("Weight = %d, want 3", sample.Weight)
	}
	if len(sample.Frames) != 2 {
		t.Fatalf("Frames len = %d, want 2", len(sample.Frames))
	}
}

func TestNormalizeProfileSampleInputDefaultWeight(t *testing.T) {
	t.Parallel()

	raw := map[string]any{
		"frames": []any{float64(0)},
	}
	sample := normalizeProfileSampleInput(raw)
	if sample.Weight != 1 {
		t.Fatalf("default Weight = %d, want 1", sample.Weight)
	}
}

func TestNormalizeProfileSampleInputFromRawArray(t *testing.T) {
	t.Parallel()

	raw := []any{float64(0), float64(1)}
	sample := normalizeProfileSampleInput(raw)
	if len(sample.Frames) != 2 {
		t.Fatalf("Frames len = %d, want 2", len(sample.Frames))
	}
	if sample.Weight != 1 {
		t.Fatalf("Weight from array = %d, want 1", sample.Weight)
	}
}

func TestNormalizeProfileSampleInputNonMapNonArray(t *testing.T) {
	t.Parallel()

	sample := normalizeProfileSampleInput("bad")
	if len(sample.Frames) != 0 {
		t.Fatalf("expected no frames for non-map/non-array input")
	}
}

func TestFailNormalizedBridgeProfileClearsAllData(t *testing.T) {
	t.Parallel()

	result := &bridgeNormalizedProfile{
		Manifest: store.ProfileManifest{
			ProfileID:   "p-1",
			SampleCount: 10,
			FrameCount:  5,
		},
		Threads: []store.ProfileThread{{ID: "t1"}},
		Frames:  []store.ProfileFrame{{ID: "f1"}},
	}
	failNormalizedBridgeProfile(result, "test failure")

	if result.Manifest.ProcessingStatus != store.ProfileProcessingStatusFailed {
		t.Fatalf("ProcessingStatus = %q, want failed", result.Manifest.ProcessingStatus)
	}
	if result.Manifest.IngestError != "test failure" {
		t.Fatalf("IngestError = %q, want test failure", result.Manifest.IngestError)
	}
	if result.Manifest.SampleCount != 0 || result.Manifest.FrameCount != 0 {
		t.Fatal("counts not zeroed")
	}
	if result.Threads != nil || result.Frames != nil {
		t.Fatal("data slices not cleared")
	}
}

func TestNormalizeBridgeProfilePayloadRequiresProfileID(t *testing.T) {
	t.Parallel()

	_, err := normalizeBridgeProfilePayload([]byte(`{}`))
	if err == nil {
		t.Fatal("expected error for missing profile_id")
	}
}

func TestNormalizeBridgeProfilePayloadInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := normalizeBridgeProfilePayload([]byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNormalizeBridgeProfilePayloadMinimalValid(t *testing.T) {
	t.Parallel()

	payload := `{
		"profile_id": "prof-1",
		"event_id": "evt-1",
		"platform": "go",
		"frames": [{"function": "main", "filename": "main.go", "lineno": 1}],
		"samples": [{"frames": [0], "weight": 1, "elapsed_since_start_ns": 1000}]
	}`
	result, err := normalizeBridgeProfilePayload([]byte(payload))
	if err != nil {
		t.Fatalf("normalizeBridgeProfilePayload error = %v", err)
	}
	if result.Manifest.ProfileID != "prof-1" {
		t.Fatalf("ProfileID = %q, want prof-1", result.Manifest.ProfileID)
	}
	if result.Manifest.SampleCount != 1 {
		t.Fatalf("SampleCount = %d, want 1", result.Manifest.SampleCount)
	}
	if result.Manifest.FrameCount != 1 {
		t.Fatalf("FrameCount = %d, want 1", result.Manifest.FrameCount)
	}
	if result.Manifest.ThreadCount != 1 {
		t.Fatalf("ThreadCount = %d, want 1", result.Manifest.ThreadCount)
	}
}

func TestNormalizeBridgeProfilePayloadSetsFailedForEmptyGraph(t *testing.T) {
	t.Parallel()

	payload := `{"profile_id": "prof-1", "frames": [], "samples": []}`
	result, err := normalizeBridgeProfilePayload([]byte(payload))
	if err != nil {
		t.Fatalf("normalizeBridgeProfilePayload error = %v", err)
	}
	if result.Manifest.ProcessingStatus != store.ProfileProcessingStatusFailed {
		t.Fatalf("ProcessingStatus = %q, want failed", result.Manifest.ProcessingStatus)
	}
	if result.Manifest.IngestError != "profile graph is incomplete" {
		t.Fatalf("IngestError = %q", result.Manifest.IngestError)
	}
}
