package dsn

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		raw       string
		wantError bool
		projectID string
		publicKey string
	}{
		{
			name:      "basic dsn",
			raw:       "https://public@example.com/42",
			projectID: "42",
			publicKey: "public",
		},
		{
			name:      "secret key allowed",
			raw:       "https://public:secret@example.com/99",
			projectID: "99",
			publicKey: "public",
		},
		{
			name:      "missing project id",
			raw:       "https://public@example.com/",
			wantError: true,
		},
		{
			name:      "missing public key",
			raw:       "https://example.com/42",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.raw)
			if tt.wantError {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.ProjectID != tt.projectID {
				t.Fatalf("project id: got %q want %q", got.ProjectID, tt.projectID)
			}
			if got.PublicKey != tt.publicKey {
				t.Fatalf("public key: got %q want %q", got.PublicKey, tt.publicKey)
			}
		})
	}
}

func TestPublicProjectID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want string
	}{
		{name: "keeps numeric ids", id: "123456", want: "123456"},
		{name: "trims numeric ids", id: " 42 ", want: "42"},
		{name: "maps default project to stable numeric id", id: "default-project", want: "860034886"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PublicProjectID(tt.id)
			if got != tt.want {
				t.Fatalf("PublicProjectID(%q) = %q, want %q", tt.id, got, tt.want)
			}
			if strings.Trim(got, "0123456789") != "" {
				t.Fatalf("PublicProjectID(%q) = %q, want digits only", tt.id, got)
			}
		})
	}
}

func TestProjectIDMatches(t *testing.T) {
	public := PublicProjectID("default-project")
	if !ProjectIDMatches("default-project", public) {
		t.Fatalf("numeric public project id %q should match canonical project", public)
	}
	if !ProjectIDMatches("default-project", "default-project") {
		t.Fatal("canonical project id should still match")
	}
	if ProjectIDMatches("default-project", "123") {
		t.Fatal("unrelated project id should not match")
	}
}
