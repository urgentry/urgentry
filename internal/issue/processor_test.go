package issue

import (
	"context"
	"encoding/json"
	"testing"

	"urgentry/internal/store"
)

func makeErrorPayload(t *testing.T, excType, excValue string, fingerprint []string) []byte {
	t.Helper()
	payload := map[string]any{
		"platform": "python",
		"level":    "error",
		"exception": map[string]any{
			"values": []map[string]any{
				{
					"type":  excType,
					"value": excValue,
					"stacktrace": map[string]any{
						"frames": []map[string]any{
							{
								"filename": "app/views.py",
								"function": "handle_request",
								"lineno":   42,
								"in_app":   true,
							},
						},
					},
				},
			},
		},
	}
	if fingerprint != nil {
		payload["fingerprint"] = fingerprint
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return data
}

func newProcessor() *Processor {
	return &Processor{
		Events: store.NewMemoryEventStore(),
		Groups: NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
	}
}

type stubOwnershipResolver struct {
	assignee   string
	teamSlug   string
	notifyTeam bool
}

func (s stubOwnershipResolver) ResolveAssignee(_ context.Context, _, _, _ string, _ map[string]string) (string, error) {
	return s.assignee, nil
}

func (s stubOwnershipResolver) ResolveOwnership(_ context.Context, _, _, _ string, _ map[string]string) (*store.OwnershipResolveResult, error) {
	if s.assignee == "" {
		return nil, nil
	}
	return &store.OwnershipResolveResult{
		Assignee:   s.assignee,
		TeamSlug:   s.teamSlug,
		NotifyTeam: s.notifyTeam,
	}, nil
}

func TestProcessor_BasicError(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()

	raw := makeErrorPayload(t, "ValueError", "bad input", nil)

	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if result.EventID == "" {
		t.Fatal("EventID is empty")
	}
	if result.GroupID == "" {
		t.Fatal("GroupID is empty")
	}
	if !result.IsNewGroup {
		t.Fatal("expected IsNewGroup=true for first event")
	}

	// Event should be stored
	evt, err := proc.Events.GetEvent(ctx, "proj-1", result.EventID)
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if evt.GroupID != result.GroupID {
		t.Fatalf("event GroupID = %q, want %q", evt.GroupID, result.GroupID)
	}
	if evt.Platform != "python" {
		t.Fatalf("Platform = %q, want python", evt.Platform)
	}
	if evt.Title == "" {
		t.Fatal("Title is empty")
	}

	// Group should exist
	group, err := proc.Groups.GetGroup(ctx, result.GroupID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if group.TimesSeen != 1 {
		t.Fatalf("TimesSeen = %d, want 1", group.TimesSeen)
	}
	if group.Status != "unresolved" {
		t.Fatalf("Status = %q, want unresolved", group.Status)
	}

	// Blob should be stored
	blob, err := proc.Blobs.Get(ctx, evt.PayloadKey)
	if err != nil {
		t.Fatalf("Blob Get: %v", err)
	}
	if len(blob) == 0 {
		t.Fatal("blob is empty")
	}
}

func TestProcessor_AppliesOwnershipRule(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()
	proc.Ownership = stubOwnershipResolver{assignee: "payments@team"}

	raw := makeErrorPayload(t, "ValueError", "bad input", nil)
	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	group, err := proc.Groups.GetGroup(ctx, result.GroupID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	if group.Assignee != "payments@team" {
		t.Fatalf("Assignee = %q, want payments@team", group.Assignee)
	}
}

func TestProcessor_TeamScopedOwnership(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()
	proc.Ownership = stubOwnershipResolver{
		assignee:   "alice@example.com",
		teamSlug:   "backend",
		notifyTeam: true,
	}

	raw := makeErrorPayload(t, "ValueError", "bad input", nil)
	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	group, err := proc.Groups.GetGroup(ctx, result.GroupID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	// When a team slug is set, assignee is prefixed with "team:"
	if group.Assignee != "team:backend" {
		t.Fatalf("Assignee = %q, want team:backend", group.Assignee)
	}
}

func TestProcessor_OwnershipWithoutTeam(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()
	proc.Ownership = stubOwnershipResolver{
		assignee: "bob@example.com",
	}

	raw := makeErrorPayload(t, "ValueError", "bad input", nil)
	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	group, err := proc.Groups.GetGroup(ctx, result.GroupID)
	if err != nil {
		t.Fatalf("GetGroup: %v", err)
	}
	// Without team slug, plain assignee is used
	if group.Assignee != "bob@example.com" {
		t.Fatalf("Assignee = %q, want bob@example.com", group.Assignee)
	}
}

func TestProcessor_SameErrorGroupsTogether(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()

	raw := makeErrorPayload(t, "ValueError", "bad input", nil)

	r1, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatal(err)
	}
	if !r1.IsNewGroup {
		t.Fatal("first event should be new group")
	}

	r2, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatal(err)
	}

	if r1.GroupID != r2.GroupID {
		t.Fatalf("same error produced different groups: %q vs %q", r1.GroupID, r2.GroupID)
	}
	if r2.IsNewGroup {
		t.Fatal("second event should not be new group")
	}

	// TimesSeen should be 2
	group, err := proc.Groups.GetGroup(ctx, r1.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.TimesSeen != 2 {
		t.Fatalf("TimesSeen = %d, want 2", group.TimesSeen)
	}
}

func TestProcessor_DifferentErrorsDifferentGroups(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()

	raw1 := makeErrorPayload(t, "ValueError", "bad input", nil)
	raw2 := makeErrorPayload(t, "TypeError", "wrong type", nil)

	r1, err := proc.Process(ctx, "proj-1", raw1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := proc.Process(ctx, "proj-1", raw2)
	if err != nil {
		t.Fatal(err)
	}

	if r1.GroupID == r2.GroupID {
		t.Fatalf("different errors should produce different groups, both got %q", r1.GroupID)
	}
	if !r1.IsNewGroup || !r2.IsNewGroup {
		t.Fatal("both should be new groups")
	}
}

func TestProcessor_FingerprintOverride(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()

	// Two different exception types, but same fingerprint
	raw1 := makeErrorPayload(t, "ValueError", "bad input", []string{"custom-fp"})
	raw2 := makeErrorPayload(t, "TypeError", "wrong type", []string{"custom-fp"})

	r1, err := proc.Process(ctx, "proj-1", raw1)
	if err != nil {
		t.Fatal(err)
	}
	r2, err := proc.Process(ctx, "proj-1", raw2)
	if err != nil {
		t.Fatal(err)
	}

	// Same fingerprint should produce same group despite different exception types
	if r1.GroupID != r2.GroupID {
		t.Fatalf("fingerprint override: different groups %q vs %q", r1.GroupID, r2.GroupID)
	}

	group, err := proc.Groups.GetGroup(ctx, r1.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.TimesSeen != 2 {
		t.Fatalf("TimesSeen = %d, want 2", group.TimesSeen)
	}
}

func TestProcessor_InvalidJSON(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()

	_, err := proc.Process(ctx, "proj-1", []byte(`not json`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestProcessor_MessageOnlyEvent(t *testing.T) {
	ctx := context.Background()
	proc := newProcessor()

	payload := map[string]any{
		"platform": "python",
		"level":    "info",
		"message":  "User signed up",
	}
	raw, _ := json.Marshal(payload)

	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.EventID == "" {
		t.Fatal("EventID is empty")
	}
	if !result.IsNewGroup {
		t.Fatal("expected new group")
	}

	group, err := proc.Groups.GetGroup(ctx, result.GroupID)
	if err != nil {
		t.Fatal(err)
	}
	if group.Title == "" || group.Title == "<no title>" {
		t.Fatalf("Title = %q, expected message-based title", group.Title)
	}
}

func TestProcessor_TransactionBypassesGrouping(t *testing.T) {
	ctx := context.Background()
	proc := &Processor{
		Events: store.NewMemoryEventStore(),
		Groups: NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: store.NewMemoryTraceStore(),
	}

	raw := []byte(`{
		"type":"transaction",
		"event_id":"22222222222222222222222222222222",
		"platform":"javascript",
		"transaction":"GET /items/:id",
		"tags":[["service.name","order-api"],["environment","production"]],
		"start_timestamp":"2026-03-27T12:00:00Z",
		"timestamp":"2026-03-27T12:00:01Z",
		"contexts":{"trace":{"trace_id":"trace-2","span_id":"root-2","op":"http.server","status":"ok"}},
		"spans":[{"trace_id":"trace-2","span_id":"child-2","parent_span_id":"root-2","op":"db","description":"SELECT 1","start_timestamp":"2026-03-27T12:00:00.100Z","timestamp":"2026-03-27T12:00:00.250Z"}]
	}`)

	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.GroupID != "" || result.IsNewGroup || result.IsRegression {
		t.Fatalf("unexpected transaction grouping result: %+v", result)
	}

	trace, err := proc.Traces.(*store.MemoryTraceStore).GetTransaction(ctx, "proj-1", result.EventID)
	if err != nil {
		t.Fatalf("GetTransaction: %v", err)
	}
	if trace.Transaction != "GET /items/:id" {
		t.Fatalf("Transaction = %q, want GET /items/:id", trace.Transaction)
	}
	if len(trace.Spans) != 1 || trace.Spans[0].Description != "SELECT 1" {
		t.Fatalf("unexpected spans: %+v", trace.Spans)
	}
	if got := trace.Tags["service.name"]; got != "order-api" {
		t.Fatalf("trace.Tags[service.name] = %q, want order-api", got)
	}

	var stored map[string]any
	if err := json.Unmarshal(trace.NormalizedJSON, &stored); err != nil {
		t.Fatalf("unmarshal stored transaction: %v", err)
	}
	tags, _ := stored["tags"].([]any)
	if len(tags) != 2 {
		t.Fatalf("len(tags) = %d, want 2", len(tags))
	}
	firstTag, _ := tags[0].(map[string]any)
	if got, _ := firstTag["key"].(string); got != "environment" {
		t.Fatalf("first tag key = %q, want environment", got)
	}
	secondTag, _ := tags[1].(map[string]any)
	if got, _ := secondTag["key"].(string); got != "service.name" {
		t.Fatalf("second tag key = %q, want service.name", got)
	}
}

func TestProcessor_ErrorWithTraceContextStillCreatesIssue(t *testing.T) {
	ctx := context.Background()
	proc := &Processor{
		Events: store.NewMemoryEventStore(),
		Groups: NewMemoryGroupStore(),
		Blobs:  store.NewMemoryBlobStore(),
		Traces: store.NewMemoryTraceStore(),
	}

	raw := []byte(`{
		"event_id":"44444444444444444444444444444444",
		"platform":"javascript",
		"level":"error",
		"transaction":"GET /items/:id",
		"contexts":{"trace":{"trace_id":"trace-4","span_id":"root-4","op":"http.server","status":"internal_error"}},
		"exception":{"values":[{"type":"TypeError","value":"boom"}]}
	}`)

	result, err := proc.Process(ctx, "proj-1", raw)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if result.GroupID == "" || !result.IsNewGroup {
		t.Fatalf("unexpected issue result: %+v", result)
	}
	if result.EventType != "error" {
		t.Fatalf("EventType = %q, want error", result.EventType)
	}
	if _, err := proc.Events.GetEvent(ctx, "proj-1", result.EventID); err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if _, err := proc.Traces.(*store.MemoryTraceStore).GetTransaction(ctx, "proj-1", result.EventID); err == nil {
		t.Fatal("unexpected transaction persisted for error event")
	}
}
