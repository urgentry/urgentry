package web

import (
	"math"
	"sort"
	"time"

	"urgentry/internal/store"
)

// waterfallNode is a single row in the span waterfall visualization.
type waterfallNode struct {
	SpanID       string
	ParentSpanID string
	Op           string
	Description  string
	Status       string
	StartTime    time.Time
	DurationMS   float64
	Depth        int
	IndentPx     int // Depth * 16 for CSS padding-left
	LeftPercent  float64
	WidthPercent float64
	Duration     string
	ColorClass   string
	HasChildren  bool
	Children     []*waterfallNode
}

// waterfallData holds everything the template needs to render the waterfall.
type waterfallData struct {
	Nodes         []*waterfallNode // flattened DFS order for rendering
	TotalDuration string
}

// buildWaterfall produces a flat list of waterfallNodes (depth-first) from
// a set of transactions and spans, computing positioning percentages relative
// to the overall trace time range.
func buildWaterfall(transactions []*store.StoredTransaction, spans []store.StoredSpan) waterfallData {
	if len(transactions) == 0 && len(spans) == 0 {
		return waterfallData{}
	}

	// Collect all span-like entries into a unified list.  Transactions are
	// top-level spans; child spans nest underneath via parent_span_id.
	type entry struct {
		spanID       string
		parentSpanID string
		op           string
		description  string
		status       string
		start        time.Time
		end          time.Time
		durationMS   float64
	}

	var entries []entry
	for _, txn := range transactions {
		e := entry{
			spanID:       txn.SpanID,
			parentSpanID: txn.ParentSpanID,
			op:           txn.Op,
			description:  txn.Transaction,
			status:       txn.Status,
			start:        txn.StartTimestamp,
			end:          txn.EndTimestamp,
			durationMS:   txn.DurationMS,
		}
		if e.durationMS == 0 && !e.start.IsZero() && !e.end.IsZero() {
			e.durationMS = e.end.Sub(e.start).Seconds() * 1000
		}
		entries = append(entries, e)
	}
	for _, sp := range spans {
		e := entry{
			spanID:       sp.SpanID,
			parentSpanID: sp.ParentSpanID,
			op:           sp.Op,
			description:  sp.Description,
			status:       sp.Status,
			start:        sp.StartTimestamp,
			end:          sp.EndTimestamp,
			durationMS:   sp.DurationMS,
		}
		if e.durationMS == 0 && !e.start.IsZero() && !e.end.IsZero() {
			e.durationMS = e.end.Sub(e.start).Seconds() * 1000
		}
		entries = append(entries, e)
	}

	// Compute global time range.
	var minStart, maxEnd time.Time
	for _, e := range entries {
		if !e.start.IsZero() && (minStart.IsZero() || e.start.Before(minStart)) {
			minStart = e.start
		}
		end := e.end
		if end.IsZero() && !e.start.IsZero() && e.durationMS > 0 {
			end = e.start.Add(time.Duration(e.durationMS * float64(time.Millisecond)))
		}
		if !end.IsZero() && (maxEnd.IsZero() || end.After(maxEnd)) {
			maxEnd = end
		}
	}
	traceDurationMS := 0.0
	if !minStart.IsZero() && !maxEnd.IsZero() {
		traceDurationMS = maxEnd.Sub(minStart).Seconds() * 1000
	}

	// Build nodes keyed by span ID.
	nodeMap := make(map[string]*waterfallNode, len(entries))
	for i := range entries {
		e := &entries[i]
		node := &waterfallNode{
			SpanID:       e.spanID,
			ParentSpanID: e.parentSpanID,
			Op:           e.op,
			Description:  e.description,
			Status:       e.status,
			StartTime:    e.start,
			DurationMS:   e.durationMS,
			Duration:     formatTraceDuration(e.durationMS),
			ColorClass:   opColorClass(e.op),
		}
		// Compute positioning percentages.
		if traceDurationMS > 0 && !e.start.IsZero() {
			offsetMS := e.start.Sub(minStart).Seconds() * 1000
			node.LeftPercent = math.Max(0, (offsetMS/traceDurationMS)*100)
			node.WidthPercent = math.Max(0.5, (e.durationMS/traceDurationMS)*100) // min 0.5% for visibility
			if node.LeftPercent+node.WidthPercent > 100 {
				node.WidthPercent = 100 - node.LeftPercent
			}
		}
		nodeMap[e.spanID] = node
	}

	// Link children to parents.
	var roots []*waterfallNode
	for _, node := range nodeMap {
		if node.ParentSpanID != "" {
			if parent, ok := nodeMap[node.ParentSpanID]; ok {
				parent.Children = append(parent.Children, node)
				parent.HasChildren = true
				continue
			}
		}
		roots = append(roots, node)
	}

	// Sort children by start time at each level, and sort roots.
	sortNodes := func(nodes []*waterfallNode) {
		sort.Slice(nodes, func(i, j int) bool {
			return nodes[i].StartTime.Before(nodes[j].StartTime)
		})
	}
	sortNodes(roots)
	var sortTree func(nodes []*waterfallNode)
	sortTree = func(nodes []*waterfallNode) {
		for _, n := range nodes {
			if len(n.Children) > 0 {
				sortNodes(n.Children)
				sortTree(n.Children)
			}
		}
	}
	sortTree(roots)

	// Flatten to DFS order with depth tracking.
	var flat []*waterfallNode
	var walk func(nodes []*waterfallNode, depth int)
	walk = func(nodes []*waterfallNode, depth int) {
		for _, n := range nodes {
			n.Depth = depth
			n.IndentPx = depth * 16
			flat = append(flat, n)
			walk(n.Children, depth+1)
		}
	}
	walk(roots, 0)

	return waterfallData{
		Nodes:         flat,
		TotalDuration: formatTraceDuration(traceDurationMS),
	}
}

// opColorClass maps span operation prefixes to CSS color classes.
func opColorClass(op string) string {
	switch {
	case op == "":
		return "wf-color-default"
	case startsWith(op, "db"):
		return "wf-color-db"
	case startsWith(op, "http"):
		return "wf-color-http"
	case startsWith(op, "template"), startsWith(op, "view"), startsWith(op, "render"), startsWith(op, "ui"):
		return "wf-color-template"
	case startsWith(op, "queue"), startsWith(op, "task"), startsWith(op, "celery"), startsWith(op, "worker"):
		return "wf-color-queue"
	case startsWith(op, "cache"), startsWith(op, "redis"), startsWith(op, "memcache"):
		return "wf-color-cache"
	case startsWith(op, "grpc"), startsWith(op, "rpc"):
		return "wf-color-rpc"
	case startsWith(op, "resource"), startsWith(op, "browser"):
		return "wf-color-resource"
	default:
		return "wf-color-default"
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
