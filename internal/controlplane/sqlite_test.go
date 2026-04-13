package controlplane

import (
	"database/sql"
	"reflect"
	"testing"

	"urgentry/internal/sqlite"
)

func TestSQLiteServicesNilDBReturnsZeroServices(t *testing.T) {
	got := SQLiteServices(nil)
	assertServicesMatch(t, got, Services{})
}

func TestSQLiteServicesBuildsSQLiteDefaults(t *testing.T) {
	db := openSQLiteDB(t)
	got := SQLiteServices(db)

	assertAllServiceFieldsPresent(t, got)
	if _, ok := got.Catalog.(*sqlite.CatalogStore); !ok {
		t.Fatalf("Catalog type = %T, want *sqlite.CatalogStore", got.Catalog)
	}
	if _, ok := got.Monitors.(*sqlite.MonitorStore); !ok {
		t.Fatalf("Monitors type = %T, want *sqlite.MonitorStore", got.Monitors)
	}
}

func TestWithSQLiteDefaultsFillsOnlyMissingFields(t *testing.T) {
	defaultDB := openSQLiteDB(t)
	currentDB := openSQLiteDB(t)
	current := SQLiteServices(currentDB)
	current.Outbox = nil
	current.Deliveries = nil

	got := WithSQLiteDefaults(defaultDB, current)

	if got.Catalog != current.Catalog {
		t.Fatalf("Catalog was replaced by defaults")
	}
	if got.Admin != current.Admin {
		t.Fatalf("Admin was replaced by defaults")
	}
	if got.Issues != current.Issues {
		t.Fatalf("Issues was replaced by defaults")
	}
	if got.IssueReads != current.IssueReads {
		t.Fatalf("IssueReads was replaced by defaults")
	}
	if got.Ownership != current.Ownership {
		t.Fatalf("Ownership was replaced by defaults")
	}
	if got.Releases != current.Releases {
		t.Fatalf("Releases was replaced by defaults")
	}
	if got.Alerts != current.Alerts {
		t.Fatalf("Alerts was replaced by defaults")
	}
	if got.Monitors != current.Monitors {
		t.Fatalf("Monitors was replaced by defaults")
	}
	if got.Outbox == nil {
		t.Fatalf("Outbox was not filled from SQLite defaults")
	}
	if got.Deliveries == nil {
		t.Fatalf("Deliveries was not filled from SQLite defaults")
	}
}

func TestWithSQLiteDefaultsLeavesCurrentWhenDBNil(t *testing.T) {
	currentDB := openSQLiteDB(t)
	current := SQLiteServices(currentDB)

	got := WithSQLiteDefaults(nil, current)

	assertServicesMatch(t, got, current)
}

func openSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func assertAllServiceFieldsPresent(t *testing.T, services Services) {
	t.Helper()

	value := reflect.ValueOf(services)
	for i := 0; i < value.NumField(); i++ {
		if value.Field(i).IsNil() {
			t.Fatalf("%s is nil", value.Type().Field(i).Name)
		}
	}
}

func assertServicesMatch(t *testing.T, got, want Services) {
	t.Helper()

	gotValue := reflect.ValueOf(got)
	wantValue := reflect.ValueOf(want)
	for i := 0; i < gotValue.NumField(); i++ {
		fieldName := gotValue.Type().Field(i).Name
		gotField := gotValue.Field(i)
		wantField := wantValue.Field(i)
		if gotField.IsNil() || wantField.IsNil() {
			if gotField.IsNil() != wantField.IsNil() {
				t.Fatalf("%s nil mismatch: got nil=%t want nil=%t", fieldName, gotField.IsNil(), wantField.IsNil())
			}
			continue
		}
		if gotField.Interface() != wantField.Interface() {
			t.Fatalf("%s = %T, want identical value %T", fieldName, gotField.Interface(), wantField.Interface())
		}
	}
}
