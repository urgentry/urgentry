package proguard

import (
	"context"
	"testing"
)

func TestMemoryStoreAndResolver(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	data := []byte(`
com.example.MainActivity -> a.b:
    42:42:void onCreate(android.os.Bundle) -> a
com.example.UserService -> c.d:
    88:88:java.lang.String getDisplayName() -> b
`)
	mapping := &Mapping{
		ID:        "map-1",
		ProjectID: "proj-1",
		ReleaseID: "android@1.2.3",
		UUID:      "debug-1",
		Name:      "mapping.txt",
	}
	if err := store.SaveMapping(ctx, mapping, data); err != nil {
		t.Fatalf("SaveMapping: %v", err)
	}

	got, payload, err := store.GetMapping(ctx, "map-1")
	if err != nil {
		t.Fatalf("GetMapping: %v", err)
	}
	if got == nil || string(payload) != string(data) {
		t.Fatalf("GetMapping = %+v payload=%q", got, payload)
	}

	list, err := store.ListByRelease(ctx, "proj-1", "android@1.2.3")
	if err != nil {
		t.Fatalf("ListByRelease: %v", err)
	}
	if len(list) != 1 || list[0].ID != "map-1" {
		t.Fatalf("ListByRelease = %+v", list)
	}

	lookedUp, _, err := store.LookupByUUID(ctx, "proj-1", "android@1.2.3", "debug-1")
	if err != nil {
		t.Fatalf("LookupByUUID: %v", err)
	}
	if lookedUp == nil || lookedUp.ID != "map-1" {
		t.Fatalf("LookupByUUID = %+v", lookedUp)
	}

	resolver := &Resolver{Store: store}
	module, file, function, line, err := resolver.Resolve(ctx, "proj-1", "android@1.2.3", "a.b", "a", 42)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if module != "com.example.MainActivity" || file != "MainActivity.java" || function != "onCreate" || line != 42 {
		t.Fatalf("Resolve = %q %q %q %d", module, file, function, line)
	}

	module, file, function, line, err = resolver.Resolve(ctx, "proj-1", "android@1.2.3", "c.d", "missing", 88)
	if err != nil {
		t.Fatalf("Resolve missing method: %v", err)
	}
	if module != "com.example.UserService" || file != "UserService.java" || function != "missing" || line != 88 {
		t.Fatalf("Resolve missing method = %q %q %q %d", module, file, function, line)
	}
}

func TestResolverHandlesMissingInputs(t *testing.T) {
	resolver := &Resolver{}
	module, file, function, line, err := resolver.Resolve(context.Background(), "", "", "", "", 0)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if module != "" || file != "" || function != "" || line != 0 {
		t.Fatalf("Resolve = %q %q %q %d", module, file, function, line)
	}
}
