package postgrescontrol

import (
	"strings"
	"testing"
)

func TestAllMigrationsReturnsNonEmpty(t *testing.T) {
	t.Parallel()

	all := AllMigrations()
	if len(all) == 0 {
		t.Fatal("AllMigrations() returned empty")
	}
}

func TestAllMigrationsVersionsAreSequential(t *testing.T) {
	t.Parallel()

	for i, m := range AllMigrations() {
		want := i + 1
		if m.Version != want {
			t.Fatalf("migration[%d].Version = %d, want %d", i, m.Version, want)
		}
	}
}

func TestAllMigrationsHaveNonEmptyNameAndSQL(t *testing.T) {
	t.Parallel()

	for _, m := range AllMigrations() {
		if strings.TrimSpace(m.Name) == "" {
			t.Fatalf("migration %d has empty name", m.Version)
		}
		if strings.TrimSpace(m.SQL) == "" {
			t.Fatalf("migration %d has empty SQL", m.Version)
		}
	}
}

func TestAllMigrationsAreAdditive(t *testing.T) {
	t.Parallel()

	for _, m := range AllMigrations() {
		upper := strings.ToUpper(m.SQL)
		if strings.Contains(upper, "DROP TABLE") {
			t.Fatalf("migration %d (%s) drops a table", m.Version, m.Name)
		}
		if strings.Contains(upper, "DROP COLUMN") {
			t.Fatalf("migration %d (%s) drops a column", m.Version, m.Name)
		}
		if strings.Contains(upper, "TRUNCATE ") {
			t.Fatalf("migration %d (%s) truncates a table", m.Version, m.Name)
		}
	}
}

func TestAllMigrationsContainCoreSchema(t *testing.T) {
	t.Parallel()

	sql := joinMigrationSQL(AllMigrations())
	requiredTables := []string{
		"organizations",
		"projects",
		"users",
		"groups",
		"personal_access_tokens",
	}
	for _, table := range requiredTables {
		if !strings.Contains(sql, table) {
			t.Fatalf("migrations missing reference to table %q", table)
		}
	}
}

func TestNormalizeIssueSearchStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filter   string
		rawQuery string
		want     string
	}{
		{"unresolved filter", "unresolved", "", "unresolved"},
		{"open filter", "open", "", "unresolved"},
		{"resolved filter", "resolved", "", "resolved"},
		{"closed filter", "closed", "", "resolved"},
		{"ignored filter", "ignored", "", "ignored"},
		{"empty filter", "", "", ""},
		{"unknown filter", "unknown", "", ""},
		{"case insensitive", "UNRESOLVED", "", "unresolved"},
		{"whitespace", "  resolved  ", "", "resolved"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeIssueSearchStatus(tt.filter, tt.rawQuery); got != tt.want {
				t.Fatalf("normalizeIssueSearchStatus(%q, %q) = %q, want %q", tt.filter, tt.rawQuery, got, tt.want)
			}
		})
	}
}

func TestClampLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		limit    int
		fallback int
		want     int
	}{
		{"zero uses fallback", 0, 100, 100},
		{"negative uses fallback", -1, 50, 50},
		{"positive is preserved", 25, 100, 25},
		{"one is preserved", 1, 100, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampLimit(tt.limit, tt.fallback); got != tt.want {
				t.Fatalf("clampLimit(%d, %d) = %d, want %d", tt.limit, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestNewIssueReadStoreCreation(t *testing.T) {
	t.Parallel()

	store := NewIssueReadStore(nil, nil)
	if store == nil {
		t.Fatal("NewIssueReadStore returned nil")
	}
	if store.controlDB != nil || store.queryDB != nil {
		t.Fatal("NewIssueReadStore should accept nil DBs")
	}
}

func joinMigrationSQL(migrations []Migration) string {
	var b strings.Builder
	for _, m := range migrations {
		b.WriteString(m.SQL)
		b.WriteString("\n")
	}
	return b.String()
}
