package domain

import "testing"

func TestIssueStatus_String(t *testing.T) {
	tests := []struct {
		status IssueStatus
		want   string
	}{
		{StatusUnresolved, "unresolved"},
		{StatusResolved, "resolved"},
		{StatusIgnored, "ignored"},
		{StatusMerged, "merged"},
		{StatusResolvedNextRel, "resolved_next_release"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("IssueStatus.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIssueStatus_Valid(t *testing.T) {
	tests := []struct {
		name  string
		s     IssueStatus
		valid bool
	}{
		{"unresolved", StatusUnresolved, true},
		{"resolved", StatusResolved, true},
		{"ignored", StatusIgnored, true},
		{"merged", StatusMerged, true},
		{"resolved_next_release", StatusResolvedNextRel, true},
		{"empty string", IssueStatus(""), false},
		{"garbage", IssueStatus("foo"), false},
		{"close-but-wrong", IssueStatus("Resolved"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Valid(); got != tt.valid {
				t.Errorf("IssueStatus(%q).Valid() = %v, want %v", tt.s, got, tt.valid)
			}
		})
	}
}

func TestLevel_String(t *testing.T) {
	tests := []struct {
		level Level
		want  string
	}{
		{LevelFatal, "fatal"},
		{LevelError, "error"},
		{LevelWarning, "warning"},
		{LevelInfo, "info"},
		{LevelDebug, "debug"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.level.String(); got != tt.want {
				t.Errorf("Level.String() = %q, want %q", got, tt.want)
			}
		})
	}
}
