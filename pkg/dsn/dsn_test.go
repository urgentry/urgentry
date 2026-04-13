package dsn

import "testing"

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
