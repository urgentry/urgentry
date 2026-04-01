package profilequery

import (
	"testing"

	"urgentry/internal/store"
)

func TestRecordHelpersBuildTreeAndHotPath(t *testing.T) {
	record := &store.ProfileRecord{
		Manifest: store.ProfileManifest{
			ProfileID:        "profile-a",
			DurationNS:       40,
			SampleCount:      4,
			ProcessingStatus: store.ProfileProcessingStatusCompleted,
		},
		Threads: []store.ProfileThread{
			{ID: "thread-main", ThreadKey: "main", ThreadName: "Main", DurationNS: 40, SampleCount: 4},
		},
		Frames: []store.ProfileFrame{
			{ID: "frame-root", FrameLabel: "rootHandler @ app.go:1", FunctionLabel: "rootHandler"},
			{ID: "frame-db", FrameLabel: "dbQuery @ db.go:12", FunctionLabel: "dbQuery"},
			{ID: "frame-render", FrameLabel: "render @ ui.go:7", FunctionLabel: "render"},
		},
		Stacks: []store.ProfileStack{
			{ID: "stack-db", LeafFrameID: "frame-db"},
			{ID: "stack-render", LeafFrameID: "frame-render"},
		},
		StackFrames: []store.ProfileStackFrame{
			{StackID: "stack-db", Position: 0, FrameID: "frame-db"},
			{StackID: "stack-db", Position: 1, FrameID: "frame-root"},
			{StackID: "stack-render", Position: 0, FrameID: "frame-render"},
			{StackID: "stack-render", Position: 1, FrameID: "frame-root"},
		},
		Samples: []store.ProfileSample{
			{ThreadRowID: "thread-main", StackID: "stack-db", Weight: 3},
			{ThreadRowID: "thread-main", StackID: "stack-render", Weight: 1},
		},
	}

	threadID, threadKey, err := ResolveThread(record, "main")
	if err != nil {
		t.Fatalf("ResolveThread: %v", err)
	}
	if threadID != "thread-main" || threadKey != "main" {
		t.Fatalf("unexpected thread resolution = (%q, %q)", threadID, threadKey)
	}

	stacks := LoadStackAggregates(record, threadID)
	tree := BuildTree(record.Manifest.ProfileID, threadKey, "top_down", stacks, store.ProfileQueryFilter{})
	if tree.TotalWeight != 4 || len(tree.Root.Children) != 1 {
		t.Fatalf("unexpected tree root: %+v", tree.Root)
	}
	if tree.Root.Children[0].Name != "rootHandler @ app.go:1" {
		t.Fatalf("unexpected top-down root child = %q", tree.Root.Children[0].Name)
	}

	hotPath := BuildHotPath(tree)
	if len(hotPath.Frames) != 2 {
		t.Fatalf("unexpected hot path length = %d", len(hotPath.Frames))
	}
	if hotPath.Frames[1].Name != "dbQuery @ db.go:12" {
		t.Fatalf("unexpected hot path leaf = %q", hotPath.Frames[1].Name)
	}

	breakdowns := Breakdowns(record, false, 2)
	if len(breakdowns) != 2 || breakdowns[0].Name != "dbQuery" || breakdowns[0].Count != 3 {
		t.Fatalf("unexpected breakdowns = %+v", breakdowns)
	}

	weights := LoadFunctionWeights(record, threadID)
	if weights["dbQuery"] != 3 || weights["render"] != 1 {
		t.Fatalf("unexpected function weights = %+v", weights)
	}
}

func TestBuildComparisonAppliesScopeNotes(t *testing.T) {
	baseline := &store.ProfileManifest{
		ProfileID:        "baseline",
		Transaction:      "GET /checkout",
		Platform:         "go",
		Environment:      "production",
		ProcessingStatus: store.ProfileProcessingStatusCompleted,
	}
	candidate := &store.ProfileManifest{
		ProfileID:        "candidate",
		Transaction:      "GET /orders",
		Platform:         "go",
		Environment:      "staging",
		ProcessingStatus: store.ProfileProcessingStatusCompleted,
	}

	comparison := BuildComparison(
		baseline,
		candidate,
		"main",
		map[string]int{"dbQuery": 3, "render": 1},
		map[string]int{"dbQuery": 5, "serialize": 2},
		40,
		55,
		4,
		5,
		store.ProfileComparisonFilter{MaxFunctions: 2},
	)

	if comparison.Confidence != "low" {
		t.Fatalf("unexpected confidence = %q", comparison.Confidence)
	}
	if len(comparison.Notes) != 3 {
		t.Fatalf("unexpected notes = %+v", comparison.Notes)
	}
	if len(comparison.TopRegressions) != 2 || comparison.TopRegressions[0].Name != "dbQuery" {
		t.Fatalf("unexpected regressions = %+v", comparison.TopRegressions)
	}
	if comparison.DurationDeltaNS != 15 || comparison.SampleCountDelta != 1 {
		t.Fatalf("unexpected deltas = %+v", comparison)
	}
}
