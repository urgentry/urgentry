package search

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, f Filter)
	}{
		{
			name:  "is:unresolved",
			input: "is:unresolved",
			check: func(t *testing.T, f Filter) {
				if f.Status != "unresolved" {
					t.Errorf("Status = %q, want %q", f.Status, "unresolved")
				}
			},
		},
		{
			name:  "is:resolved",
			input: "is:resolved",
			check: func(t *testing.T, f Filter) {
				if f.Status != "resolved" {
					t.Errorf("Status = %q, want %q", f.Status, "resolved")
				}
			},
		},
		{
			name:  "is:ignored",
			input: "is:ignored",
			check: func(t *testing.T, f Filter) {
				if f.Status != "ignored" {
					t.Errorf("Status = %q, want %q", f.Status, "ignored")
				}
			},
		},
		{
			name:  "is:open normalizes to unresolved",
			input: "is:open",
			check: func(t *testing.T, f Filter) {
				if f.Status != "unresolved" {
					t.Errorf("Status = %q, want %q", f.Status, "unresolved")
				}
			},
		},
		{
			name:  "!is:resolved",
			input: "!is:resolved",
			check: func(t *testing.T, f Filter) {
				if len(f.NegatedStatuses) != 1 || f.NegatedStatuses[0] != "resolved" {
					t.Errorf("NegatedStatuses = %v, want [resolved]", f.NegatedStatuses)
				}
			},
		},
		{
			name:  "level:error",
			input: "level:error",
			check: func(t *testing.T, f Filter) {
				if f.Level != "error" {
					t.Errorf("Level = %q, want %q", f.Level, "error")
				}
			},
		},
		{
			name:  "level:Warning normalizes to lowercase",
			input: "level:Warning",
			check: func(t *testing.T, f Filter) {
				if f.Level != "warning" {
					t.Errorf("Level = %q, want %q", f.Level, "warning")
				}
			},
		},
		{
			name:  "!level:error",
			input: "!level:error",
			check: func(t *testing.T, f Filter) {
				if f.NegLevel != "error" {
					t.Errorf("NegLevel = %q, want %q", f.NegLevel, "error")
				}
			},
		},
		{
			name:  "release:1.2.3",
			input: "release:1.2.3",
			check: func(t *testing.T, f Filter) {
				if f.Release != "1.2.3" {
					t.Errorf("Release = %q, want %q", f.Release, "1.2.3")
				}
			},
		},
		{
			name:  "environment:production",
			input: "environment:production",
			check: func(t *testing.T, f Filter) {
				if f.Environment != "production" {
					t.Errorf("Environment = %q, want %q", f.Environment, "production")
				}
			},
		},
		{
			name:  "env:staging alias",
			input: "env:staging",
			check: func(t *testing.T, f Filter) {
				if f.Environment != "staging" {
					t.Errorf("Environment = %q, want %q", f.Environment, "staging")
				}
			},
		},
		{
			name:  "assigned:email@example.com",
			input: "assigned:user@example.com",
			check: func(t *testing.T, f Filter) {
				if f.Assigned != "user@example.com" {
					t.Errorf("Assigned = %q, want %q", f.Assigned, "user@example.com")
				}
			},
		},
		{
			name:  "has:assignee",
			input: "has:assignee",
			check: func(t *testing.T, f Filter) {
				if len(f.HasFields) != 1 || f.HasFields[0] != "assignee" {
					t.Errorf("HasFields = %v, want [assignee]", f.HasFields)
				}
			},
		},
		{
			name:  "!has:assignee",
			input: "!has:assignee",
			check: func(t *testing.T, f Filter) {
				if len(f.NotHasFields) != 1 || f.NotHasFields[0] != "assignee" {
					t.Errorf("NotHasFields = %v, want [assignee]", f.NotHasFields)
				}
			},
		},
		{
			name:  "tag filter browser.name:Chrome",
			input: "browser.name:Chrome",
			check: func(t *testing.T, f Filter) {
				if len(f.Tags) != 1 || f.Tags[0].Key != "browser.name" || f.Tags[0].Value != "Chrome" {
					t.Errorf("Tags = %+v, want [{Key:browser.name Value:Chrome}]", f.Tags)
				}
			},
		},
		{
			name:  "negated tag filter",
			input: "!browser.name:Chrome",
			check: func(t *testing.T, f Filter) {
				if len(f.NegTags) != 1 || f.NegTags[0].Key != "browser.name" || f.NegTags[0].Value != "Chrome" {
					t.Errorf("NegTags = %+v, want [{Key:browser.name Value:Chrome}]", f.NegTags)
				}
			},
		},
		{
			name:  "bare text terms",
			input: "connection refused",
			check: func(t *testing.T, f Filter) {
				if len(f.Terms) != 2 || f.Terms[0] != "connection" || f.Terms[1] != "refused" {
					t.Errorf("Terms = %v, want [connection refused]", f.Terms)
				}
			},
		},
		{
			name:  "quoted text as single term",
			input: `"connection refused"`,
			check: func(t *testing.T, f Filter) {
				if len(f.Terms) != 1 || f.Terms[0] != "connection refused" {
					t.Errorf("Terms = %v, want [connection refused]", f.Terms)
				}
			},
		},
		{
			name:  "event.type:error",
			input: "event.type:error",
			check: func(t *testing.T, f Filter) {
				if f.EventType != "error" {
					t.Errorf("EventType = %q, want %q", f.EventType, "error")
				}
			},
		},
		{
			name:  "type:transaction alias",
			input: "type:transaction",
			check: func(t *testing.T, f Filter) {
				if f.EventType != "transaction" {
					t.Errorf("EventType = %q, want %q", f.EventType, "transaction")
				}
			},
		},
		{
			name:  "complex mixed query",
			input: `is:unresolved level:error environment:production has:assignee "connection timeout" browser.name:Chrome`,
			check: func(t *testing.T, f Filter) {
				if f.Status != "unresolved" {
					t.Errorf("Status = %q, want %q", f.Status, "unresolved")
				}
				if f.Level != "error" {
					t.Errorf("Level = %q, want %q", f.Level, "error")
				}
				if f.Environment != "production" {
					t.Errorf("Environment = %q, want %q", f.Environment, "production")
				}
				if len(f.HasFields) != 1 || f.HasFields[0] != "assignee" {
					t.Errorf("HasFields = %v, want [assignee]", f.HasFields)
				}
				if len(f.Terms) != 1 || f.Terms[0] != "connection timeout" {
					t.Errorf("Terms = %v, want [connection timeout]", f.Terms)
				}
				if len(f.Tags) != 1 || f.Tags[0].Key != "browser.name" || f.Tags[0].Value != "Chrome" {
					t.Errorf("Tags = %+v, want [{Key:browser.name Value:Chrome}]", f.Tags)
				}
			},
		},
		{
			name:  "empty string",
			input: "",
			check: func(t *testing.T, f Filter) {
				if f.Status != "" || f.Level != "" || f.Release != "" || len(f.Terms) != 0 {
					t.Errorf("expected empty filter, got %+v", f)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Parse(tt.input)
			tt.check(t, f)
		})
	}
}
