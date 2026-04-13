package web

import (
	"testing"
	"time"

	"urgentry/internal/store"
)

func TestBuildWaterfallEmpty(t *testing.T) {
	result := buildWaterfall(nil, nil)
	if len(result.Nodes) != 0 {
		t.Fatalf("expected 0 nodes, got %d", len(result.Nodes))
	}
}

func TestBuildWaterfallTreeStructure(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	txns := []*store.StoredTransaction{
		{
			SpanID:         "root-span",
			ParentSpanID:   "",
			Op:             "http.server",
			Transaction:    "GET /api/users",
			Status:         "ok",
			StartTimestamp: base,
			EndTimestamp:    base.Add(500 * time.Millisecond),
			DurationMS:     500,
		},
	}
	spans := []store.StoredSpan{
		{
			SpanID:         "child-1",
			ParentSpanID:   "root-span",
			Op:             "db.query",
			Description:    "SELECT * FROM users",
			Status:         "ok",
			StartTimestamp: base.Add(50 * time.Millisecond),
			EndTimestamp:    base.Add(150 * time.Millisecond),
			DurationMS:     100,
		},
		{
			SpanID:         "child-2",
			ParentSpanID:   "root-span",
			Op:             "template.render",
			Description:    "users/list.html",
			Status:         "ok",
			StartTimestamp: base.Add(200 * time.Millisecond),
			EndTimestamp:    base.Add(400 * time.Millisecond),
			DurationMS:     200,
		},
		{
			SpanID:         "grandchild",
			ParentSpanID:   "child-1",
			Op:             "db.connection",
			Description:    "pool.acquire",
			Status:         "ok",
			StartTimestamp: base.Add(50 * time.Millisecond),
			EndTimestamp:    base.Add(60 * time.Millisecond),
			DurationMS:     10,
		},
	}

	result := buildWaterfall(txns, spans)

	if len(result.Nodes) != 4 {
		t.Fatalf("expected 4 nodes, got %d", len(result.Nodes))
	}

	// Verify DFS order: root, child-1, grandchild, child-2
	wantOrder := []struct {
		spanID string
		depth  int
	}{
		{"root-span", 0},
		{"child-1", 1},
		{"grandchild", 2},
		{"child-2", 1},
	}
	for i, want := range wantOrder {
		got := result.Nodes[i]
		if got.SpanID != want.spanID {
			t.Errorf("node[%d] spanID = %q, want %q", i, got.SpanID, want.spanID)
		}
		if got.Depth != want.depth {
			t.Errorf("node[%d] depth = %d, want %d", i, got.Depth, want.depth)
		}
	}

	// Verify indent pixels
	if result.Nodes[0].IndentPx != 0 {
		t.Errorf("root IndentPx = %d, want 0", result.Nodes[0].IndentPx)
	}
	if result.Nodes[2].IndentPx != 32 {
		t.Errorf("grandchild IndentPx = %d, want 32", result.Nodes[2].IndentPx)
	}

	// Verify positioning percentages (trace is 500ms total)
	root := result.Nodes[0]
	if root.LeftPercent != 0 {
		t.Errorf("root LeftPercent = %f, want 0", root.LeftPercent)
	}
	if root.WidthPercent != 100 {
		t.Errorf("root WidthPercent = %f, want 100", root.WidthPercent)
	}

	child1 := result.Nodes[1]
	// child-1 starts at 50ms into 500ms = 10%
	if child1.LeftPercent != 10 {
		t.Errorf("child-1 LeftPercent = %f, want 10", child1.LeftPercent)
	}
	// child-1 duration 100ms / 500ms = 20%
	if child1.WidthPercent != 20 {
		t.Errorf("child-1 WidthPercent = %f, want 20", child1.WidthPercent)
	}
}

func TestBuildWaterfallHasChildren(t *testing.T) {
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	txns := []*store.StoredTransaction{
		{
			SpanID:         "root",
			Op:             "http.server",
			StartTimestamp: base,
			EndTimestamp:    base.Add(100 * time.Millisecond),
			DurationMS:     100,
		},
	}
	spans := []store.StoredSpan{
		{
			SpanID:         "leaf",
			ParentSpanID:   "root",
			Op:             "db",
			StartTimestamp: base.Add(10 * time.Millisecond),
			EndTimestamp:    base.Add(50 * time.Millisecond),
			DurationMS:     40,
		},
	}

	result := buildWaterfall(txns, spans)
	if !result.Nodes[0].HasChildren {
		t.Error("root should have HasChildren=true")
	}
	if result.Nodes[1].HasChildren {
		t.Error("leaf should have HasChildren=false")
	}
}

func TestOpColorClass(t *testing.T) {
	cases := []struct {
		op   string
		want string
	}{
		{"db.query", "wf-color-db"},
		{"db", "wf-color-db"},
		{"http.client", "wf-color-http"},
		{"http.server", "wf-color-http"},
		{"template.render", "wf-color-template"},
		{"view.render", "wf-color-template"},
		{"render.page", "wf-color-template"},
		{"ui.load", "wf-color-template"},
		{"queue.publish", "wf-color-queue"},
		{"task.run", "wf-color-queue"},
		{"celery.task", "wf-color-queue"},
		{"worker.process", "wf-color-queue"},
		{"cache.get", "wf-color-cache"},
		{"redis.command", "wf-color-cache"},
		{"grpc.client", "wf-color-rpc"},
		{"rpc.call", "wf-color-rpc"},
		{"resource.css", "wf-color-resource"},
		{"browser.paint", "wf-color-resource"},
		{"unknown.op", "wf-color-default"},
		{"", "wf-color-default"},
	}
	for _, tc := range cases {
		got := opColorClass(tc.op)
		if got != tc.want {
			t.Errorf("opColorClass(%q) = %q, want %q", tc.op, got, tc.want)
		}
	}
}

func TestBuildWaterfallMinWidth(t *testing.T) {
	// A very short span relative to the trace should get at least 0.5% width.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	txns := []*store.StoredTransaction{
		{
			SpanID:         "root",
			Op:             "http.server",
			StartTimestamp: base,
			EndTimestamp:    base.Add(10 * time.Second),
			DurationMS:     10000,
		},
	}
	spans := []store.StoredSpan{
		{
			SpanID:         "tiny",
			ParentSpanID:   "root",
			Op:             "db",
			StartTimestamp: base.Add(1 * time.Millisecond),
			EndTimestamp:    base.Add(2 * time.Millisecond),
			DurationMS:     1, // 1ms out of 10000ms = 0.01%
		},
	}

	result := buildWaterfall(txns, spans)
	tiny := result.Nodes[1]
	if tiny.WidthPercent < 0.5 {
		t.Errorf("tiny span WidthPercent = %f, want >= 0.5", tiny.WidthPercent)
	}
}

func TestBuildWaterfallOrphanedSpansAreRoots(t *testing.T) {
	// Spans whose parent_span_id doesn't match any known span become roots.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	spans := []store.StoredSpan{
		{
			SpanID:         "orphan-1",
			ParentSpanID:   "missing-parent",
			Op:             "http",
			StartTimestamp: base,
			EndTimestamp:    base.Add(100 * time.Millisecond),
			DurationMS:     100,
		},
		{
			SpanID:         "orphan-2",
			ParentSpanID:   "",
			Op:             "db",
			StartTimestamp: base.Add(50 * time.Millisecond),
			EndTimestamp:    base.Add(150 * time.Millisecond),
			DurationMS:     100,
		},
	}

	result := buildWaterfall(nil, spans)
	if len(result.Nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(result.Nodes))
	}
	for i, n := range result.Nodes {
		if n.Depth != 0 {
			t.Errorf("node[%d] depth = %d, want 0 (orphan should be root)", i, n.Depth)
		}
	}
}
