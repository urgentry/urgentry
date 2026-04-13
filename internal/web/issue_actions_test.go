package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urgentry/internal/auth"
	"urgentry/internal/store"
)

type issueActionStoreStub struct {
	patchErr        error
	recordErr       error
	bookmarkErr     error
	subscriptionErr error
	patches         []store.IssuePatch
	recorded        []string
}

func (s *issueActionStoreStub) PatchIssue(_ context.Context, _ string, patch store.IssuePatch) error {
	s.patches = append(s.patches, patch)
	return s.patchErr
}

func (s *issueActionStoreStub) RecordIssueActivity(_ context.Context, groupID, userID, kind, summary, details string) error {
	s.recorded = append(s.recorded, kind+":"+summary+":"+details+":"+groupID+":"+userID)
	return s.recordErr
}

func (s *issueActionStoreStub) ListIssueComments(context.Context, string, int) ([]store.IssueComment, error) {
	return nil, nil
}

func (s *issueActionStoreStub) AddIssueComment(context.Context, string, string, string) (store.IssueComment, error) {
	return store.IssueComment{}, nil
}

func (s *issueActionStoreStub) ListIssueActivity(context.Context, string, int) ([]store.IssueActivityEntry, error) {
	return nil, nil
}

func (s *issueActionStoreStub) MergeIssue(context.Context, string, string, string) error { return nil }
func (s *issueActionStoreStub) UnmergeIssue(context.Context, string, string) error       { return nil }

func (s *issueActionStoreStub) ToggleIssueBookmark(_ context.Context, _, _ string, _ bool) error {
	return s.bookmarkErr
}

func (s *issueActionStoreStub) ToggleIssueSubscription(_ context.Context, _, _ string, _ bool) error {
	return s.subscriptionErr
}

func (s *issueActionStoreStub) DeleteGroup(context.Context, string) error { return nil }

func (s *issueActionStoreStub) BulkDeleteGroups(context.Context, []string) error { return nil }

func (s *issueActionStoreStub) BulkMutateGroups(_ context.Context, _ []string, _ store.IssuePatch) error {
	return nil
}

func issueActionRequest(t *testing.T, target, body string, principal *auth.Principal) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if principal != nil {
		req = req.WithContext(auth.WithPrincipal(req.Context(), principal))
	}
	return req
}

func TestUpdateIssueAssigneeFailsWhenPatchFails(t *testing.T) {
	h := &Handler{issues: &issueActionStoreStub{patchErr: errors.New("patch failed")}}
	req := issueActionRequest(t, "/issues/issue-1/assign", "assignee=alice%40example.com", nil)
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()

	h.updateIssueAssignee(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Failed to update issue") {
		t.Fatalf("body = %q, want patch failure message", rec.Body.String())
	}
}

func TestUpdateIssueAssigneeHTMXFailureStaysPlainText(t *testing.T) {
	h := &Handler{issues: &issueActionStoreStub{patchErr: errors.New("patch failed")}}
	req := issueActionRequest(t, "/issues/issue-1/assign", "assignee=alice%40example.com", nil)
	req.Header.Set("HX-Request", "true")
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()

	h.updateIssueAssignee(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<!DOCTYPE html>") {
		t.Fatalf("body = %q, want plain-text HTMX failure", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Failed to update issue") {
		t.Fatalf("body = %q, want patch failure message", rec.Body.String())
	}
}

func TestUpdateIssueStatusIgnoresActivityFailureAfterPatch(t *testing.T) {
	stub := &issueActionStoreStub{recordErr: errors.New("activity failed")}
	h := &Handler{issues: stub}
	req := issueActionRequest(t, "/issues/issue-1/status", "action=resolve", &auth.Principal{
		Kind: auth.CredentialSession,
		User: &auth.User{ID: "user-1"},
	})
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()

	h.updateIssueStatus(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if len(stub.patches) != 1 {
		t.Fatalf("patch count = %d, want 1", len(stub.patches))
	}
	if len(stub.recorded) != 1 {
		t.Fatalf("record count = %d, want 1", len(stub.recorded))
	}
}

func TestToggleIssueBookmarkIgnoresActivityFailureAfterToggle(t *testing.T) {
	stub := &issueActionStoreStub{recordErr: errors.New("activity failed")}
	h := &Handler{issues: stub}
	req := issueActionRequest(t, "/issues/issue-1/bookmark", "bookmark=1", &auth.Principal{
		Kind: auth.CredentialSession,
		User: &auth.User{ID: "user-1"},
	})
	req.SetPathValue("id", "issue-1")
	rec := httptest.NewRecorder()

	h.toggleIssueBookmark(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if len(stub.recorded) != 1 {
		t.Fatalf("record count = %d, want 1", len(stub.recorded))
	}
}
