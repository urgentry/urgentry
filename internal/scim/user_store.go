package scim

import (
	"context"
	"strings"
	"time"
)

// UserStore abstracts SCIM user CRUD for org-scoped provisioning.
type UserStore interface {
	ListUsers(ctx context.Context, orgID string, startIndex, count int, filter string) ([]UserRecord, int, error)
	GetUser(ctx context.Context, orgID, userID string) (*UserRecord, error)
	CreateUser(ctx context.Context, orgID string, user UserRecord) (*UserRecord, error)
	PatchUser(ctx context.Context, orgID, userID string, ops []PatchOp) (*UserRecord, error)
	DeleteUser(ctx context.Context, orgID, userID string) (bool, error)
}

// UserRecord is the store-facing representation of a SCIM user.
type UserRecord struct {
	ID          string
	ExternalID  string
	Email       string
	DisplayName string
	GivenName   string
	FamilyName  string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PatchOp represents one RFC 7644 PATCH operation.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value any    `json:"value"`
}

// UserFilter is the parsed subset of SCIM filters Urgentry supports today.
type UserFilter struct {
	Field string
	Value string
}

// ParseUserFilter parses simple equality filters such as `userName eq "a@b.c"`.
func ParseUserFilter(raw string) (UserFilter, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return UserFilter{}, false
	}
	parts := strings.SplitN(raw, " eq ", 2)
	if len(parts) != 2 {
		return UserFilter{}, false
	}
	field := normalizePath(parts[0])
	value := strings.TrimSpace(parts[1])
	value = strings.Trim(value, `"`)
	value = strings.Trim(value, `'`)
	if value == "" {
		return UserFilter{}, false
	}
	switch field {
	case "id", "username", "email", "displayname", "externalid":
		return UserFilter{Field: field, Value: value}, true
	default:
		return UserFilter{}, false
	}
}

// NormalizeUserRecord trims and normalizes the fields Urgentry persists.
func NormalizeUserRecord(rec *UserRecord) {
	if rec == nil {
		return
	}
	rec.Email = strings.ToLower(strings.TrimSpace(rec.Email))
	rec.DisplayName = strings.TrimSpace(rec.DisplayName)
	rec.GivenName = strings.TrimSpace(rec.GivenName)
	rec.FamilyName = strings.TrimSpace(rec.FamilyName)
	rec.ExternalID = strings.TrimSpace(rec.ExternalID)
	if rec.DisplayName == "" && (rec.GivenName != "" || rec.FamilyName != "") {
		rec.DisplayName = strings.TrimSpace(rec.GivenName + " " + rec.FamilyName)
	}
}

// InferNameParts derives coarse given/family names from display name text.
func InferNameParts(displayName string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(displayName))
	if len(parts) == 0 {
		return "", ""
	}
	if len(parts) == 1 {
		return parts[0], ""
	}
	return parts[0], strings.Join(parts[1:], " ")
}

// ApplyPatchOps updates rec in place for the supported SCIM patch subset.
func ApplyPatchOps(rec *UserRecord, ops []PatchOp) {
	if rec == nil {
		return
	}
	for _, op := range ops {
		kind := strings.ToLower(strings.TrimSpace(op.Op))
		if kind == "" {
			kind = "replace"
		}
		switch kind {
		case "add", "replace":
			applyPatchValue(rec, normalizePath(op.Path), op.Value)
		case "remove":
			applyPatchRemove(rec, normalizePath(op.Path))
		}
	}
	NormalizeUserRecord(rec)
}

func applyPatchValue(rec *UserRecord, path string, value any) {
	if path == "" {
		values, ok := value.(map[string]any)
		if !ok {
			return
		}
		for key, item := range values {
			applyPatchValue(rec, normalizePath(key), item)
		}
		return
	}

	switch path {
	case "active":
		if active, ok := value.(bool); ok {
			rec.Active = active
		}
	case "displayname":
		if text, ok := value.(string); ok {
			rec.DisplayName = strings.TrimSpace(text)
		}
	case "username", "email":
		if text, ok := value.(string); ok {
			rec.Email = strings.TrimSpace(text)
		}
	case "externalid":
		if text, ok := value.(string); ok {
			rec.ExternalID = strings.TrimSpace(text)
		}
	case "name":
		values, ok := value.(map[string]any)
		if !ok {
			return
		}
		for key, item := range values {
			applyPatchValue(rec, "name."+normalizePath(key), item)
		}
	case "name.givenname":
		if text, ok := value.(string); ok {
			rec.GivenName = strings.TrimSpace(text)
		}
	case "name.familyname":
		if text, ok := value.(string); ok {
			rec.FamilyName = strings.TrimSpace(text)
		}
	case "emails":
		if email := firstEmailValue(value); email != "" {
			rec.Email = email
		}
	}
}

func applyPatchRemove(rec *UserRecord, path string) {
	switch path {
	case "displayname":
		rec.DisplayName = ""
	case "externalid":
		rec.ExternalID = ""
	case "name":
		rec.GivenName = ""
		rec.FamilyName = ""
	case "name.givenname":
		rec.GivenName = ""
	case "name.familyname":
		rec.FamilyName = ""
	case "active":
		rec.Active = false
	}
}

func firstEmailValue(value any) string {
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	first := ""
	for _, item := range items {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		raw, _ := entry["value"].(string)
		email := strings.TrimSpace(raw)
		if email == "" {
			continue
		}
		if first == "" {
			first = email
		}
		if primary, ok := entry["primary"].(bool); ok && primary {
			return email
		}
	}
	return first
}

func normalizePath(path string) string {
	return strings.ToLower(strings.TrimSpace(path))
}
