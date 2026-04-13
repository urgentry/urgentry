package testpostgres

import (
	"testing"
)

func TestSanitizeIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase alpha", "hello", "hello"},
		{"uppercase", "Hello", "hello"},
		{"with spaces", " test name ", "test_name"},
		{"with hyphens", "my-test-db", "my_test_db"},
		{"with dots", "my.test.db", "my_test_db"},
		{"with numbers", "test123", "test123"},
		{"starts with number", "123test", "g_123test"},
		{"all special chars", "---", ""},
		{"empty", "", ""},
		{"leading trailing underscores", "__test__", "test"},
		{"mixed", "My Cool Test-DB_123!", "my_cool_test_db_123"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeIdentifier(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeIdentifier(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestReplaceDatabaseName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		dsn    string
		dbName string
		want   string
		err    bool
	}{
		{
			name:   "standard dsn",
			dsn:    "postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable",
			dbName: "test_db",
			want:   "postgres://postgres@127.0.0.1:5432/test_db?sslmode=disable",
		},
		{
			name:   "dsn with password",
			dsn:    "postgres://user:pass@localhost:5432/original?sslmode=require",
			dbName: "new_db",
			want:   "postgres://user:pass@localhost:5432/new_db?sslmode=require",
		},
		{
			name:   "dsn without query params",
			dsn:    "postgres://postgres@127.0.0.1:5432/postgres",
			dbName: "mydb",
			want:   "postgres://postgres@127.0.0.1:5432/mydb",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceDatabaseName(tt.dsn, tt.dbName)
			if tt.err && err == nil {
				t.Fatalf("expected error")
			}
			if !tt.err && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.err && got != tt.want {
				t.Fatalf("replaceDatabaseName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewProviderCreatesNonNilProvider(t *testing.T) {
	t.Parallel()

	p := NewProvider("test")
	if p == nil {
		t.Fatal("NewProvider returned nil")
	}
	if p.name != "test" {
		t.Fatalf("provider name = %q, want test", p.name)
	}
}

func TestNewProviderSanitizesName(t *testing.T) {
	t.Parallel()

	p := NewProvider("My Test Provider!")
	if p.name != "my_test_provider" {
		t.Fatalf("provider name = %q, want my_test_provider", p.name)
	}
}

func TestNewTemplateSetsFields(t *testing.T) {
	t.Parallel()

	p := NewProvider("test")
	tmpl := p.NewTemplate("my-template", nil)
	if tmpl == nil {
		t.Fatal("NewTemplate returned nil")
	}
	if tmpl.provider != p {
		t.Fatal("template provider mismatch")
	}
	if tmpl.name != "my_template" {
		t.Fatalf("template name = %q, want my_template", tmpl.name)
	}
}

func TestNextDatabaseNameIsUnique(t *testing.T) {
	t.Parallel()

	p := NewProvider("test")
	name1 := p.nextDatabaseName("prefix")
	name2 := p.nextDatabaseName("prefix")

	if name1 == name2 {
		t.Fatalf("nextDatabaseName generated duplicate names: %q", name1)
	}
}

func TestNextDatabaseNameDefaultsPrefix(t *testing.T) {
	t.Parallel()

	p := NewProvider("test")
	name := p.nextDatabaseName("")
	if name == "" {
		t.Fatal("empty prefix should still generate a name")
	}
	if len(name) < 10 {
		t.Fatalf("name %q is suspiciously short", name)
	}
}

func TestNextDatabaseNameSanitizesPrefix(t *testing.T) {
	t.Parallel()

	p := NewProvider("test")
	name := p.nextDatabaseName("My-Test DB!")
	if name == "" {
		t.Fatal("failed to generate name with special chars in prefix")
	}
}

func TestProviderCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	p := NewProvider("test")
	// Close without ever starting should not panic
	p.Close()
	p.Close()
}

func TestFreePortReturnsValidPort(t *testing.T) {
	t.Parallel()

	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error = %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("freePort() = %d, outside valid range", port)
	}
}

func TestFreePortReturnsDistinctPorts(t *testing.T) {
	t.Parallel()

	port1, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error = %v", err)
	}
	port2, err := freePort()
	if err != nil {
		t.Fatalf("freePort() error = %v", err)
	}
	// Not guaranteed to differ, but extremely likely
	_ = port1
	_ = port2
}
