package migration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestImporter_FullPayload(t *testing.T) {
	payload := ImportPayload{
		Projects:    []ProjectImport{{Slug: "web", Name: "Web"}},
		Releases:    []ReleaseImport{{Version: "1.0.0"}},
		Issues:      []IssueImport{{ID: "iss-1", ProjectSlug: "web", Title: "bug"}},
		Events:      []EventImport{{EventID: "evt-1", ProjectSlug: "web", GroupID: "iss-1"}},
		ProjectKeys: []ProjectKeyImport{{PublicKey: "pk-1", ProjectSlug: "web"}},
		AlertRules:  []AlertRuleImport{{Name: "rule-1", ProjectSlug: "web"}},
		Members:     []MemberImport{{Email: "test@example.com"}},
		Artifacts:   []ArtifactImport{{Kind: "attachment", Name: "crash.dmp", ProjectSlug: "web"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	imp := &Importer{
		CreateProject:    func(_ context.Context, _ string, _ ProjectImport) error { return nil },
		CreateRelease:    func(_ context.Context, _ string, _ ReleaseImport) error { return nil },
		CreateIssue:      func(_ context.Context, _ string, _ IssueImport) error { return nil },
		CreateEvent:      func(_ context.Context, _ string, _ EventImport) error { return nil },
		CreateProjectKey: func(_ context.Context, _ string, _ ProjectKeyImport) error { return nil },
		CreateAlertRule:  func(_ context.Context, _ string, _ AlertRuleImport) error { return nil },
		CreateMember:     func(_ context.Context, _ string, _ MemberImport) error { return nil },
		CreateArtifact:   func(_ context.Context, _ string, _ ArtifactImport) error { return nil },
	}

	result, err := imp.Import(context.Background(), "org-1", data)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if result.ProjectsImported != 1 {
		t.Errorf("ProjectsImported = %d, want 1", result.ProjectsImported)
	}
	if result.ReleasesImported != 1 {
		t.Errorf("ReleasesImported = %d, want 1", result.ReleasesImported)
	}
	if result.IssuesImported != 1 {
		t.Errorf("IssuesImported = %d, want 1", result.IssuesImported)
	}
	if result.EventsImported != 1 {
		t.Errorf("EventsImported = %d, want 1", result.EventsImported)
	}
	if result.ProjectKeysImported != 1 {
		t.Errorf("ProjectKeysImported = %d, want 1", result.ProjectKeysImported)
	}
	if result.AlertRulesImported != 1 {
		t.Errorf("AlertRulesImported = %d, want 1", result.AlertRulesImported)
	}
	if result.MembersImported != 1 {
		t.Errorf("MembersImported = %d, want 1", result.MembersImported)
	}
	if result.ArtifactsImported != 1 {
		t.Errorf("ArtifactsImported = %d, want 1", result.ArtifactsImported)
	}
	if len(result.Errors) != 0 {
		t.Errorf("Errors = %v, want empty", result.Errors)
	}
}

func TestImporter_NilCallbacks(t *testing.T) {
	payload := ImportPayload{
		Projects:    []ProjectImport{{Slug: "web"}},
		Releases:    []ReleaseImport{{Version: "1.0.0"}},
		Issues:      []IssueImport{{ID: "iss-1", ProjectSlug: "web", Title: "bug"}},
		Events:      []EventImport{{EventID: "evt-1", ProjectSlug: "web"}},
		ProjectKeys: []ProjectKeyImport{{PublicKey: "pk-1", ProjectSlug: "web"}},
		AlertRules:  []AlertRuleImport{{Name: "rule-1", ProjectSlug: "web"}},
		Members:     []MemberImport{{Email: "test@example.com"}},
		Artifacts:   []ArtifactImport{{Kind: "attachment", Name: "f.txt", ProjectSlug: "web"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	// All callbacks nil -- should still count items.
	imp := &Importer{}
	result, err := imp.Import(context.Background(), "org-1", data)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if result.ProjectsImported != 1 {
		t.Errorf("ProjectsImported = %d, want 1", result.ProjectsImported)
	}
	if result.EventsImported != 1 {
		t.Errorf("EventsImported = %d, want 1", result.EventsImported)
	}
	if result.ArtifactsImported != 1 {
		t.Errorf("ArtifactsImported = %d, want 1", result.ArtifactsImported)
	}
}

func TestImporter_CallbackErrors(t *testing.T) {
	payload := ImportPayload{
		Projects: []ProjectImport{
			{Slug: "ok"},
			{Slug: "fail"},
			{Slug: "also-ok"},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	imp := &Importer{
		CreateProject: func(_ context.Context, _ string, p ProjectImport) error {
			if p.Slug == "fail" {
				return errors.New("forced error")
			}
			return nil
		},
	}
	result, err := imp.Import(context.Background(), "org-1", data)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if result.ProjectsImported != 2 {
		t.Errorf("ProjectsImported = %d, want 2 (1 skipped due to error)", result.ProjectsImported)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("Errors count = %d, want 1", len(result.Errors))
	}
	if result.Errors[0] == "" {
		t.Error("expected non-empty error string")
	}
}

func TestImporter_AllResourceTypeErrors(t *testing.T) {
	payload := ImportPayload{
		Releases:    []ReleaseImport{{Version: "v1"}},
		Issues:      []IssueImport{{ID: "i1", ProjectSlug: "x", Title: "t"}},
		Events:      []EventImport{{EventID: "e1", ProjectSlug: "x"}},
		ProjectKeys: []ProjectKeyImport{{PublicKey: "k1", ProjectSlug: "x"}},
		AlertRules:  []AlertRuleImport{{Name: "a1", ProjectSlug: "x"}},
		Members:     []MemberImport{{Email: "m@x.com"}},
		Artifacts:   []ArtifactImport{{Kind: "attachment", Name: "a", ProjectSlug: "x"}},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	imp := &Importer{
		CreateRelease:    func(_ context.Context, _ string, _ ReleaseImport) error { return boom },
		CreateIssue:      func(_ context.Context, _ string, _ IssueImport) error { return boom },
		CreateEvent:      func(_ context.Context, _ string, _ EventImport) error { return boom },
		CreateProjectKey: func(_ context.Context, _ string, _ ProjectKeyImport) error { return boom },
		CreateAlertRule:  func(_ context.Context, _ string, _ AlertRuleImport) error { return boom },
		CreateMember:     func(_ context.Context, _ string, _ MemberImport) error { return boom },
		CreateArtifact:   func(_ context.Context, _ string, _ ArtifactImport) error { return boom },
	}

	result, err := imp.Import(context.Background(), "org-1", data)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}

	if len(result.Errors) != 7 {
		t.Errorf("Errors count = %d, want 7 (one per resource type)", len(result.Errors))
	}
	// Counters should all be zero since all callbacks failed.
	if result.ReleasesImported != 0 {
		t.Errorf("ReleasesImported = %d, want 0", result.ReleasesImported)
	}
}

func TestImporter_InvalidJSON(t *testing.T) {
	imp := &Importer{}
	_, err := imp.Import(context.Background(), "org-1", []byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestImporter_EmptyPayload(t *testing.T) {
	imp := &Importer{}
	result, err := imp.Import(context.Background(), "org-1", []byte(`{}`))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if result.ProjectsImported != 0 || result.EventsImported != 0 {
		t.Error("expected zero counts for empty payload")
	}
}

func TestImportPayload_JSONRoundtrip(t *testing.T) {
	original := ImportPayload{
		Projects: []ProjectImport{
			{Name: "Frontend", Slug: "frontend", Platform: "javascript"},
		},
		Releases: []ReleaseImport{
			{Version: "1.2.3", Ref: "abc123"},
		},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ImportPayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if len(decoded.Projects) != 1 || decoded.Projects[0].Slug != "frontend" {
		t.Errorf("projects round-trip failed: got %+v", decoded.Projects)
	}
	if len(decoded.Releases) != 1 || decoded.Releases[0].Version != "1.2.3" {
		t.Errorf("releases round-trip failed: got %+v", decoded.Releases)
	}
}
